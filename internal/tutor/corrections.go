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
}

const maxCorrections = 3

// The prompt spells out the JSON shape because OpenAI-compatible providers
// only offer JSON mode (no schema), and JSON mode requires the word "JSON".
const correctionPrompt = `You are an English teacher reviewing ONE message written by a learner.
List only real mistakes: grammar, wrong word choice, or phrasing a native speaker would not use.
Ignore capitalization, punctuation and obvious typos. If the message is fine, return an empty list.
For each mistake give "original" (the exact wrong fragment), "better" (the corrected fragment), "category" (two or three lowercase English words, e.g. "past simple", "articles", "word order") and "note" (at most 12 words, written in %s).
Also list the durable facts the learner states about themselves, each as "key" and "value".
Use the keys name, country, city and hobby for those four; any other lasting fact (job, family, plans) gets a short lowercase key of your own.
Only facts written in the message: never invent one, never reuse an example from these instructions, and keep their words.
Answer with a JSON object and nothing else, in exactly this shape:
{"corrections":[{"original":"...","better":"...","category":"...","note":"..."}],"facts":[{"key":"...","value":"..."}]}
Never a bare list: with nothing to report, answer {"corrections":[],"facts":[]}.
Inside a value, never use double quotes: write the words plainly, without quoting them.`

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
          "note":     {"type": "string"}
        },
        "required": ["original", "better", "category", "note"]
      }
    },
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
// categories and the durable facts in the profile, and returns both so the app
// can show them. It sends only that message (no history), so it is cheap, and
// it uses the same model as the conversation, so nothing else is loaded.
func (t *Tutor) Correct(ctx context.Context, text string) ([]Correction, []Fact, error) {
	if len(strings.Fields(text)) < 2 {
		return nil, nil, nil
	}
	raw, err := t.chat(ctx, t.correctionRequest(text))
	if err != nil {
		return nil, nil, err
	}
	corrections, facts, err := parseReview(raw)
	if err != nil {
		return nil, nil, err
	}
	var changed []string
	seen := map[string]bool{}
	err = t.store.Update(func(d *conv.Data) {
		for _, c := range corrections {
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
		for _, f := range facts {
			record(f.Key, f.Value)
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
		return corrections, nil, err
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
	return corrections, learned, nil
}

// correctionRequest builds the request Correct sends. ProbeCorrections reuses
// it so that --selftest probes exactly what production sends.
func (t *Tutor) correctionRequest(text string) llm.Request {
	msgs := []llm.Message{
		{Role: "system", Content: fmt.Sprintf(correctionPrompt, t.st.NotesLang)},
		{Role: "user", Content: "Learner message: " + text},
	}
	req := t.request(msgs, 0, 300)
	req.JSONSchema = correctionSchema
	return req
}

// ProbeCorrections sends the review request for a knowingly wrong sample and
// reports what came back. It never touches the store, so --selftest can run it
// without polluting the learner profile.
func ProbeCorrections(ctx context.Context, lm llm.Chatter, st Settings) ([]Correction, error) {
	t := New(lm, nil, st) // this path never uses the store
	raw, err := t.chat(ctx, t.correctionRequest(ProbeSample))
	if err != nil {
		return nil, err
	}
	corrections, _, err := parseReview(raw)
	return corrections, err
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
func parseReview(raw string) ([]Correction, []Fact, error) {
	body := extractJSON(raw)
	var wrapped struct {
		Corrections []Correction `json:"corrections"`
		Facts       []Fact       `json:"facts"`
	}
	err := json.Unmarshal([]byte(body), &wrapped)
	if err == nil {
		return normalizeCorrections(wrapped.Corrections), normalizeFacts(wrapped.Facts), nil
	}
	if list, ok := decodeList(body); ok {
		return normalizeCorrections(list), nil, nil
	}
	if salvaged := salvageCorrections(body); len(salvaged) > 0 {
		return normalizeCorrections(salvaged), nil, nil
	}
	return nil, nil, fmt.Errorf("could not read corrections: %w", err)
}

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
// most maxCorrections.
func normalizeCorrections(in []Correction) []Correction {
	var out []Correction
	for _, c := range in {
		c.Original, c.Better = strings.TrimSpace(c.Original), strings.TrimSpace(c.Better)
		if c.Original == "" || c.Better == "" || strings.EqualFold(c.Original, c.Better) {
			continue
		}
		out = append(out, c)
		if len(out) == maxCorrections {
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
