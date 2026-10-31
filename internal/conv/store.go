// Package conv owns the conversation memory: a small persisted profile, a
// rolling summary and a short window of recent turns. The app, not the model,
// is the source of truth, and every request is rebuilt from this state.
package conv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxTurnsOnDisk = 200
	maxEvents      = 1000
	maxMistakes    = 30
	maxTopics      = 5
	maxFacts       = 40
	maxFactKey     = 40
	maxFactValue   = 200
)

type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// At is when the turn happened. Turns stored before this existed have no
	// timestamp and stay out of the time-based stats.
	At time.Time `json:"at,omitempty"`
}

// Event is one dated thing that happened, for the progress view. Turns are
// already dated in Data.Turns; the counters (mistakes, facts, mnemonics) are
// not, so they leave a trail here.
type Event struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // mistake, fact, mnemonic
	Key  string    `json:"key"`
}

type Profile struct {
	Level    string            `json:"level"`
	Topics   []string          `json:"topics,omitempty"`
	Mistakes map[string]int    `json:"mistakes,omitempty"`
	Facts    map[string]string `json:"facts,omitempty"`
}

type Data struct {
	Profile Profile `json:"profile"`
	Summary string  `json:"summary"`
	Turns   []Turn  `json:"turns"`
	// Epoch increases on every Reset so that background work started before a
	// reset (e.g. a summary) cannot be applied to the fresh conversation.
	Epoch int `json:"epoch"`
	// Events is a bounded trail of dated mistakes, facts and mnemonics: the
	// counters alone cannot answer "this week".
	Events []Event `json:"events,omitempty"`
}

// AddTurn appends a turn, dropping the oldest if the hard cap is exceeded
// (a safety net for when summarization keeps failing).
func (d *Data) AddTurn(role, content string) {
	d.Turns = append(d.Turns, Turn{Role: role, Content: content, At: time.Now().UTC()})
	if over := len(d.Turns) - maxTurnsOnDisk; over > 0 {
		d.Turns = append([]Turn(nil), d.Turns[over:]...)
	}
}

// DropLastTurn removes the last turn if it has the given role.
func (d *Data) DropLastTurn(role string) {
	if n := len(d.Turns); n > 0 && d.Turns[n-1].Role == role {
		d.Turns = d.Turns[:n-1]
	}
}

// Reset starts a new conversation but keeps the learner profile.
func (d *Data) Reset() {
	d.Turns = nil
	d.Summary = ""
	d.Epoch++
}

// FoldSummary replaces the summary and removes the n oldest turns it covers.
// It reports false (and changes nothing) if the conversation was reset since
// the summary was planned.
func (d *Data) FoldSummary(summary string, n, epoch int) bool {
	if epoch != d.Epoch {
		return false
	}
	if n > len(d.Turns) {
		n = len(d.Turns)
	}
	if n < 0 {
		n = 0
	}
	d.Turns = append([]Turn(nil), d.Turns[n:]...)
	d.Summary = summary
	return true
}

// AddEvent appends a dated event, dropping the oldest when the trail is full.
func (d *Data) AddEvent(kind, key string) {
	d.Events = append(d.Events, Event{At: time.Now().UTC(), Kind: kind, Key: key})
	if over := len(d.Events) - maxEvents; over > 0 {
		d.Events = append([]Event(nil), d.Events[over:]...)
	}
}

// RecordMistake counts a recurring issue by a short normalized key.
func (d *Data) RecordMistake(note string) {
	key := strings.ToLower(strings.TrimSpace(note))
	if r := []rune(key); len(r) > 60 {
		key = string(r[:60])
	}
	if key == "" {
		return
	}
	if d.Profile.Mistakes == nil {
		d.Profile.Mistakes = map[string]int{}
	}
	d.Profile.Mistakes[key]++
	d.AddEvent("mistake", key)
	for len(d.Profile.Mistakes) > maxMistakes {
		// Evict the least frequent entry, but never the one just recorded:
		// otherwise a new category that sorts first would evict itself every
		// time and could never accumulate repeats.
		lowK, lowV := "", int(^uint(0)>>1)
		for k, v := range d.Profile.Mistakes {
			if k == key {
				continue
			}
			if v < lowV || (v == lowV && k < lowK) {
				lowK, lowV = k, v
			}
		}
		delete(d.Profile.Mistakes, lowK)
	}
}

