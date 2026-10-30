package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameModel(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"phonon", "phonon:latest", true},
		{"qwen3:8b", "qwen3:8b", true},
		{"qwen3:8b", "qwen3:4b", false},
		{"qwen3", "qwen3:8b", false},
	}
	for _, c := range cases {
		if got := SameModel(c.a, c.b); got != c.want {
			t.Errorf("SameModel(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.40.9", true}, {"0.40.8", false}, {"0.41.0", true}, {"1.0.0", true},
		{"0.39.99", false}, {"v0.40.9", true}, {"0.40.9-rc1", true}, {"0.40.10", true},
		{"weird", true}, {"", true},
	}
	for _, c := range cases {
		if got := VersionAtLeast(c.v, 0, 40, 9); got != c.want {
			t.Errorf("VersionAtLeast(%q)=%v, want %v", c.v, got, c.want)
		}
	}
}

func TestRunningUnloadLoad(t *testing.T) {
	var unload, load map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			io.WriteString(w, `{"models":[{"name":"llama3:8b","size":5368709120},{"name":"phonon:latest","size":100}]}`)
		case "/api/generate":
			json.NewDecoder(r.Body).Decode(&unload)
			io.WriteString(w, `{"done":true}`)
		case "/api/chat":
			json.NewDecoder(r.Body).Decode(&load)
			io.WriteString(w, `{"done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	ctx := context.Background()

	models, err := c.Running(ctx)
	if err != nil || len(models) != 2 || models[0].Name != "llama3:8b" || models[0].Size != 5368709120 {
		t.Fatalf("Running: %v %+v", err, models)
	}
	if err := c.Unload(ctx, "llama3:8b"); err != nil {
		t.Fatal(err)
	}
	if unload["model"] != "llama3:8b" || unload["keep_alive"] != float64(0) {
		t.Errorf("unload body %v", unload)
	}
	if err := c.Load(ctx, "qwen3:8b", "30m", map[string]any{"num_ctx": 8192}); err != nil {
		t.Fatal(err)
	}
	if load["model"] != "qwen3:8b" || load["keep_alive"] != "30m" {
		t.Errorf("load body %v", load)
	}
	if msgs, ok := load["messages"].([]any); !ok || len(msgs) != 0 {
		t.Errorf("load must send an empty messages array, got %v", load["messages"])
	}
	if opts := load["options"].(map[string]any); opts["num_ctx"] != float64(8192) {
		t.Errorf("load options %v", opts)
	}
}

func TestChatStreamAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			io.WriteString(w, `{"message":{"role":"assistant","content":"Hel"},"done":false}`+"\n")
			io.WriteString(w, `{"message":{"role":"assistant","content":"lo"},"done":false}`+"\n")
			io.WriteString(w, `{"done":true,"prompt_eval_count":42,"eval_count":2}`+"\n")
		case "/v1/audio/speech":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["response_format"] != "pcm" || body["model"] != "phonon" {
				http.Error(w, "bad request", 400)
				return
			}
			w.Write([]byte{1, 2, 3, 4})
		default:
			http.Error(w, "boom", 500)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	text, st, err := c.Chat(context.Background(), ChatRequest{Model: "m"})
	if err != nil || text != "Hello" || st.PromptTokens != 42 || st.EvalTokens != 2 {
		t.Fatalf("Chat: %q %+v %v", text, st, err)
	}
	pcm, err := c.Speech(context.Background(), "phonon", "", "hi")
	if err != nil || len(pcm) != 4 {
		t.Fatalf("Speech: %v %v", pcm, err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Error("expected an HTTP error to surface")
	}
}

func TestChatStreamTruncatedIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":{"role":"assistant","content":"Hel"},"done":false}`+"\n")
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL).Chat(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Error("a stream that ends without done must be an error")
	}
}
