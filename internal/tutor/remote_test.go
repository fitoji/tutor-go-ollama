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
	"speakeasy/internal/openai"
)

// fakeCompat is an OpenAI-compatible /chat/completions server.
type fakeCompat struct {
	mu      sync.Mutex
	reqs    []map[string]any
	reply   []string
	corr    string
	summary string
}

func (f *fakeCompat) handler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
		http.Error(w, `{"error":{"message":"bad route or key"}}`, http.StatusUnauthorized)
		return
	}
	msgs, _ := req["messages"].([]any)
	first, _ := msgs[0].(map[string]any)["content"].(string)
	var chunks []string
	switch {
	case req["response_format"] != nil:
		chunks = []string{f.corr}
	case strings.HasPrefix(first, "You maintain the memory"):
		chunks = []string{f.summary}
	default:
		chunks = f.reply
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	for _, c := range chunks {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": c}}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"x_groq\":{\"usage\":{\"prompt_tokens\":210,\"completion_tokens\":14}}}\n\ndata: [DONE]\n\n")
}

func (f *fakeCompat) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reqs...)
}

func setupRemote(t *testing.T, f *fakeCompat) (*Tutor, *conv.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	store, err := conv.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	tut := New(openai.New(srv.URL+"/v1", "test-key"), store, Settings{
		Model: "llama-3.3-70b-versatile", NumCtx: 4096, NotesLang: "Spanish", // ThinkField off: remote
	})
	return tut, store
}

func TestTutorOverOpenAICompatibleAPI(t *testing.T) {
	f := &fakeCompat{
		reply: []string{"<think>planning</think>", "Sounds like fun. ", "Where did you go", " afterwards?"},
		corr:  "```json\n{\"corrections\":[{\"original\":\"I go yesterday\",\"better\":\"I went yesterday\",\"category\":\"past simple\",\"note\":\"Usa el pasado.\"}]}\n```",
	}
	tut, store := setupRemote(t, f)
	var screen bytes.Buffer
	v := &sayer{}

	res, err := tut.Reply(context.Background(), "I go yesterday to the beach", &screen, v)
	if err != nil {
		t.Fatal(err)
	}
	if screen.String() != "Sounds like fun. Where did you go afterwards?" {
		t.Errorf("screen %q (think block must not show)", screen.String())
	}
	if fmt.Sprint(v.said) != "[Sounds like fun. Where did you go afterwards?]" {
		t.Errorf("voice %q", v.said)
	}
	if res.PromptTokens != 210 || tut.LastPromptTokens() != 210 {
		t.Errorf("prompt tokens %+v", res)
	}

	corr, _, err := tut.Correct(context.Background(), "I go yesterday to the beach")
	if err != nil || len(corr) != 1 || corr[0].Better != "I went yesterday" {
		t.Fatalf("corrections %+v %v", corr, err)
	}
	if store.View().Profile.Mistakes["past simple"] != 1 {
		t.Error("mistake not recorded")
	}

	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests", len(reqs))
	}
	chat, fix := reqs[0], reqs[1]
	if chat["max_tokens"] != float64(ReplyReserve) || chat["stream"] != true {
		t.Errorf("chat request %v", chat)
	}
	if _, has := chat["response_format"]; has {
		t.Error("chat must not use JSON mode")
	}
	rf, _ := fix["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("corrections must use JSON mode: %v", fix["response_format"])
	}
	sys := fix["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "JSON") {
		t.Error("JSON mode requires the word JSON in the prompt")
	}
	for _, r := range reqs {
		for _, k := range []string{"think", "options", "keep_alive"} {
			if _, has := r[k]; has {
				t.Errorf("Ollama-only field %q sent to a remote provider", k)
			}
		}
	}
}

func TestRemoteFoldAndAuthFailure(t *testing.T) {
	f := &fakeCompat{summary: "Ana is a nurse who loves hiking."}
	tut, store := setupRemote(t, f)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 14; i++ {
			d.AddTurn("user", strings.Repeat("word ", 100))
			d.AddTurn("assistant", strings.Repeat("word ", 100))
		}
	})
	tut.FoldAsync(context.Background(), func(err error) { t.Error(err) })
	if !tut.Wait(2 * time.Second) {
		t.Fatal("fold timed out")
	}
	if store.View().Summary != "Ana is a nurse who loves hiking." {
		t.Errorf("summary %q", store.View().Summary)
	}

	// A wrong key surfaces a clear error and leaves the state consistent.
	bad := New(openai.New("http://127.0.0.1:1/v1", "x"), store, Settings{Model: "m", NumCtx: 4096})
	before := len(store.View().Turns)
	if _, err := bad.Reply(context.Background(), "hello there", nil, nil); err == nil {
		t.Fatal("expected a connection error")
	}
	if len(store.View().Turns) != before {
		t.Error("failed reply left a dangling learner turn")
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:                           `{"a":1}`,
		"```json\n{\"a\":1}\n```":           `{"a":1}`,
		`Sure! {"a":{"b":2}} Hope it helps`: `{"a":{"b":2}}`,
		`[]`:                                `[]`,
		`Sure: [{"a":1}] ok`:                `[{"a":1}]`,
		`no json here`:                      `no json here`,
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q)=%q", in, got)
		}
	}
}
