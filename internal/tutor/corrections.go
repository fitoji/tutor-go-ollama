package tutor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"speakeasy/internal/conv"
	"speakeasy/internal/llm"
)

type Correction struct {
	Original string `json:"original"`
	Better   string `json:"better"`
	Category string `json:"category"`
	Note     string `json:"note"`
	// Mnemonic is a Campayo-style image for a trouble spot that keeps coming
	// back. Empty for a first mistake: it is not a pattern yet.
	Mnemonic string `json:"mnemonic,omitempty"`
}

// Review is one structured pass over the learner's message: what to correct,
// what to remember and, from C1 up, how a native speaker would say it.
type Review struct {
	Corrections []Correction
	Facts       []Fact
	Rewrite     string
}

// correctionPolicy is how hard the review looks. The ladder grows with the
// level: a beginner is not buried in notes, an advanced learner is corrected
// on everything.
type correctionPolicy struct {
	Max     int    // corrections to keep
	Rules   string // the paragraph that sets the tone
	Rewrite bool   // ask for a native rewrite of the whole message
	Tokens  int    // reply budget
}

func policyFor(level string) correctionPolicy {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "A1", "A2":
		return correctionPolicy{
			Max:    2,
			Rules:  "Be gentle: list only mistakes that block understanding or that a beginner should already control, and never more than 2.",
			Tokens: 300,
		}
	case "C1", "C2":
		return correctionPolicy{
			Max:     6,
			Rules:   "Be exhaustive: list everything a native speaker would change — grammar, collocation, register, redundancy, word choice — even when the message is understandable. Up to 6.",
			Rewrite: true,
			Tokens:  700,
		}
	default:
		// B1, B2 and any level we do not know: demanding, but still bounded.
		return correctionPolicy{
			Max:    4,
			Rules:  "Be demanding: list every real mistake at or below their level, including word choice and word order. Up to 4.",
			Tokens: 450,
		}
	}
}

// troubleSpot is a mistake category the learner keeps repeating.
type troubleSpot struct {
	Category string
	Count    int
}

func (s troubleSpot) String() string { return fmt.Sprintf("%s (%d times)", s.Category, s.Count) }

// mnemonicKey is the fact key that holds the image for a category, or "" when
// the category is too long to fit one.
func mnemonicKey(category string) string {
	return conv.NormalizeFactKey(conv.MnemonicPrefix + category)
}

// repeatedTroubleSpots lists what the learner keeps getting wrong and still has
// no mnemonic for. A category seen once is not a pattern, and one that already
// has an image is not asked again.
func repeatedTroubleSpots(d conv.Data) []troubleSpot {
	var out []troubleSpot
	for _, k := range d.TopMistakes(6) {
		if d.Profile.Mistakes[k] < 2 {
			continue
		}
		key := mnemonicKey(k)
		if key == "" || d.Profile.Facts[key] != "" {
			continue
		}
		out = append(out, troubleSpot{Category: k, Count: d.Profile.Mistakes[k]})
		if len(out) == 3 {
			break
		}
	}
	return out
}

