package learner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateFilePerLanguage(t *testing.T) {
	base := filepath.Join("/x", ".speakeasy", "state.json")
	for _, tc := range []struct{ name, lang, want string }{
		{"Ana", "", "/x/.speakeasy/users/ana/en/state.json"},
		{"Ana", "DE", "/x/.speakeasy/users/ana/de/state.json"},
		{"Ana María", "de", "/x/.speakeasy/users/ana-mar-a/de/state.json"},
	} {
		if got := StateFile(base, tc.name, tc.lang); got != tc.want {
			t.Errorf("StateFile(%q,%q)=%q want %q", tc.name, tc.lang, got, tc.want)
		}
	}
}

func TestFlatProfileGoesToEnglishOnly(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "state.json")
	flat := filepath.Join(dir, "users", "ana", "state.json")
	if err := os.MkdirAll(filepath.Dir(flat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, []byte(`{"profile":{"level":"C1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := ImportFlatProfile(base, "Ana"); err != nil || !c {
		t.Fatalf("copied=%v err=%v", c, err)
	}
	if _, err := os.Stat(StateFile(base, "Ana", "de")); !os.IsNotExist(err) {
		t.Errorf("german profile must not be created")
	}
	if b, _ := os.ReadFile(flat); string(b) != `{"profile":{"level":"C1"}}` {
		t.Errorf("original modified: %s", b)
	}
	if c, err := ImportFlatProfile(base, "Ana"); err != nil || c {
		t.Errorf("second run copied=%v err=%v", c, err)
	}
}

func TestSharedFileNotImportedTwice(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "state.json")
	if err := os.WriteFile(base, []byte(`{"profile":{"level":"A2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := ImportLegacy(base, StateFile(base, "Ana", "en")); err != nil || !c {
		t.Fatalf("first copied=%v err=%v", c, err)
	}
	if c, err := ImportLegacy(base, StateFile(base, "Beto", "en")); err != nil || c {
		t.Errorf("beto copied=%v err=%v, want no second import", c, err)
	}
}
