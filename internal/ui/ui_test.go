package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStylePaintsOnlyWhenEnabled(t *testing.T) {
	on := New(true)
	if got := on.Amber("tutor"); got != "\x1b[33mtutor\x1b[0m" {
		t.Errorf("amber = %q", got)
	}
	off := New(false)
	for _, got := range []string{off.Bold("x"), off.Dim("x"), off.Amber("x"), off.Cyan("x")} {
		if got != "x" {
			t.Errorf("disabled style changed the text: %q", got)
		}
	}
	// Empty text stays empty: no stray escape sequences.
	if on.Bold("") != "" {
		t.Error("empty text must not be painted")
	}
}

// TestSpinnerIsInertOffTerminal keeps pipes and tests free of control codes.
func TestSpinnerIsInertOffTerminal(t *testing.T) {
	var out bytes.Buffer
	s := Start(&out, false, "cargando")
	s.Stop()
	if out.Len() != 0 {
		t.Errorf("an inert spinner wrote %q", out.String())
	}
}

// TestSpinnerAnimatesAndClears is the visible half: it draws the message and
// clears the line on stop.
func TestSpinnerAnimatesAndClears(t *testing.T) {
	var out bytes.Buffer
	s := Start(&out, true, "cargando")
	time.Sleep(150 * time.Millisecond)
	s.Stop()
	got := out.String()
	if !strings.Contains(got, "cargando") {
		t.Errorf("spinner did not draw the message: %q", got)
	}
	if !strings.HasSuffix(got, "\r\x1b[K") {
		t.Errorf("spinner did not clear the line: %q", got)
	}
	// Stopping twice must not panic or hang.
	s.Stop()
}

// TestClearWipesTheScreenOnlyWhenAsked keeps the sequence in one place.
func TestClearWipesTheScreenOnlyWhenAsked(t *testing.T) {
	var out bytes.Buffer
	Clear(&out)
	if out.String() != "\x1b[2J\x1b[H" {
		t.Errorf("clear = %q", out.String())
	}
	// A nil writer must not panic: callers pass one when there is no terminal.
	Clear(nil)
}
