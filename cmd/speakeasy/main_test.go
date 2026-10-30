package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"speakeasy/internal/config"
	"speakeasy/internal/conv"
	"speakeasy/internal/learner"
	"speakeasy/internal/ollama"
	"speakeasy/internal/openai"
	"speakeasy/internal/speech"
	"speakeasy/internal/tutor"
)

// capture runs fn with os.Stdout and os.Stderr replaced by pipes and returns
// both streams, so the tests can assert on what the user would read.
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	outc := make(chan string, 1)
	errc := make(chan string, 1)
	go func() { b, _ := io.ReadAll(rOut); outc <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); errc <- string(b) }()

	fn()

	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	return <-outc, <-errc
}

// fakeOllama records what prepare does to a (fake) Ollama daemon.
type fakeOllama struct {
	mu       sync.Mutex
	paths    []string
	version  string
	tags     []string
	running  []map[string]any
	unloaded []string
	loaded   map[string]any
	chatBody map[string]any
}

func newFakeOllama() *fakeOllama {
	return &fakeOllama{
		version: "0.40.9",
		tags:    []string{"qwen3:8b", "phonon:latest"},
		running: []map[string]any{{"name": "llama3:8b", "size": 5368709120}},
	}
}

func (f *fakeOllama) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()

	switch r.URL.Path {
	case "/api/version":
		json.NewEncoder(w).Encode(map[string]string{"version": f.version})
	case "/api/tags":
		var models []map[string]string
		for _, t := range f.tags {
			models = append(models, map[string]string{"name": t})
		}
		json.NewEncoder(w).Encode(map[string]any{"models": models})
	case "/api/ps":
		json.NewEncoder(w).Encode(map[string]any{"models": f.running})
	case "/api/generate":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.unloaded = append(f.unloaded, body["model"].(string))
		f.mu.Unlock()
		io.WriteString(w, `{"done":true}`)
	case "/api/chat":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.chatBody = body
		f.mu.Unlock()
		io.WriteString(w, `{"done":true}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOllama) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeOllama) snapshot() (paths, unloaded []string, load map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...), append([]string(nil), f.unloaded...), f.chatBody
}

func localConfig(host string) config.Config {
	return config.Config{
		Host: host, Provider: "ollama", LLM: "qwen3:8b", TTS: "phonon",
		NumCtx: 8192, KeepAlive: "30m", Corrections: true, NotesLang: "Spanish",
	}
}

// TestPrepareSkipsOllamaWithoutVoiceOnARemoteProvider pins the documented
// promise: a remote provider with --mute needs no Ollama at all.
func TestPrepareSkipsOllamaWithoutVoiceOnARemoteProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("prepare must not call Ollama, called %s", r.URL.Path)
	}))
	defer srv.Close()
	cfg := config.Config{Provider: "groq", LLM: "m", Mute: true, Host: srv.URL}

	if err := prepare(context.Background(), ollama.New(srv.URL), cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
}

func TestPrepareExplainsHowToStartOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens on this port any more

	cfg := config.Config{Provider: "groq", LLM: "m", TTS: "phonon", Host: url}
	err := prepare(context.Background(), ollama.New(url), cfg)
	if err == nil {
		t.Fatal("expected an error when Ollama is down")
	}
	for _, want := range []string{"no puedo conectar con Ollama", "ollama serve"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}
}

func TestPrepareReportsAMissingModel(t *testing.T) {
	f := newFakeOllama()
	f.tags = []string{"phonon:latest"}
	srv := f.server(t)

	cfg := localConfig(srv.URL)
	err := prepare(context.Background(), ollama.New(srv.URL), cfg)
	if err == nil {
		t.Fatal("expected an error for a missing chat model")
	}
	if !strings.Contains(err.Error(), `falta el modelo "qwen3:8b"`) || !strings.Contains(err.Error(), "ollama pull") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// TestPrepareFreesForeignModelsAndLoadsOurs is the core of the memory policy:
// everything that is not ours is unloaded, and our model is loaded once with
// the same num_ctx later requests will use.
func TestPrepareFreesForeignModelsAndLoadsOurs(t *testing.T) {
	f := newFakeOllama()
	f.running = []map[string]any{
		{"name": "llama3:8b", "size": 5368709120},
		{"name": "phonon:latest", "size": 104857600},
	}
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var err error
	out, _ := capture(t, func() {
		err = prepare(context.Background(), ollama.New(srv.URL), cfg)
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	_, unloaded, load := f.snapshot()
	if len(unloaded) != 1 || unloaded[0] != "llama3:8b" {
		t.Errorf("should unload only the foreign model, unloaded %v", unloaded)
	}
	if load["model"] != "qwen3:8b" {
		t.Errorf("should load the chat model, body %v", load)
	}
	if load["keep_alive"] != "30m" {
		t.Errorf("keep_alive not passed, body %v", load)
	}
	if opts, ok := load["options"].(map[string]any); !ok || opts["num_ctx"] != float64(8192) {
		t.Errorf("num_ctx not passed to Load, body %v", load)
	}
	for _, want := range []string{"descargado llama3:8b", "cargando qwen3:8b"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should mention %q:\n%s", want, out)
		}
	}
}

func TestPrepareWarnsButContinuesOnAnOldOllama(t *testing.T) {
	f := newFakeOllama()
	f.version = "0.30.0"
	srv := f.server(t)
	cfg := localConfig(srv.URL)

	var err error
	_, errOut := capture(t, func() {
		err = prepare(context.Background(), ollama.New(srv.URL), cfg)
	})
	if err != nil {
		t.Fatalf("an old Ollama must not fail a text-only start: %v", err)
	}
	if !strings.Contains(errOut, "0.40.9") {
		t.Errorf("expected a warning naming the required version, got %q", errOut)
	}
}

func newStore(t *testing.T) *conv.Store {
	t.Helper()
	s, err := conv.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// statusConfig is remote and muted, so /status never calls Ollama.
func statusConfig() config.Config {
	return config.Config{Provider: "groq", LLM: "llama-3.3-70b-versatile", NumCtx: 4096, Mute: true}
}

func TestCommandResetKeepsTheProfile(t *testing.T) {
	store := newStore(t)
	store.Update(func(d *conv.Data) {
		d.Profile.Level = "C1"
		d.AddTopic("cine")
		d.AddTurn("user", "hello")
		d.AddTurn("assistant", "hi there")
	})
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()

	out, _ := capture(t, func() {
		command(context.Background(), "/reset", ollama.New("http://127.0.0.1:1"), cfg, store, tut)
	})
	if !strings.Contains(out, "conversación reiniciada") {
		t.Errorf("unexpected output: %q", out)
	}
	d := store.View()
	if len(d.Turns) != 0 || d.Summary != "" {
		t.Errorf("reset must clear the conversation, got %+v", d)
	}
	if d.Profile.Level != "C1" || len(d.Profile.Topics) != 1 {
		t.Errorf("reset must keep the profile, got %+v", d.Profile)
	}
}

func TestCommandLevel(t *testing.T) {
	cases := []struct {
		arg, want, out string
	}{
		{"b2", "B2", "nivel: B2"},
		{" C1 ", "C1", "nivel: C1"},
		{"Z9", "B1", "usa A2, B1, B2 o C1"},
	}
	for _, c := range cases {
		store := newStore(t)
		tut := tutor.New(nil, store, tutor.Settings{})
		cfg := statusConfig()

		out, _ := capture(t, func() {
			command(context.Background(), "/level "+c.arg, ollama.New("http://127.0.0.1:1"), cfg, store, tut)
		})
		if !strings.Contains(out, c.out) {
			t.Errorf("/level %q output %q, want %q", c.arg, out, c.out)
		}
		if got := store.View().Profile.Level; got != c.want {
			t.Errorf("/level %q set level %q, want %q", c.arg, got, c.want)
		}
	}
}

func TestCommandTopic(t *testing.T) {
	store := newStore(t)
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()
	cl := ollama.New("http://127.0.0.1:1")

	out, _ := capture(t, func() { command(context.Background(), "/topic cine", cl, cfg, store, tut) })
	if !strings.Contains(out, "tema añadido") {
		t.Errorf("unexpected output: %q", out)
	}
	// The same topic twice, with different case, is stored once.
	capture(t, func() { command(context.Background(), "/topic Cine", cl, cfg, store, tut) })
	if got := store.View().Profile.Topics; len(got) != 1 || got[0] != "cine" {
		t.Errorf("topics = %v", got)
	}
	// No argument explains the usage instead of silently doing nothing.
	out, _ = capture(t, func() { command(context.Background(), "/topic", cl, cfg, store, tut) })
	if !strings.Contains(out, "uso: /topic") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestCommandHelpAndUnknown(t *testing.T) {
	store := newStore(t)
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()
	cl := ollama.New("http://127.0.0.1:1")

	out, _ := capture(t, func() { command(context.Background(), "/help", cl, cfg, store, tut) })
	for _, want := range []string{"/reset", "/level", "/topic", "/status", "/quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help should list %q:\n%s", want, out)
		}
	}
	out, _ = capture(t, func() { command(context.Background(), "/nope", cl, cfg, store, tut) })
	if !strings.Contains(out, "comando desconocido") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestStatusReportsTheConversationAndTheBudget(t *testing.T) {
	store := newStore(t)
	store.Update(func(d *conv.Data) {
		d.Profile.Level = "B2"
		d.Summary = "they talked about films"
		d.AddTurn("user", "hello")
		d.AddTurn("assistant", "hi")
	})
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()

	out, _ := capture(t, func() {
		status(context.Background(), ollama.New("http://127.0.0.1:1"), cfg, store, tut)
	})
	for _, want := range []string{"groq", "2 turnos", "4 palabras", "nivel B2", "tamaño no informado", "4096"} {
		if !strings.Contains(out, want) {
			t.Errorf("status should mention %q:\n%s", want, out)
		}
	}
}

func TestStatusShowsTheRecurringTroubleSpots(t *testing.T) {
	store := newStore(t)
	store.Update(func(d *conv.Data) {
		for i := 0; i < 4; i++ {
			d.RecordMistake("past simple")
		}
		for i := 0; i < 2; i++ {
			d.RecordMistake("articles")
		}
		d.RecordMistake("word order")
	})
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()

	out, _ := capture(t, func() {
		status(context.Background(), ollama.New("http://127.0.0.1:1"), cfg, store, tut)
	})
	for _, want := range []string{"errores recurrentes", "past simple ×4", "articles ×2", "word order ×1"} {
		if !strings.Contains(out, want) {
			t.Errorf("status should mention %q:\n%s", want, out)
		}
	}
	// The most frequent one comes first: that is the order the prompt uses.
	if i, j := strings.Index(out, "past simple"), strings.Index(out, "articles"); i > j {
		t.Errorf("trouble spots out of order:\n%s", out)
	}
}

func TestStatusSaysWhenThereAreNoTroubleSpotsYet(t *testing.T) {
	store := newStore(t)
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()

	out, _ := capture(t, func() {
		status(context.Background(), ollama.New("http://127.0.0.1:1"), cfg, store, tut)
	})
	if !strings.Contains(out, "errores recurrentes: todavía ninguno") {
		t.Errorf("an empty profile should say so instead of printing nothing:\n%s", out)
	}
}

func TestStatusAsksOllamaWhatIsLoaded(t *testing.T) {
	f := newFakeOllama()
	srv := f.server(t)
	store := newStore(t)
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := localConfig(srv.URL)
	cfg.Mute = true

	out, _ := capture(t, func() {
		status(context.Background(), ollama.New(srv.URL), cfg, store, tut)
	})
	if !strings.Contains(out, "llama3:8b") || !strings.Contains(out, "5.0 GiB") {
		t.Errorf("status should report the loaded models:\n%s", out)
	}
}

// TestPickLearnerAsksOnTheTerminalAndGivesEachNameItsOwnFile pins the feature:
// at startup the app asks who is practising and the answer selects the state
// file, so two people never share corrections.
func TestPickLearnerAsksOnTheTerminalAndGivesEachNameItsOwnFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	cfg := config.Config{StatePath: base}

	var out strings.Builder
	name, path, err := pickLearner(cfg, bufio.NewScanner(strings.NewReader("Ana\n")), &out, true)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Ana" {
		t.Errorf("name = %q, want Ana", name)
	}
	want := filepath.Join(filepath.Dir(base), "users", "ana", "state.json")
	if path != want {
		t.Errorf("state path = %q, want %q", path, want)
	}
	if !strings.Contains(out.String(), "usuario:") {
		t.Errorf("the app should ask for the user, output %q", out.String())
	}
}

// TestPickLearnerWithTheUserFlagSkipsThePrompt keeps scripts and tests able to
// start a known profile without a terminal.
func TestPickLearnerWithTheUserFlagSkipsThePrompt(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	cfg := config.Config{StatePath: base, User: "Beto"}

	var out strings.Builder
	name, path, err := pickLearner(cfg, bufio.NewScanner(strings.NewReader("Ana\n")), &out, true)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Beto" || out.Len() != 0 {
		t.Errorf("name=%q prompt=%q, want Beto and no prompt", name, out.String())
	}
	if want := filepath.Join(filepath.Dir(base), "users", "beto", "state.json"); path != want {
		t.Errorf("state path = %q, want %q", path, want)
	}
}

// TestPickLearnerHonoursAnExplicitStateFile keeps --state meaning exactly the
// file it names, without a per-user directory.
func TestPickLearnerHonoursAnExplicitStateFile(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "mine.json")
	cfg := config.Config{StatePath: explicit, StateSet: true, User: "ana"}

	_, path, err := pickLearner(cfg, bufio.NewScanner(strings.NewReader("")), io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != explicit {
		t.Errorf("state path = %q, want the explicit %q", path, explicit)
	}
}

// TestPickLearnerReusesTheLastNameWithoutATerminal covers the non-interactive
// case: piped input must not hang or invent a new profile.
func TestPickLearnerReusesTheLastNameWithoutATerminal(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if err := learner.Remember(base, "Ana"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StatePath: base}

	var out strings.Builder
	name, _, err := pickLearner(cfg, bufio.NewScanner(strings.NewReader("")), &out, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Ana" {
		t.Errorf("name = %q, want the last one", name)
	}
	if out.Len() != 0 {
		t.Errorf("no prompt without a terminal, output %q", out.String())
	}
}

// TestPickLearnerImportsTheOldSingleProfileOnce is the upgrade path: the first
// learner inherits the profile that used to live in one shared file, and the
// second one starts clean.
func TestPickLearnerImportsTheOldSingleProfileOnce(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(base, []byte(`{"profile":{"level":"C1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	_, path, err := pickLearner(config.Config{StatePath: base, User: "Ana"}, bufio.NewScanner(strings.NewReader("")), &out, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "importado") {
		t.Errorf("the import should be announced, output %q", out.String())
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "C1") {
		t.Errorf("imported profile = %q, err %v", b, err)
	}

	_, beto, err := pickLearner(config.Config{StatePath: base, User: "Beto"}, bufio.NewScanner(strings.NewReader("")), io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(beto); !os.IsNotExist(err) {
		t.Errorf("a second learner must start clean, stat err %v", err)
	}
}

func TestStatusNamesTheLearnerAndTheirFile(t *testing.T) {
	store := newStore(t)
	tut := tutor.New(nil, store, tutor.Settings{})
	cfg := statusConfig()
	cfg.User = "ana"
	cfg.StatePath = "/home/u/.speakeasy/users/ana/state.json"

	out, _ := capture(t, func() {
		status(context.Background(), ollama.New("http://127.0.0.1:1"), cfg, store, tut)
	})
	for _, want := range []string{"usuario: ana", "/users/ana/state.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("status should mention %q:\n%s", want, out)
		}
	}
}

// TestIsTerminalIsFalseOnAPipe guards the branch that decides whether to ask:
// piped input must never block waiting for a name.
func TestIsTerminalIsFalseOnAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe is not a terminal")
	}
}

