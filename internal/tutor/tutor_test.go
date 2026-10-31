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

// TestReviewRecordsFactsAndStoresThem pins the memory path: the review pass is
// where the tutor learns who the learner is, and a fact is announced only when
// it is real news.
func TestReviewRecordsFactsAndStoresThem(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[{"key":"name","value":"Fito"},{"key":"city","value":"Madrid"}]}`}
	tut, store := setup(t, f, 8192)

	rev, err := tut.Correct(context.Background(), "Hi, my name is Fito and I live in Madrid", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(rev.Facts); got != "[name=Fito city=Madrid]" {
		t.Errorf("facts = %v", got)
	}
	d := store.View()
	if d.Profile.Facts["name"] != "Fito" || d.Profile.Facts["city"] != "Madrid" {
		t.Errorf("stored facts = %v", d.Profile.Facts)
	}
	// The same answer twice is not news the second time.
	again, err := tut.Correct(context.Background(), "Hi, my name is Fito and I live in Madrid", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Facts) != 0 {
		t.Errorf("repeated facts announced again: %v", again.Facts)
	}
}

// TestReviewRejectsInventedFacts guards against a model that copies the
// instructions instead of reading the message.
func TestReviewRejectsInventedFacts(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[{"key":"key","value":"value"},{"key":"name","value":"your_name"},{"key":"","value":"x"},{"key":"city","value":""}]}`}
	tut, store := setup(t, f, 8192)

	if rev, err := tut.Correct(context.Background(), "hello there friend", false); err != nil || len(rev.Facts) != 0 {
		t.Errorf("facts=%v err=%v, want nothing stored", rev.Facts, err)
	}
	if n := len(store.View().Profile.Facts); n != 0 {
		t.Errorf("stored %d invented facts", n)
	}
}

