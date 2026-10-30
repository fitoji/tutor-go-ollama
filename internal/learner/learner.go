// Package learner decides who is practising and where their conversation
// lives. The app asks once at startup; every learner keeps their own state
// file so levels and corrections never mix.
package learner

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultName is used when there is nobody to ask and no name was remembered.
const DefaultName = "default"

const maxSlug = 40

// Slug turns a display name into a directory name: lower case, letters and
// digits kept, everything else collapsed into a single dash. It returns "" when
// the name has nothing usable, so callers can fall back to DefaultName.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > maxSlug {
		s = strings.Trim(s[:maxSlug], "-")
	}
	return s
}

// StateFile is the state file of one learner, next to the base state path that
// used to hold everybody's profile: <dir(base)>/users/<slug>/state.json.
func StateFile(base, name string) string {
	slug := Slug(name)
	if slug == "" {
		slug = DefaultName
	}
	return filepath.Join(filepath.Dir(base), "users", slug, "state.json")
}

// LastFile is where the last used name is remembered, so the next start can
// offer it instead of asking from scratch.
func LastFile(base string) string {
	return filepath.Join(filepath.Dir(base), "last-user")
}

// Last returns the last used name, or "" when there is none. A missing or
// unreadable file is not an error: it only means we ask.
func Last(base string) string {
	if base == "" {
		return ""
	}
	b, err := os.ReadFile(LastFile(base))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Remember stores the name for the next start. A failure is reported but must
// never block a session.
func Remember(base, name string) error {
	if base == "" || strings.TrimSpace(name) == "" {
		return nil
	}
	path := LastFile(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(name+"\n"), 0o600)
}

// Ask writes the prompt and reads one answer. An empty answer keeps the
// previous name; an unusable one falls back to DefaultName.
func Ask(sc *bufio.Scanner, out io.Writer, last string) string {
	if last != "" {
		fmt.Fprintf(out, "usuario [%s]: ", last)
	} else {
		fmt.Fprint(out, "usuario: ")
	}
	typed := ""
	if sc.Scan() {
		typed = strings.TrimSpace(sc.Text())
	}
	return resolve(typed, last)
}

// resolve applies the fallbacks in one place: what you typed, else the previous
// name, else the default. A name that slugs to nothing is unusable.
func resolve(typed, last string) string {
	name := strings.TrimSpace(typed)
	if name == "" {
		name = strings.TrimSpace(last)
	}
	if Slug(name) == "" {
		return DefaultName
	}
	return name
}

// ImportLegacy copies the profile that used to live in one shared file into a
// learner's directory, once. It only runs while nobody else has a profile yet,
// so a second learner can never inherit someone else's mistakes, and it copies
// instead of moving: the original file stays where it is.
func ImportLegacy(base, target string) (bool, error) {
	if base == "" || target == "" || filepath.Clean(base) == filepath.Clean(target) {
		return false, nil
	}
	if _, err := os.Stat(target); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	b, err := os.ReadFile(base)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Another learner already exists: the shared profile was theirs, not ours.
	usersDir := filepath.Dir(filepath.Dir(target))
	if others, err := filepath.Glob(filepath.Join(usersDir, "*", "state.json")); err == nil && len(others) > 0 {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return false, err
	}
	return true, nil
}
