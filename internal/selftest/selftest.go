// Package selftest probes the configured providers with the same requests the
// app makes at runtime, so that `speakeasy --selftest` can answer, in a few
// seconds, the questions the README otherwise leaves open: does Ollama serve
// phonon, does it return PCM, does the chat stream, does the model obey the
// correction JSON.
package selftest

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"speakeasy/internal/config"
	"speakeasy/internal/llm"
	"speakeasy/internal/ollama"
	"speakeasy/internal/speech"
	"speakeasy/internal/tutor"
)

const (
	// Short probes talk to a local daemon; it should answer at once.
	shortTimeout = 5 * time.Second
	// Speech and chat may load a model on the first request, which takes a
	// while on a CPU-only machine.
	longTimeout = 2 * time.Minute
)

// Env is everything the probes need, already configured and wired by the
// caller so that the probes exercise the same clients the app uses.
type Env struct {
	Cfg      config.Config
	Ollama   *ollama.Client
	Chat     llm.Chatter
	Settings tutor.Settings
}

// Result is one probe outcome. Detail is user-facing and always filled in,
// including on success, so a passing run still shows what was observed.
type Result struct {
	Name   string
	Detail string
	OK     bool
}

// Run executes every applicable probe, writes one line per probe to w and
// returns an error if any probe failed. Probes are skipped when the feature
// they cover is switched off, so a muted remote setup probes only the chat.
func Run(ctx context.Context, w io.Writer, env Env) error {
	results := probes(ctx, env)
	failed := 0
	for _, r := range results {
		status := "[ok]"
		if !r.OK {
			status = "[FAIL]"
			failed++
		}
		fmt.Fprintf(w, "%-7s %-13s %s\n", status, r.Name+":", r.Detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}

func probes(ctx context.Context, env Env) []Result {
	var out []Result
	// Ollama is needed for a local chat model and for the voice, no matter
	// where the chat runs.
	if !env.Cfg.Remote() || !env.Cfg.Mute {
		out = append(out, checkOllama(ctx, env))
	}
	if !env.Cfg.Mute {
		out = append(out, checkVoiceModel(ctx, env), checkSpeech(ctx, env))
	}
	return append(out, checkChat(ctx, env), checkCorrections(ctx, env))
}

func checkOllama(ctx context.Context, env Env) Result {
	vctx, cancel := context.WithTimeout(ctx, shortTimeout)
	defer cancel()
	ver, err := env.Ollama.Version(vctx)
	if err != nil {
		return Result{"ollama", fmt.Sprintf("cannot reach %s: %v", env.Cfg.Host, err), false}
	}
	detail := fmt.Sprintf("%s at %s", ver, env.Cfg.Host)
	// Only the voice needs a recent Ollama; a text-only local chat works on
	// older versions.
	if !env.Cfg.Mute && !ollama.VersionAtLeast(ver, 0, 40, 9) {
		return Result{"ollama", detail + "; the voice needs 0.40.9 or newer", false}
	}
	return Result{"ollama", detail, true}
}

func checkVoiceModel(ctx context.Context, env Env) Result {
	vctx, cancel := context.WithTimeout(ctx, shortTimeout)
	defer cancel()
	ok, err := env.Ollama.HasModel(vctx, env.Cfg.TTS)
	if err != nil {
		return Result{"voice", err.Error(), false}
	}
	if !ok {
		return Result{"voice", fmt.Sprintf("%s is not pulled: run `ollama pull %s`", env.Cfg.TTS, env.Cfg.TTS), false}
	}
	// Report the tag Ollama actually resolves, so the user sees "phonon:latest".
	name := env.Cfg.TTS
	if !strings.Contains(name, ":") {
		name += ":latest"
	}
	return Result{"voice", name + " is pulled", true}
}

// checkSpeech proves the whole voice path: a real synthesis request, and PCM
// that can be wrapped in a WAV header and played.
func checkSpeech(ctx context.Context, env Env) Result {
	sctx, cancel := context.WithTimeout(ctx, longTimeout)
	defer cancel()
	pcm, err := env.Ollama.Speech(sctx, env.Cfg.TTS, env.Cfg.Voice, "Hello, this is a test of the speech model.")
	if err != nil {
		return Result{"speech", err.Error(), false}
	}
	if len(pcm) == 0 {
		return Result{"speech", "no PCM returned", false}
	}
	if len(pcm)%2 != 0 {
		return Result{"speech", fmt.Sprintf("%d bytes, not whole 16-bit samples", len(pcm)), false}
	}
	secs := float64(len(pcm)) / (2 * speech.SampleRate)
	return Result{"speech", fmt.Sprintf("%.1f s of PCM (%d bytes)", secs, len(pcm)), true}
}

func checkChat(ctx context.Context, env Env) Result {
	cctx, cancel := context.WithTimeout(ctx, longTimeout)
	defer cancel()
	reply, tokens, err := tutor.ProbeReply(cctx, env.Chat, env.Settings)
	if err != nil {
		return Result{"chat", err.Error(), false}
	}
	if tokens > 0 {
		return Result{"chat", fmt.Sprintf("%q (%d prompt tokens)", reply, tokens), true}
	}
	return Result{"chat", fmt.Sprintf("%q (prompt size not reported)", reply), true}
}

func checkCorrections(ctx context.Context, env Env) Result {
	cctx, cancel := context.WithTimeout(ctx, longTimeout)
	defer cancel()
	got, err := tutor.ProbeCorrections(cctx, env.Chat, env.Settings)
	if err != nil {
		return Result{"corrections", err.Error(), false}
	}
	if len(got) == 0 {
		return Result{"corrections", fmt.Sprintf("the model found no mistake in %q", tutor.ProbeSample), false}
	}
	first := got[0]
	return Result{"corrections", fmt.Sprintf("%d corrections, first %q -> %q (%s)", len(got), first.Original, first.Better, first.Category), true}
}
