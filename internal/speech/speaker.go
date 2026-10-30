package speech

import (
	"context"
	"sync"
)

// Synth turns text into raw PCM. *ollama.Client satisfies it.
type Synth interface {
	Speech(ctx context.Context, model, voice, input string) ([]byte, error)
}

// Speaker starts one Utterance per assistant reply. Starting a new one stops
// the previous one first, so typing while the tutor is still talking simply
// interrupts it.
type Speaker struct {
	synth  Synth
	player Player
	model  string
	onErr  func(error)
	mu     sync.Mutex
	// vmu guards only the voice, so changing it can never deadlock against
	// Begin/Stop, which hold mu while waiting for the current utterance.
	vmu     sync.Mutex
	voice   string
	current *Utterance
}

func NewSpeaker(s Synth, p Player, model, voice string, onErr func(error)) *Speaker {
	return &Speaker{synth: s, player: p, model: model, voice: voice, onErr: onErr}
}

// SetVoice changes the voice used from the next sentence on. Audio already
// queued keeps the voice it was synthesized with.
func (sp *Speaker) SetVoice(v string) {
	sp.vmu.Lock()
	sp.voice = v
	sp.vmu.Unlock()
}

// Voice returns the voice in use, which may be empty (the model default).
func (sp *Speaker) Voice() string {
	sp.vmu.Lock()
	defer sp.vmu.Unlock()
	return sp.voice
}

// Begin stops whatever is playing and returns a fresh utterance.
func (sp *Speaker) Begin() *Utterance {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.current != nil {
		sp.current.Cancel()
	}
	u := newUtterance(sp)
	sp.current = u
	return u
}

// Stop silences the current utterance and waits until playback has ended.
func (sp *Speaker) Stop() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.current != nil {
		sp.current.Cancel()
		sp.current = nil
	}
}

// Utterance is one spoken reply. Memory is bounded by construction: a text
// queue of a few sentences and a PCM queue of one clip, so at most about
// three clips (playing, waiting, being synthesized) exist at any moment.
type Utterance struct {
	ctx    context.Context
	cancel context.CancelFunc
	text   chan string
	pcm    chan []byte
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	errOnce sync.Once
	sp      *Speaker
}

func newUtterance(sp *Speaker) *Utterance {
	ctx, cancel := context.WithCancel(context.Background())
	u := &Utterance{
		ctx:    ctx,
		cancel: cancel,
		text:   make(chan string, 8),
		pcm:    make(chan []byte, 1),
		sp:     sp,
	}
	u.wg.Add(2)
	go u.synthesize()
	go u.play()
	return u
}

func (u *Utterance) fail(err error) {
	if u.ctx.Err() != nil || u.sp.onErr == nil {
		return
	}
	u.errOnce.Do(func() { u.sp.onErr(err) })
}

func (u *Utterance) synthesize() {
	defer u.wg.Done()
	defer close(u.pcm)
	for {
		select {
		case <-u.ctx.Done():
			return
		case s, ok := <-u.text:
			if !ok {
				return
			}
			b, err := u.sp.synth.Speech(u.ctx, u.sp.model, u.sp.Voice(), s)
			if err != nil {
				u.fail(err)
				continue
			}
			select {
			case u.pcm <- b:
			case <-u.ctx.Done():
				return
			}
		}
	}
}

func (u *Utterance) play() {
	defer u.wg.Done()
	for b := range u.pcm {
		if u.ctx.Err() != nil {
			continue // drain so the synthesizer can exit
		}
		if err := u.sp.player.Play(u.ctx, b); err != nil {
			u.fail(err)
		}
	}
}

// Say queues one sentence. It only blocks if the small queue is full, which
// applies backpressure to the LLM stream instead of buffering without limit.
func (u *Utterance) Say(sentence string) {
	sentence = CleanForSpeech(sentence)
	if sentence == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	select {
	case u.text <- sentence:
	case <-u.ctx.Done():
	}
}

// Finish says no more text is coming. Audio already queued keeps playing.
func (u *Utterance) Finish() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.closed {
		u.closed = true
		close(u.text)
	}
}

// Cancel stops synthesis and playback and waits for both to end.
func (u *Utterance) Cancel() {
	u.cancel()
	u.wg.Wait()
}

// Wait blocks until everything queued has been played (after Finish).
func (u *Utterance) Wait() { u.wg.Wait() }
