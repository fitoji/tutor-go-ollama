package conv

import (
	"strings"
	"testing"
)

func TestLanguageName(t *testing.T) {
	cases := map[string]string{"en": "English", "de": "German", "": "English", "fr": "English"}
	for code, want := range cases {
		if got := LanguageName(code); got != want {
			t.Errorf("LanguageName(%q) = %q, want %q", code, got, want)
		}
	}
}

// TestGermanTargetChangesThePrompt is T2: the language reaches the rules, the
// summary and the correction pass.
func TestGermanTargetChangesThePrompt(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	sys := System(d, "", "de")
	for _, want := range []string{"German conversation partner", "Always answer in German"} {
		if !strings.Contains(sys, want) {
			t.Errorf("german prompt should contain %q:\n%s", want, sys)
		}
	}
	// Until the German profile exists (T3), the English grammar must not leak.
	if strings.Contains(sys, "present perfect") {
		t.Errorf("german prompt carries English grammar:\n%s", sys)
	}
	if !strings.Contains(sys, "Reply length: 2-3 sentences") {
		t.Errorf("the length must survive:\n%s", sys)
	}

	sum := SummaryRequest("", nil, "de")
	if !strings.Contains(sum[0].Content, "German tutor") {
		t.Errorf("summary prompt = %q", sum[0].Content)
	}

	// English is still the default and keeps its full profile.
	en := System(d, "", "")
	if !strings.Contains(en, "English conversation partner") || !strings.Contains(en, "present perfect") {
		t.Errorf("english prompt regressed:\n%s", en)
	}
}

// TestGermanA1TeachesFromZero: at A1 the tutor gives a lesson, not a chat.
func TestGermanA1TeachesFromZero(t *testing.T) {
	a1 := System(Data{Profile: Profile{Level: "A1"}}, "", "de")
	for _, want := range []string{"first German lesson", "Sprich nach", "numbers 1-10", "Teach, do not chat"} {
		if !strings.Contains(a1, want) {
			t.Errorf("A1 german prompt should contain %q:\n%s", want, a1)
		}
	}
	// Only A1 German: a B1 learner converses, and English is untouched.
	if b1 := System(Data{Profile: Profile{Level: "B1"}}, "", "de"); strings.Contains(b1, "Sprich nach") {
		t.Errorf("B1 german must not be a lesson:\n%s", b1)
	}
	if en := System(Data{Profile: Profile{Level: "A1"}}, "", "en"); strings.Contains(en, "Sprich nach") {
		t.Errorf("english A1 must not be a German lesson:\n%s", en)
	}
}
