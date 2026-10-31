package conv

import (
	"strings"
	"testing"
)

// TestSystemNeverMentionsATagProtocol guards the design: the app reads
// structured JSON from the review pass, so the system prompt must never teach a
// text tag protocol. A model that is told to write tags leaks them into the
// spoken reply, and nothing parses them any more.
func TestSystemNeverMentionsATagProtocol(t *testing.T) {
	profiles := map[string]Data{
		"empty":   {Profile: Profile{Level: "B1"}},
		"partial": {Profile: Profile{Level: "B1", Facts: map[string]string{"name": "Fito"}}},
		"full": {Profile: Profile{Level: "B1", Facts: map[string]string{
			"name": "Fito", "country": "Argentina", "city": "Madrid", "hobby": "cooking"}}},
	}
	for name, d := range profiles {
		sys := System(d, "", "en")
		for _, bad := range []string{"REMEMBER", "key=value", "tag protocol"} {
			if strings.Contains(sys, bad) {
				t.Errorf("%s prompt mentions %q:\n%s", name, bad, sys)
			}
		}
		if strings.Contains(strings.ToLower(sys), " tag") {
			t.Errorf("%s prompt mentions a tag:\n%s", name, sys)
		}
	}
}

// TestFactLimitsCountRunes pins the limits on characters, not bytes, so
// accented input is not cut early.
func TestFactLimitsCountRunes(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	if !d.SetFact(strings.Repeat("é", 40), "v") {
		t.Error("a 40-rune key must be accepted")
	}
	if d.SetFact(strings.Repeat("é", 41), "v") {
		t.Error("a 41-rune key must be rejected")
	}
	d.SetFact("long", strings.Repeat("é", 250))
	if n := len([]rune(d.Profile.Facts["long"])); n != 200 {
		t.Errorf("value runes = %d, want 200", n)
	}
}
