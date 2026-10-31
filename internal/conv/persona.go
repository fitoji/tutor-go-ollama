package conv

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// LoadPersona reads the file that replaces the tutor's style. Everything above
// a line with only "---" is documentation and never reaches the model, so the
// file can explain itself to the person editing it. A missing file is not an
// error: the built-in style is used.
func LoadPersona(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	text := string(b)
	if i := strings.Index(text, "\n---\n"); i >= 0 {
		text = text[i+len("\n---\n"):]
	}
	return strings.TrimSpace(text), nil
}

// ListPersonas returns the names of the persona files in dir, without the .md
// and without the -review companions: those belong to their main persona.
func ListPersonas(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if strings.HasSuffix(name, "-review") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