// correctionSystem builds the review prompt. The JSON shape is spelled out
// because OpenAI-compatible providers only offer JSON mode (no schema), and
// JSON mode requires the word "JSON".
func correctionSystem(level string, p correctionPolicy, notesLang string, spots []troubleSpot, reviewPersona, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a %s teacher reviewing ONE message written by a learner at CEFR level %s.\n", conv.LanguageName(target), level)
	b.WriteString(p.Rules + "\n")
	b.WriteString("Ignore capitalization, punctuation and obvious typos. If the message is fine, return an empty list.\n")
	if tone := strings.TrimSpace(reviewPersona); tone != "" {
		fmt.Fprintf(&b, "For each mistake give \"original\" (the exact wrong fragment), \"better\" (the corrected fragment), \"category\" (two or three lowercase English words, e.g. \"past simple\", \"articles\", \"word order\") and \"note\" (at most 16 words, written in %s).\n", notesLang)
		b.WriteString("Tone for the notes:\n")
		b.WriteString(tone + "\n")
		b.WriteString("The note must still name what is wrong: the joke rides on the explanation, it never replaces it. Tease the mistake, never the person. No double quotes inside the note.\n")
	} else {
		fmt.Fprintf(&b, "For each mistake give \"original\" (the exact wrong fragment), \"better\" (the corrected fragment), \"category\" (two or three lowercase English words, e.g. \"past simple\", \"articles\", \"word order\") and \"note\" (at most 12 words, written in %s).\n", notesLang)
	}
	b.WriteString("Also list the durable facts the learner states about themselves, each as \"key\" and \"value\".\n")
	b.WriteString("Use the keys name, country, city and hobby for those four; any other lasting fact (job, family, plans) gets a short lowercase key of your own.\n")
	b.WriteString("Only facts written in the message: never invent one, never reuse an example from these instructions, and keep their words.\n")
	if len(spots) > 0 {
		parts := make([]string, len(spots))
		for i, sp := range spots {
			parts[i] = sp.String()
		}
		fmt.Fprintf(&b, "The learner keeps getting these wrong: %s.\n", strings.Join(parts, ", "))
		fmt.Fprintf(&b, "For a correction whose category is one of those, also add \"mnemonic\": a Ramón Campayo memory image in %s — a keyword that sounds like the right form plus one absurd, exaggerated, moving image, at most 20 words. Leave it empty for any other category.\n", notesLang)
	}
	if p.Rewrite {
		b.WriteString("Also add \"rewrite\": the whole message as a native speaker would write it, keeping the learner's meaning and detail, in one sentence.\n")
		b.WriteString("Answer with a JSON object and nothing else, in exactly this shape:\n")
		b.WriteString("{\"corrections\":[{\"original\":\"...\",\"better\":\"...\",\"category\":\"...\",\"note\":\"...\",\"mnemonic\":\"...\"}],\"facts\":[{\"key\":\"...\",\"value\":\"...\"}],\"rewrite\":\"...\"}\n")
		b.WriteString("Never a bare list: with nothing to report, answer {\"corrections\":[],\"facts\":[],\"rewrite\":\"\"}.\n")
	} else {
		b.WriteString("Answer with a JSON object and nothing else, in exactly this shape:\n")
		b.WriteString("{\"corrections\":[{\"original\":\"...\",\"better\":\"...\",\"category\":\"...\",\"note\":\"...\",\"mnemonic\":\"...\"}],\"facts\":[{\"key\":\"...\",\"value\":\"...\"}]}\n")
		b.WriteString("Never a bare list: with nothing to report, answer {\"corrections\":[],\"facts\":[]}.\n")
	}
	b.WriteString("Inside a value, never use double quotes: write the words plainly, without quoting them.")
	return b.String()
}

var correctionSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "corrections": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "original": {"type": "string"},
          "better":   {"type": "string"},
          "category": {"type": "string"},
          "note":     {"type": "string"},
          "mnemonic": {"type": "string"}
        },
        "required": ["original", "better", "category", "note"]
      }
    },
    "rewrite": {"type": "string"},
    "facts": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "key":   {"type": "string"},
          "value": {"type": "string"}
        },
        "required": ["key", "value"]
      }
    }
  },
  "required": ["corrections", "facts"]
}`)

// Correct runs the review pass over one learner message: it records the mistake
// categories and the durable facts in the profile, and returns the review so the
// app can show it. How hard it looks depends on the learner's level. It sends
// only that message (no history), so it is cheap, and it uses the same model as
// the conversation, so nothing else is loaded.
func (t *Tutor) Correct(ctx context.Context, text string) (Review, error) {
	if len(strings.Fields(text)) < 2 {
		return Review{}, nil
	}
	level := "B1"
	if t.store != nil {
		level = t.store.Level()
	}
	policy := policyFor(level)
	var spots []troubleSpot
	if t.store != nil {
		spots = repeatedTroubleSpots(t.store.View())
	}
	raw, err := t.chat(ctx, t.correctionRequest(text, level, policy, spots))
	if err != nil {
		return Review{}, err
	}
	rev, err := parseReview(raw)
	if err != nil {
		return Review{}, err
	}
	if len(rev.Corrections) > policy.Max {
		rev.Corrections = rev.Corrections[:policy.Max]
	}
	// The native rewrite is a C1/C2 feature: never hand one to a beginner.
	if !policy.Rewrite {
		rev.Rewrite = ""
	}
	// A mnemonic is only kept for a trouble spot we asked about, and it is
	// stored so the next turns can reuse the same image.
	asked := map[string]bool{}
	for _, sp := range spots {
		asked[strings.ToLower(sp.Category)] = true
	}
	var mnemonicFacts []Fact
	for i := range rev.Corrections {
		c := &rev.Corrections[i]
		c.Mnemonic = strings.TrimSpace(c.Mnemonic)
		if c.Mnemonic == "" || !asked[strings.ToLower(c.Category)] {
			c.Mnemonic = ""
			continue
		}
		if key := mnemonicKey(c.Category); key != "" {
			mnemonicFacts = append(mnemonicFacts, Fact{Key: key, Value: c.Mnemonic})
		}
	}

	var changed []string
	seen := map[string]bool{}
	err = t.store.Update(func(d *conv.Data) {
		for _, c := range rev.Corrections {
			d.RecordMistake(c.Category)
		}
		record := func(key, value string) {
			if !d.SetFact(key, value) {
				return
			}
			if k := conv.NormalizeFactKey(key); !seen[k] {
				seen[k] = true
				changed = append(changed, k)
			}
		}
		for _, f := range rev.Facts {
			record(f.Key, f.Value)
		}
		// The image is stored silently: the ✧ line already showed it.
		for _, f := range mnemonicFacts {
			d.SetFact(f.Key, f.Value)
		}
		// Safety net for the first turn: an introduction is the one fact worth
		// catching when the model returns nothing, but a known name wins.
		if d.Profile.Facts["name"] == "" {
			if n := nameFromText(text); n != "" {
				record("name", n)
			}
		}
	})
	if err != nil {
		return Review{}, err
	}
	// Report what was stored: SetFact normalizes the key and may cut a long
	// value, so the announcement cannot be built from the raw answer.
	var learned []Fact
	if len(changed) > 0 {
		d := t.store.View()
		for _, k := range changed {
			learned = append(learned, Fact{Key: k, Value: d.Profile.Facts[k]})
		}
	}
	rev.Facts = learned
	return rev, nil
}

// review returns the live review persona.
func (t *Tutor) review() string {
	_, r := t.Personas()
	return r
}

// correctionRequest builds the review request for a level and its policy.
// ProbeCorrections reuses it so that --selftest probes exactly what production
// sends for a new learner.
func (t *Tutor) correctionRequest(text, level string, p correctionPolicy, spots []troubleSpot) llm.Request {
	msgs := []llm.Message{
		{Role: "system", Content: correctionSystem(level, p, t.st.NotesLang, spots, t.review(), t.st.Target)},
		{Role: "user", Content: "Learner message: " + text},
	}
	req := t.request(msgs, 0, p.Tokens)
	req.JSONSchema = correctionSchema
	return req
}

// ProbeCorrections sends the review request for a knowingly wrong sample and
// reports what came back. It never touches the store, so --selftest can run it
// without polluting the learner profile.
func ProbeCorrections(ctx context.Context, lm llm.Chatter, st Settings) ([]Correction, error) {
	t := New(lm, nil, st) // this path never uses the store
	raw, err := t.chat(ctx, t.correctionRequest(ProbeSample, "B1", policyFor("B1"), nil))
	if err != nil {
		return nil, err
	}
	rev, err := parseReview(raw)
	return rev.Corrections, err
}

// ProbeSample is the sentence the correction probe sends: it has one
// unmistakable mistake (past simple), so an empty answer means the model is
// not following the correction prompt.
const ProbeSample = "Yesterday I go to the cinema with my friend."

// parseReview accepts the shapes seen in the wild:
//  1. the documented object,
//  2. a bare list (qwen3.5:9b answers `[]` when it finds nothing),
//  3. an answer that is not valid JSON at all because a value contains
//     unescaped double quotes ("note":"Usa "otra" forma"), which is salvaged
//     field by field. Only corrections survive this path: salvaging facts out
//     of broken JSON is not worth the risk of inventing one.
func parseReview(raw string) (Review, error) {
	body := extractJSON(raw)
	var wrapped struct {
		Corrections []Correction `json:"corrections"`
		Facts       []Fact       `json:"facts"`
		Rewrite     string       `json:"rewrite"`
	}
	err := json.Unmarshal([]byte(body), &wrapped)
	if err == nil {
		return Review{
			Corrections: normalizeCorrections(wrapped.Corrections, maxCorrectionsAllowed),
			Facts:       normalizeFacts(wrapped.Facts),
			Rewrite:     strings.TrimSpace(wrapped.Rewrite),
		}, nil
	}
	if list, ok := decodeList(body); ok {
		return Review{Corrections: normalizeCorrections(list, maxCorrectionsAllowed)}, nil
	}
	if salvaged := salvageCorrections(body); len(salvaged) > 0 {
		return Review{Corrections: normalizeCorrections(salvaged, maxCorrectionsAllowed)}, nil
	}
	return Review{}, fmt.Errorf("could not read corrections: %w", err)
}

// maxCorrectionsAllowed is the most any level may keep; the policy trims it.
const maxCorrectionsAllowed = 6

// maxFactsPerMessage keeps one rambling answer from flooding the profile.
const maxFactsPerMessage = 5

// normalizeFacts drops the empty, the invented and the duplicated, and folds
// the key the same way the store does so two spellings cannot both land.
func normalizeFacts(in []Fact) []Fact {
	var out []Fact
	seen := map[string]bool{}
	for _, f := range in {
		f, ok := normalizeFact(f)
		if !ok {
			continue
		}
		if k := conv.NormalizeFactKey(f.Key); k != "" {
			if seen[k] {
				continue
			}
			seen[k] = true
			f.Key = k
		} else {
			continue
		}
		out = append(out, f)
		if len(out) == maxFactsPerMessage {
			break
		}
	}
	return out
}

func decodeList(body string) ([]Correction, bool) {
	var list []Correction
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return nil, false
	}
	return list, true
}

// salvageCorrections reads the fixed set of fields straight out of the text,
// ignoring JSON boundaries. It only runs when every real parse failed, and
// normalizeCorrections still drops anything that is not a real change.
func salvageCorrections(body string) []Correction {
	var out []Correction
	for _, chunk := range strings.Split(body, `"original"`)[1:] {
		original := salvageValue(chunk, ":", `"better"`)
		better := salvageValue(chunk, `"better"`, `"category"`)
		if original == "" || better == "" {
			continue
		}
		out = append(out, Correction{
			Original: original,
			Better:   better,
			Category: salvageValue(chunk, `"category"`, `"note"`),
			Note:     salvageValue(chunk, `"note"`, ""),
		})
	}
	return out
}

// salvageValue returns the text after start up to the next end marker (or the
// end of the chunk), with the punctuation that belongs to the JSON around it
// trimmed off.
func salvageValue(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	seg := s[i+len(start):]
	if end != "" {
		if k := strings.Index(seg, end); k >= 0 {
			seg = seg[:k]
		}
	}
	seg = strings.TrimSpace(seg)
	seg = strings.TrimPrefix(seg, ":")
	seg = strings.TrimRight(seg, " \t\r\n,]}")
	seg = strings.Trim(seg, `"`)
	return strings.TrimSpace(seg)
}

// normalizeCorrections drops entries that are not a real change and keeps at
// most max.
func normalizeCorrections(in []Correction, max int) []Correction {
	var out []Correction
	for _, c := range in {
		c.Original, c.Better = strings.TrimSpace(c.Original), strings.TrimSpace(c.Better)
		if c.Original == "" || c.Better == "" || strings.EqualFold(c.Original, c.Better) {
			continue
		}
		out = append(out, c)
		if len(out) == max {
			break
		}
	}
	return out
}

// extractJSON tolerates models that wrap the answer in a code fence or in a
// sentence: it returns the text from the first "{" or "[" to the matching
// last closer.
func extractJSON(s string) string {
	i := strings.IndexAny(s, "{[")
	if i < 0 {
		return s
	}
	closer := byte(']')
	if s[i] == '{' {
		closer = '}'
	}
	j := strings.LastIndexByte(s, closer)
	if j < i {
		return s
	}
	return s[i : j+1]
}
