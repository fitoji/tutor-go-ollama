package conv

import "strings"

// LevelProfile is what one CEFR level means for the tutor: the vocabulary to
// aim at, what the learner can already do, the grammar they control and the
// topics that fit. A bare letter tells a small model almost nothing, so the
// prompt carries this block instead.
type LevelProfile struct {
	Name    string   // the level in words: beginner, intermediate...
	Vocab   string   // target vocabulary size
	Can     []string // communication goals
	Grammar []string // structures the learner already controls
	Topics  []string // subjects worth talking about
}

var levelProfiles = map[string]LevelProfile{
	"A1": {
		Name:  "beginner",
		Vocab: "500-1000 words",
		Can: []string{
			"greet people and introduce themselves",
			"ask and answer simple questions about daily life",
			"name people, places and things",
		},
		Grammar: []string{
			"present simple", "be and have got", "articles", "plurals",
			"can and can't", "basic prepositions",
		},
		Topics: []string{"family", "home", "food", "daily routine", "numbers and time"},
	},
	"A2": {
		Name:  "elementary",
		Vocab: "1000-2000 words",
		Can: []string{
			"handle simple transactions",
			"describe past events and future plans",
			"talk about routines and preferences",
		},
		Grammar: []string{
			"past simple", "present continuous", "going to", "comparatives and superlatives",
			"basic modals", "countable and uncountable nouns",
		},
		Topics: []string{"travel", "shopping", "work", "health", "free time", "weather"},
	},
	"B1": {
		Name:  "intermediate",
		Vocab: "2000-3000 words",
		Can: []string{
			"hold everyday conversations",
			"explain opinions and plans",
			"describe experiences in detail",
		},
		Grammar: []string{
			"present perfect", "past continuous", "first and second conditional",
			"passive voice (basic)", "relative clauses", "reported speech (basic)",
			"gerunds and infinitives", "modals of advice and obligation",
		},
		Topics: []string{
			"career and education", "current events", "technology",
			"the environment", "health and lifestyle", "relationships and culture",
		},
	},
	"B2": {
		Name:  "upper intermediate",
		Vocab: "3000-4000 words",
		Can: []string{
			"argue a position",
			"discuss abstract topics",
			"follow nuance and implied meaning",
		},
		Grammar: []string{
			"third and mixed conditionals", "passive in all tenses", "reported speech",
			"modals of deduction", "non-defining relative clauses", "participle clauses",
			"basic inversion",
		},
		Topics: []string{
			"politics and society", "science", "media", "work culture",
			"ethics", "environmental policy",
		},
	},
	"C1": {
		Name:  "advanced",
		Vocab: "4000-8000 words",
		Can: []string{
			"speak fluently and spontaneously",
			"handle academic and professional topics",
			"use idiom and shift register",
		},
		Grammar: []string{
			"inversion", "cleft sentences", "subjunctive after wish and if only",
			"advanced modality", "ellipsis", "discourse markers",
		},
		Topics: []string{
			"specialist and abstract subjects", "culture", "economics",
			"research", "current affairs in depth",
		},
	},
	"C2": {
		Name:  "mastery",
		Vocab: "8000+ words",
		Can: []string{
			"express themselves with near-native precision",
			"control style and register",
			"handle humour and nuance",
		},
		Grammar: []string{
			"the full range, including rare structures and stylistic control",
		},
		Topics: []string{"anything, including nuance, humour and specialised debate"},
	},
}

// LevelOf looks a level up, ignoring case and surrounding spaces.
func LevelOf(level string) (LevelProfile, bool) {
	p, ok := levelProfiles[strings.ToUpper(strings.TrimSpace(level))]
	return p, ok
}

// Levels lists the accepted levels, from easiest to hardest.
func Levels() []string { return []string{"A1", "A2", "B1", "B2", "C1", "C2"} }

// Block renders the profile as the lines the system prompt carries.
func (p LevelProfile) Block() string {
	var sb strings.Builder
	sb.WriteString("\nAt this level (" + p.Name + ") the learner can: " + strings.Join(p.Can, "; ") + ".")
	sb.WriteString("\nVocabulary target: " + p.Vocab + ".")
	sb.WriteString("\nGrammar they already control: " + strings.Join(p.Grammar, ", ") + ".")
	sb.WriteString("\nGood topics: " + strings.Join(p.Topics, ", ") + ".")
	return sb.String()
}