// TestReviewCapturesTheNameWhenTheModelForgets is the safety net: an
// introduction is the one fact worth catching without the model's help.
func TestReviewCapturesTheNameWhenTheModelForgets(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, store := setup(t, f, 8192)

	rev, err := tut.Correct(context.Background(), "Hi, my name is Ana", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.View().Profile.Facts["name"]; got != "Ana" {
		t.Errorf("fact name = %q, want Ana", got)
	}
	if fmt.Sprint(rev.Facts) != "[name=Ana]" {
		t.Errorf("facts = %v", rev.Facts)
	}
	// A known name is not overwritten by a later sentence.
	if _, err := tut.Correct(context.Background(), "my name is Bea", false); err != nil {
		t.Fatal(err)
	}
	if got := store.View().Profile.Facts["name"]; got != "Ana" {
		t.Errorf("name changed to %q", got)
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
	if _, err := tut.Correct(context.Background(), "one more thing here", false); err != nil {
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
	rev, err := tut.Correct(context.Background(), "I go yesterday to the beach", false)
	if err != nil {
		t.Fatal(err)
	}
	// The default level is B1, so the review keeps at most four.
	if len(rev.Corrections) != 4 {
		t.Fatalf("got %d corrections, want the first 4 valid ones: %+v", len(rev.Corrections), rev.Corrections)
	}
	if rev.Corrections[0].Better != "I went yesterday" {
		t.Errorf("%+v", rev.Corrections[0])
	}
	m := store.View().Profile.Mistakes
	// Four corrections survive at B1: one past simple and three articles.
	if m["past simple"] != 1 || m["articles"] != 3 || m["noop"] != 0 {
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
		rev, err := tut.Correct(context.Background(), "I go yesterday to the beach", false)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(rev.Corrections) != c.want {
			t.Errorf("%s: got %d corrections, want %d", c.name, len(rev.Corrections), c.want)
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

	rev, err := tut.Correct(context.Background(), "I go to the cinema yesterday", false)
	if err != nil {
		t.Fatalf("a recoverable answer must not fail: %v", err)
	}
	if len(rev.Corrections) != 1 {
		t.Fatalf("got %d corrections, want 1: %+v", len(rev.Corrections), rev.Corrections)
	}
	if rev.Corrections[0].Original != "I go" || rev.Corrections[0].Better != "I went" || rev.Corrections[0].Category != "past simple" {
		t.Errorf("fields mangled: %+v", rev.Corrections[0])
	}
	if !strings.Contains(rev.Corrections[0].Note, `"otra"`) {
		t.Errorf("the note lost its quoted words: %q", rev.Corrections[0].Note)
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

	rev, err := tut.Correct(context.Background(), "I go to a office yesterday", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Corrections) != 2 {
		t.Fatalf("got %d corrections, want 2: %+v", len(rev.Corrections), rev.Corrections)
	}
	if rev.Corrections[1].Original != "a office" || rev.Corrections[1].Better != "an office" {
		t.Errorf("second entry mangled: %+v", rev.Corrections[1])
	}
}

func TestCorrectSkipsSingleWordsAndHandlesBadJSON(t *testing.T) {
	f := &fakeOllama{json: `not json`}
	tut, _ := setup(t, f, 8192)
	if rev, err := tut.Correct(context.Background(), "hello", false); len(rev.Corrections) != 0 || err != nil {
		t.Errorf("single word: %v %v", rev.Corrections, err)
	}
	if len(f.requests()) != 0 {
		t.Error("single words must not cost an LLM call")
	}
	if _, err := tut.Correct(context.Background(), "hello there friend", false); err == nil {
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

// TestCorrectionStrictnessGrowsWithTheLevel pins the ladder: a beginner gets a
// short, gentle list; from B1 the tutor is demanding; C1 and C2 correct
// everything and add a native rewrite.
func TestCorrectionStrictnessGrowsWithTheLevel(t *testing.T) {
	cases := []struct {
		level string
		want  []string
		not   []string
	}{
		{"A1", []string{"CEFR level A1", "gentle", "never more than 2"}, []string{"exhaustive", "rewrite"}},
		{"B2", []string{"CEFR level B2", "demanding", "Up to 4"}, []string{"gentle", "rewrite"}},
		{"C1", []string{"CEFR level C1", "exhaustive", "Up to 6", "rewrite"}, []string{"gentle"}},
		{"C2", []string{"CEFR level C2", "exhaustive", "rewrite"}, nil},
		{"B9", []string{"demanding"}, []string{"exhaustive"}},
	}
	for _, c := range cases {
		f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
		tut, store := setup(t, f, 8192)
		store.Update(func(d *conv.Data) { d.Profile.Level = c.level })

		if _, err := tut.Correct(context.Background(), "I have went to the cinema yesterday", false); err != nil {
			t.Fatal(err)
		}
		msgs := f.requests()[0]["messages"].([]any)
		sys, _ := msgs[0].(map[string]any)["content"].(string)
		for _, want := range c.want {
			if !strings.Contains(sys, want) {
				t.Errorf("%s prompt should contain %q:\n%s", c.level, want, sys)
			}
		}
		for _, not := range c.not {
			if strings.Contains(sys, not) {
				t.Errorf("%s prompt should not contain %q", c.level, not)
			}
		}
	}
}

// TestCorrectionCapFollowsTheLevel keeps the list short for a beginner and
// long for an advanced learner.
func TestCorrectionCapFollowsTheLevel(t *testing.T) {
	cases := map[string]int{"A2": 2, "B1": 4, "C2": 6}
	for level, want := range cases {
		if got := policyFor(level).Max; got != want {
			t.Errorf("policyFor(%s).Max = %d, want %d", level, got, want)
		}
	}
}

// TestReviewReturnsTheRewriteOnlyWhenAsked keeps the native rewrite a C1/C2
// feature: a B1 learner must not get a rewritten sentence they did not ask for.
func TestReviewReturnsTheRewriteOnlyWhenAsked(t *testing.T) {
	body := `{"corrections":[],"facts":[],"rewrite":"I went to the cinema yesterday."}`
	for level, want := range map[string]bool{"B1": false, "C1": true} {
		f := &fakeOllama{json: body}
		tut, store := setup(t, f, 8192)
		store.Update(func(d *conv.Data) { d.Profile.Level = level })
		rev, err := tut.Correct(context.Background(), "I have went to the cinema yesterday", false)
		if err != nil {
			t.Fatal(err)
		}
		if want && rev.Rewrite != "I went to the cinema yesterday." {
			t.Errorf("%s rewrite = %q", level, rev.Rewrite)
		}
		if !want && rev.Rewrite != "" {
			t.Errorf("%s must not return a rewrite, got %q", level, rev.Rewrite)
		}
	}
}

// TestCorrectionProbeDefaultsToB1 keeps --selftest working without a store.
func TestCorrectionProbeDefaultsToB1(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	if _, err := ProbeCorrections(context.Background(), ollama.Chatter{C: ollama.New(srv.URL)}, Settings{Model: "m", NumCtx: 8192}); err != nil {
		t.Fatal(err)
	}
	msgs := f.requests()[0]["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "CEFR level B1") {
		t.Errorf("a probe without a profile must use B1:\n%s", sys)
	}
}

// TestRepeatedCategoriesGetAMnemonic is the Campayo hook: only a trouble spot
// that keeps coming back earns an image, and the image is remembered for next
// time.
func TestRepeatedCategoriesGetAMnemonic(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[{"original":"I go yesterday","better":"I went yesterday","category":"past simple","note":"Usa pasado.","mnemonic":"Un tren que ya pasó se aleja y no vuelve."}],"facts":[]}`}
	tut, store := setup(t, f, 8192)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 3; i++ {
			d.RecordMistake("past simple")
		}
	})

	rev, err := tut.Correct(context.Background(), "I go yesterday to the beach", false)
	if err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "past simple (3 times)") {
		t.Errorf("the prompt should name the repeated category:\n%s", sys)
	}
	if !strings.Contains(sys, "mnemonic") {
		t.Errorf("the prompt should ask for a mnemonic:\n%s", sys)
	}
	if len(rev.Corrections) != 1 || rev.Corrections[0].Mnemonic == "" {
		t.Fatalf("the mnemonic did not survive parsing: %+v", rev.Corrections)
	}
	if got := store.View().Profile.Facts["mnemonic-past-simple"]; got != "Un tren que ya pasó se aleja y no vuelve." {
		t.Errorf("stored mnemonic = %q", got)
	}
}

// TestNewCategoriesGetNoMnemonic keeps the noise out: a mistake seen once is
// not a trouble spot yet.
func TestNewCategoriesGetNoMnemonic(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[{"original":"I go yesterday","better":"I went yesterday","category":"past simple","note":"Usa pasado.","mnemonic":"Inventado."}],"facts":[]}`}
	tut, store := setup(t, f, 8192)

	rev, err := tut.Correct(context.Background(), "I go yesterday to the beach", false)
	if err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(sys, "keeps getting these wrong") {
		t.Errorf("a single mistake is not a pattern:\n%s", sys)
	}
	if rev.Corrections[0].Mnemonic != "" {
		t.Errorf("the mnemonic must be dropped: %q", rev.Corrections[0].Mnemonic)
	}
	if _, ok := store.View().Profile.Facts["mnemonic-past-simple"]; ok {
		t.Error("an unwanted mnemonic was stored")
	}
}

// TestMnemonicIsAskedOnlyOnce stops the tutor from reinventing the same image
// every turn.
func TestMnemonicIsAskedOnlyOnce(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, store := setup(t, f, 8192)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 5; i++ {
			d.RecordMistake("past simple")
		}
		d.SetFact("mnemonic-past-simple", "Un tren que ya pasó.")
	})

	if _, err := tut.Correct(context.Background(), "I go yesterday to the beach", false); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	// It is still reported as recurring (so callbacks are real), but it must not
	// be asked for a mnemonic again.
	if !strings.Contains(sys, "past simple (5 times)") {
		t.Errorf("a recurring category must still be listed:\n%s", sys)
	}
	if strings.Contains(sys, "has no mnemonic yet (past simple)") {
		t.Errorf("a category with a mnemonic must not be asked again:\n%s", sys)
	}
}

// TestReviewPersonaSetsTheToneOfTheNotes pins the second persona: the tutor's
// voice does not change the correction pass, a separate file does.
func TestReviewPersonaSetsTheToneOfTheNotes(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, _ := setup(t, f, 8192)
	tut.SetPersonas("", "Roast them affectionately.")

	if _, err := tut.Correct(context.Background(), "I have went to the cinema", false); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	for _, want := range []string{"Roast them affectionately", "at most 16 words", "Tease the mistake, never the person"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the review prompt should contain %q:\n%s", want, sys)
		}
	}
}

// TestNoReviewPersonaKeepsTheNeutralNote keeps the default a teacher, not a
// heckler.
func TestNoReviewPersonaKeepsTheNeutralNote(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, _ := setup(t, f, 8192)

	if _, err := tut.Correct(context.Background(), "I have went to the cinema", false); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(sys, "Tone for the notes") {
		t.Errorf("a neutral review must not carry a tone block:\n%s", sys)
	}
	if !strings.Contains(sys, "at most 12 words") {
		t.Errorf("the neutral note keeps its 12-word limit:\n%s", sys)
	}
}

// TestSetPersonasChangesTheNextPrompt is the runtime switch seen from the model
// side: the next reply must carry the new style.
func TestSetPersonasChangesTheNextPrompt(t *testing.T) {
	f := &fakeOllama{reply: []string{"Hey there."}}
	tut, _ := setup(t, f, 8192)
	tut.SetPersonas("You are a surfer. Say dude.", "Roast them.")

	if _, err := tut.Reply(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "Say dude") {
		t.Errorf("the new persona is missing:\n%s", sys)
	}
	if voice, review := tut.Personas(); !strings.Contains(voice, "surfer") || review != "Roast them." {
		t.Errorf("Personas = %q / %q", voice, review)
	}
}

// TestGermanCorrectionsNameTheLanguage keeps the review pass in the target
// language: a German learner must not be reviewed by an English teacher.
func TestGermanCorrectionsNameTheLanguage(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, _ := setup(t, f, 8192)
	tut.st.Target = "de"

	if _, err := tut.Correct(context.Background(), "Ich habe gestern ins Kino gegangen", false); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "German teacher") {
		t.Errorf("review prompt = %s", sys)
	}
}

// TestProgressSummarySendsTheNumbers is T4: the note is built from the stats,
// never invented.
func TestProgressSummarySendsTheNumbers(t *testing.T) {
	f := &fakeOllama{reply: []string{"  Buena semana, Fito.  "}}
	tut, _ := setup(t, f, 8192)
	p := conv.Progress{Period: "week", ActiveDays: 4, Turns: 20, Streak: 3,
		TopErrors: []conv.MistakeCount{{Key: "past simple", Count: 5}},
		Facts:     1, Mnemonics: 2}

	note, err := tut.ProgressSummary(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if note != "Buena semana, Fito." {
		t.Errorf("note = %q", note)
	}
	msgs := f.requests()[0]["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "progress note") || !strings.Contains(sys, "Spanish") {
		t.Errorf("system prompt = %q", sys)
	}
	user := msgs[1].(map[string]any)["content"].(string)
	for _, want := range []string{"Turns: 20", "Streak: 3 days", "past simple x5", "Mnemonics earned: 2"} {
		if !strings.Contains(user, want) {
			t.Errorf("numbers missing %q:\n%s", want, user)
		}
	}
}

// TestVoiceTurnsWarnAboutMishearings: a transcription glitch is not a language
// mistake, and the review pass must not roast the learner for it.
func TestVoiceTurnsWarnAboutMishearings(t *testing.T) {
	f := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut, _ := setup(t, f, 8192)
	if _, err := tut.Correct(context.Background(), "I went to the cinema", true); err != nil {
		t.Fatal(err)
	}
	sys := f.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "transcribed from speech") {
		t.Errorf("a voice turn should warn about mis-hearings:\n%s", sys)
	}

	// A typed turn says nothing about speech.
	f2 := &fakeOllama{json: `{"corrections":[],"facts":[]}`}
	tut2, _ := setup(t, f2, 8192)
	if _, err := tut2.Correct(context.Background(), "I went to the cinema", false); err != nil {
		t.Fatal(err)
	}
	sys2 := f2.requests()[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(sys2, "transcribed from speech") {
		t.Errorf("a typed turn must not mention speech:\n%s", sys2)
	}
}
