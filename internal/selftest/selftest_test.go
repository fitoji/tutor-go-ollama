package selftest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"speakeasy/internal/config"
	"speakeasy/internal/ollama"
	"speakeasy/internal/openai"
	"speakeasy/internal/tutor"
)

// fake is one server that answers both the Ollama API and the OpenAI-
// compatible API, so a probe run can be inspected path by path.
type fake struct {
	mu             sync.Mutex
	paths          []string
	version        string
	tags           []string
	pcm            []byte
	chatChunks     []string
	correctionJSON string
	chatStatus     int
	correctionBody string
}

func newFake() *fake {
	return &fake{
		version:        "0.40.9",
		tags:           []string{"phonon:latest", "qwen3:8b"},
		pcm:            make([]byte, 4800),
		chatChunks:     []string{"Ready"},
		correctionJSON: `{"corrections":[{"original":"I go","better":"I went","category":"past simple","note":"usa el pasado"}]}`,
	}
}

func (f *fake) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()

	switch r.URL.Path {
	case "/api/version":
		fmt.Fprintf(w, `{"version":%q}`, f.version)
	case "/api/tags":
		var models []map[string]string
		for _, t := range f.tags {
			models = append(models, map[string]string{"name": t})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	case "/v1/audio/speech":
		_, _ = w.Write(f.pcm)
	case "/api/chat":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.chatStatus != 0 {
			http.Error(w, "model crashed", f.chatStatus)
			return
		}
		chunks := f.chatChunks
		if req["format"] != nil {
			chunks = []string{f.correctionJSON}
		}
		fl := w.(http.Flusher)
		for _, c := range chunks {
			b, _ := json.Marshal(map[string]any{"message": map[string]string{"role": "assistant", "content": c}, "done": false})
			fmt.Fprintf(w, "%s\n", b)
			fl.Flush()
		}
		fmt.Fprintf(w, `{"done":true,"prompt_eval_count":120,"eval_count":2}`+"\n")
	case "/chat/completions":
		if f.chatStatus != 0 {
			http.Error(w, "model crashed", f.chatStatus)
			return
		}
		body, _ := io.ReadAll(r.Body)
		content := strings.Join(f.chatChunks, "")
		if strings.Contains(string(body), "json_object") {
			content = f.correctionJSON
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]string{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": 33, "completion_tokens": 2},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	default:
		http.NotFound(w, r)
	}
}

func (f *fake) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return srv
}

func localConfig(host string) config.Config {
	return config.Config{
		Host: host, Provider: "ollama", LLM: "qwen3:8b", TTS: "phonon",
		NumCtx: 8192, Corrections: true, NotesLang: "Spanish",
	}
}

func remoteConfig(base string) config.Config {
	return config.Config{
		Provider: "groq", LLM: "llama-3.3-70b-versatile", TTS: "phonon",
		APIBase: base, APIKey: "k", NumCtx: 4096, Corrections: true, NotesLang: "Spanish",
	}
}

func settings(cfg config.Config) tutor.Settings {
	return tutor.Settings{Model: cfg.LLM, NumCtx: cfg.NumCtx, NotesLang: cfg.NotesLang, ThinkField: !cfg.Remote()}
}

// TestRunLocalEverythingOK is the happy path with a local model and voice.
func TestRunLocalEverythingOK(t *testing.T) {
	f := newFake()
	srv := f.server(t)
	cfg := localConfig(srv.URL)
	cl := ollama.New(srv.URL)

	var out strings.Builder
	err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: cl, Chat: ollama.Chatter{C: cl}, Settings: settings(cfg),
	})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"[ok]", "0.40.9", "phonon:latest", "0.1 s", "Ready", "1 corrections"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[FAIL]") {
		t.Errorf("unexpected failure line:\n%s", got)
	}
	if n := strings.Count(got, "[ok]"); n != 5 {
		t.Errorf("expected 5 passing probes, got %d:\n%s", n, got)
	}
}

