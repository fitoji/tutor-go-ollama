// Package config parses flags and environment into a single Config.
package config

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"speakeasy/internal/speech"
)

type Config struct {
	Host            string // Ollama base URL (also hosts the voice model)
	Provider        string // ollama, groq, xai or custom
	LLM             string // chat model
	TTS             string // speech model (always served by Ollama)
	Voice           string
	NumCtx          int    // prompt budget in tokens; for Ollama also its num_ctx
	KeepAlive       string // how long Ollama keeps the LLM loaded
	Think           bool   // allow thinking mode (Ollama only)
	Corrections     bool   // run the correction pass
	NotesLang       string // language of the short correction notes
	Target          string // language being practised: en or de
	Persona         string // file that replaces the tutor's style
	ReviewPersona   string // file that sets the tone of the correction notes
	User            string // learner name (empty = ask at startup)
	StatePath       string // state file, or the base path each learner derives from
	StateSet        bool   // true when --state named an exact file
	TTSSet          bool   // true when --tts was given
	STTLangSet      bool   // true when --stt-lang was given
	Mute            bool
	MemLimitMiB     int  // soft Go heap limit
	SelfTest        bool // check the configured providers and exit
	ProgressSummary bool // ask the model for the /progress note

	APIBase   string // OpenAI-compatible base URL (remote providers)
	APIKeyEnv string // name of the environment variable holding the key
	APIKey    string

	// Voice input: Enter on an empty line records and transcribes.
	STT          string // groq, whisper or off
	STTModel     string
	STTLang      string // ISO-639-1; empty lets the model guess
	STTDevice    string // ffmpeg avfoundation audio device
	STTBase      string
	STTKeyEnv    string
	STTKey       string
	VoiceNote    string // set when a voice choice could not be applied
	WhisperBin   string
	WhisperModel string
	RecordMaxSec int
	RecordMax    time.Duration
	VADMs        int
	VAD          time.Duration
}

// Remote reports whether the chat model runs on a remote API.
func (c Config) Remote() bool { return c.Provider != "ollama" }

type preset struct {
	base, keyEnv, model string
	numCtx              int
}

// Remote providers get a smaller prompt budget by default: it keeps every
// request cheap and well under free-tier token-per-minute limits.
var presets = map[string]preset{
	"ollama": {model: "qwen3:8b", numCtx: 8192},
	"groq":   {base: "https://api.groq.com/openai/v1", keyEnv: "GROQ_API_KEY", model: "llama-3.3-70b-versatile", numCtx: 4096},
	"xai":    {base: "https://api.x.ai/v1", keyEnv: "XAI_API_KEY", numCtx: 4096},
	"custom": {keyEnv: "LLM_API_KEY", numCtx: 4096},
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

// NormalizeHost turns OLLAMA_HOST-style values ("127.0.0.1", "host:11434",
// "http://host:11434/") into a base URL with scheme and port.
func NormalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		h = "127.0.0.1:11434"
	}
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	u, err := url.Parse(h)
	if err != nil || u.Host == "" {
		return "http://127.0.0.1:11434"
	}
	if u.Port() == "" {
		u.Host += ":11434"
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/")
}

