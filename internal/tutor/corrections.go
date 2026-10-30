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
{"corrections":[{"original":"...","better":"...","category":"...","note":"..."}]}`

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
	msgs := []llm.Message{
		{Role: "system", Content: fmt.Sprintf(correctionPrompt, t.st.NotesLang)},
		{Role: "user", Content: "Learner message: " + text},
	}
	req := t.request(msgs, 0, 300)
	req.JSONSchema = correctionSchema
	raw, err := t.chat(ctx, req)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Corrections []Correction `json:"corrections"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &parsed); err != nil {
		return nil, fmt.Errorf("could not read corrections: %w", err)
	}
	var out []Correction
	for _, c := range parsed.Corrections {
		c.Original, c.Better = strings.TrimSpace(c.Original), strings.TrimSpace(c.Better)
		if c.Original == "" || c.Better == "" || strings.EqualFold(c.Original, c.Better) {
			continue
		}
		out = append(out, c)
		if len(out) == maxCorrections {
			break
		}
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

// extractJSON tolerates models that wrap the object in a code fence or add a
// sentence around it: it returns the text between the first "{" and the last "}".
func extractJSON(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
