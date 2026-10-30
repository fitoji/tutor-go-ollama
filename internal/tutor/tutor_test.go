package tutor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"speakeasy/internal/conv"
	"speakeasy/internal/ollama"
)

// fakeOllama answers /api/chat like a real server would: plain streamed text
// for conversation, JSON for requests with a format, a summary for the
// summarizer prompt. It records every request.
type fakeOllama struct {
	mu          sync.Mutex
	reqs        []map[string]any
	reply       []string // chunks of the streamed reply
	json        string   // body for structured (format) requests
	summary     string
	failChat    bool
	rejectThink bool
}

func (f *fakeOllama) handler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()

	if f.failChat {
		http.Error(w, "model crashed", http.StatusInternalServerError)
		return
	}
	if _, has := req["think"]; has && f.rejectThink {
		http.Error(w, `{"error":"\"x\" does not support thinking"}`, http.StatusBadRequest)
		return
	}
	var chunks []string
	msgs, _ := req["messages"].([]any)
	first := ""
	if len(msgs) > 0 {
		first, _ = msgs[0].(map[string]any)["content"].(string)
	}
	switch {
	case req["format"] != nil:
		chunks = []string{f.json}
	case strings.HasPrefix(first, "You maintain the memory"):
		chunks = []string{f.summary}
	default:
		chunks = f.reply
	}
	fl := w.(http.Flusher)
	for _, c := range chunks {
		b, _ := json.Marshal(map[string]any{"message": map[string]string{"role": "assistant", "content": c}, "done": false})
		fmt.Fprintf(w, "%s\n", b)
		fl.Flush()
	}
	fmt.Fprintf(w, `{"done":true,"prompt_eval_count":120,"eval_count":9}`+"\n")
}

func (f *fakeOllama) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reqs...)
}

type sayer struct {
	mu   sync.Mutex
	said []string
}

func (s *sayer) Say(x string) { s.mu.Lock(); s.said = append(s.said, x); s.mu.Unlock() }

func setup(t *testing.T, f *fakeOllama, numCtx int) (*Tutor, *conv.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	store, err := conv.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	tut := New(ollama.Chatter{C: ollama.New(srv.URL)}, store, Settings{
		Model: "qwen3:8b", NumCtx: numCtx, KeepAlive: "30m", ThinkField: true, Think: false, NotesLang: "Spanish",
	})
	return tut, store
}

func TestReplyStreamsToScreenAndVoice(t *testing.T) {
	f := &fakeOllama{reply: []string{"That sounds", " really fun", ". Where did", " you go", "?"}}
	tut, store := setup(t, f, 8192)
	var screen bytes.Buffer
	v := &sayer{}

	res, err := tut.Reply(context.Background(), "I went to the beach", &screen, v)
	if err != nil {
		t.Fatal(err)
	}
	if screen.String() != "That sounds really fun. Where did you go?" {
		t.Errorf("screen: %q", screen.String())
	}
	if want := []string{"That sounds really fun.", "Where did you go?"}; fmt.Sprint(v.said) != fmt.Sprint(want) {
		t.Errorf("voice: %q", v.said)
	}
	if res.PromptTokens != 120 || tut.LastPromptTokens() != 120 {
		t.Errorf("prompt tokens: %+v", res)
	}
	d := store.View()
	if len(d.Turns) != 2 || d.Turns[0].Role != "user" || d.Turns[1].Content != res.Reply {
		t.Errorf("stored turns: %+v", d.Turns)
	}

	req := f.requests()[0]
	opts := req["options"].(map[string]any)
	if opts["num_ctx"] != float64(8192) || opts["num_predict"] != float64(ReplyReserve) {
		t.Errorf("options: %v", opts)
	}
	if req["think"] != false {
		t.Errorf("think must be sent explicitly as false, got %v", req["think"])
	}
	if req["keep_alive"] != "30m" {
		t.Errorf("keep_alive: %v", req["keep_alive"])
	}
	msgs := req["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Error("first message must be the system prompt")
	}
	if last := msgs[len(msgs)-1].(map[string]any); last["content"] != "I went to the beach" {
		t.Errorf("last message: %v", last)
	}
}

func TestEveryRequestUsesTheSameNumCtx(t *testing.T) {
	// Different num_ctx values make Ollama reload the model. Replies,
	// summaries and corrections must all agree.
	f := &fakeOllama{reply: []string{"Nice one, tell me more about it please."}, summary: "A summary.", json: `{"corrections":[]}`}
	tut, store := setup(t, f, 2048)
	for i := 0; i < 8; i++ {
		store.Update(func(d *conv.Data) {
			d.AddTurn("user", strings.Repeat("blah ", 120))
			d.AddTurn("assistant", strings.Repeat("okay ", 120))
		})
	}
	if _, err := tut.Reply(context.Background(), "one more thing here", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tut.Correct(context.Background(), "one more thing here"); err != nil {
		t.Fatal(err)
	}
	tut.FoldAsync(context.Background(), nil)
	tut.Wait(2 * time.Second)

	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("expected reply+correction+summary, got %d requests", len(reqs))
	}
	for i, r := range reqs {
		if n := r["options"].(map[string]any)["num_ctx"]; n != float64(2048) {
			t.Errorf("request %d num_ctx=%v", i, n)
		}
		if r["model"] != "qwen3:8b" {
			t.Errorf("request %d uses model %v: a second chat model would double memory", i, r["model"])
		}
	}
}

