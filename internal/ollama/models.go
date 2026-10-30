package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// RunningModel is one entry of /api/ps: a model currently resident in memory.
type RunningModel struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Running lists the models Ollama currently keeps loaded.
func (c *Client) Running(ctx context.Context) ([]RunningModel, error) {
	var r struct {
		Models []RunningModel `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/ps", &r); err != nil {
		return nil, err
	}
	return r.Models, nil
}

// Unload asks Ollama to drop a model from memory right away.
func (c *Client) Unload(ctx context.Context, model string) error {
	return c.postJSON(ctx, "/api/generate", map[string]any{"model": model, "keep_alive": 0})
}

// Load brings a model into memory without generating anything. The options
// must match the ones used by later requests (notably num_ctx): Ollama
// reloads a model whenever they differ.
func (c *Client) Load(ctx context.Context, model, keepAlive string, options map[string]any) error {
	return c.postJSON(ctx, "/api/chat", map[string]any{
		"model":      model,
		"messages":   []Message{},
		"keep_alive": keepAlive,
		"options":    options,
	})
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(hr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

// SameModel compares model tags, treating a missing tag as ":latest".
func SameModel(a, b string) bool { return normTag(a) == normTag(b) }

func normTag(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, ":") {
		s += ":latest"
	}
	return s
}

// VersionAtLeast reports whether a version such as "0.40.9" or "v0.41.0-rc1"
// is >= major.minor.patch. Unparseable versions return true so that a
// surprising format never blocks the user.
func VersionAtLeast(v string, major, minor, patch int) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return true
	}
	got := [3]int{}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return true
		}
		got[i] = n
	}
	want := [3]int{major, minor, patch}
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}
