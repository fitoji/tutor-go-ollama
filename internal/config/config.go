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

	"speakeasy/internal/speech"
)

type Config struct {
	Host        string // Ollama base URL (also hosts the voice model)
	Provider    string // ollama, groq, xai or custom
	LLM         string // chat model
	TTS         string // speech model (always served by Ollama)
	Voice       string
	NumCtx      int    // prompt budget in tokens; for Ollama also its num_ctx
	KeepAlive   string // how long Ollama keeps the LLM loaded
	Think       bool   // allow thinking mode (Ollama only)
	Corrections bool   // run the correction pass
	NotesLang   string // language of the short correction notes
	User        string // learner name (empty = ask at startup)
	StatePath   string // state file, or the base path each learner derives from
	StateSet    bool   // true when --state named an exact file
	Mute        bool
	MemLimitMiB int  // soft Go heap limit
	SelfTest    bool // check the configured providers and exit

	APIBase   string // OpenAI-compatible base URL (remote providers)
	APIKeyEnv string // name of the environment variable holding the key
	APIKey    string
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
	fs.StringVar(&c.User, "user", envOr("SPEAKEASY_USER", ""), "learner name; each name keeps its own level and corrections (asks at startup when empty)")
	fs.StringVar(&c.StatePath, "state", filepath.Join(home, ".speakeasy", "state.json"), "state file; without this flag each learner gets a directory next to it")
	fs.BoolVar(&c.Mute, "mute", false, "text only, no voice")
	fs.IntVar(&c.MemLimitMiB, "mem-limit", 256, "soft memory limit for this process, in MiB")
	fs.BoolVar(&c.SelfTest, "selftest", false, "check the configured chat, corrections and voice, then exit")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	// Only an explicit --state keeps the single-file behaviour; the default
	// path is a base that each learner derives their own directory from.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "state" {
			c.StateSet = true
		}
	})
	c.User = strings.TrimSpace(c.User)
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
