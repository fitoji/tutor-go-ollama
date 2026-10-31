package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// clean isolates a test from the developer's real environment.
func clean(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SPEAKEASY_PROVIDER", "SPEAKEASY_LLM", "SPEAKEASY_API_BASE", "SPEAKEASY_API_KEY_ENV",
		"SPEAKEASY_TTS", "SPEAKEASY_VOICE", "SPEAKEASY_NUM_CTX", "SPEAKEASY_KEEP_ALIVE", "SPEAKEASY_NOTES",
		"SPEAKEASY_USER", "SPEAKEASY_TARGET", "OLLAMA_HOST", "GROQ_API_KEY", "XAI_API_KEY", "LLM_API_KEY", "MY_KEY",
	} {
		t.Setenv(k, "")
	}
}

func TestDefaultsToGroq(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "  gsk_test \n")
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "groq" || !c.Remote() {
		t.Errorf("provider %q", c.Provider)
	}
	if c.LLM != "llama-3.3-70b-versatile" || c.NumCtx != 4096 {
		t.Errorf("llm=%q ctx=%d", c.LLM, c.NumCtx)
	}
	if c.APIBase != "https://api.groq.com/openai/v1" || c.APIKey != "gsk_test" {
		t.Errorf("base=%q key=%q", c.APIBase, c.APIKey)
	}
	if c.TTS != "phonon" || c.Host != "http://127.0.0.1:11434" {
		t.Errorf("voice side: tts=%q host=%q", c.TTS, c.Host)
	}
}

func TestMissingKeyExplainsWhatToDo(t *testing.T) {
	clean(t)
	_, err := Parse(nil)
	if err == nil || !strings.Contains(err.Error(), "GROQ_API_KEY") || !strings.Contains(err.Error(), "--provider ollama") {
		t.Errorf("error %v", err)
	}
}

func TestOllamaNeedsNoKey(t *testing.T) {
	clean(t)
	c, err := Parse([]string{"--provider", "ollama"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Remote() || c.LLM != "qwen3:8b" || c.NumCtx != 8192 || c.APIKey != "" {
		t.Errorf("%+v", c)
	}
}

func TestXAIAndCustomProviders(t *testing.T) {
	clean(t)
	t.Setenv("XAI_API_KEY", "xai-1")
	if _, err := Parse([]string{"--provider", "xai"}); err == nil || !strings.Contains(err.Error(), "--llm") {
		t.Errorf("xai without a model: %v", err)
	}
	c, err := Parse([]string{"--provider", "xai", "--llm", "some-model"})
	if err != nil || c.APIBase != "https://api.x.ai/v1" || c.APIKey != "xai-1" || c.LLM != "some-model" {
		t.Errorf("xai: %+v %v", c, err)
	}

	t.Setenv("MY_KEY", "k-1")
	if _, err := Parse([]string{"--provider", "custom", "--llm", "m", "--api-key-env", "MY_KEY"}); err == nil || !strings.Contains(err.Error(), "--api-base") {
		t.Errorf("custom without base: %v", err)
	}
	c, err = Parse([]string{"--provider", "custom", "--llm", "m", "--api-base", "https://api.deepseek.com/v1", "--api-key-env", "MY_KEY"})
	if err != nil || c.APIBase != "https://api.deepseek.com/v1" || c.APIKey != "k-1" {
		t.Errorf("custom: %+v %v", c, err)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "k")
	c, err := Parse([]string{"--llm", "llama-3.1-8b-instant", "--num-ctx", "3000", "--mute"})
	if err != nil || c.LLM != "llama-3.1-8b-instant" || c.NumCtx != 3000 || !c.Mute {
		t.Errorf("%+v %v", c, err)
	}
	if _, err := Parse([]string{"--num-ctx", "1000"}); err == nil {
		t.Error("tiny num-ctx must be rejected")
	}
	if _, err := Parse([]string{"--provider", "openai"}); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unknown provider: %v", err)
	}
	t.Setenv("SPEAKEASY_PROVIDER", "OLLAMA")
	if c, err := Parse(nil); err != nil || c.Provider != "ollama" {
		t.Errorf("provider from env, case-insensitive: %+v %v", c, err)
	}
}

func TestSelfTestFlag(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "k")
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.SelfTest {
		t.Error("selftest must be off by default")
	}
	c, err = Parse([]string{"--selftest"})
	if err != nil || !c.SelfTest {
		t.Errorf("--selftest not parsed: %+v %v", c, err)
	}
	// A selftest still needs a usable configuration, so a missing key fails
	// before any probe runs.
	clean(t)
	if _, err := Parse([]string{"--selftest"}); err == nil || !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("a selftest without a key must explain what is missing: %v", err)
	}
}