// Parse reads flags (with env fallbacks) from args.
func Parse(args []string) (Config, error) {
	var c Config
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	fs := flag.NewFlagSet("speakeasy", flag.ContinueOnError)
	fs.StringVar(&c.Provider, "provider", envOr("SPEAKEASY_PROVIDER", "groq"), "chat provider: groq, xai, ollama (local) or custom (with --api-base)")
	fs.StringVar(&c.Host, "host", os.Getenv("OLLAMA_HOST"), "Ollama server, used for the voice (default $OLLAMA_HOST or 127.0.0.1:11434)")
	fs.StringVar(&c.LLM, "llm", envOr("SPEAKEASY_LLM", ""), "chat model (default depends on the provider)")
	fs.StringVar(&c.APIBase, "api-base", envOr("SPEAKEASY_API_BASE", ""), "OpenAI-compatible base URL (default depends on the provider)")
	fs.StringVar(&c.APIKeyEnv, "api-key-env", envOr("SPEAKEASY_API_KEY_ENV", ""), "environment variable that holds the API key")
	fs.StringVar(&c.TTS, "tts", envOr("SPEAKEASY_TTS", "phonon"), "speech model")
	fs.StringVar(&c.Voice, "voice", envOr("SPEAKEASY_VOICE", ""), "voice name (empty = model default)")
	fs.IntVar(&c.NumCtx, "num-ctx", envInt("SPEAKEASY_NUM_CTX", 0), "prompt budget in tokens (default 4096 remote, 8192 ollama)")
	fs.StringVar(&c.KeepAlive, "keep-alive", envOr("SPEAKEASY_KEEP_ALIVE", "30m"), "how long Ollama keeps the LLM in memory")
	fs.BoolVar(&c.Think, "think", false, "allow model thinking mode (ollama only)")
	fs.BoolVar(&c.Corrections, "corrections", true, "show corrections after each turn (the review pass still runs: it also remembers facts)")
	fs.StringVar(&c.NotesLang, "notes", envOr("SPEAKEASY_NOTES", "Spanish"), "language of correction notes")
	fs.StringVar(&c.Persona, "persona", envOr("SPEAKEASY_PERSONA", filepath.Join(home, ".speakeasy", "tutor.md")), "file that replaces the tutor's style (missing = built-in)")
	fs.StringVar(&c.ReviewPersona, "review-persona", envOr("SPEAKEASY_REVIEW_PERSONA", filepath.Join(home, ".speakeasy", "review.md")), "file that sets the tone of the correction notes (missing = neutral)")
	fs.StringVar(&c.Target, "target", envOr("SPEAKEASY_TARGET", "en"), "language you are practising: en or de")
	fs.StringVar(&c.User, "user", envOr("SPEAKEASY_USER", ""), "learner name; each name keeps its own level and corrections (asks at startup when empty)")
	fs.StringVar(&c.StatePath, "state", filepath.Join(home, ".speakeasy", "state.json"), "state file; without this flag each learner gets a directory next to it")
	fs.BoolVar(&c.Mute, "mute", false, "text only, no voice")
	fs.IntVar(&c.MemLimitMiB, "mem-limit", 256, "soft memory limit for this process, in MiB")
	fs.BoolVar(&c.SelfTest, "selftest", false, "check the configured chat, corrections and voice, then exit")
	fs.BoolVar(&c.ProgressSummary, "progress-summary", true, "ask the model for the short note in /progress")
	fs.StringVar(&c.STT, "stt", envOr("SPEAKEASY_STT", "groq"), "speech to text: groq, whisper (local) or off (Enter on an empty line records)")
	fs.StringVar(&c.STTModel, "stt-model", envOr("SPEAKEASY_STT_MODEL", "whisper-large-v3-turbo"), "transcription model")
	fs.StringVar(&c.STTLang, "stt-lang", envOr("SPEAKEASY_STT_LANG", "en"), "language you speak, ISO-639-1 (empty = guess)")
	fs.StringVar(&c.STTDevice, "stt-device", envOr("SPEAKEASY_STT_DEVICE", ":0"), "ffmpeg avfoundation audio device")
	fs.StringVar(&c.STTBase, "stt-base", envOr("SPEAKEASY_STT_BASE", "https://api.groq.com/openai/v1"), "OpenAI-compatible transcription base URL")
	fs.StringVar(&c.STTKeyEnv, "stt-key-env", envOr("SPEAKEASY_STT_KEY_ENV", "GROQ_API_KEY"), "environment variable with the transcription key")
	fs.StringVar(&c.WhisperBin, "whisper-bin", envOr("SPEAKEASY_WHISPER_BIN", "whisper-cli"), "whisper.cpp binary (for --stt whisper)")
	fs.StringVar(&c.WhisperModel, "whisper-model", envOr("SPEAKEASY_WHISPER_MODEL", filepath.Join(home, ".speakeasy", "models", "ggml-small.en.bin")), "whisper.cpp ggml model (for --stt whisper)")
	fs.IntVar(&c.RecordMaxSec, "record-max", envInt("SPEAKEASY_RECORD_MAX", 30), "longest recording, in seconds")
	fs.IntVar(&c.VADMs, "vad", envInt("SPEAKEASY_VAD", 800), "stop recording after this many ms of silence once you speak (0 = only the Enter stops it)")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	// Only an explicit --state keeps the single-file behaviour; the default
	// path is a base that each learner derives their own directory from.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "state":
			c.StateSet = true
		case "tts":
			c.TTSSet = true
		case "stt-lang":
			c.STTLangSet = true
		}
	})
	c.User = strings.TrimSpace(c.User)
	c.Target = strings.ToLower(strings.TrimSpace(c.Target))
	if c.Target != "en" && c.Target != "de" {
		return c, fmt.Errorf("unknown target %q (use en or de)", c.Target)
	}
	// The language decides the voice model and the microphone language, unless
	// the flag was given on purpose.
	if !c.TTSSet && c.Target != "en" {
		c.TTS = "phonon:" + c.Target
	}
	if !c.STTLangSet {
		c.STTLang = c.Target
	}
	// The 40M checkpoints carry a single voice named "default": a measured
	// alias (femenina/masculina) cannot apply, and sending it would fail at
	// speech time.
	if SingleVoiceModel(c.TTS) && c.Voice != "" && c.Voice != "default" {
		c.VoiceNote = fmt.Sprintf("%s tiene una sola voz: uso la del modelo", c.TTS)
		c.Voice = ""
	}
	c.STT = strings.ToLower(strings.TrimSpace(c.STT))
	if c.STT != "groq" && c.STT != "whisper" && c.STT != "off" {
		return c, fmt.Errorf("unknown stt %q (use groq, whisper or off)", c.STT)
	}
	if c.STT == "groq" {
		c.STTKey = strings.TrimSpace(os.Getenv(c.STTKeyEnv))
	}
	if c.RecordMaxSec < 1 {
		c.RecordMaxSec = 1
	}
	if c.RecordMaxSec > 300 {
		c.RecordMaxSec = 300
	}
	c.RecordMax = time.Duration(c.RecordMaxSec) * time.Second
	if c.VADMs < 0 {
		c.VADMs = 0
	}
	if c.VADMs > 10000 {
		c.VADMs = 10000
	}
	c.VAD = time.Duration(c.VADMs) * time.Millisecond
	// "femenina" / "masculina" pick a measured Phonon voice; any other value is
	// an exact voice name.
	c.Voice = speech.ResolveVoice(c.Voice)

	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	p, ok := presets[c.Provider]
	if !ok {
		return c, fmt.Errorf("unknown provider %q (use groq, xai, ollama or custom)", c.Provider)
	}
	if c.LLM == "" {
		c.LLM = p.model
	}
	if c.LLM == "" {
		return c, fmt.Errorf("provider %s needs a model: pass --llm", c.Provider)
	}
	if c.NumCtx == 0 {
		c.NumCtx = p.numCtx
	}
	if c.NumCtx < 2048 {
		return c, fmt.Errorf("num-ctx must be at least 2048, got %d", c.NumCtx)
	}
	c.Host = NormalizeHost(c.Host)

	if c.Remote() {
		if c.APIBase == "" {
			c.APIBase = p.base
		}
		if c.APIBase == "" {
			return c, fmt.Errorf("provider %s needs --api-base (e.g. https://host/v1)", c.Provider)
		}
		if c.APIKeyEnv == "" {
			c.APIKeyEnv = p.keyEnv
		}
		c.APIKey = strings.TrimSpace(os.Getenv(c.APIKeyEnv))
		if c.APIKey == "" {
			return c, fmt.Errorf("falta la clave de API: exporta %s=... (créala en la consola del proveedor) o usa --provider ollama", c.APIKeyEnv)
		}
	}
	return c, nil
}

// SingleVoiceModel reports whether a Phonon checkpoint carries only its
// "default" voice. The 31M English model has the measured catalogue; the 40M
// per-language and multilingual ones do not.
func SingleVoiceModel(tts string) bool {
	t := strings.ToLower(strings.TrimSpace(tts))
	if !strings.HasPrefix(t, "phonon") {
		return false
	}
	for _, tag := range []string{"de", "fr", "es", "pt", "multilingual", "40m-"} {
		if strings.Contains(t, ":"+tag) {
			return true
		}
	}
	return false
}
