package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"speakeasy/internal/llm"
)

func sseWrite(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	for _, e := range events {
		fmt.Fprintf(w, "data: %s\n\n", e)
		fl.Flush()
	}
}

func delta(s string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": s}, "finish_reason": nil}}})
	return string(b)
}

const finishChunk = `{"choices":[{"delta":{},"finish_reason":"stop"}]}`

func collect(t *testing.T, c *Client, req llm.Request) (string, llm.Stats, error) {
	t.Helper()
	var sb strings.Builder
	st, err := c.ChatStream(context.Background(), req, func(d string) error {
		sb.WriteString(d)
		return nil
	})
	return sb.String(), st, err
}

func simpleReq() llm.Request {
	return llm.Request{
		Model:       "llama-3.3-70b-versatile",
		Messages:    []llm.Message{{Role: "system", Content: "be nice"}, {Role: "user", Content: "hi"}},
		MaxTokens:   256,
		Temperature: 0,
	}
}

func TestStreamRequestAndResponse(t *testing.T) {
	var gotPath, gotAuth, gotUA, gotCT string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotUA, gotCT = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&body)
		sseWrite(w,
			`{"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			delta("Hel"), delta("lo"),
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"x_groq":{"usage":{"prompt_tokens":77,"completion_tokens":5}}}`,
			"[DONE]")
	}))
	defer srv.Close()

	text, st, err := collect(t, New(srv.URL+"/openai/v1/", "secret"), simpleReq())
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello" {
		t.Errorf("text %q", text)
	}
	if st.PromptTokens != 77 || st.CompletionTokens != 5 {
		t.Errorf("stats %+v", st)
	}
	if gotPath != "/openai/v1/chat/completions" {
		t.Errorf("path %q", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth %q", gotAuth)
	}
	if !strings.HasPrefix(gotUA, "speakeasy/") {
		t.Errorf("user agent %q must identify the app, not the Go library", gotUA)
	}
	if gotCT != "application/json" {
		t.Errorf("content type %q", gotCT)
	}
	if body["model"] != "llama-3.3-70b-versatile" || body["stream"] != true || body["max_tokens"] != float64(256) {
		t.Errorf("body %v", body)
	}
	if temp, ok := body["temperature"]; !ok || temp != float64(0) {
		t.Errorf("temperature 0 must be sent explicitly, got %v (present=%v)", temp, ok)
	}
	if _, has := body["response_format"]; has {
		t.Error("response_format must be absent for plain chat")
	}
	for _, ollamaOnly := range []string{"think", "options", "keep_alive", "format", "num_ctx"} {
		if _, has := body[ollamaOnly]; has {
			t.Errorf("Ollama-only field %q leaked into the request", ollamaOnly)
		}
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["content"] != "hi" {
		t.Errorf("messages %v", msgs)
	}
}

func TestUsageFromStandardField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseWrite(w, delta("ok"), finishChunk, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`, "[DONE]")
	}))
	defer srv.Close()
	_, st, err := collect(t, New(srv.URL, "k"), simpleReq())
	if err != nil || st.PromptTokens != 10 || st.CompletionTokens != 2 {
		t.Errorf("%+v %v", st, err)
	}
}

func TestJSONModeOnlyWhenSchemaGiven(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		sseWrite(w, delta("{}"), finishChunk, "[DONE]")
	}))
	defer srv.Close()
	req := simpleReq()
	req.JSONSchema = json.RawMessage(`{"type":"object"}`)
	if _, _, err := collect(t, New(srv.URL, "k"), req); err != nil {
		t.Fatal(err)
	}
	rf, _ := body["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format %v", body["response_format"])
	}
}

func TestReasoningFieldIsIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseWrite(w,
			`{"choices":[{"delta":{"reasoning":"let me think about it"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"reasoning_content":"more thinking"},"finish_reason":null}]}`,
			delta("Nice to meet you."), finishChunk, "[DONE]")
	}))
	defer srv.Close()
	text, _, err := collect(t, New(srv.URL, "k"), simpleReq())
	if err != nil || text != "Nice to meet you." {
		t.Errorf("%q %v", text, err)
	}
}

func TestThinkBlocksAreRemovedAcrossChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseWrite(w, delta("<thi"), delta("nk>secret plan</th"), delta("ink>\n\nHello"), delta(" there!"), finishChunk, "[DONE]")
	}))
	defer srv.Close()
	text, _, err := collect(t, New(srv.URL, "k"), simpleReq())
	if err != nil || text != "Hello there!" {
		t.Errorf("%q %v", text, err)
	}
}

func TestThinkFilter(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"plain text untouched", []string{"Hello ", "world"}, "Hello world"},
		{"block at start", []string{"<think>abc</think>Hi"}, "Hi"},
		{"whitespace after block trimmed", []string{"<think>abc</think>", "\n\n", "Hi"}, "Hi"},
		{"text before and after", []string{"A <think>x</think>B"}, "A B"},
		{"split open tag", []string{"<th", "ink>x</think>ok"}, "ok"},
		{"split close tag", []string{"<think>x</th", "ink>ok"}, "ok"},
		{"unterminated block is dropped", []string{"<think>never closes"}, ""},
		{"lone less-than is kept", []string{"2 < 3 and a <th"}, "2 < 3 and a <th"},
		{"two blocks", []string{"<think>a</think>X<think>b</think>Y"}, "XY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f thinkFilter
			var sb strings.Builder
			for _, c := range tc.chunks {
				sb.WriteString(f.Push(c))
			}
			sb.WriteString(f.Flush())
			if sb.String() != tc.want {
				t.Errorf("got %q, want %q", sb.String(), tc.want)
			}
		})
	}
}

func TestHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   []string
	}{
		{401, `{"error":{"message":"Invalid API Key","type":"invalid_request_error"}}`, []string{"401", "Invalid API Key", "check the API key"}},
		{500, `upstream exploded`, []string{"500", "upstream exploded"}},
		{404, `{"error":{"message":"model not found"}}`, []string{"404", "model not found"}},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			io.WriteString(w, c.body)
		}))
		_, _, err := collect(t, New(srv.URL, "k"), simpleReq())
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected an error", c.status)
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("status %d: error %q lacks %q", c.status, err, w)
			}
		}
	}
}

func TestRateLimitRetriesOnceThenSucceeds(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"Rate limit reached"}}`)
			return
		}
		sseWrite(w, delta("fine"), finishChunk, "[DONE]")
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	var waited []time.Duration
	c.wait = func(ctx context.Context, d time.Duration) error { waited = append(waited, d); return nil }

	text, _, err := collect(t, c, simpleReq())
	if err != nil || text != "fine" {
		t.Fatalf("%q %v", text, err)
	}
	if calls != 2 || len(waited) != 1 || waited[0] != 2*time.Second {
		t.Errorf("calls=%d waited=%v", calls, waited)
	}
}

