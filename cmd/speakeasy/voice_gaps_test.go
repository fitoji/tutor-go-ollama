package main

import (
	"bufio"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"speakeasy/internal/ui"
)

type gapRec struct {
	pcm     []byte
	blocked chan struct{}
	started chan struct{}
	stopped atomic.Bool
}

func (g *gapRec) Record(ctx context.Context, max time.Duration) ([]byte, error) {
	if g.started != nil {
		close(g.started)
	}
	if g.blocked != nil {
		<-g.blocked
		return nil, nil
	}
	select {
	case <-ctx.Done():
		g.stopped.Store(true)
	case <-time.After(2 * time.Millisecond):
	}
	return g.pcm, nil
}

type gapTr struct {
	text string
	n    int32
}

func (g *gapTr) Transcribe(ctx context.Context, pcm []byte, lang, prompt string) (string, error) {
	atomic.AddInt32(&g.n, 1)
	return g.text, nil
}

func gapLoud(n int) []byte {
	b := make([]byte, n*2)
	for i := 0; i < len(b); i += 2 {
		b[i], b[i+1] = 0x88, 0x13
	}
	return b
}

// TestRecordTurnStopsAtEOF covers a piped session: EOF is a stop, and the
// buffer already captured is still transcribed.
func TestRecordTurnStopsAtEOF(t *testing.T) {
	rec := &gapRec{pcm: gapLoud(16000)}
	var got string
	if err := recordTurn(context.Background(), bufio.NewScanner(strings.NewReader("")), rec, &gapTr{text: " at eof "}, "en", "", time.Hour, &strings.Builder{}, func(s string) { got = s }, ui.New(false)); err != nil {
		t.Fatal(err)
	}
	if got != "at eof" || !rec.stopped.Load() {
		t.Errorf("text=%q stopped=%v", got, rec.stopped.Load())
	}
}

func TestRecordTurnSilenceSkipsEverything(t *testing.T) {
	tr := &gapTr{text: "must not be used"}
	called := false
	var out strings.Builder
	if err := recordTurn(context.Background(), bufio.NewScanner(strings.NewReader("\n")), &gapRec{pcm: make([]byte, 32000)}, tr, "en", "", time.Second, &out, func(string) { called = true }, ui.New(false)); err != nil {
		t.Fatal(err)
	}
	if called || tr.n != 0 || !strings.Contains(out.String(), "no entendí nada") {
		t.Errorf("called=%v transcribed=%d out=%q", called, tr.n, out.String())
	}
}
