package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func feed(s *Splitter, chunks ...string) []string {
	var out []string
	for _, c := range chunks {
		out = append(out, s.Push(c)...)
	}
	if tail := s.Flush(); tail != "" {
		out = append(out, tail)
	}
	return out
}

func TestSplitter(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"two sentences", []string{"That sounds really fun. Where did you go?"}, []string{"That sounds really fun.", "Where did you go?"}},
		{"odd chunking", []string{"That sounds", " really fun", ".", " Where did", " you go", "?"}, []string{"That sounds really fun.", "Where did you go?"}},
		{"decimal is not a boundary", []string{"The price is 3.5 euros today. Is that fine?"}, []string{"The price is 3.5 euros today.", "Is that fine?"}},
		{"abbreviation", []string{"I talked to Dr. Smith about the plan. It went well."}, []string{"I talked to Dr. Smith about the plan.", "It went well."}},
		{"initial", []string{"I met J. Smith at the station. He was late."}, []string{"I met J. Smith at the station.", "He was late."}},
		{"short fragment merges forward", []string{"Oh! That is a great idea for the weekend."}, []string{"Oh! That is a great idea for the weekend."}},
		{"quote after period", []string{`She said "see you tomorrow." Then she left the building.`}, []string{`She said "see you tomorrow."`, "Then she left the building."}},
		{"newline is a boundary", []string{"First line is long enough\nsecond line is also long enough"}, []string{"First line is long enough", "second line is also long enough"}},
		{"no terminator waits for flush", []string{"What do you usually do on weekends"}, []string{"What do you usually do on weekends"}},
		{"ellipsis and question run", []string{"Well... I am not sure about that?! Tell me more."}, []string{"Well... I am not sure about that?!", "Tell me more."}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s Splitter
			if got := feed(&s, tc.chunks...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitterDoesNotEmitUnconfirmedBoundary(t *testing.T) {
	var s Splitter
	if got := s.Push("This is a complete sentence."); len(got) != 0 {
		t.Fatalf("emitted before the boundary was confirmed: %q", got)
	}
	if got := s.Push(" Next"); len(got) != 1 || got[0] != "This is a complete sentence." {
		t.Fatalf("got %q", got)
	}
}

func TestSplitterCutsOverlongText(t *testing.T) {
	long := strings.Repeat("word ", 80) // 400 chars, no punctuation
	var s Splitter
	got := feed(&s, long)
	if len(got) < 2 {
		t.Fatalf("expected the run to be cut, got %d piece(s)", len(got))
	}
	for _, p := range got {
		if n := len([]rune(p)); n > maxSentenceRunes {
			t.Errorf("piece of %d runes exceeds the limit", n)
		}
	}
	if strings.Join(got, " ") != strings.TrimSpace(long) {
		t.Error("cutting lost or changed text")
	}
}

func TestCleanForSpeech(t *testing.T) {
	if got := CleanForSpeech("  **Hello** `there`\n  friend  "); got != "Hello there friend" {
		t.Errorf("got %q", got)
	}
}

func TestWAVHeader(t *testing.T) {
	pcm := make([]byte, 480)
	w := WAV(pcm)
	if len(w) != 44+480 {
		t.Fatalf("length %d", len(w))
	}
	if string(w[0:4]) != "RIFF" || string(w[8:12]) != "WAVE" || string(w[36:40]) != "data" {
		t.Error("bad chunk ids")
	}
	if got := binary.LittleEndian.Uint32(w[4:]); got != 36+480 {
		t.Errorf("riff size %d", got)
	}
	if got := binary.LittleEndian.Uint32(w[24:]); got != 24000 {
		t.Errorf("sample rate %d", got)
	}
	if got := binary.LittleEndian.Uint16(w[22:]); got != 1 {
		t.Errorf("channels %d", got)
	}
	if got := binary.LittleEndian.Uint32(w[40:]); got != 480 {
		t.Errorf("data size %d", got)
	}
}

type fakeSynth struct {
	mu     sync.Mutex
	calls  []string
	voices []string
	delay  time.Duration
	fail   map[string]bool
}

func (f *fakeSynth) Speech(ctx context.Context, model, voice, input string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, input)
	f.voices = append(f.voices, voice)
	f.mu.Unlock()
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.fail[input] {
		return nil, errors.New("synth failed: " + input)
	}
	return []byte(input), nil
}

type fakePlayer struct {
	mu     sync.Mutex
	played []string
	delay  time.Duration
	active atomic.Int32
	maxAct atomic.Int32
}

func (p *fakePlayer) Play(ctx context.Context, pcm []byte) error {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		m := p.maxAct.Load()
		if n <= m || p.maxAct.CompareAndSwap(m, n) {
			break
		}
	}
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.Lock()
	p.played = append(p.played, string(pcm))
	p.mu.Unlock()
	return nil
}