// TestRunRemoteMutedSkipsOllama proves that a remote provider with --mute does
// not touch Ollama at all: that combination is documented as needing no Ollama.
func TestRunRemoteMutedSkipsOllama(t *testing.T) {
	f := newFake()
	srv := f.server(t)
	cfg := remoteConfig(srv.URL)
	cfg.Mute = true

	var out strings.Builder
	err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: openai.New(srv.URL, "k"), Settings: settings(cfg),
	})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	for _, p := range f.seen() {
		if strings.HasPrefix(p, "/api/") || p == "/v1/audio/speech" {
			t.Errorf("muted remote run must not call Ollama, called %s", p)
		}
	}
	if n := strings.Count(out.String(), "[ok]"); n != 2 {
		t.Errorf("expected 2 probes (chat, corrections), got %d:\n%s", n, out.String())
	}
}

// TestRunLocalMutedSkipsSpeech keeps the voice checks out when voice is off.
func TestRunLocalMutedSkipsSpeech(t *testing.T) {
	f := newFake()
	srv := f.server(t)
	cfg := localConfig(srv.URL)
	cfg.Mute = true

	var out strings.Builder
	if err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	}); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	for _, p := range f.seen() {
		if p == "/v1/audio/speech" {
			t.Error("muted run must not synthesize speech")
		}
	}
	if n := strings.Count(out.String(), "[ok]"); n != 3 {
		t.Errorf("expected 3 probes (ollama, chat, corrections), got %d:\n%s", n, out.String())
	}
}

// TestRunReportsBrokenSpeech checks that an empty PCM body is a failure and
// that the remaining probes still run.
func TestRunReportsBrokenSpeech(t *testing.T) {
	f := newFake()
	f.pcm = nil
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var out strings.Builder
	err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	})
	if err == nil {
		t.Fatalf("expected a failure, got none:\n%s", out.String())
	}
	got := out.String()
	if !strings.Contains(got, "[FAIL]") || !strings.Contains(got, "speech") {
		t.Errorf("speech failure not reported:\n%s", got)
	}
	if !strings.Contains(got, "chat") || !strings.Contains(got, "corrections") {
		t.Errorf("later probes must still run:\n%s", got)
	}
}

// TestRunReportsUnreadableCorrections covers the case the README warns about.
func TestRunReportsUnreadableCorrections(t *testing.T) {
	f := newFake()
	f.correctionJSON = "I could not do that"
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var out strings.Builder
	if err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	}); err == nil {
		t.Fatalf("expected a failure:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "corrections") {
		t.Errorf("missing corrections failure:\n%s", out.String())
	}
}

// TestRunReportsEmptyCorrections: a model that ignores the prompt and returns
// nothing for a clearly wrong sentence is a real failure, not a pass.
func TestRunReportsEmptyCorrections(t *testing.T) {
	f := newFake()
	f.correctionJSON = `{"corrections":[]}`
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var out strings.Builder
	if err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	}); err == nil {
		t.Fatalf("expected a failure for an empty correction list:\n%s", out.String())
	}
}

// TestRunReportsChatFailure keeps a dead model from looking healthy.
func TestRunReportsChatFailure(t *testing.T) {
	f := newFake()
	f.chatStatus = http.StatusInternalServerError
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var out strings.Builder
	if err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	}); err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(out.String(), "[FAIL]") {
		t.Errorf("no failure line:\n%s", out.String())
	}
}

// TestRunWarnsOnOldOllama: phonon needs 0.40.9 or newer.
func TestRunWarnsOnOldOllama(t *testing.T) {
	f := newFake()
	f.version = "0.39.0"
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var out strings.Builder
	if err := Run(context.Background(), &out, Env{
		Cfg: cfg, Ollama: ollama.New(srv.URL), Chat: ollama.Chatter{C: ollama.New(srv.URL)}, Settings: settings(cfg),
	}); err == nil {
		t.Fatalf("expected a failure for an old Ollama:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "0.40.9") {
		t.Errorf("the required version should be named:\n%s", out.String())
	}
}
