package learner

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Ana":         "ana",
		"  Ana  ":     "ana",
		"Ana María":   "ana-mar-a",
		"ana/maría":   "ana-mar-a",
		"a   b":       "a-b",
		"!!?":         "",
		"  ":          "",
		"User_1":      "user-1",
		"juan--perez": "juan-perez",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	// A name longer than the limit is cut on a dash boundary, never mid-word
	// leaving a trailing dash.
	long := strings.Repeat("ab ", 30)
	if got := Slug(long); len(got) > 40 || strings.HasSuffix(got, "-") {
		t.Errorf("Slug(long) = %q (%d chars)", got, len(got))
	}
}

// TestSlugIgnoresCase pins the reason two spellings of one name must share a
// profile: "Ana" and "ana" are the same person.
func TestSlugIgnoresCase(t *testing.T) {
	if Slug("ANA") != Slug("ana") {
		t.Error("case must not create two profiles")
	}
}

func TestSlugCapsAtFortyCharacters(t *testing.T) {
	if got := Slug("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH"); got != "abcdefghijklmnopqrstuvwxyz0123456789abcd" {
		t.Errorf("Slug = %q, want the first 40 characters", got)
	}
	// A cut that lands on a separator must not leave a trailing dash.
	if got := Slug(strings.Repeat("a", 39) + " bbbb"); strings.HasSuffix(got, "-") || len(got) > 40 {
		t.Errorf("Slug = %q (%d chars), want no trailing dash", got, len(got))
	}
}

func TestAskPromptIsExactlyTheDocumentedLine(t *testing.T) {
	var out strings.Builder
	Ask(bufio.NewScanner(strings.NewReader("Ana\n")), &out, "")
	if out.String() != "usuario: " {
		t.Errorf("prompt = %q, want %q", out.String(), "usuario: ")
	}
	out.Reset()
	Ask(bufio.NewScanner(strings.NewReader("\n")), &out, "Ana")
	if out.String() != "usuario [Ana]: " {
		t.Errorf("prompt = %q, want %q", out.String(), "usuario [Ana]: ")
	}
}

func TestStateFileLivesUnderTheDefaultBase(t *testing.T) {
	base := filepath.Join("/home", "u", ".speakeasy", "state.json")
	want := filepath.Join("/home", "u", ".speakeasy", "users", "ana", "state.json")
	if got := StateFile(base, "Ana"); got != want {
		t.Errorf("StateFile = %q, want %q", got, want)
	}
	// An unusable name still gets a directory instead of an empty path segment.
	want = filepath.Join("/home", "u", ".speakeasy", "users", DefaultName, "state.json")
	if got := StateFile(base, "!!!"); got != want {
		t.Errorf("StateFile(!!!) = %q, want %q", got, want)
	}
}

func TestAskUsesWhatYouType(t *testing.T) {
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader("  Beto \n"))
	if got := Ask(sc, &out, ""); got != "Beto" {
		t.Errorf("Ask = %q, want Beto", got)
	}
	if !strings.Contains(out.String(), "usuario: ") {
		t.Errorf("prompt %q should ask for the user", out.String())
	}
}

func TestAskKeepsTheLastNameOnAnEmptyAnswer(t *testing.T) {
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader("\n"))
	if got := Ask(sc, &out, "Ana"); got != "Ana" {
		t.Errorf("Ask = %q, want the previous name", got)
	}
	if !strings.Contains(out.String(), "[Ana]") {
		t.Errorf("prompt %q should offer the previous name", out.String())
	}
}