// TestIsTerminalIsFalseOnDevNull pins S3 for the common non-interactive
// redirect: /dev/null is a character device, so a mode check alone treats it as
// a terminal and the app prints a prompt nobody can answer.
func TestIsTerminalIsFalseOnDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Errorf("isTerminal(%s) = true; a non-interactive stdin must never prompt", os.DevNull)
	}
}

// TestPickLearnerWithoutATerminalDoesNotConsumeStdin guards the piped case: the
// line waiting in the buffer belongs to the turn loop, not to a name prompt.
func TestPickLearnerWithoutATerminalDoesNotConsumeStdin(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state.json")
	sc := bufio.NewScanner(strings.NewReader("Ana\n/quit\n"))
	var out strings.Builder
	name, _, err := pickLearner(config.Config{StatePath: base}, sc, &out, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != learner.DefaultName {
		t.Errorf("name = %q, want %q (piped input is not a name)", name, learner.DefaultName)
	}
	if out.Len() != 0 {
		t.Errorf("prompt leaked to stdout: %q", out.String())
	}
	if !sc.Scan() || sc.Text() != "Ana" {
		t.Error("the name prompt consumed a piped line meant for the turn loop")
	}
}

// TestPickLearnerWithExplicitStateLeavesNoUsersDir keeps --state honest: it
// names one file and nothing else is created next to it.
func TestPickLearnerWithExplicitStateLeavesNoUsersDir(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "mine.json")
	if err := os.WriteFile(explicit, []byte(`{"profile":{"level":"C1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{StatePath: explicit, StateSet: true, User: "Ana"}
	name, path, err := pickLearner(cfg, bufio.NewScanner(strings.NewReader("")), io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Ana" || path != explicit {
		t.Errorf("name=%q path=%q, want Ana and the explicit %q", name, path, explicit)
	}
	if _, err := os.Stat(filepath.Join(dir, "users")); !os.IsNotExist(err) {
		t.Errorf("--state must not create a per-learner directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "last-user")); !os.IsNotExist(err) {
		t.Errorf("--state must not write a last-user file beside it: %v", err)
	}
	if b, err := os.ReadFile(explicit); err != nil || !strings.Contains(string(b), "C1") {
		t.Errorf("explicit state file = %q, err %v", b, err)
	}
}

func TestLabelsAndHostParsing(t *testing.T) {
	if got := ttsLabel(config.Config{Mute: true, TTS: "phonon"}, nil); got != "off" {
		t.Errorf("muted ttsLabel = %q", got)
	}
	if got := ttsLabel(config.Config{TTS: "phonon"}, nil); got != "off" {
		t.Errorf("a missing speaker must read as off, got %q", got)
	}
	if got := ttsLabel(config.Config{TTS: "phonon"}, &speech.Speaker{}); got != "phonon" {
		t.Errorf("ttsLabel = %q", got)
	}
	for _, c := range []struct{ in, want string }{
		{"https://api.groq.com/openai/v1", "api.groq.com"},
		{"https://api.x.ai/v1", "api.x.ai"},
		{"http://127.0.0.1:11434", "127.0.0.1:11434"},
	} {
		if got := hostOf(c.in); got != c.want {
			t.Errorf("hostOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOursAndGib(t *testing.T) {
	need := []string{"qwen3:8b", "phonon"}
	if !ours("qwen3:8b", need) {
		t.Error("the chat model is ours")
	}
	if !ours("phonon:latest", need) {
		t.Error("a missing tag should match :latest")
	}
	if ours("llama3:8b", need) {
		t.Error("a foreign model is not ours")
	}
	if got := gib(1 << 30); got != 1 {
		t.Errorf("gib(1<<30) = %v", got)
	}
}

// TestSettingsForMatchesTheConfiguredProvider guards the wiring shared by the
// turn loop and --selftest: the probe must send the same num_ctx and think
// field as production, or it proves nothing.
func TestSettingsForMatchesTheConfiguredProvider(t *testing.T) {
	cfg := config.Config{Provider: "ollama", LLM: "qwen3:8b", NumCtx: 8192, KeepAlive: "30m", Think: true, NotesLang: "Spanish"}
	st := settingsFor(cfg)
	if st.Model != "qwen3:8b" || st.NumCtx != 8192 || st.KeepAlive != "30m" || st.NotesLang != "Spanish" {
		t.Errorf("settings %+v", st)
	}
	if !st.ThinkField || !st.Think {
		t.Error("a local provider sends the think field")
	}
	cfg.Provider = "groq"
	if settingsFor(cfg).ThinkField {
		t.Error("a remote provider must not send the Ollama-only think field")
	}
}

// TestNewChatterFollowsTheProvider pins the wiring --selftest depends on.
func TestNewChatterFollowsTheProvider(t *testing.T) {
	cl := ollama.New("http://127.0.0.1:1")
	if _, ok := newChatter(config.Config{Provider: "ollama"}, cl).(ollama.Chatter); !ok {
		t.Error("a local provider must use the Ollama client")
	}
	cfg := config.Config{Provider: "groq", APIBase: "https://api.groq.com/openai/v1", APIKey: "k"}
	if _, ok := newChatter(cfg, cl).(*openai.Client); !ok {
		t.Error("a remote provider must use the OpenAI-compatible client")
	}
}
