package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWAVHeader(t *testing.T) {
	pcm := make([]byte, 3200) // 100 ms at 16 kHz
	w := WAV(pcm)
	if string(w[0:4]) != "RIFF" || string(w[8:12]) != "WAVE" || string(w[36:40]) != "data" {
		t.Fatalf("bad container: %q", w[0:44])
	}
	if got := binary.LittleEndian.Uint32(w[24:]); got != SampleRate {
		t.Errorf("sample rate = %d, want %d", got, SampleRate)
	}
	if got := binary.LittleEndian.Uint32(w[40:]); got != uint32(len(pcm)) {
		t.Errorf("data size = %d, want %d", got, len(pcm))
	}
	if len(w) != 44+len(pcm) {
		t.Errorf("length = %d", len(w))
	}
}

func pcmOf(samples int, amp int16) []byte {
	b := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(amp))
	}
	return b
}

func TestSilent(t *testing.T) {
	cases := []struct {
		name string
		pcm  []byte
		want bool
	}{
		{"empty", nil, true},
		{"a keypress", pcmOf(SampleRate/10, 8000), true},
		{"quiet room", pcmOf(SampleRate, 20), true},
		{"speech", pcmOf(SampleRate, 6000), false},
		{"speech but too short", pcmOf(SampleRate/10, 6000), true},
	}
	for _, c := range cases {
		if got := Silent(c.pcm); got != c.want {
			t.Errorf("%s: Silent = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestFFmpegArgs pins the command: avfoundation, the configured device, the
// duration cap and raw 16 kHz mono PCM on stdout.
func TestFFmpegArgs(t *testing.T) {
	r := &FFmpegRecorder{Device: ":1"}
	args := strings.Join(r.Args(30*time.Second), " ")
	for _, want := range []string{"-f avfoundation", "-i :1", "-t 30", "-ar 16000", "-ac 1", "-f s16le", "-"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
	// A zero duration must not ask ffmpeg for an infinite capture.
	zero := &FFmpegRecorder{}
	if got := strings.Join(zero.Args(0), " "); !strings.Contains(got, "-t 1") {
		t.Errorf("zero duration = %s", got)
	}
	if got := strings.Join(zero.Args(time.Second), " "); !strings.Contains(got, "-i :0") {
		t.Errorf("default device = %s", got)
	}
}

func TestRecordWithoutFFmpeg(t *testing.T) {
	r := &FFmpegRecorder{Path: "/nope/ffmpeg"}
	_, err := r.Record(context.Background(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "brew install ffmpeg") {
		t.Errorf("a missing ffmpeg must say how to install it: %v", err)
	}
}

func TestMicErrorTranslatesThePermissionProblem(t *testing.T) {
	cases := map[string]string{
		"[avfoundation] Input/output error": "Privacidad y seguridad",
		"device is busy":                    "ocupado",
		"audio device does not exist":       "dispositivo de audio",
		"something else went wrong":         "something else",
	}
	for stderr, want := range cases {
		if err := micError(stderr); !strings.Contains(err.Error(), want) {
			t.Errorf("micError(%q) = %v, want %q", stderr, err, want)
		}
	}
}

func TestGroqTranscribe(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLang, gotFormat string
	var gotWAV []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		gotFormat = r.FormValue("response_format")
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no file field: %v", err)
			return
		}
		defer f.Close()
		buf := make([]byte, 44)
		_, _ = f.Read(buf)
		gotWAV = buf
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"  I went to the cinema yesterday.  "}`))
	}))
	defer srv.Close()

	g := GroqTranscriber{Base: srv.URL, Key: "k1", Model: "whisper-large-v3-turbo"}
	text, err := g.Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en")
	if err != nil {
		t.Fatal(err)
	}
	if text != "I went to the cinema yesterday." {
		t.Errorf("text = %q", text)
	}
	if gotPath != "/audio/transcriptions" || gotAuth != "Bearer k1" {
		t.Errorf("path=%q auth=%q", gotPath, gotAuth)
	}
	if gotModel != "whisper-large-v3-turbo" || gotLang != "en" || gotFormat != "json" {
		t.Errorf("model=%q lang=%q format=%q", gotModel, gotLang, gotFormat)
	}
	if string(gotWAV[0:4]) != "RIFF" {
		t.Errorf("the upload is not a WAV: %q", gotWAV)
	}
}

func TestGroqTranscribeDefaultsAndErrors(t *testing.T) {
	// An empty audio buffer is not worth a request.
	if text, err := (GroqTranscriber{}).Transcribe(context.Background(), nil, "en"); err != nil || text != "" {
		t.Errorf("empty audio: %q %v", text, err)
	}

	for code, want := range map[int]string{
		http.StatusUnauthorized:    "clave",
		http.StatusTooManyRequests: "429",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			w.Write([]byte(`{"error":"nope"}`))
		}))
		g := GroqTranscriber{Base: srv.URL, Key: "k"}
		if _, err := g.Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v, want %q", code, err, want)
		}
		srv.Close()
	}
}

// TestLoudOnShortBlocks is what the VAD uses: a 100 ms block is too short for
// Silent's "not speech yet" rule, but its level still tells noise from voice.
func TestLoudOnShortBlocks(t *testing.T) {
	if loud(pcmOf(SampleRate/10, 6000)) != true {
		t.Error("speech level in a short block must read as loud")
	}
	if loud(pcmOf(SampleRate/10, 10)) != false {
		t.Error("room noise in a short block must read as quiet")
	}
}

// TestCopyUntilSilenceStopsAfterSpeech pins the VAD: it must ignore the leading
// silence, stop after the hold once speech was heard, and keep the audio.
func TestCopyUntilSilenceStopsAfterSpeech(t *testing.T) {
	lead := pcmOf(SampleRate/2, 5)     // 500 ms of room noise
	voice := pcmOf(SampleRate/2, 6000) // 500 ms of speech
	tail := pcmOf(SampleRate*3, 5)     // 3 s of silence
	src := bytes.NewReader(append(append(lead, voice...), tail...))

	var out bytes.Buffer
	start := time.Now()
	copyUntilSilence(src, &out, 300*time.Millisecond, nil)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the VAD should stop reading, took %v", d)
	}
	if out.Len() >= len(lead)+len(voice)+len(tail) {
		t.Errorf("the VAD read everything (%d bytes)", out.Len())
	}
	// It kept the speech and did not cut before it started.
	if out.Len() < len(lead)+len(voice) {
		t.Errorf("the VAD cut before the speech ended: %d bytes", out.Len())
	}
}

// TestCopyUntilSilenceWithoutVADReadsEverything keeps --vad 0 meaning "only the
// Enter stops it".
func TestCopyUntilSilenceWithoutVADReadsEverything(t *testing.T) {
	pcm := pcmOf(SampleRate/2, 6000)
	var out bytes.Buffer
	copyUntilSilence(bytes.NewReader(pcm), &out, 0, nil)
	if out.Len() != len(pcm) {
		t.Errorf("read %d of %d bytes", out.Len(), len(pcm))
	}
}
