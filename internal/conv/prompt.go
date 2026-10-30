package conv

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EstimateTokens is a deliberately pessimistic token estimate (3 chars per
// token). Being conservative keeps us under num_ctx, so Ollama never has to
// truncate silently.
func EstimateTokens(s string) int {
	return utf8.RuneCountInString(s)/3 + 1
}

func turnTokens(t Turn) int { return EstimateTokens(t.Content) + 6 }

const tutorPrompt = `You are a friendly English conversation partner helping a learner practice speaking.
Your replies are read aloud, so:
- Write 1 to 3 short, natural sentences. Never more than 45 words.
- Plain spoken text only: no markdown, lists, emojis, asterisks or stage directions.
- End with one simple follow-up question that keeps the conversation going.
- Match the learner's level (given below). Prefer common words; explain a hard word only if asked.
- Do not point out mistakes. Another tool shows corrections. If the learner makes an error, just use the correct form naturally in your reply.
- Always answer in English, even if the learner writes in another language, unless they explicitly ask for a translation.
- Stay on the learner's topic. Remember what they told you earlier in the conversation.`

// System assembles the system prompt: fixed tutor rules, the learner profile
// and the rolling summary. Everything the model must never forget lives here,
// at the top, and is rebuilt on every request.
func System(d Data) string {
	var sb strings.Builder
	sb.WriteString(tutorPrompt)
	fmt.Fprintf(&sb, "\n\nLearner level: %s.", d.Profile.Level)
	if len(d.Profile.Topics) > 0 {
		fmt.Fprintf(&sb, "\nLearner interests: %s.", strings.Join(d.Profile.Topics, ", "))
	}
	if m := d.TopMistakes(3); len(m) > 0 {
		fmt.Fprintf(&sb, "\nRecurring trouble spots (model them correctly, never lecture): %s.", strings.Join(m, ", "))
	}
	if d.Summary != "" {
		fmt.Fprintf(&sb, "\n\nSummary of the conversation so far:\n%s", d.Summary)
	}
	return sb.String()
}

// Build returns the exact message list for the next chat request: the system
// prompt followed by as many recent turns as fit in the budget
// (numCtx - replyReserve). The newest turn is always included.
func Build(d Data, numCtx, replyReserve int) []Turn {
	sys := System(d)
	budget := numCtx - replyReserve - EstimateTokens(sys) - 6
	start := len(d.Turns)
	used := 0
	for start > 0 {
		c := turnTokens(d.Turns[start-1])
		if used+c > budget && start < len(d.Turns) {
			break
		}
		used += c
		start--
	}
	kept := d.Turns[start:]
	// Don't begin the window on a dangling assistant answer.
	if len(kept) > 1 && kept[0].Role == "assistant" {
		kept = kept[1:]
	}
	out := make([]Turn, 0, len(kept)+1)
	out = append(out, Turn{Role: "system", Content: sys})
	return append(out, kept...)
}

// PlanFold decides how many of the oldest turns should be folded into the
// summary. It returns 0 until the stored turns use more than 45% of the
// context; then it keeps roughly the newest 20% verbatim and folds the rest.
func PlanFold(d Data, numCtx int) int {
	total := 0
	for _, t := range d.Turns {
		total += turnTokens(t)
	}
	if total <= numCtx*45/100 {
		return 0
	}
	keepBudget := numCtx * 20 / 100
	keep, used := 0, 0
	for i := len(d.Turns) - 1; i >= 0; i-- {
		c := turnTokens(d.Turns[i])
		if used+c > keepBudget && keep >= 2 {
			break
		}
		used += c
		keep++
	}
	n := len(d.Turns) - keep
	// Keep the verbatim window starting at a learner turn.
	for n < len(d.Turns) && d.Turns[n].Role != "user" {
		n++
	}
	if n >= len(d.Turns) {
		return 0
	}
	return n
}

const summaryPrompt = `You maintain the memory of an English tutor. Merge the previous summary and the new transcript into one updated summary of at most 120 words.
Keep: facts about the learner (job, family, plans, opinions), topics discussed, anything the tutor asked that is still unanswered, and the learner's recurring difficulties.
Drop greetings and filler. Write in third person, plain prose. Output only the summary.`

// SummaryRequest builds the messages for the summarization call.
func SummaryRequest(prev string, old []Turn) []Turn {
	var sb strings.Builder
	if prev == "" {
		sb.WriteString("Previous summary: (none)\n\n")
	} else {
		sb.WriteString("Previous summary:\n" + prev + "\n\n")
	}
	sb.WriteString("New transcript:\n")
	for _, t := range old {
		who := "Learner"
		if t.Role == "assistant" {
			who = "Tutor"
		}
		sb.WriteString(who + ": " + t.Content + "\n")
	}
	return []Turn{
		{Role: "system", Content: summaryPrompt},
		{Role: "user", Content: sb.String()},
	}
}
