package voice

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func gapPCM(n int, amp int16) []byte {
	b := make([]byte, n*2)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(amp))
	}
	return b
}

// TestSilentBoundaries pins the two thresholds: 0.2 s and -48 dBFS.
func TestSilentBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		pcm  []byte
		want bool
	}{
		{"0.199s", gapPCM(3184, 8000), true},
		{"0.200s", gapPCM(3200, 8000), false},
		{"below -48dBFS", gapPCM(3200, 131), true},
		{"above -48dBFS", gapPCM(3200, 132), false},
		{"negative samples", gapPCM(3200, -8000), false},
	} {
		if Silent(c.pcm) != c.want {
			t.Errorf("%s: Silent=%v want %v", c.name, Silent(c.pcm), c.want)
		}
	}
}

// TestGroqMultipartGaps covers the fields a real endpoint needs and the
// promise that an empty buffer never costs a request.
func TestGroqMultipartGaps(t *testing.T) {
	var hits int32
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer sk-1" {
			t.Errorf("path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = r.ParseMultipartForm(4 << 20)
		form = map[string]string{
			"model": r.FormValue("model"), "response_format": r.FormValue("response_format"),
			"temperature": r.FormValue("temperature"), "language": r.FormValue("language"),
			"prompt": r.FormValue("prompt"),
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("file: %v", err)
		}
		w.Write([]byte(`{"text":"  hi  "}`))
	}))
	defer srv.Close()

	g := GroqTranscriber{Base: srv.URL + "/", Key: "sk-1", Model: "whisper-large-v3-turbo"}
	if text, err := g.Transcribe(context.Background(), gapPCM(16000, 5000), "en", "English lesson. Learner name: Fito."); err != nil || text != "hi" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	for k, v := range map[string]string{"model": "whisper-large-v3-turbo", "response_format": "json", "temperature": "0", "language": "en", "prompt": "English lesson. Learner name: Fito."} {
		if form[k] != v {
			t.Errorf("%s=%q want %q", k, form[k], v)
		}
	}
	before := atomic.LoadInt32(&hits)
	if _, err := g.Transcribe(context.Background(), nil, "en", ""); err != nil || atomic.LoadInt32(&hits) != before {
		t.Errorf("empty PCM made a request: err=%v", err)
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer denied.Close()
	if _, err := (GroqTranscriber{Base: denied.URL, Key: "k"}).Transcribe(context.Background(), gapPCM(16000, 5000), "en", ""); err == nil || !strings.Contains(err.Error(), "clave") {
		t.Errorf("403: %v", err)
	}
}
