package conv

import "strings"

// LevelProfile is what one CEFR level means for the tutor: the vocabulary to
// aim at, what the learner can already do, the grammar they control and the
// topics that fit. A bare letter tells a small model almost nothing, so the
// prompt carries this block instead.
type LevelProfile struct {
	Name    string   // the level in words: beginner, intermediate...
	Vocab   string   // target vocabulary size
	Length  string   // how many sentences a reply should have
	Can     []string // communication goals
	Grammar []string // structures the learner already controls
	Topics  []string // subjects worth talking about
}

var englishLevels = map[string]LevelProfile{
	"A1": {
		Length: "1-2 sentences",
		Name:   "beginner",
		Vocab:  "500-1000 words",
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
		Length: "1-2 sentences",
		Name:   "elementary",
		Vocab:  "1000-2000 words",
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
		Length: "2-3 sentences",
		Name:   "intermediate",
		Vocab:  "2000-3000 words",
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
		Length: "2-3 sentences",
		Name:   "upper intermediate",
		Vocab:  "3000-4000 words",
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
		Length: "3-5 sentences",
		Name:   "advanced",
		Vocab:  "4000-8000 words",
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
		Length: "3-5 sentences",
		Name:   "mastery",
		Vocab:  "8000+ words",
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

// germanLevels is the German curriculum from odd/tasks/hito-4-temario-aleman.md.
// The grammar uses German terms on purpose: "Präsens" is what a German course
// calls it, and translating it to "present tense" invites the model to reach
// for English structures that do not exist here.
var germanLevels = map[string]LevelProfile{
	"A1": {
		Length: "1-2 sentences",
		Name:   "beginner",
		Vocab:  "650 words",
		Can: []string{
			"introduce themselves and answer questions about their life",
			"order food, shop and ask for prices",
			"understand simple instructions",
		},
		Grammar: []string{
			"Präsens (regular and irregular)", "Nominativ and Akkusativ", "articles der/die/das",
			"negation with nicht and kein", "possessive articles", "trennbare Verben",
			"Modalverben", "W-Fragen and yes/no questions", "Imperativ", "verb in second position",
		},
		Topics: []string{"family", "home", "food and drink", "shopping", "work (basics)", "daily routine", "countries and languages"},
	},
	"A2": {
		Length: "1-2 sentences",
		Name:   "elementary",
		Vocab:  "1300 words",
		Can: []string{
			"talk about the past and about plans",
			"handle travel, health and appointments",
			"understand short everyday texts",
		},
		Grammar: []string{
			"Perfekt with haben and sein", "Präteritum of sein, haben and modals", "Dativ",
			"Wechselpräpositionen", "Nebensätze with weil, dass and wenn", "Komparativ and Superlativ",
			"adjective declension (nominative and accusative)", "reflexive Verben", "temporal clauses with als and wenn",
		},
		Topics: []string{"travel and holidays", "health and doctor", "housing", "celebrations", "clothes", "weather", "transport"},
	},
	"B1": {
		Length: "2-3 sentences",
		Name:   "intermediate",
		Vocab:  "2400 words",
		Can: []string{
			"tell experiences and plans in detail",
			"argue a simple opinion",
			"deal with offices and paperwork",
		},
		Grammar: []string{
			"narrative Präteritum", "Konjunktiv II for politeness and wishes", "Passiv (Präsens and Präteritum)",
			"Relativsätze (nominative and accusative)", "Genitiv (basics)", "full adjective declension",
			"Verben mit Präpositionen", "Konnektoren deshalb, trotzdem, außerdem", "Nebensätze with obwohl, damit, während",
			"Infinitiv mit zu", "indirect questions",
		},
		Topics: []string{"work and applications", "education", "environment", "society", "offices and paperwork", "consumption", "media"},
	},
	"B2": {
		Length: "2-3 sentences",
		Name:   "upper intermediate",
		Vocab:  "4000-5000 words",
		Can: []string{
			"argue with nuance and follow a discussion",
			"understand specialised texts in their field",
			"write structured texts",
		},
		Grammar: []string{
			"Konjunktiv II (unreal, present and past)", "Passiv with modals", "Konjunktiv I for reported speech (basics)",
			"Relativsätze (dative, genitive, with prepositions)", "Partizipialkonstruktionen", "Nominalisierung",
			"Konnektoren dennoch, allerdings, daher, sodass", "entweder…oder, sowohl…als auch, je…desto",
			"Futur I and II", "prepositions with Genitiv (wegen, trotz, während)", "word order TeKaMoLo",
		},
		Topics: []string{"politics", "economy", "science", "culture", "working life", "environmental policy", "ethics"},
	},
	"C1": {
		Length: "3-5 sentences",
		Name:   "advanced",
		Vocab:  "8000 words",
		Can: []string{
			"speak fluently and spontaneously",
			"use German flexibly for academic and professional purposes",
			"understand long, demanding texts",
		},
		Grammar: []string{
			"Nominalstil", "Funktionsverbgefüge", "full Partizipialkonstruktionen", "Konjunktiv I in reported speech",
			"Passiversatzformen (lassen sich, sein + zu + Infinitiv)", "Konnektoren sofern, indem, wohingegen",
			"marked word order and inversion", "Genitivattribute", "Wortbildung", "Modalpartikeln",
		},
		Topics: []string{"society", "science", "law", "philosophy", "economy", "migration", "technology"},
	},
	"C2": {
		Length: "3-5 sentences",
		Name:   "mastery",
		Vocab:  "16000 words",
		Can: []string{
			"understand with ease everything read or heard",
			"express themselves precisely and with nuance",
			"catch irony and register",
		},
		Grammar: []string{
			"stylistic control of register", "Redewendungen and idiomaticity", "rare structures",
			"ironic nuance", "literary and scientific texts", "archaisms and rhetorical figures",
		},
		Topics: []string{"anything, including humour, irony and specialised debate"},
	},
}

// levelProfiles is the curriculum per language. A language without an entry has
// no profile yet, and the prompt falls back to the bare level plus its length.
var levelProfiles = map[string]map[string]LevelProfile{
	"en": englishLevels,
	"de": germanLevels,
}

// LevelOf looks a level up in one language, ignoring case and spaces. An empty
// language means English.
func LevelOf(lang, level string) (LevelProfile, bool) {
	if lang == "" {
		lang = "en"
	}
	byLevel, ok := levelProfiles[lang]
	if !ok {
		return LevelProfile{}, false
	}
	p, ok := byLevel[strings.ToUpper(strings.TrimSpace(level))]
	return p, ok
}

// Levels lists the accepted levels, from easiest to hardest.
func Levels() []string { return []string{"A1", "A2", "B1", "B2", "C1", "C2"} }

// Block renders the profile as the lines the system prompt carries.
func (p LevelProfile) Block() string {
	var sb strings.Builder
	sb.WriteString("\nAt this level (" + p.Name + ") the learner can: " + strings.Join(p.Can, "; ") + ".")
	sb.WriteString("\nVocabulary target: " + p.Vocab + ".")
	sb.WriteString("\nReply length: " + p.Length + ".")
	sb.WriteString("\nGrammar they already control: " + strings.Join(p.Grammar, ", ") + ".")
	sb.WriteString("\nGood topics: " + strings.Join(p.Topics, ", ") + ".")
	return sb.String()
}
