package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FFmpegRecorder captures the microphone through ffmpeg's avfoundation input.
// It shells out instead of using cgo: one external tool, zero Go dependencies,
// and the same process the user already has for other audio work.
type FFmpegRecorder struct {
	Path   string // "ffmpeg" by default
	Device string // avfoundation audio device, ":0" by default
}

func (r FFmpegRecorder) path() string {
	if r.Path == "" {
		return "ffmpeg"
	}
	return r.Path
}

func (r FFmpegRecorder) device() string {
	if r.Device == "" {
		return ":0"
	}
	return r.Device
}

// Args is the exact command line, exposed so a test can pin it.
func (r FFmpegRecorder) Args(max time.Duration) []string {
	secs := int(max.Seconds())
	if secs < 1 {
		secs = 1
	}
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "avfoundation", "-i", r.device(),
		"-t", strconv.Itoa(secs),
		"-ar", strconv.Itoa(SampleRate), "-ac", "1",
		"-f", "s16le", "-",
	}
}

// Record captures until ctx is cancelled (the user pressed Enter again) or max
// elapses, whichever comes first. Raw PCM has no trailer to finalize, so an
// interrupted capture is still a valid buffer.
func (r FFmpegRecorder) Record(ctx context.Context, max time.Duration) ([]byte, error) {
	cmd := exec.Command(r.path(), r.Args(max)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
			strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(err.Error(), "no such file") {
			return nil, fmt.Errorf("no encuentro ffmpeg: instalalo con `brew install ffmpeg`")
		}
		return nil, fmt.Errorf("no pude arrancar la grabación: %w", err)
	}

	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	select {
	case <-ctx.Done():
		// SIGINT makes ffmpeg flush what it already captured.
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	case <-done:
	}

	if out.Len() == 0 {
		return nil, micError(errb.String())
	}
	return out.Bytes(), nil
}

// micError turns ffmpeg's stderr into something a person can act on.
func micError(stderr string) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "permission"), strings.Contains(low, "not authorized"),
		strings.Contains(low, "input/output error"):
		return errors.New("macOS no dio permiso al micrófono: Ajustes del Sistema → Privacidad y seguridad → Micrófono, y volvé a abrir la terminal")
	case strings.Contains(low, "device") && strings.Contains(low, "busy"):
		return errors.New("el micrófono está ocupado por otra app")
	case strings.Contains(low, "does not exist"), strings.Contains(low, "not found"):
		return fmt.Errorf("no existe ese dispositivo de audio (probá otro con --stt-device; `ffmpeg -f avfoundation -list_devices true -i \"\"` los lista)")
	}
	line := strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])
	if line == "" {
		return errors.New("la grabación no devolvió audio")
	}
	return fmt.Errorf("la grabación falló: %s", line)
}
