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
Answer with a JSON object and nothing else, in exactly this shape:
{"corrections":[{"original":"...","better":"...","category":"...","note":"..."}]}
Never a bare list: with nothing to correct, answer {"corrections":[]}.
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
    }
  },
  "required": ["corrections"]
}`)

// Correct reviews one learner message and records the mistake categories in
// the profile. It sends only that message (no history), so it is cheap, and
// it uses the same model as the conversation, so nothing else is loaded.
func (t *Tutor) Correct(ctx context.Context, text string) ([]Correction, error) {
	if len(strings.Fields(text)) < 2 {
		return nil, nil
	}
	raw, err := t.chat(ctx, t.correctionRequest(text))
	if err != nil {
		return nil, err
	}
	out, err := parseCorrections(raw)
	if err != nil {
		return nil, err
	}
	if len(out) > 0 {
		err = t.store.Update(func(d *conv.Data) {
			for _, c := range out {
				d.RecordMistake(c.Category)
			}
		})
	}
	return out, err
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

// ProbeCorrections sends the correction request for a knowingly wrong sample
// and reports what came back. It never touches the store, so --selftest can
// run it without polluting the learner profile.
func ProbeCorrections(ctx context.Context, lm llm.Chatter, st Settings) ([]Correction, error) {
	t := New(lm, nil, st) // this path never uses the store
	raw, err := t.chat(ctx, t.correctionRequest(ProbeSample))
	if err != nil {
		return nil, err
	}
	return parseCorrections(raw)
}

// ProbeSample is the sentence the correction probe sends: it has one
// unmistakable mistake (past simple), so an empty answer means the model is
// not following the correction prompt.
const ProbeSample = "Yesterday I go to the cinema with my friend."

// parseCorrections accepts the shapes seen in the wild:
//  1. the documented object,
//  2. a bare list (qwen3.5:9b answers `[]` when it finds nothing),
//  3. an answer that is not valid JSON at all because a value contains
//     unescaped double quotes ("note":"Usa "otra" forma"), which is salvaged
//     field by field.
func parseCorrections(raw string) ([]Correction, error) {
	body := extractJSON(raw)
	var wrapped struct {
		Corrections []Correction `json:"corrections"`
	}
	if err := json.Unmarshal([]byte(body), &wrapped); err == nil {
		return normalizeCorrections(wrapped.Corrections), nil
	} else if list, ok := decodeList(body); ok {
		return normalizeCorrections(list), nil
	} else if salvaged := salvageCorrections(body); len(salvaged) > 0 {
		return normalizeCorrections(salvaged), nil
	} else {
		return nil, fmt.Errorf("could not read corrections: %w", err)
	}
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
