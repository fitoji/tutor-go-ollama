package conv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPersonaStripsTheDocumentationHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tutor.md")
	body := "docs for the human\n---\nYou are a cowboy. Say howdy.\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPersona(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "You are a cowboy. Say howdy." {
		t.Errorf("persona = %q", got)
	}
}

func TestLoadPersonaMissingFileIsNotAnError(t *testing.T) {
	got, err := LoadPersona(filepath.Join(t.TempDir(), "nope.md"))
	if err != nil || got != "" {
		t.Errorf("missing persona: %q %v", got, err)
	}
}

// TestSystemUsesThePersonaAsTheVoice keeps the two halves apart: the invariants
// stay, the style is whatever the file says, and it comes last.
func TestSystemUsesThePersonaAsTheVoice(t *testing.T) {
	sys := System(Data{Profile: Profile{Level: "B1"}}, "You are a cowboy. Say howdy.")
	if !strings.Contains(sys, "Say howdy") {
		t.Errorf("the persona is missing:\n%s", sys)
	}
	if !strings.Contains(sys, "Plain spoken text only") || !strings.Contains(sys, "Do not point out mistakes") {
		t.Errorf("the invariants must survive a persona:\n%s", sys)
	}
	if strings.Contains(sys, "warm, patient and straightforward") {
		t.Errorf("the built-in style should be replaced, not merged:\n%s", sys)
	}
	if i, j := strings.Index(sys, "Learner level"), strings.Index(sys, "Say howdy"); i > j {
		t.Errorf("the style must come after the data:\n%s", sys)
	}
}

// TestSystemFallsBackToTheBuiltInStyle covers the default file being absent.
func TestSystemFallsBackToTheBuiltInStyle(t *testing.T) {
	sys := System(Data{Profile: Profile{Level: "B1"}}, "   ")
	if !strings.Contains(sys, "warm, patient and straightforward") {
		t.Errorf("the default style is missing:\n%s", sys)
	}
}

// TestListPersonasSkipsTheReviewCompanions keeps the menu clean: a -review file
// belongs to its main persona, it is not its own choice.
func TestListPersonasSkipsTheReviewCompanions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"slang.md", "slang-review.md", "cowboy.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ListPersonas(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "cowboy,slang" {
		t.Errorf("names = %v", names)
	}
	if got, err := ListPersonas(filepath.Join(dir, "missing")); err != nil || got != nil {
		t.Errorf("missing dir: %v %v", got, err)
	}
}
