package conv

import (
	"strings"
	"testing"
)

// TestLevelProfilesCoverTheWholeScale pins the six CEFR levels the app accepts,
// each with something to say in every section.
func TestLevelProfilesCoverTheWholeScale(t *testing.T) {
	for _, lvl := range []string{"A1", "A2", "B1", "B2", "C1", "C2"} {
		p, ok := LevelOf("en", lvl)
		if !ok {
			t.Errorf("level %s is missing", lvl)
			continue
		}
		if p.Name == "" || p.Vocab == "" || len(p.Can) == 0 || len(p.Grammar) == 0 || len(p.Topics) == 0 {
			t.Errorf("level %s is incomplete: %+v", lvl, p)
		}
	}
	// Case and spaces are not part of the level.
	if p, ok := LevelOf("en", " b2 "); !ok || p.Name == "" {
		t.Error("LevelOf must trim and ignore case")
	}
	if _, ok := LevelOf("en", "B9"); ok {
		t.Error("an unknown level must not pretend to have a profile")
	}
}

// TestSystemInjectsTheLevelProfile is the point of the feature: the letter is
// not enough, the model gets what that level means.
func TestSystemInjectsTheLevelProfile(t *testing.T) {
	sys := System(Data{Profile: Profile{Level: "B1"}}, "", "en")
	for _, want := range []string{
		"Learner level: B1", "intermediate", "2000-3000",
		"present perfect", "conditional", "topics",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("B1 prompt should mention %q:\n%s", want, sys)
		}
	}
	// A1 must not carry B1's grammar.
	a1 := System(Data{Profile: Profile{Level: "A1"}}, "", "en")
	if strings.Contains(a1, "present perfect") {
		t.Errorf("A1 prompt should not teach B1 grammar:\n%s", a1)
	}
	// Length grows with the level.
	if a1 := System(Data{Profile: Profile{Level: "A1"}}, "", "en"); !strings.Contains(a1, "Reply length: 1-2 sentences") {
		t.Errorf("A1 length missing:\n%s", a1)
	}
	if c1 := System(Data{Profile: Profile{Level: "C1"}}, "", "en"); !strings.Contains(c1, "Reply length: 3-5 sentences") {
		t.Errorf("C1 length missing:\n%s", c1)
	}
	// A level nobody knows still produces a usable prompt, without a block.
	odd := System(Data{Profile: Profile{Level: "B9"}}, "", "en")
	if !strings.Contains(odd, "Learner level: B9") || strings.Contains(odd, "Vocabulary target") || !strings.Contains(odd, "Reply length: 1-3 sentences") {
		t.Errorf("an unknown level must fall back to the bare line:\n%s", odd)
	}
}

// TestGermanProfilesAreGermanAndComplete guards the copy-paste failure: the
// German profile must exist for every level and must not carry English grammar.
func TestGermanProfilesAreGermanAndComplete(t *testing.T) {
	english := []string{"present perfect", "phrasal verbs", "articles"}
	for _, lvl := range Levels() {
		p, ok := LevelOf("de", lvl)
		if !ok {
			t.Errorf("german level %s is missing", lvl)
			continue
		}
		if p.Name == "" || p.Vocab == "" || p.Length == "" || len(p.Can) == 0 || len(p.Grammar) == 0 || len(p.Topics) == 0 {
			t.Errorf("german level %s is incomplete: %+v", lvl, p)
		}
		for _, bad := range english {
			for _, g := range p.Grammar {
				if g == bad {
					t.Errorf("german %s carries English grammar %q", lvl, bad)
				}
			}
		}
	}
	// A language without a profile says so instead of pretending.
	if _, ok := LevelOf("fr", "B1"); ok {
		t.Error("a language without curriculum must not have a profile")
	}
}
