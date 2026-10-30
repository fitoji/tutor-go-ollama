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
	"unicode/utf8"
)

const (
	maxTurnsOnDisk = 200
	maxMistakes    = 30
	maxTopics      = 5
)

type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Profile struct {
	Level    string         `json:"level"`
	Topics   []string       `json:"topics,omitempty"`
	Mistakes map[string]int `json:"mistakes,omitempty"`
}

type Data struct {
	Profile Profile `json:"profile"`
	Summary string  `json:"summary"`
	Turns   []Turn  `json:"turns"`
	// Epoch increases on every Reset so that background work started before a
	// reset (e.g. a summary) cannot be applied to the fresh conversation.
	Epoch int `json:"epoch"`
}

// AddTurn appends a turn, dropping the oldest if the hard cap is exceeded
// (a safety net for when summarization keeps failing).
func (d *Data) AddTurn(role, content string) {
	d.Turns = append(d.Turns, Turn{Role: role, Content: content})
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