func TestUtterancePlaysInOrder(t *testing.T) {
	syn := &fakeSynth{delay: time.Millisecond}
	pl := &fakePlayer{delay: time.Millisecond}
	sp := NewSpeaker(syn, pl, "phonon", "", nil)
	u := sp.Begin()
	want := []string{"one", "two", "three", "four", "five"}
	for _, s := range want {
		u.Say(s)
	}
	u.Finish()
	u.Wait()
	if !reflect.DeepEqual(pl.played, want) {
		t.Errorf("played %q, want %q", pl.played, want)
	}
	if pl.maxAct.Load() != 1 {
		t.Errorf("clips overlapped: %d at once", pl.maxAct.Load())
	}
}

func TestUtterancePipelinesSynthesisWithPlayback(t *testing.T) {
	// 4 sentences, 40 ms to synthesize and 40 ms to play each. Sequential
	// would take ~320 ms; a pipeline needs ~200 ms.
	syn := &fakeSynth{delay: 40 * time.Millisecond}
	pl := &fakePlayer{delay: 40 * time.Millisecond}
	sp := NewSpeaker(syn, pl, "phonon", "", nil)
	start := time.Now()
	u := sp.Begin()
	for _, s := range []string{"a", "b", "c", "d"} {
		u.Say(s)
	}
	u.Finish()
	u.Wait()
	if el := time.Since(start); el > 290*time.Millisecond {
		t.Errorf("took %v: synthesis is not overlapping playback", el)
	}
}

func TestBeginInterruptsPreviousUtterance(t *testing.T) {
	syn := &fakeSynth{delay: time.Millisecond}
	pl := &fakePlayer{delay: 5 * time.Second}
	sp := NewSpeaker(syn, pl, "phonon", "", nil)
	u1 := sp.Begin()
	u1.Say("first clip")
	u1.Finish()
	time.Sleep(30 * time.Millisecond) // let it start playing

	done := make(chan struct{})
	go func() { sp.Begin().Cancel(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Begin did not interrupt the playing clip")
	}
	if len(pl.played) != 0 {
		t.Errorf("an interrupted clip must not count as played: %q", pl.played)
	}
}

func TestSynthErrorReportedOnceAndSpeechContinues(t *testing.T) {
	syn := &fakeSynth{fail: map[string]bool{"bad one": true, "bad two": true}}
	pl := &fakePlayer{}
	var errs atomic.Int32
	sp := NewSpeaker(syn, pl, "phonon", "", func(error) { errs.Add(1) })
	u := sp.Begin()
	for _, s := range []string{"bad one", "good", "bad two", "fine"} {
		u.Say(s)
	}
	u.Finish()
	u.Wait()
	if errs.Load() != 1 {
		t.Errorf("errors reported %d times, want once", errs.Load())
	}
	if !reflect.DeepEqual(pl.played, []string{"good", "fine"}) {
		t.Errorf("played %q", pl.played)
	}
}

func TestSayAfterFinishIsIgnored(t *testing.T) {
	sp := NewSpeaker(&fakeSynth{}, &fakePlayer{}, "phonon", "", nil)
	u := sp.Begin()
	u.Finish()
	u.Say("too late") // must not panic
	u.Finish()        // idempotent
	u.Wait()
}

func TestSayAppliesBackpressure(t *testing.T) {
	// A stalled player must not let queued text grow without limit.
	syn := &fakeSynth{}
	pl := &fakePlayer{delay: time.Hour}
	sp := NewSpeaker(syn, pl, "phonon", "", nil)
	u := sp.Begin()
	defer u.Cancel()
	var accepted atomic.Int32
	go func() {
		for i := 0; i < 100; i++ {
			u.Say("sentence")
			accepted.Add(1)
		}
	}()
	time.Sleep(150 * time.Millisecond)
	// playing(1) + pcm queue(1) + in synthesis(1) + text queue(8) + slack
	if n := accepted.Load(); n > 14 {
		t.Errorf("%d sentences accepted with a stalled player: queue is not bounded", n)
	}
}

// TestSetVoiceAppliesToTheNextSentence is the runtime half of the voice switch:
// audio already queued keeps its voice, the next utterance uses the new one.
func TestSetVoiceAppliesToTheNextSentence(t *testing.T) {
	syn := &fakeSynth{delay: time.Millisecond}
	sp := NewSpeaker(syn, &fakePlayer{}, "phonon", "Freya", nil)

	u := sp.Begin()
	u.Say("First sentence here.")
	u.Finish()
	u.Wait()

	sp.SetVoice("Spencer")
	if got := sp.Voice(); got != "Spencer" {
		t.Errorf("Voice = %q", got)
	}

	u = sp.Begin()
	u.Say("Second sentence here.")
	u.Finish()
	u.Wait()

	syn.mu.Lock()
	got := append([]string(nil), syn.voices...)
	syn.mu.Unlock()
	if len(got) != 2 || got[0] != "Freya" || got[1] != "Spencer" {
		t.Errorf("voices used = %v, want [Freya Spencer]", got)
	}
}
