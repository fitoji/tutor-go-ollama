package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FFmpegRecorder captures the microphone through ffmpeg's avfoundation input.
// It shells out instead of using cgo: one external tool, zero Go dependencies,
// and the same process the user already has for other audio work.
type FFmpegRecorder struct {
	Path   string // "ffmpeg" by default
	Device string // avfoundation audio device, ":0" by default
	// VAD stops the capture after this much silence, once speech has been
	// heard. Zero disables it and the caller decides when to stop. It can change
	// mid-session with /vad.
	VAD time.Duration

	mu    sync.Mutex
	vad   time.Duration
	isSet bool
}

// SetVAD changes the silence hold for the next recording. Zero means only the
// Enter stops the capture.
func (r *FFmpegRecorder) SetVAD(d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.mu.Lock()
	r.vad, r.isSet = d, true
	r.mu.Unlock()
}

// CurrentVAD is the hold in use, with the initial value until it is changed.
func (r *FFmpegRecorder) CurrentVAD() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.isSet {
		return r.VAD
	}
	return r.vad
}

// vadBlock is how much audio is measured at a time: 100 ms at 16 kHz.
const vadBlock = SampleRate / 10 * 2

func (r *FFmpegRecorder) path() string {
	if r.Path == "" {
		return "ffmpeg"
	}
	return r.Path
}

func (r *FFmpegRecorder) device() string {
	if r.Device == "" {
		return ":0"
	}
	return r.Device
}

// Args is the exact command line, exposed so a test can pin it.
func (r *FFmpegRecorder) Args(max time.Duration) []string {
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
func (r *FFmpegRecorder) Record(ctx context.Context, max time.Duration) ([]byte, error) {
	cmd := exec.Command(r.path(), r.Args(max)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var out, errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
			strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(err.Error(), "no such file") {
			return nil, fmt.Errorf("no encuentro ffmpeg: instalalo con `brew install ffmpeg`")
		}
		return nil, fmt.Errorf("no pude arrancar la grabación: %w", err)
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		copyUntilSilence(stdout, &out, r.CurrentVAD())
	}()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	select {
	case <-ctx.Done():
	case <-readDone: // the VAD ended the capture, or ffmpeg closed the pipe
	case <-done:
	}
	// SIGINT makes ffmpeg flush what it already captured.
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	// A grandchild can keep the pipe open after the process is gone: closing it
	// unblocks the reader instead of hanging the session.
	_ = stdout.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
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

// copyUntilSilence reads the capture in small blocks and stops writing after
// hold of silence, but only once something louder than room noise was heard:
// without that guard it would cut before the learner starts talking.
func copyUntilSilence(stdout io.Reader, out *bytes.Buffer, hold time.Duration) {
	if hold <= 0 {
		_, _ = io.Copy(out, stdout)
		return
	}
	needSilentBytes := int(hold.Seconds() * SampleRate * 2)
	buf := make([]byte, vadBlock)
	spoken, silent := false, 0
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			if loud(buf[:n]) {
				spoken, silent = true, 0
			} else if spoken {
				silent += n
			}
			if spoken && silent >= needSilentBytes {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