func TestRateLimitGivesUpWhenWaitIsLongOrRepeated(t *testing.T) {
	for _, retry := range []string{"120", "1"} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", retry)
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"Rate limit reached for model"}}`)
		}))
		c := New(srv.URL, "k")
		c.wait = func(ctx context.Context, d time.Duration) error { return nil }
		_, _, err := collect(t, c, simpleReq())
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "Rate limit reached for model") {
			t.Errorf("Retry-After %s: err %v", retry, err)
		}
		if retry == "120" && (calls != 1 || !strings.Contains(err.Error(), "retry in 120s")) {
			t.Errorf("a long wait must not be retried and should be reported: calls=%d err=%v", calls, err)
		}
		if retry == "1" && calls != 2 {
			t.Errorf("short wait: exactly one retry expected, got %d calls", calls)
		}
	}
}

func TestStreamEndings(t *testing.T) {
	serve := func(events ...string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sseWrite(w, events...) }))
	}
	// Cut off mid-reply: neither [DONE] nor a finish reason.
	cut := serve(delta("Hel"))
	defer cut.Close()
	if _, _, err := collect(t, New(cut.URL, "k"), simpleReq()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated stream: %v", err)
	}
	// A finish reason without [DONE] is a complete reply.
	ok := serve(delta("Hi."), finishChunk)
	defer ok.Close()
	if text, _, err := collect(t, New(ok.URL, "k"), simpleReq()); err != nil || text != "Hi." {
		t.Errorf("finish_reason only: %q %v", text, err)
	}
	// An error event in the middle of the stream.
	bad := serve(delta("Hel"), `{"error":{"message":"model overloaded"}}`)
	defer bad.Close()
	if _, _, err := collect(t, New(bad.URL, "k"), simpleReq()); err == nil || !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("error event: %v", err)
	}
	// Garbage in a data line.
	junk := serve(`not json`)
	defer junk.Close()
	if _, _, err := collect(t, New(junk.URL, "k"), simpleReq()); err == nil {
		t.Error("unreadable chunk must be an error")
	}
}

func TestCallbackErrorAbortsAndContextCancels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseWrite(w, delta("one"), delta("two"), finishChunk, "[DONE]")
	}))
	defer srv.Close()
	stop := errors.New("stop")
	_, err := New(srv.URL, "k").ChatStream(context.Background(), simpleReq(), func(string) error { return stop })
	if !errors.Is(err, stop) {
		t.Errorf("callback error not propagated: %v", err)
	}

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Net/http only notices a closed client connection after the request
		// body has been read; the release channel is a safety net.
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer hang.Close()
	defer close(release) // runs first, so Close can never block on this handler
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := New(hang.URL, "k").ChatStream(ctx, simpleReq(), func(string) error { return nil }); err == nil {
		t.Error("expected a context error")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("cancellation was not prompt")
	}
}