// TestUserAndStateFlags pins how a learner is chosen from the command line:
// --user selects the profile without asking, and only an explicit --state
// keeps the single-file behaviour instead of the per-learner directory.
func TestUserAndStateFlags(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "k")
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "" || c.StateSet {
		t.Errorf("nothing should be preselected: user=%q stateSet=%v", c.User, c.StateSet)
	}
	if !strings.HasSuffix(c.StatePath, filepath.Join(".speakeasy", "state.json")) {
		t.Errorf("default state path = %q", c.StatePath)
	}

	c, err = Parse([]string{"--user", "  Ana  "})
	if err != nil || c.User != "Ana" || c.StateSet {
		t.Errorf("--user: %+v %v", c, err)
	}

	c, err = Parse([]string{"--state", "/tmp/x/state.json"})
	if err != nil || !c.StateSet || c.StatePath != "/tmp/x/state.json" {
		t.Errorf("--state: %+v %v", c, err)
	}

	t.Setenv("SPEAKEASY_USER", "Beto")
	if c, err := Parse(nil); err != nil || c.User != "Beto" {
		t.Errorf("user from env: %+v %v", c, err)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"":                       "http://127.0.0.1:11434",
		"127.0.0.1":              "http://127.0.0.1:11434",
		"myhost:9999":            "http://myhost:9999",
		"http://box:11434/":      "http://box:11434",
		"https://secure.example": "https://secure.example:11434",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestVoiceAliases pins the short names people actually type, while an exact
// Phonon voice name keeps working.
func TestVoiceAliases(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "k")
	c, err := Parse([]string{"--voice", "femenina"})
	if err != nil || c.Voice != "Freya" {
		t.Errorf("--voice femenina: %+v %v", c, err)
	}
	c, err = Parse([]string{"--voice", "M"})
	if err != nil || c.Voice != "Spencer" {
		t.Errorf("--voice M: %+v %v", c, err)
	}
	c, err = Parse([]string{"--voice", "Harper"})
	if err != nil || c.Voice != "Harper" {
		t.Errorf("--voice Harper: %+v %v", c, err)
	}
	t.Setenv("SPEAKEASY_VOICE", "masculina")
	if c, err := Parse(nil); err != nil || c.Voice != "Spencer" {
		t.Errorf("voice from env: %+v %v", c, err)
	}
}

// TestTargetLanguage pins the only two languages that exist today.
func TestTargetLanguage(t *testing.T) {
	clean(t)
	t.Setenv("GROQ_API_KEY", "k")
	if c, err := Parse(nil); err != nil || c.Target != "en" {
		t.Errorf("default target: %+v %v", c, err)
	}
	if c, err := Parse([]string{"--target", "DE"}); err != nil || c.Target != "de" {
		t.Errorf("--target DE: %+v %v", c, err)
	}
	if _, err := Parse([]string{"--target", "fr"}); err == nil || !strings.Contains(err.Error(), "en or de") {
		t.Errorf("unknown target: %v", err)
	}
	if _, err := Parse([]string{"--target", ""}); err == nil || !strings.Contains(err.Error(), "en or de") {
		t.Errorf("empty target: %v", err)
	}
	t.Setenv("SPEAKEASY_TARGET", "de")
	if c, err := Parse(nil); err != nil || c.Target != "de" {
		t.Errorf("target from env: %+v %v", c, err)
	}
}
