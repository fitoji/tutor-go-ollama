// Package openai is a minimal, dependency-free client for OpenAI-compatible
// /chat/completions endpoints with streaming: Groq, xAI, DeepSeek, OpenRouter...
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"speakeasy/internal/llm"
)

const (
	// Some providers sit behind bot filters that reject the default Go
	// user agent, and ask clients to identify themselves anyway.
	userAgent = "speakeasy/0.1"
	// A 429 with a Retry-After up to this long is waited out once.
	maxRetryWait = 15 * time.Second
)

type Client struct {
	base string
	key  string
	hc   *http.Client
	wait func(ctx context.Context, d time.Duration) error // replaced in tests
}

// New returns a client for base (e.g. "https://api.groq.com/openai/v1").
// No global timeout is set because replies stream; callers use contexts.
func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{}, wait: sleep}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatBody struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	Stream         bool            `json:"stream"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"` // 0 is meaningful: always sent
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

func buildBody(r llm.Request) chatBody {
	b := chatBody{
		Model:       r.Model,
		Stream:      true,
		MaxTokens:   r.MaxTokens,
		Temperature: r.Temperature,
		Messages:    make([]message, len(r.Messages)),
	}
	for i, m := range r.Messages {
		b.Messages[i] = message{Role: m.Role, Content: m.Content}
	}
	if len(r.JSONSchema) > 0 {
		// JSON mode is supported far more widely than strict schemas; the
		// prompt describes the shape and the caller validates the result.
		b.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	return b
}

// ChatStream sends the request and streams the reply. Ollama-only fields of
// the request are ignored. <think>...</think> blocks are removed from the
// output so reasoning models do not speak their thoughts aloud.
func (c *Client) ChatStream(ctx context.Context, req llm.Request, onDelta func(string) error) (llm.Stats, error) {
	body, err := json.Marshal(buildBody(req))
	if err != nil {
		return llm.Stats{}, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.post(ctx, body)
		if err != nil {
			return llm.Stats{}, err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if d, ok := retryAfter(resp); ok && d <= maxRetryWait {
				resp.Body.Close()
				if err := c.wait(ctx, d); err != nil {
					return llm.Stats{}, err
				}
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError(resp)
			resp.Body.Close()
			return llm.Stats{}, err
		}
		st, err := readSSE(resp.Body, onDelta)
		resp.Body.Close()
		return st, err
	}
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	hr.Header.Set("Authorization", "Bearer "+c.key)
	hr.Header.Set("User-Agent", userAgent)
	return c.hc.Do(hr)
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

func apiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(raw))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	hint := ""
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		hint = " (check the API key)"
	case http.StatusTooManyRequests:
		hint = " (rate limit reached; wait a moment)"
		if d, ok := retryAfter(resp); ok {
			hint = fmt.Sprintf(" (rate limit reached; retry in %.0fs)", d.Seconds())
		}
	}
	return fmt.Errorf("api: %s: %s%s", resp.Status, msg, hint)
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
	XGroq *struct {
		Usage *usage `json:"usage"`
	} `json:"x_groq"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c chunk) usage() *usage {
	if c.Usage != nil {
		return c.Usage
	}
	if c.XGroq != nil {
		return c.XGroq.Usage
	}
	return nil
}

// readSSE consumes a text/event-stream of chat.completion.chunk events.
func readSSE(r io.Reader, onDelta func(string) error) (llm.Stats, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var (
		st       llm.Stats
		f        thinkFilter
		done     bool
		finished bool
	)
	emit := func(s string) error {
		if s == "" {
			return nil
		}
		return onDelta(s)
	}
	for !done && sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // comments, blank separators, event names
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		switch data {
		case "":
			continue
		case "[DONE]":
			done = true
			continue
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return st, fmt.Errorf("api: unreadable stream chunk: %w", err)
		}
		if ch.Error != nil && ch.Error.Message != "" {
			return st, errors.New("api: " + ch.Error.Message)
		}
		if u := ch.usage(); u != nil {
			st = llm.Stats{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}
		}
		for _, c := range ch.Choices {
			if err := emit(f.Push(c.Delta.Content)); err != nil {
				return st, err
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finished = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return st, err
	}
	if !done && !finished {
		return st, io.ErrUnexpectedEOF
	}
	return st, emit(f.Flush())
}
