// Package ui is the small amount of terminal dressing the app needs: colour by
// role and a spinner for the blocking waits. It is deliberately dependency-free
// and disables itself when the output is not a terminal, so pipes, logs and
// tests keep getting plain text.
package ui

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Style paints text with ANSI codes, or leaves it alone when disabled.
type Style struct{ on bool }

// New returns a Style. Pass false for anything that is not a terminal.
func New(on bool) Style { return Style{on: on} }

// Enabled reports whether the style paints anything. Callers use it to decide
// if an animation makes sense.
func (s Style) Enabled() bool { return s.on }

func (s Style) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string    { return s.wrap("1", t) }
func (s Style) Dim(t string) string     { return s.wrap("2", t) }
func (s Style) Green(t string) string   { return s.wrap("32", t) }
func (s Style) Amber(t string) string   { return s.wrap("33", t) }
func (s Style) Cyan(t string) string    { return s.wrap("36", t) }
func (s Style) Magenta(t string) string { return s.wrap("35", t) }

// Clear wipes the visible screen and homes the cursor. It does not clear the
// scrollback: what you ran before is still there if you scroll up.
func Clear(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprint(w, "\x1b[2J\x1b[H")
}

// spinnerFrames is a braille spinner: it reads well at small sizes and is one
// rune per frame.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Spinner animates one line while something blocks. Start it only around a wait
// that produces no output, and always Stop it before printing anything else.
type Spinner struct {
	out  io.Writer
	msg  string
	on   bool
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Start begins animating. A nil or non-terminal out returns an inert spinner,
// so callers do not need to branch.
func Start(out io.Writer, on bool, msg string) *Spinner {
	s := &Spinner{out: out, msg: msg, on: on, stop: make(chan struct{}), done: make(chan struct{})}
	if !on || out == nil {
		close(s.done)
		return s
	}
	go s.loop()
	return s
}

func (s *Spinner) loop() {
	defer close(s.done)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			fmt.Fprint(s.out, "\r\x1b[K")
			return
		case <-t.C:
			fmt.Fprintf(s.out, "\r%c %s", spinnerFrames[i%len(spinnerFrames)], s.msg)
			i++
		}
	}
}

// Stop clears the line and waits for the animation to end. It is safe to call
// twice.
func (s *Spinner) Stop() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}
