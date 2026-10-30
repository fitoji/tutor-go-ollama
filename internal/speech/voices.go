package speech

import "strings"

// voiceAliases maps the words people use to the Phonon voices measured for
// them. The numbers come from synthesizing a sample with every voice and
// taking the median fundamental frequency: the feminine voices sit around
// 170-210 Hz, the masculine ones around 78-158 Hz.
var voiceAliases = map[string]string{
	"femenina":  "Freya", // ~183 Hz
	"female":    "Freya",
	"f":         "Freya",
	"masculina": "Spencer", // ~110 Hz
	"male":      "Spencer",
	"m":         "Spencer",
}

// ResolveVoice turns an alias into a Phonon voice name. Anything that is not an
// alias is taken as an exact name, so `--voice Harper` still works, and an
// empty name keeps the model default.
func ResolveVoice(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if v, ok := voiceAliases[n]; ok {
		return v
	}
	return strings.TrimSpace(name)
}
