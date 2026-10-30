// Package speech turns a streamed text reply into audio with a small, bounded
// pipeline: sentence splitter -> synthesis -> playback.
package speech

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Fragments shorter than this are merged into the next sentence, so "Oh!"
	// does not cost a whole synthesis request of its own.
	minSentenceRunes = 18
	// A sentence that grows past this without punctuation is cut at a clause.
	maxSentenceRunes = 220
)

var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true,
	"sr": true, "jr": true, "st": true, "vs": true,
	"e.g": true, "i.e": true, "a.m": true, "p.m": true,
}

// Splitter cuts a stream of text deltas into sentences as soon as each one is
// complete. The zero value is ready to use.
type Splitter struct {
	pending string
	carry   string
}

// Push adds a delta and returns the sentences it completed.
func (s *Splitter) Push(delta string) []string {
	s.pending += delta
	var out []string
	for {
		sent, ok := s.next()
		if !ok {
			break
		}
		if sent == "" {
			continue
		}
		if r := s.emit(sent); r != "" {
			out = append(out, r)
		}
	}
	for utf8.RuneCountInString(s.pending) > maxSentenceRunes {
		rs := []rune(s.pending)
		cut := cutPoint(rs)
		head := strings.TrimSpace(string(rs[:cut]))
		s.pending = strings.TrimLeftFunc(string(rs[cut:]), unicode.IsSpace)
		if r := s.emit(head); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// Flush returns whatever is left at the end of the stream.
func (s *Splitter) Flush() string {
	out := strings.TrimSpace(s.carry + s.pending)
	s.carry, s.pending = "", ""
	return out
}

// emit applies the minimum-length merge.
func (s *Splitter) emit(sent string) string {
	sent = strings.TrimSpace(s.carry + sent)
	s.carry = ""
	if sent == "" {
		return ""
	}
	if utf8.RuneCountInString(sent) < minSentenceRunes {
		s.carry = sent + " "
		return ""
	}
	return sent
}

// next extracts one complete sentence from pending, if there is one.
func (s *Splitter) next() (string, bool) {
	rs := []rune(s.pending)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' {
			return s.take(rs, i, i+1), true
		}
		if !isTerminator(r) {
			continue
		}
		j := i + 1
		for j < len(rs) && isTerminator(rs[j]) {
			j++
		}
		for j < len(rs) && isCloser(rs[j]) {
			j++
		}
		if j >= len(rs) {
			return "", false // cannot confirm the boundary yet
		}
		if !unicode.IsSpace(rs[j]) {
			i = j - 1 // e.g. "3.5", "U.S.A"
			continue
		}
		if r == '.' && j == i+1 && isAbbreviation(rs[:i]) {
			i = j - 1
			continue
		}
		return s.take(rs, j, j), true
	}
	return "", false
}

func (s *Splitter) take(rs []rune, end, resume int) string {
	sent := strings.TrimSpace(string(rs[:end]))
	s.pending = strings.TrimLeftFunc(string(rs[resume:]), unicode.IsSpace)
	return sent
}

func isTerminator(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '…' }

func isCloser(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', ')', ']':
		return true
	}
	return false
}

// isAbbreviation looks at the word just before a period.
func isAbbreviation(before []rune) bool {
	i := len(before)
	for i > 0 && (unicode.IsLetter(before[i-1]) || before[i-1] == '.') {
		i--
	}
	word := strings.Trim(string(before[i:]), ".")
	if word == "" {
		return false
	}
	if abbreviations[strings.ToLower(word)] {
		return true
	}
	// A single capital letter is an initial ("J. Smith"), except "I".
	r := []rune(word)
	return len(r) == 1 && unicode.IsUpper(r[0]) && r[0] != 'I'
}

// cutPoint picks where to cut an over-long run of text: after the last
// clause punctuation, else at the last space, else hard at the limit.
func cutPoint(rs []rune) int {
	limit := maxSentenceRunes
	if limit > len(rs) {
		limit = len(rs)
	}
	for i := limit - 1; i > 0; i-- {
		if (rs[i] == ',' || rs[i] == ';' || rs[i] == ':') && i+1 < len(rs) && unicode.IsSpace(rs[i+1]) {
			return i + 1
		}
	}
	for i := limit - 1; i > 0; i-- {
		if unicode.IsSpace(rs[i]) {
			return i
		}
	}
	return limit
}

// CleanForSpeech removes characters that would be read aloud or that only
// make sense on screen.
func CleanForSpeech(s string) string {
	s = strings.NewReplacer("*", "", "`", "", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
