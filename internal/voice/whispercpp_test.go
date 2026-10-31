package voice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhisperArgs(t *testing.T) {
	w := WhisperCPP{Model: "/m/ggml-small.en.bin"}
	got := strings.Join(w.Args("/tmp/a.wav", "/tmp/out", "en", ""), " ")
	for _, want := range []string{"-m /m/ggml-small.en.bin", "-f /tmp/a.wav", "-l en", "-nt", "-np", "-otxt", "-of /tmp/out"} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q: %s", want, got)
		}
	}
	// An empty language must not become "-l ".
	if got := strings.Join(w.Args("a", "o", "", ""), " "); !strings.Contains(got, "-l auto") {
		t.Errorf("empty language = %s", got)
	}
}

// fakeWhisper writes a shell script that behaves like whisper-cli: it honours
// -of and leaves the text in <out>.txt.
func fakeWhisper(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "whisper-cli")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWhisperTranscribeReadsTheTextFile(t *testing.T) {
	bin := fakeWhisper(t, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -of) out="$2"; shift 2;;
    *) shift;;
  esac
done
printf '  I went to the cinema.  \n' > "$out.txt"
`)
	w := WhisperCPP{Bin: bin, Model: "/m.bin"}
	text, err := w.Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if text != "I went to the cinema." {
		t.Errorf("text = %q", text)
	}
}

func TestWhisperErrors(t *testing.T) {
	if _, err := (WhisperCPP{Bin: "/nope/whisper-cli", Model: "/m.bin"}).Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en", ""); err == nil || !strings.Contains(err.Error(), "brew install whisper-cpp") {
		t.Errorf("missing binary: %v", err)
	}
	if _, err := (WhisperCPP{Model: ""}).Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en", ""); err == nil || !strings.Contains(err.Error(), "--whisper-model") {
		t.Errorf("missing model: %v", err)
	}
	bin := fakeWhisper(t, "#!/bin/sh\necho 'whisper_model_load: failed to load model' >&2\nexit 1\n")
	if _, err := (WhisperCPP{Bin: bin, Model: "/m.bin"}).Transcribe(context.Background(), pcmOf(SampleRate, 5000), "en", ""); err == nil || !strings.Contains(err.Error(), "modelo") {
		t.Errorf("broken model: %v", err)
	}
	// Empty audio is never worth a process.
	if text, err := (WhisperCPP{Model: "/m.bin"}).Transcribe(context.Background(), nil, "en", ""); err != nil || text != "" {
		t.Errorf("empty audio: %q %v", text, err)
	}
}

// TestWhisperArgsCarriesThePrompt pins the priming: without it, names and topic
// words are the first thing the recogniser gets wrong.
func TestWhisperArgsCarriesThePrompt(t *testing.T) {
	w := WhisperCPP{Model: "/m.bin"}
	got := strings.Join(w.Args("a.wav", "/tmp/o", "en", "English lesson. Learner name: Fito."), " ")
	if !strings.Contains(got, "--prompt English lesson. Learner name: Fito.") {
		t.Errorf("args = %s", got)
	}
	// Without a prompt the flag is not sent at all.
	if got := strings.Join(w.Args("a.wav", "/o", "en", ""), " "); strings.Contains(got, "--prompt") {
		t.Errorf("args = %s", got)
	}
}