// TopMistakes returns up to n most frequent issues.
func (d *Data) TopMistakes(n int) []string {
	keys := make([]string, 0, len(d.Profile.Mistakes))
	for k := range d.Profile.Mistakes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := d.Profile.Mistakes[keys[i]], d.Profile.Mistakes[keys[j]]
		if a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// MnemonicPrefix marks a fact that holds a memory trick (a Campayo image), not
// something the tutor knows about the person. The prompt lists them apart.
const MnemonicPrefix = "mnemonic-"

// basicFacts are the four things the tutor asks for on the first meeting.
// They are the ones that make the small talk personal.
var basicFacts = []string{"name", "country", "city", "hobby"}

// SetFact stores one durable fact about the learner, normalized so the same
// fact written twice does not become two entries. It reports whether anything
// changed, so the app only announces real news.
func (d *Data) SetFact(key, value string) bool {
	key = NormalizeFactKey(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return false
	}
	if r := []rune(value); len(r) > maxFactValue {
		value = strings.TrimSpace(string(r[:maxFactValue]))
	}
	if d.Profile.Facts == nil {
		d.Profile.Facts = map[string]string{}
	}
	kind := "fact"
	if strings.HasPrefix(key, MnemonicPrefix) {
		kind = "mnemonic"
	}
	if old, ok := d.Profile.Facts[key]; ok {
		if old == value {
			return false
		}
		d.Profile.Facts[key] = value
		d.AddEvent(kind, key)
		return true
	}
	if len(d.Profile.Facts) >= maxFacts {
		return false
	}
	d.Profile.Facts[key] = value
	d.AddEvent(kind, key)
	return true
}

// DeleteFact forgets one fact. It reports whether there was something to
// forget, so the command can answer honestly.
func (d *Data) DeleteFact(key string) bool {
	key = NormalizeFactKey(key)
	if key == "" {
		return false
	}
	if _, ok := d.Profile.Facts[key]; !ok {
		return false
	}
	delete(d.Profile.Facts, key)
	return true
}

// FactKeys returns the stored keys in a stable order, so the prompt and the
// status line do not shuffle between requests.
func (d *Data) FactKeys() []string {
	keys := make([]string, 0, len(d.Profile.Facts))
	for k := range d.Profile.Facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MissingBasics lists the facts the tutor still has to ask for.
func (d *Data) MissingBasics() []string {
	var out []string
	for _, k := range basicFacts {
		if strings.TrimSpace(d.Profile.Facts[k]) == "" {
			out = append(out, k)
		}
	}
	return out
}

// NormalizeFactKey folds a key the way a person would: case and spacing are
// not part of the fact, so "Home City" and "home city" are the same entry. It
// returns "" for a key that is empty or too long to store.
func NormalizeFactKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.Join(strings.Fields(k), "-")
	if k == "" || utf8.RuneCountInString(k) > maxFactKey {
		return ""
	}
	return k
}

// AddTopic remembers a topic of interest, keeping only the latest few.
func (d *Data) AddTopic(topic string) {
	topic = strings.TrimSpace(topic)
	if topic == "" || utf8.RuneCountInString(topic) > 40 {
		return
	}
	for _, t := range d.Profile.Topics {
		if strings.EqualFold(t, topic) {
			return
		}
	}
	d.Profile.Topics = append(d.Profile.Topics, topic)
	if len(d.Profile.Topics) > maxTopics {
		d.Profile.Topics = d.Profile.Topics[len(d.Profile.Topics)-maxTopics:]
	}
}

// Store persists Data as one small JSON file with atomic writes.
type Store struct {
	mu   sync.Mutex
	path string
	d    Data
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, d: Data{Profile: Profile{Level: "B1"}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if s.d.Profile.Level == "" {
		s.d.Profile.Level = "B1"
	}
	return s, nil
}

// Level returns the learner's level without copying the whole state. A missing
// level reads as B1, the same default Open applies.
func (s *Store) Level() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Profile.Level == "" {
		return "B1"
	}
	return s.d.Profile.Level
}

// View returns a deep copy that is safe to use without holding the lock.
func (s *Store) View() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.d
	c.Turns = append([]Turn(nil), s.d.Turns...)
	c.Profile.Topics = append([]string(nil), s.d.Profile.Topics...)
	if s.d.Profile.Mistakes != nil {
		c.Profile.Mistakes = make(map[string]int, len(s.d.Profile.Mistakes))
		for k, v := range s.d.Profile.Mistakes {
			c.Profile.Mistakes[k] = v
		}
	}
	c.Events = append([]Event(nil), s.d.Events...)
	if s.d.Profile.Facts != nil {
		c.Profile.Facts = make(map[string]string, len(s.d.Profile.Facts))
		for k, v := range s.d.Profile.Facts {
			c.Profile.Facts[k] = v
		}
	}
	return c
}

// Update applies fn under the lock and persists the result.
func (s *Store) Update(fn func(*Data)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.d)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s.d)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