func TestReplyErrorRemovesLearnerTurn(t *testing.T) {
	f := &fakeOllama{failChat: true}
	tut, store := setup(t, f, 8192)
	if _, err := tut.Reply(context.Background(), "hello there", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if n := len(store.View().Turns); n != 0 {
		t.Errorf("%d turns left after a failed reply", n)
	}
}

func TestEmptyReplyIsAnError(t *testing.T) {
	f := &fakeOllama{reply: []string{"  "}}
	tut, store := setup(t, f, 8192)
	if _, err := tut.Reply(context.Background(), "hello there", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if len(store.View().Turns) != 0 {
		t.Error("learner turn must be dropped")
	}
}

func TestCancelledReplyLeavesConsistentState(t *testing.T) {
	f := &fakeOllama{reply: []string{"Hi"}}
	tut, store := setup(t, f, 8192)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tut.Reply(ctx, "hello there", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if len(store.View().Turns) != 0 {
		t.Error("learner turn must be dropped")
	}
}

func TestThinkRejectedFallsBackOnce(t *testing.T) {
	f := &fakeOllama{reply: []string{"Hello, nice to meet you today."}, rejectThink: true}
	tut, _ := setup(t, f, 8192)
	if _, err := tut.Reply(context.Background(), "hi there", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tut.Reply(context.Background(), "and again", nil, nil); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 3 { // rejected + retry, then a direct one
		t.Fatalf("%d requests", len(reqs))
	}
	if _, has := reqs[1]["think"]; has {
		t.Error("retry must omit think")
	}
	if _, has := reqs[2]["think"]; has {
		t.Error("later requests must remember and omit think")
	}
}

func TestCorrect(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[
		{"original":"I go yesterday","better":"I went yesterday","category":"past simple","note":"Usa pasado."},
		{"original":"same","better":"Same","category":"noop","note":"x"},
		{"original":"","better":"x","category":"empty","note":"x"},
		{"original":"a","better":"b","category":"articles","note":"y"},
		{"original":"c","better":"d","category":"articles","note":"y"},
		{"original":"e","better":"f","category":"articles","note":"y"}]}`}
	tut, store := setup(t, f, 8192)
	got, err := tut.Correct(context.Background(), "I go yesterday to the beach")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxCorrections {
		t.Fatalf("got %d corrections, want the first %d valid ones: %+v", len(got), maxCorrections, got)
	}
	if got[0].Better != "I went yesterday" {
		t.Errorf("%+v", got[0])
	}
	m := store.View().Profile.Mistakes
	if m["past simple"] != 1 || m["articles"] != 2 || m["noop"] != 0 {
		t.Errorf("mistakes recorded: %v", m)
	}

	req := f.requests()[0]
	if req["format"] == nil {
		t.Error("corrections must use structured output")
	}
	if sys := req["messages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(sys, "Spanish") {
		t.Error("notes language missing from the prompt")
	}
	if len(req["messages"].([]any)) != 2 {
		t.Error("corrections must not send the conversation history")
	}
}

// TestCorrectAcceptsABareList covers models that answer with the list itself
// instead of the documented object. qwen3.5:9b returns a bare `[]` when it
// finds nothing, and the old parser failed with a cryptic unmarshal error.
func TestCorrectAcceptsABareList(t *testing.T) {
	cases := []struct {
		name, json string
		notes      int
		want       int
	}{
		{"empty list", `[]`, 0, 0},
		{"list of corrections", `[{"original":"I go yesterday","better":"I went yesterday","category":"past simple","note":"Usa pasado."}]`, 1, 1},
		{"list wrapped in prose", "Sure:\n[{\"original\":\"I am agree\",\"better\":\"I agree\",\"category\":\"verb form\",\"note\":\"Sin 'am'.\"}]\nHope this helps.", 1, 1},
	}
	for _, c := range cases {
		f := &fakeOllama{json: c.json}
		tut, store := setup(t, f, 8192)
		got, err := tut.Correct(context.Background(), "I go yesterday to the beach")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != c.want {
			t.Errorf("%s: got %d corrections, want %d", c.name, len(got), c.want)
		}
		if n := len(store.View().Profile.Mistakes); n != c.notes {
			t.Errorf("%s: recorded %d mistake categories, want %d", c.name, n, c.notes)
		}
	}
}

// TestCorrectRecoversFromUnescapedQuotes reproduces the reported error: a
// model that writes "note":"Usa "otra" forma" produces invalid JSON, and the
// parser saw 'o' where it expected a ','. The correction must still arrive.
func TestCorrectRecoversFromUnescapedQuotes(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[{"original":"I go","better":"I went","category":"past simple","note":"Usa "otra" forma, no "go"."}]}`}
	tut, store := setup(t, f, 8192)

	got, err := tut.Correct(context.Background(), "I go to the cinema yesterday")
	if err != nil {
		t.Fatalf("a recoverable answer must not fail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d corrections, want 1: %+v", len(got), got)
	}
	if got[0].Original != "I go" || got[0].Better != "I went" || got[0].Category != "past simple" {
		t.Errorf("fields mangled: %+v", got[0])
	}
	if !strings.Contains(got[0].Note, `"otra"`) {
		t.Errorf("the note lost its quoted words: %q", got[0].Note)
	}
	if n := store.View().Profile.Mistakes["past simple"]; n != 1 {
		t.Errorf("the category must still be recorded, got %d", n)
	}
}

// TestCorrectRecoversSeveralBrokenEntries keeps the salvage pass honest when
// only some of the entries are malformed.
func TestCorrectRecoversSeveralBrokenEntries(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[
		{"original":"I go","better":"I went","category":"past simple","note":"Usa "otra" forma."},
		{"original":"a office","better":"an office","category":"articles","note":"Antes de vocal."}]}`}
	tut, _ := setup(t, f, 8192)

	got, err := tut.Correct(context.Background(), "I go to a office yesterday")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d corrections, want 2: %+v", len(got), got)
	}
	if got[1].Original != "a office" || got[1].Better != "an office" {
		t.Errorf("second entry mangled: %+v", got[1])
	}
}

func TestCorrectSkipsSingleWordsAndHandlesBadJSON(t *testing.T) {
	f := &fakeOllama{json: `not json`}
	tut, _ := setup(t, f, 8192)
	if got, err := tut.Correct(context.Background(), "hello"); got != nil || err != nil {
		t.Errorf("single word: %v %v", got, err)
	}
	if len(f.requests()) != 0 {
		t.Error("single words must not cost an LLM call")
	}
	if _, err := tut.Correct(context.Background(), "hello there friend"); err == nil {
		t.Error("bad JSON must be reported, not ignored")
	}
}

func TestFoldCompactsOldTurnsIntoSummary(t *testing.T) {
	f := &fakeOllama{summary: "The learner is Ana, a nurse who loves hiking."}
	tut, store := setup(t, f, 2048)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 14; i++ {
			d.AddTurn("user", fmt.Sprintf("user message %d ", i)+strings.Repeat("word ", 100))
			d.AddTurn("assistant", fmt.Sprintf("tutor message %d ", i)+strings.Repeat("word ", 100))
		}
	})
	before := len(store.View().Turns)

	tut.FoldAsync(context.Background(), func(err error) { t.Error(err) })
	if !tut.Wait(2 * time.Second) {
		t.Fatal("fold did not finish")
	}
	d := store.View()
	if d.Summary != "The learner is Ana, a nurse who loves hiking." {
		t.Errorf("summary: %q", d.Summary)
	}
	if len(d.Turns) >= before || d.Turns[0].Role != "user" {
		t.Errorf("turns %d -> %d, first role %s", before, len(d.Turns), d.Turns[0].Role)
	}
	// The newest message must survive verbatim.
	if !strings.HasPrefix(d.Turns[len(d.Turns)-1].Content, "tutor message 13") {
		t.Error("recent turns were lost")
	}
	// The next prompt carries the summary in the system message.
	f.reply = []string{"Great to hear that, Ana."}
	if _, err := tut.Reply(context.Background(), "yes I do", nil, nil); err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	last := reqs[len(reqs)-1]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(last, "nurse who loves hiking") {
		t.Error("summary missing from the next system prompt")
	}
}

func TestFoldDoesNothingForShortConversations(t *testing.T) {
	f := &fakeOllama{}
	tut, store := setup(t, f, 8192)
	store.Update(func(d *conv.Data) { d.AddTurn("user", "hi"); d.AddTurn("assistant", "hello") })
	tut.FoldAsync(context.Background(), nil)
	tut.Wait(time.Second)
	if len(f.requests()) != 0 {
		t.Error("no summary call expected")
	}
}

func TestFoldSurvivesResetDuringSummary(t *testing.T) {
	f := &fakeOllama{summary: "stale"}
	tut, store := setup(t, f, 2048)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 14; i++ {
			d.AddTurn("user", strings.Repeat("word ", 100))
			d.AddTurn("assistant", strings.Repeat("word ", 100))
		}
	})
	// Simulate /reset between planning and applying the fold.
	d := store.View()
	n := conv.PlanFold(d, 2048)
	store.Update(func(x *conv.Data) { x.Reset(); x.AddTurn("user", "new conversation") })
	store.Update(func(x *conv.Data) { x.FoldSummary("stale", n, d.Epoch) })
	if got := store.View(); got.Summary != "" || len(got.Turns) != 1 {
		t.Errorf("stale fold applied: %+v", got)
	}
	_ = tut
}
