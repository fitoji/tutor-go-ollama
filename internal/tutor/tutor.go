// Package tutor runs one conversation turn: it rebuilds the prompt from the
// stored state, streams the reply to the screen and to the voice, and keeps
// the memory compact with a rolling summary.
package tutor

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"speakeasy/internal/conv"
	"speakeasy/internal/llm"
	"speakeasy/internal/speech"
)

// ReplyReserve is the token budget kept free for the model's answer. It is
// also the hard cap on generated tokens.
const ReplyReserve = 256

type Settings struct {
	Model string
	// NumCtx is the prompt budget the conversation is built to fit. For
	// Ollama it is also sent as num_ctx; remote providers ignore it, but it
	// still keeps each request (and the free-tier token rate) small.
	NumCtx    int
	KeepAlive string // Ollama only
	Think     bool
	// ThinkField says whether to send the "think" field (Ollama only).
	ThinkField bool
	NotesLang  string
}

// Voice receives each finished sentence of the reply.
type Voice interface{ Say(sentence string) }

type nopVoice struct{}

func (nopVoice) Say(string) {}

type Result struct {
	Reply        string
	PromptTokens int // as reported by the provider; 0 if unknown
}

type Tutor struct {
	lm    llm.Chatter
	st    Settings
	store *conv.Store

	foldMu     sync.Mutex
	wg         sync.WaitGroup
	noThink    atomic.Bool
	lastPrompt atomic.Int64
}

func New(lm llm.Chatter, store *conv.Store, st Settings) *Tutor {
	return &Tutor{lm: lm, store: store, st: st}
}

// LastPromptTokens is the prompt size of the most recent reply (0 if the
// provider does not report it).
func (t *Tutor) LastPromptTokens() int { return int(t.lastPrompt.Load()) }

func (t *Tutor) request(msgs []llm.Message, temperature float64, predict int) llm.Request {
	req := llm.Request{
		Model:       t.st.Model,
		Messages:    msgs,
		MaxTokens:   predict,
		Temperature: temperature,
		// For Ollama, num_ctx must be identical in every request to this
		// model: if it changes, Ollama unloads and reloads the model.
		NumCtx:    t.st.NumCtx,
		KeepAlive: t.st.KeepAlive,
	}
	if t.st.ThinkField && !t.noThink.Load() {
		think := t.st.Think
		req.Think = &think
	}
	return req
}

// stream wraps ChatStream with one safety net: some Ollama models reject the
// "think" field, in which case we retry once without it.
func (t *Tutor) stream(ctx context.Context, req llm.Request, fn func(string) error) (llm.Stats, error) {
	st, err := t.lm.ChatStream(ctx, req, fn)
	if err != nil && req.Think != nil && strings.Contains(strings.ToLower(err.Error()), "think") {
		t.noThink.Store(true)
		req.Think = nil
		return t.lm.ChatStream(ctx, req, fn)
	}
	return st, err
}

func (t *Tutor) chat(ctx context.Context, req llm.Request) (string, error) {
	var sb strings.Builder
	_, err := t.stream(ctx, req, func(d string) error {
		sb.WriteString(d)
		return nil
	})
	return sb.String(), err
}

// ProbeReply sends one minimal message through the same path a turn uses,
// including the think-field fallback, and returns the reply and the prompt
// size the provider reported (0 if it did not). It never touches the store,
// so --selftest can run it against a real provider safely.
func ProbeReply(ctx context.Context, lm llm.Chatter, st Settings) (string, int, error) {
	t := New(lm, nil, st) // this path never uses the store
	req := t.request([]llm.Message{{Role: "user", Content: "Reply with the single word: ready"}}, 0, 16)
	var sb strings.Builder
	stats, err := t.stream(ctx, req, func(d string) error {
		sb.WriteString(d)
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	reply := strings.TrimSpace(sb.String())
	if reply == "" {
		return "", stats.PromptTokens, errors.New("the model returned an empty reply")
	}
	return reply, stats.PromptTokens, nil
}

func toMessages(turns []conv.Turn) []llm.Message {
	out := make([]llm.Message, len(turns))
	for i, t := range turns {
		out[i] = llm.Message{Role: t.Role, Content: t.Content}
	}
	return out
}

// Reply handles one learner message. The reply is written to out as it
// arrives and handed to v sentence by sentence. On failure the learner's turn
// is removed again so the stored conversation stays consistent.
func (t *Tutor) Reply(ctx context.Context, user string, out io.Writer, v Voice) (Result, error) {
	if v == nil {
		v = nopVoice{}
	}
	if err := t.store.Update(func(d *conv.Data) { d.AddTurn("user", user) }); err != nil {
		return Result{}, err
	}
	drop := func() { _ = t.store.Update(func(d *conv.Data) { d.DropLastTurn("user") }) }

	msgs := toMessages(conv.Build(t.store.View(), t.st.NumCtx, ReplyReserve))
	var sp speech.Splitter
	var full strings.Builder
	stats, err := t.stream(ctx, t.request(msgs, 0.7, ReplyReserve), func(delta string) error {
		full.WriteString(delta)
		if out != nil {
			_, _ = io.WriteString(out, delta)
		}
		for _, s := range sp.Push(delta) {
			v.Say(s)
		}
		return nil
	})
	if err != nil {
		drop()
		return Result{}, err
	}
	if tail := sp.Flush(); tail != "" {
		v.Say(tail)
	}
	reply := strings.TrimSpace(full.String())
	if reply == "" {
		drop()
		return Result{}, errors.New("the model returned an empty reply (a reasoning model may have spent the whole token budget thinking; use a non-reasoning model)")
	}
	if err := t.store.Update(func(d *conv.Data) { d.AddTurn("assistant", reply) }); err != nil {
		return Result{}, err
	}
	t.lastPrompt.Store(int64(stats.PromptTokens))
	return Result{Reply: reply, PromptTokens: stats.PromptTokens}, nil
}

// FoldAsync compacts old turns into the summary in the background if the
// stored conversation has grown past the threshold. At most one runs at a time.
func (t *Tutor) FoldAsync(ctx context.Context, onErr func(error)) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		if err := t.fold(ctx); err != nil && onErr != nil && ctx.Err() == nil {
			onErr(err)
		}
	}()
}

func (t *Tutor) fold(ctx context.Context) error {
	if !t.foldMu.TryLock() {
		return nil
	}
	defer t.foldMu.Unlock()
	d := t.store.View()
	n := conv.PlanFold(d, t.st.NumCtx)
	if n == 0 {
		return nil
	}
	msgs := toMessages(conv.SummaryRequest(d.Summary, d.Turns[:n]))
	text, err := t.chat(ctx, t.request(msgs, 0.2, 320))
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(text)
	if summary == "" {
		return errors.New("the model returned an empty summary")
	}
	return t.store.Update(func(x *conv.Data) { x.FoldSummary(summary, n, d.Epoch) })
}

// Wait blocks until background work has finished or the timeout passes.
func (t *Tutor) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
