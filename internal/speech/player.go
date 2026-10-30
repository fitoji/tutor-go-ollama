package speech

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// Player plays one clip of raw 24 kHz mono 16-bit PCM and returns when it
// has finished, or as soon as ctx is cancelled.
type Player interface {
	Play(ctx context.Context, pcm []byte) error
}

type cmdPlayer struct {
	bin  string
	args func(path string) []string
}

var candidates = []struct {
	bin  string
	args func(path string) []string
}{
	{"afplay", func(p string) []string { return []string{p} }}, // macOS
	{"ffplay", func(p string) []string { return []string{"-nodisp", "-autoexit", "-loglevel", "error", p} }},
	{"paplay", func(p string) []string { return []string{p} }},
	{"aplay", func(p string) []string { return []string{"-q", p} }},
}

// NewPlayer picks the first audio player available on this machine. On macOS
// that is afplay, which ships with the system.
func NewPlayer() (Player, error) {
	for _, c := range candidates {
		if path, err := exec.LookPath(c.bin); err == nil {
			return &cmdPlayer{bin: path, args: c.args}, nil
		}
	}
	return nil, errors.New("no audio player found (afplay, ffplay, paplay or aplay)")
}

func (p *cmdPlayer) Play(ctx context.Context, pcm []byte) error {
	f, err := os.CreateTemp("", "speakeasy-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(WAV(pcm)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	err = exec.CommandContext(ctx, p.bin, p.args(f.Name())...).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
