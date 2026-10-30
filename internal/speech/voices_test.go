package speech

import "testing"

// TestResolveVoice pins the aliases against the Phonon voices measured for
// them: the median pitch of the feminine ones sits around 170-210 Hz and the
// masculine ones around 78-158 Hz.
func TestResolveVoice(t *testing.T) {
	cases := map[string]string{
		"femenina":       "Freya",
		"Femenina":       "Freya",
		"  female  ":     "Freya",
		"f":              "Freya",
		"masculina":      "Spencer",
		"MALE":           "Spencer",
		"m":              "Spencer",
		"Harper":         "Harper",
		"The Snow Queen": "The Snow Queen",
		"":               "",
	}
	for in, want := range cases {
		if got := ResolveVoice(in); got != want {
			t.Errorf("ResolveVoice(%q) = %q, want %q", in, got, want)
		}
	}
}
