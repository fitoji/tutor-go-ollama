package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WhisperCPP transcribes with a local whisper.cpp binary: no network, no
// daemon, no Python. The model is a ggml .bin file (ggml-small.en.bin is 465 MB
// and transcribes a sentence in well under a second once the model is in the
// page cache).
type WhisperCPP struct {
	Bin   string // "whisper-cli"
	Model string // path to ggml-*.bin
}

func (w WhisperCPP) bin() string {
	if w.Bin == "" {
		return "whisper-cli"
	}
	return w.Bin
}

// Args is the exact command line, exposed so a test can pin it. -otxt writes
// the text to <out>.txt, which is cleaner than parsing stdout: whisper prints
// its own noise there even with -np.
func (w WhisperCPP) Args(wav, out, lang, prompt string) []string {
	if lang == "" {
		lang = "auto"
	}
	args := []string{"-m", w.Model, "-f", wav, "-l", lang, "-nt", "-np", "-otxt", "-of", out}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	return args
}

// Transcribe writes the audio to a temporary WAV, runs the binary and reads the
// text back. The scratch directory is removed either way.
func (w WhisperCPP) Transcribe(ctx context.Context, pcm []byte, lang, prompt string) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	if w.Model == "" {
		return "", errors.New("falta el modelo de whisper: pasá --whisper-model")
	}
	dir, err := os.MkdirTemp("", "speakeasy-whisper")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	wav := filepath.Join(dir, "speech.wav")
	if err := os.WriteFile(wav, WAV(pcm), 0o600); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "out")

	cmd := exec.CommandContext(ctx, w.bin(), w.Args(wav, out, lang, prompt)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", whisperError(err, errb.String(), w)
	}
	b, err := os.ReadFile(out + ".txt")
	if err != nil {
		return "", fmt.Errorf("whisper no dejó texto: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// whisperError explains the two failures a person can fix: the binary and the
// model.
func whisperError(err error, stderr string, w WhisperCPP) error {
	low := strings.ToLower(stderr)
	switch {
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist),
		strings.Contains(low, "executable file not found"):
		return errors.New("no encuentro whisper-cli: instalalo con `brew install whisper-cpp`")
	case strings.Contains(low, "failed to load model"), strings.Contains(low, "model file"):
		return fmt.Errorf("no pude cargar el modelo de whisper %q: bajá el .bin de ggerganov/whisper.cpp", w.Model)
	}
	if line := strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0]); line != "" {
		return fmt.Errorf("whisper falló: %s", line)
	}
	return fmt.Errorf("whisper falló: %w", err)
}
