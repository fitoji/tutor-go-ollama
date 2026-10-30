package tutor

import (
	"regexp"
	"strings"
	"unicode"
)

// Fact is one durable thing the tutor learned about the learner, such as
// name=Fito. It lives in the learner profile and is rebuilt into the system
// prompt on every request.
type Fact struct {
	Key   string
	Value string
}

func (f Fact) String() string { return f.Key + "=" + f.Value }

// badKeys and badValues are what a model writes when it copies the instructions
// instead of reading the message. None of them is ever a real fact. The keys
// name, country, city and hobby are legitimate, so only the template words are
// refused as keys.
var badKeys = map[string]bool{"key": true, "value": true}

var badValues = map[string]bool{
	"key": true, "value": true, "your_name": true, "yourname": true,
	"name": true, "unknown": true, "not mentioned": true, "none": true,
}

// normalizeFact trims a fact and rejects the empty and the invented shapes, so
// a lazy model cannot fill the profile with noise.
func normalizeFact(f Fact) (Fact, bool) {
	f.Key = strings.TrimSpace(f.Key)
	f.Value = strings.TrimSpace(f.Value)
	f.Value = strings.Trim(f.Value, `"'`)
	if f.Key == "" || f.Value == "" {
		return Fact{}, false
	}
	if badValues[strings.ToLower(f.Value)] || badKeys[strings.ToLower(f.Key)] {
		return Fact{}, false
	}
	// A real value (a name, a city, a hobby) has no underscores or angle
	// brackets; those are how instructions and templates are written.
	if strings.ContainsAny(f.Value, "_<>") || strings.ContainsAny(f.Key, "_<>") {
		return Fact{}, false
	}
	// Models often lower-case a name they read in a sentence. "fito" reads
	// wrong everywhere it is shown, so a name gets its capitals back.
	if strings.EqualFold(f.Key, "name") {
		f.Value = titleCase(f.Value)
	}
	return f, true
}

// titleCase capitalises the first letter of every word, leaving the rest
// alone, so "ana maría" becomes "Ana María".
func titleCase(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		switch {
		case up && unicode.IsLetter(r):
			b.WriteRune(unicode.ToUpper(r))
			up = false
		case r == ' ' || r == '-':
			b.WriteRune(r)
			up = true
		default:
			b.WriteRune(r)
			up = false
		}
	}
	return b.String()
}

// namePatterns are the safety net for the first turn: if the review pass comes
// back empty, an introduction is still worth catching. The trigger is
// case-insensitive but the captured name must be capitalised, so "my name is
// not important" cannot become a fact.
var namePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(?i:my name is) ([A-Z][\p{L}'-]*)`),
	regexp.MustCompile(`\b(?i:call me) ([A-Z][\p{L}'-]*)`),
	regexp.MustCompile(`\b(?i:me llamo) ([A-Z][\p{L}'-]*)`),
}

func nameFromText(s string) string {
	for _, re := range namePatterns {
		if m := re.FindStringSubmatch(s); m != nil {
			return strings.TrimRight(m[1], "'-")
		}
	}
	return ""
}