func TestAskFallsBackToTheDefaultName(t *testing.T) {
	var out strings.Builder
	if got := Ask(bufio.NewScanner(strings.NewReader("")), &out, ""); got != DefaultName {
		t.Errorf("Ask on EOF = %q, want %q", got, DefaultName)
	}
	out.Reset()
	if got := Ask(bufio.NewScanner(strings.NewReader("\n")), &out, ""); got != DefaultName {
		t.Errorf("Ask on an empty answer = %q, want %q", got, DefaultName)
	}
	// A name with no usable characters must not become an empty directory.
	out.Reset()
	if got := Ask(bufio.NewScanner(strings.NewReader("!!!\n")), &out, ""); got != DefaultName {
		t.Errorf("Ask(!!!) = %q, want %q", got, DefaultName)
	}
}

func TestRememberAndLast(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if got := Last(base); got != "" {
		t.Errorf("Last on a fresh base = %q, want empty", got)
	}
	if err := Remember(base, "Ana"); err != nil {
		t.Fatal(err)
	}
	if got := Last(base); got != "Ana" {
		t.Errorf("Last = %q, want Ana", got)
	}
}

// TestImportLegacyCopiesTheOldProfileOnce is the upgrade path: the profile that
// lived in one shared file is handed to the first learner, and never again, so
// a second person cannot inherit someone else's mistakes.
func TestImportLegacyCopiesTheOldProfileOnce(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	content := `{"profile":{"level":"C1"}}`
	if err := os.WriteFile(base, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	ana := StateFile(base, "Ana")
	copied, err := ImportLegacy(base, ana)
	if err != nil {
		t.Fatal(err)
	}
	if !copied {
		t.Fatal("the old profile should be imported for the first learner")
	}
	got, err := os.ReadFile(ana)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("imported content = %q, want %q", got, content)
	}
	// The original is a copy source, not something we move or delete.
	if _, err := os.Stat(base); err != nil {
		t.Errorf("the old file must survive the import: %v", err)
	}

	beto := StateFile(base, "Beto")
	copied, err = ImportLegacy(base, beto)
	if err != nil {
		t.Fatal(err)
	}
	if copied {
		t.Error("a second learner must not inherit the shared profile")
	}
	if _, err := os.Stat(beto); !os.IsNotExist(err) {
		t.Errorf("no file should be created for the second learner, stat err %v", err)
	}
}

func TestImportLegacyLeavesAnExistingProfileAlone(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(base, []byte(`{"profile":{"level":"C1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := StateFile(base, "Ana")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"profile":{"level":"A2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	copied, err := ImportLegacy(base, target)
	if err != nil {
		t.Fatal(err)
	}
	if copied {
		t.Error("an existing profile must never be overwritten")
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "A2") {
		t.Errorf("target was modified: %q", got)
	}
}

// TestImportLegacyWritesA0600CopyAndNoTempFile pins the file the migration
// leaves behind: private permissions and no half-written temp file.
func TestImportLegacyWritesA0600CopyAndNoTempFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(base, []byte(`{"profile":{"level":"C1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := StateFile(base, "Ana")
	if _, err := ImportLegacy(base, target); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("imported mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

// TestImportLegacyNeverTouchesTheLegacyBytesNorTheSecondLearner is the promise
// that makes the migration safe to run unattended.
func TestImportLegacyNeverTouchesTheLegacyBytesNorTheSecondLearner(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(base, []byte(`{"profile":{"level":"C1","mistakes":{"past simple":4}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportLegacy(base, StateFile(base, "Ana")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("the legacy file must survive the import: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("legacy bytes changed: %q -> %q", before, after)
	}
	beto := StateFile(base, "Beto")
	copied, err := ImportLegacy(base, beto)
	if err != nil || copied {
		t.Errorf("copied=%v err=%v, want no second import", copied, err)
	}
	if _, err := os.Stat(filepath.Dir(beto)); !os.IsNotExist(err) {
		t.Errorf("the second learner's directory must not exist: %v", err)
	}
}

func TestImportLegacyWithoutALegacyFileDoesNothing(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	copied, err := ImportLegacy(base, StateFile(base, "Ana"))
	if err != nil || copied {
		t.Errorf("copied=%v err=%v, want nothing to do", copied, err)
	}
}
