// Package ollama is a minimal, dependency-free client for the parts of the
// Ollama HTTP API this app needs: streaming chat, speech and model checks.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxSpeechRunes is the documented per-request limit of /v1/audio/speech.
const MaxSpeechRunes = 4096

type Client struct {
	base string
	hc   *http.Client
}

// New returns a client. No global timeout is set because chat responses
// stream; callers control lifetime with contexts.
func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{}}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model     string          `json:"model"`
	Messages  []Message       `json:"messages"`
	Stream    bool            `json:"stream"`
	Think     *bool           `json:"think,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
}

type Stats struct {
	PromptTokens int
	EvalTokens   int
}

type chatChunk struct {
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
	Error           string  `json:"error"`
}

// ChatStream sends req (always streaming) and calls onDelta for every piece
// of content. Returning an error from onDelta aborts the request.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onDelta func(string) error) (Stats, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return Stats{}, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Stats{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(hr)
	if err != nil {
		return Stats{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Stats{}, httpError(resp)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var ch chatChunk
		if err := dec.Decode(&ch); err != nil {
			if errors.Is(err, io.EOF) {
				return Stats{}, io.ErrUnexpectedEOF
			}
			return Stats{}, err
		}
		if ch.Error != "" {
			return Stats{}, errors.New(ch.Error)
		}
		if ch.Message.Content != "" {
			if err := onDelta(ch.Message.Content); err != nil {
				return Stats{}, err
			}
		}
		if ch.Done {
			return Stats{PromptTokens: ch.PromptEvalCount, EvalTokens: ch.EvalCount}, nil
		}
	}
}

// Chat is ChatStream collected into a string.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (string, Stats, error) {
	var sb strings.Builder
	st, err := c.ChatStream(ctx, req, func(d string) error {
		sb.WriteString(d)
		return nil
	})
	return sb.String(), st, err
}

// Speech synthesizes input and returns raw PCM: 16-bit little-endian, mono,
// 24 kHz, no header. Returning PCM (not WAV) avoids the unknown-length WAV
// header Ollama writes while streaming.
func (c *Client) Speech(ctx context.Context, model, voice, input string) ([]byte, error) {
	if r := []rune(input); len(r) > MaxSpeechRunes {
		input = string(r[:MaxSpeechRunes])
	}
	payload := map[string]string{"model": model, "input": input, "response_format": "pcm"}
	if voice != "" {
		payload["voice"] = voice
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(hr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version returns the server version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/api/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// HasModel reports whether a model is pulled locally. A name without a tag
// matches "<name>:latest".
func (c *Client) HasModel(ctx context.Context, name string) (bool, error) {
	var t struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/tags", &t); err != nil {
		return false, err
	}
	want := name
	if !strings.Contains(name, ":") {
		want = name + ":latest"
	}
	for _, m := range t.Models {
		if m.Name == want || m.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func httpError(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("ollama: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
}
