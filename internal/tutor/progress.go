package tutor

import (
	"context"
	"fmt"
	"strings"

	"speakeasy/internal/conv"
	"speakeasy/internal/llm"
)

// progressPrompt turns dry numbers into one short note. It is a separate call,
// only when the learner asks for /progress, and it never blocks the numbers.
const progressPrompt = `You write a short progress note for a language learner.
Use the numbers you are given; never invent activity. Write at most two sentences, warm and concrete, in %s.
No lists, no markdown, no numbers repeated verbatim.`

// ProgressSummary asks the model for a two-line note about a period. The caller
// owns the timeout and treats any error as "no note".
func (t *Tutor) ProgressSummary(ctx context.Context, p conv.Progress) (string, error) {
	msgs := []llm.Message{
		{Role: "system", Content: fmt.Sprintf(progressPrompt, t.st.NotesLang)},
		{Role: "user", Content: describeProgress(p)},
	}
	text, err := t.chat(ctx, t.request(msgs, 0.4, 200))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// describeProgress renders the numbers as the plain text the model reads.
func describeProgress(p conv.Progress) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Period: %s\n", p.Period)
	fmt.Fprintf(&b, "Active days: %d\n", p.ActiveDays)
	fmt.Fprintf(&b, "Turns: %d\n", p.Turns)
	fmt.Fprintf(&b, "Streak: %d days\n", p.Streak)
	if len(p.TopErrors) > 0 {
		parts := make([]string, len(p.TopErrors))
		for i, e := range p.TopErrors {
			parts[i] = fmt.Sprintf("%s x%d", e.Key, e.Count)
		}
		fmt.Fprintf(&b, "Recurring mistakes: %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "Facts learned: %d\n", p.Facts)
	fmt.Fprintf(&b, "Mnemonics earned: %d\n", p.Mnemonics)
	return b.String()
}
