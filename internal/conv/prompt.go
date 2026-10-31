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

// tutorRules are the invariants: what makes the reply work on screen and in
// the voice. A persona file may change the tutor's style, never these.
const tutorRules = `You are a friendly %s conversation partner helping a learner practice speaking.
Your replies are read aloud, so:
- Plain spoken text only: no markdown, lists, emojis, asterisks or stage directions.
- End with one simple follow-up question that keeps the conversation going.
- Do not point out mistakes. Another tool shows corrections. If the learner makes an error, just use the correct form naturally in your reply.
- Always answer in %s, even if the learner writes in another language, unless they explicitly ask for a translation.
- Match the learner's level (given below). Prefer common words; explain a hard word only if asked.
- Never exceed 45 words.`

// defaultStyle is the editable half: ~/.speakeasy/tutor.md replaces it, so a
// learner can make the tutor warmer, stricter or funnier without recompiling.
const defaultStyle = `You are a warm, patient and straightforward tutor.
Keep your vocabulary and structures inside the learner's level.
No grammar lectures: correct in the flow, never explain a rule unless asked.
Do not apologise for correcting the learner.
If the learner changes the subject, follow them naturally.`

// System assembles the system prompt: fixed tutor rules, the learner profile
// and the rolling summary. Everything the model must never forget lives here,
// at the top, and is rebuilt on every request.
func System(d Data, persona, target string) string {
	if target == "" {
		target = "en"
	}
	lang := LanguageName(target)
	var sb strings.Builder
	fmt.Fprintf(&sb, tutorRules, lang, lang)
	fmt.Fprintf(&sb, "\n\nLearner level: %s.", d.Profile.Level)
	// The letter alone says nothing: carry what that level means so a small
	// model aims at the right vocabulary, grammar and subjects. Until the
	// target has its own profile (hito 4, T3), only the length is safe: an
	// English grammar list would teach the wrong language.
	if p, ok := LevelOf(d.Profile.Level); ok && target == "en" {
		sb.WriteString(p.Block())
	} else if ok {
		fmt.Fprintf(&sb, "\nReply length: %s.", p.Length)
	} else {
		sb.WriteString("\nReply length: 1-3 sentences.")
	}
	if len(d.Profile.Topics) > 0 {
		fmt.Fprintf(&sb, "\nLearner interests: %s.", strings.Join(d.Profile.Topics, ", "))
	}
	if m := d.TopMistakes(3); len(m) > 0 {
		fmt.Fprintf(&sb, "\nRecurring trouble spots (model them correctly, never lecture): %s.", strings.Join(m, ", "))
	}
	writeFacts(&sb, d)
	if d.Summary != "" {
		fmt.Fprintf(&sb, "\n\nSummary of the conversation so far:\n%s", d.Summary)
	}
	// The style goes last: it is the voice, and the model gives the end of the
	// prompt more weight than the middle.
	sb.WriteString("\n\nHow you talk:\n")
	sb.WriteString(styleOf(persona))
	return sb.String()
}

// styleOf returns the persona the learner wrote, or the built-in one.
func styleOf(persona string) string {
	if p := strings.TrimSpace(persona); p != "" {
		return p
	}
	return defaultStyle
}

// writeFacts adds what the tutor remembers about the learner. On the first
// meetings it also asks for the four basics, one question per turn: that is the
// onboarding that makes the talk personal. The app stores what the learner says
// in the review pass, so the tutor never has to write anything special.
func writeFacts(sb *strings.Builder, d Data) {
	var facts, mnemonics []string
	for _, k := range d.FactKeys() {
		if strings.HasPrefix(k, MnemonicPrefix) {
			mnemonics = append(mnemonics, k)
		} else {
			facts = append(facts, k)
		}
	}
	if len(facts) > 0 {
		sb.WriteString("\n\nWhat you already know about the learner (use it naturally, never recite the list):")
		for _, k := range facts {
			fmt.Fprintf(sb, "\n- %s: %s", k, d.Profile.Facts[k])
		}
	}
	if len(mnemonics) > 0 {
		sb.WriteString("\n\nMnemonics that already worked for them (reuse one only if that trouble spot comes up; never recite them):")
		for _, k := range mnemonics {
			fmt.Fprintf(sb, "\n- %s: %s", strings.TrimPrefix(k, MnemonicPrefix), d.Profile.Facts[k])
		}
	}
	if missing := d.MissingBasics(); len(missing) > 0 {
		fmt.Fprintf(sb, "\n\nYou still do not know their %s. Ask for them one per turn, starting with their name if it is missing, woven into the conversation, before practising the topic.", strings.Join(missing, ", "))
	}
}

// Build returns the exact message list for the next chat request: the system
// prompt followed by as many recent turns as fit in the budget
// (numCtx - replyReserve). The newest turn is always included.
func Build(d Data, persona, target string, numCtx, replyReserve int) []Turn {
	sys := System(d, persona, target)
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

const summaryPrompt = `You maintain the memory of a %s tutor. Merge the previous summary and the new transcript into one updated summary of at most 120 words.
Keep: facts about the learner (job, family, plans, opinions), topics discussed, anything the tutor asked that is still unanswered, and the learner's recurring difficulties.
Drop greetings and filler. Write in third person, plain prose. Output only the summary.`

// SummaryRequest builds the messages for the summarization call.
func SummaryRequest(prev string, old []Turn, target string) []Turn {
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
		{Role: "system", Content: fmt.Sprintf(summaryPrompt, LanguageName(target))},
		{Role: "user", Content: sb.String()},
	}
}
