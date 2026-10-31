package conv

// languageNames maps the code the app uses to the name the prompt needs.
// Only the languages with a real profile are listed: adding one here without
// its level content would produce a tutor that claims German and teaches
// English grammar.
var languageNames = map[string]string{
	"en": "English",
	"de": "German",
}

// LanguageName returns the display name for a target code, defaulting to
// English: an unknown code must never produce an empty sentence.
func LanguageName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return languageNames["en"]
}
