// speakeasy: practice spoken English with an LLM, in the terminal.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"speakeasy/internal/config"
	"speakeasy/internal/conv"
	"speakeasy/internal/learner"
	"speakeasy/internal/llm"
	"speakeasy/internal/ollama"
	"speakeasy/internal/openai"
	"speakeasy/internal/selftest"
	"speakeasy/internal/speech"
	"speakeasy/internal/tutor"
	"speakeasy/internal/voice"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		return err
	}
	// Keep this process small: it is only an orchestrator.
	debug.SetMemoryLimit(int64(cfg.MemLimitMiB) << 20)
	debug.SetGCPercent(50)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	// Ollama always hosts the voice; it hosts the chat model only locally.
	cl := ollama.New(cfg.Host)
	chat := newChatter(cfg, cl)

	// The persona file replaces the tutor's style; a missing one is fine.
	persona, perr := conv.LoadPersona(cfg.Persona)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "aviso: no pude leer la persona %s: %v\n", cfg.Persona, perr)
	}
	reviewPersona, rerr := conv.LoadPersona(cfg.ReviewPersona)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "aviso: no pude leer la persona de correcciones %s: %v\n", cfg.ReviewPersona, rerr)
	}
	st := settingsFor(cfg, persona, reviewPersona)

	// --selftest probes the real providers and exits before touching the
	// stored conversation or unloading anything from Ollama.
	if cfg.SelfTest {
		return selftest.Run(ctx, os.Stdout, selftest.Env{
			Cfg:      cfg,
			Ollama:   cl,
			Chat:     chat,
			Settings: st,
		})
	}

	// One scanner for the whole session: the name prompt and the turn loop must
	// not lose buffered input by reading stdin twice.
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 1<<20)

	// Who is practising decides which state file we open, so this runs before
	// the slow part (loading the model).
	name, statePath, err := pickLearner(cfg, sc, os.Stdout, isTerminal(os.Stdin))
	if err != nil {
		return err
	}
	cfg.User, cfg.StatePath = name, statePath

	if err := prepare(ctx, cl, cfg); err != nil {
		return err
	}

	store, err := conv.Open(cfg.StatePath)
	if err != nil {
		return err
	}
	tut := tutor.New(chat, store, st)

	var spk *speech.Speaker
	if !cfg.Mute {
		player, err := speech.NewPlayer()
		if err != nil {
			fmt.Fprintln(os.Stderr, "aviso:", err, "- continúo sin voz")
		} else {
			spk = speech.NewSpeaker(cl, player, cfg.TTS, cfg.Voice, func(err error) {
				fmt.Fprintln(os.Stderr, "\n[voz]", err)
			})
		}
	}

	if cfg.VoiceNote != "" {
		fmt.Fprintln(os.Stderr, "aviso:", cfg.VoiceNote)
	}
	vs := voiceInput(cfg)
	if vs.Warn != "" {
		fmt.Fprintln(os.Stderr, vs.Warn)
	}
	rec, tr := vs.Rec, vs.Tr

	finish := func() {
		if spk != nil {
			spk.Stop()
		}
		tut.Wait(3 * time.Second)
	}

	// Ctrl-C interrupts the current turn; at the prompt it quits.
	var mu sync.Mutex
	var cancelTurn context.CancelFunc
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sigc {
			mu.Lock()
			c := cancelTurn
			mu.Unlock()
			if c != nil {
				c()
				if spk != nil {
					spk.Stop()
				}
				continue
			}
			fmt.Println()
			finish()
			os.Exit(0)
		}
	}()

	fmt.Printf("speakeasy · %s · %s: %s · voz=%s · mic=%s · ctx=%d · /help para ver comandos\n", cfg.User, cfg.Provider, cfg.LLM, ttsLabel(cfg, spk), micLabel(cfg, vs), cfg.NumCtx)
	if cfg.Remote() {
		fmt.Printf("aviso: tus mensajes se envían a %s\n", hostOf(cfg.APIBase))
	}
	tut.FoldAsync(ctx, func(err error) { fmt.Fprintln(os.Stderr, "\n[memoria]", err) })

	runTurn := func(line string) {
		turnCtx, cancel := context.WithCancel(ctx)
		mu.Lock()
		cancelTurn = cancel
		mu.Unlock()
		defer func() {
			mu.Lock()
			cancelTurn = nil
			mu.Unlock()
			cancel()
		}()

		var v tutor.Voice
		var u *speech.Utterance
		if spk != nil {
			u = spk.Begin()
			v = u
		}
		fmt.Print("\ntutor> ")
		_, err := tut.Reply(turnCtx, line, os.Stdout, v)
		fmt.Println()
		if err != nil {
			if u != nil {
				u.Cancel()
			}
			if errors.Is(err, context.Canceled) {
				fmt.Println("(interrumpido)")
			} else {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			return
		}
		if u != nil {
			u.Finish() // audio keeps playing while we compute the corrections
		}
		// The review pass runs every turn: it carries the corrections and the
		// facts the tutor remembers, so --corrections only hides the ✎ lines.
		rev, err := tut.Correct(turnCtx, line)
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "[correcciones]", err)
		}
		for _, f := range rev.Facts {
			fmt.Printf("  ✚ recordado: %s\n", f)
		}
		if cfg.Corrections {
			for _, c := range rev.Corrections {
				fmt.Printf("  ✎ %q → %q  (%s)\n", c.Original, c.Better, c.Note)
				if c.Mnemonic != "" {
					fmt.Printf("  ✧ %s\n", c.Mnemonic)
				}
			}
			// From C1 up the review also rewrites the whole message.
			if rev.Rewrite != "" {
				fmt.Printf("  ↻ %s\n", rev.Rewrite)
			}
		}
		tut.FoldAsync(ctx, func(err error) { fmt.Fprintln(os.Stderr, "\n[memoria]", err) })
	}

	for {
		fmt.Print("\ntú> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			if rec == nil || tr == nil {
				continue
			}
			if err := recordTurn(ctx, sc, rec, tr, cfg.STTLang, cfg.RecordMax, os.Stdout, runTurn); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			continue
		case line == "/quit" || line == "/exit":
			finish()
			return nil
		case strings.HasPrefix(line, "/"):
			command(ctx, line, cl, cfg, store, tut, spk)
		default:
			runTurn(line)
		}
	}
	finish()
	return nil
}

// pickLearner resolves who is practising and where their state lives. It asks
// on the terminal unless --user was given, and keeps one state file per learner
// so levels and corrections never mix. Without a terminal it reuses the last
// name, so piped input never blocks waiting for an answer.
func pickLearner(cfg config.Config, sc *bufio.Scanner, out io.Writer, interactive bool) (name, statePath string, err error) {
	// With an explicit --state the file already names one profile: remembering
	// a name beside it would be a side effect nobody asked for.
	var last string
	if !cfg.StateSet {
		last = learner.Last(cfg.StatePath)
	}
	switch {
	case cfg.User != "":
		name = cfg.User
	case interactive:
		name = learner.Ask(sc, out, last)
	default:
		name = last
		if name == "" {
			name = learner.DefaultName
		}
	}
	if learner.Slug(name) == "" {
		name = learner.DefaultName
	}

	statePath = cfg.StatePath
	if !cfg.StateSet {
		// Everything written before languages existed was English: both the
		// shared file and the per-learner one are migrated to the English
		// profile, never to the language being started now.
		enPath := learner.StateFile(cfg.StatePath, name, learner.DefaultLang)
		copied, err := learner.ImportLegacy(cfg.StatePath, enPath)
		if err != nil {
			fmt.Fprintf(out, "aviso: no pude importar el perfil anterior: %v\n", err)
		} else if copied {
			fmt.Fprintf(out, "perfil de inglés importado desde %s\n", cfg.StatePath)
		}
		if copied, err := learner.ImportFlatProfile(cfg.StatePath, name); err != nil {
			fmt.Fprintf(out, "aviso: no pude migrar el perfil a %s: %v\n", enPath, err)
		} else if copied {
			fmt.Fprintf(out, "perfil de inglés migrado a %s\n", enPath)
		}
		statePath = learner.StateFile(cfg.StatePath, name, cfg.Target)
	}
	// Remembered best effort: failing to write it only means we ask next time.
	if !cfg.StateSet && name != last {
		if err := learner.Remember(cfg.StatePath, name); err != nil {
			fmt.Fprintf(out, "aviso: no pude recordar el usuario: %v\n", err)
		}
	}
	return name, statePath, nil
}

// isTerminal tells the name prompt whether there is someone to answer it. A
// character device is not enough: `< /dev/null` and a closed stdin (which the Go
// runtime reopens on /dev/null) are character devices too, and prompting there
// would glue a question to the first line of output in a cron job or a pipe.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

// newChatter picks the client for the configured provider. run, prepare and
// --selftest all go through it, so a probe always exercises the same client.
func newChatter(cfg config.Config, cl *ollama.Client) llm.Chatter {
	if cfg.Remote() {
		return openai.New(cfg.APIBase, cfg.APIKey)
	}
	return ollama.Chatter{C: cl}
}

// settingsFor turns the config into tutor settings. Shared by the real turn
// loop and by --selftest so the probes cannot drift from production.
func settingsFor(cfg config.Config, persona, reviewPersona string) tutor.Settings {
	return tutor.Settings{
		Model:         cfg.LLM,
		NumCtx:        cfg.NumCtx,
		KeepAlive:     cfg.KeepAlive,
		Think:         cfg.Think,
		ThinkField:    !cfg.Remote(),
		NotesLang:     cfg.NotesLang,
		Target:        cfg.Target,
		Persona:       persona,
		ReviewPersona: reviewPersona,
	}
}

func ttsLabel(cfg config.Config, spk *speech.Speaker) string {
	if cfg.Mute || spk == nil {
		return "off"
	}
	if v := spk.Voice(); v != "" {
		return cfg.TTS + "/" + v
	}
	return cfg.TTS
}

// voiceSetup is the voice pipeline plus why it is missing when it is.
type voiceSetup struct {
	Rec    voice.Recorder
	Tr     voice.Transcriber
	Warn   string // the full line printed to stderr, once
	Reason string // the short reason the header shows, e.g. "sin GROQ_API_KEY"
}

// voiceInput builds the voice pipeline. When voice input is enabled but cannot
// work it returns a warning and nil interfaces, so an empty line stays inert
// instead of opening the microphone for nothing. The header repeats the reason
// in short form: "mic=off" alone does not tell you what to fix.
func voiceInput(cfg config.Config) voiceSetup {
	if cfg.STT == "off" {
		return voiceSetup{}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return voiceSetup{
			Warn:   "aviso: no encuentro ffmpeg (`brew install ffmpeg`), así que no puedo grabar; el Enter vacío queda sin efecto",
			Reason: "falta ffmpeg",
		}
	}
	rec := voice.FFmpegRecorder{Path: "ffmpeg", Device: cfg.STTDevice}
	switch cfg.STT {
	case "whisper":
		if _, err := exec.LookPath(cfg.WhisperBin); err != nil {
			return voiceSetup{
				Warn:   "aviso: no encuentro whisper-cli (`brew install whisper-cpp`), así que no puedo transcribir; el Enter vacío queda sin efecto",
				Reason: "falta whisper-cli",
			}
		}
		if _, err := os.Stat(cfg.WhisperModel); err != nil {
			return voiceSetup{
				Warn:   fmt.Sprintf("aviso: no encuentro el modelo de whisper en %s (bajá ggml-small.en.bin de ggerganov/whisper.cpp); el Enter vacío queda sin efecto", cfg.WhisperModel),
				Reason: "falta el modelo de whisper",
			}
		}
		// A .en model does not fail on another language: it transcribes it
		// wrong, which is worse. Refuse and say which model to get.
		if cfg.Target != "en" && strings.HasSuffix(strings.ToLower(cfg.WhisperModel), ".en.bin") {
			return voiceSetup{
				Warn:   fmt.Sprintf("aviso: %s solo transcribe inglés, así que no puedo transcribir %s; bajá el modelo multilingüe (ggml-small.bin) o pasá --whisper-model; el Enter vacío queda sin efecto", cfg.WhisperModel, cfg.Target),
				Reason: "modelo de whisper solo inglés",
			}
		}
		return voiceSetup{Rec: rec, Tr: voice.WhisperCPP{Bin: cfg.WhisperBin, Model: cfg.WhisperModel}}
	default:
		if cfg.STTKey == "" {
			return voiceSetup{
				Warn:   fmt.Sprintf("aviso: falta %s, así que no puedo transcribir; el Enter vacío queda sin efecto", cfg.STTKeyEnv),
				Reason: "sin " + cfg.STTKeyEnv,
			}
		}
		return voiceSetup{Rec: rec, Tr: voice.GroqTranscriber{Base: cfg.STTBase, Key: cfg.STTKey, Model: cfg.STTModel}}
	}
}

// personaLabel names a persona by its first line, or the built-in default.
func personaLabel(persona string) string {
	if strings.TrimSpace(persona) == "" {
		return "de fábrica"
	}
	first := strings.TrimSpace(strings.SplitN(persona, "\n", 2)[0])
	if r := []rune(first); len(r) > 42 {
		first = string(r[:42]) + "…"
	}
	return first
}

// resolvePersona turns a name or a path into the two files to load: the voice
// and, when it exists, its -review companion. "off" resets both to the built-in
// defaults.
func resolvePersona(dir, arg string) (voice, review string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("uso: /persona <nombre|ruta> (sin argumento lista las disponibles)")
	}
	if arg == "off" || arg == "neutral" {
		return "", "", nil
	}
	base := arg
	if !strings.ContainsAny(arg, "/\\") && !strings.HasSuffix(arg, ".md") {
		base = filepath.Join(dir, arg+".md")
	}
	if _, statErr := os.Stat(base); statErr != nil {
		if names, _ := conv.ListPersonas(dir); len(names) > 0 {
			return "", "", fmt.Errorf("no encuentro %s; disponibles: %s", base, strings.Join(names, ", "))
		}
		return "", "", fmt.Errorf("no encuentro %s", base)
	}
	rev := strings.TrimSuffix(base, ".md") + "-review.md"
	if _, statErr := os.Stat(rev); statErr == nil {
		review = rev
	}
	return base, review, nil
}

// micLabel names the transcription backend, or off.
func micLabel(cfg config.Config, vs voiceSetup) string {
	if vs.Rec == nil {
		if vs.Reason != "" {
			return "off (" + vs.Reason + ")"
		}
		return "off"
	}
	return cfg.STT
}

// recordTurn records one utterance, transcribes it and hands the text to run.
// It is split out of the loop so a test can drive it with fake recorder and
// transcriber, without a microphone or a network.
func recordTurn(ctx context.Context, sc *bufio.Scanner, rec voice.Recorder, tr voice.Transcriber, lang string, max time.Duration, out io.Writer, run func(string)) error {
	fmt.Fprint(out, "🎙  grabando… (Enter para cortar)")
	rctx, cancel := context.WithCancel(ctx)
	type captured struct {
		pcm []byte
		err error
	}
	ch := make(chan captured, 1)
	go func() {
		pcm, err := rec.Record(rctx, max)
		ch <- captured{pcm, err}
	}()
	sc.Scan() // the second Enter, or EOF
	cancel()
	fmt.Fprintln(out)

	// The recorder is supposed to honour the cancel; this bound is the safety
	// net that keeps a wedged one from hanging the session.
	var got captured
	select {
	case got = <-ch:
	case <-time.After(5 * time.Second):
		return errors.New("la grabación no terminó a tiempo")
	}
	if got.err != nil {
		return got.err
	}
	if voice.Silent(got.pcm) {
		fmt.Fprintln(out, "no entendí nada (silencio)")
		return nil
	}
	text, err := tr.Transcribe(ctx, got.pcm, lang)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		fmt.Fprintln(out, "no entendí nada")
		return nil
	}
	fmt.Fprintf(out, "tú (voz)> %s\n", text)
	run(text)
	return nil
}

func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

// prepare checks Ollama, frees memory held by models that are not ours and,
// for a local chat model, loads it once before the first message. With a
// remote provider and no voice, Ollama is not needed at all.
func prepare(ctx context.Context, cl *ollama.Client, cfg config.Config) error {
	if cfg.Remote() && cfg.Mute {
		return nil
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ver, err := cl.Version(vctx)
	if err != nil {
		return fmt.Errorf("no puedo conectar con Ollama en %s (¿está arrancado? prueba `ollama serve`; con --mute y un proveedor remoto no hace falta): %w", cfg.Host, err)
	}
	if !cfg.Mute && !ollama.VersionAtLeast(ver, 0, 40, 9) {
		fmt.Fprintf(os.Stderr, "aviso: Ollama %s; la voz (phonon) necesita 0.40.9 o superior\n", ver)
	}

	var need []string
	if !cfg.Remote() {
		need = append(need, cfg.LLM)
	}
	if !cfg.Mute {
		need = append(need, cfg.TTS)
	}
	for _, m := range need {
		ok, err := cl.HasModel(vctx, m)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("falta el modelo %q: ejecuta `ollama pull %s`", m, m)
		}
	}

	// 16 GB do not fit several models: drop everything that is not ours.
	running, err := cl.Running(vctx)
	if err == nil {
		for _, m := range running {
			if ours(m.Name, need) {
				continue
			}
			if err := cl.Unload(vctx, m.Name); err != nil {
				fmt.Fprintf(os.Stderr, "aviso: no pude descargar %s: %v\n", m.Name, err)
				continue
			}
			fmt.Printf("descargado %s (%.1f GiB liberados)\n", m.Name, gib(m.Size))
		}
	}

	if cfg.Remote() {
		return nil
	}
	fmt.Printf("cargando %s...\n", cfg.LLM)
	lctx, lcancel := context.WithTimeout(ctx, 5*time.Minute)
	defer lcancel()
	return cl.Load(lctx, cfg.LLM, cfg.KeepAlive, map[string]any{"num_ctx": cfg.NumCtx})
}

func ours(name string, need []string) bool {
	for _, n := range need {
		if ollama.SameModel(name, n) {
			return true
		}
	}
	return false
}

func gib(b int64) float64 { return float64(b) / (1 << 30) }

func command(ctx context.Context, line string, cl *ollama.Client, cfg config.Config, store *conv.Store, tut *tutor.Tutor, spk *speech.Speaker) {
	fields := strings.Fields(line)
	arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	switch fields[0] {
	case "/help":
		fmt.Println(`/reset        empezar una conversación nueva (mantiene tu perfil)
/level B1     cambiar tu nivel (A1 a C2)
/topic X      añadir un tema de interés
/forget clave olvidar un dato que el tutor guardó (ej: /forget city)
/voice femenina  cambiar la voz (femenina, masculina o un nombre de ollama show phonon)
/persona slang   cambiar la voz del tutor (sin argumento: lista las disponibles)
/status       modelo, memoria usada y estado de la conversación
/quit         salir

Enter vacío   hablar en vez de escribir (graba hasta el próximo Enter)`)
	case "/reset":
		_ = store.Update(func(d *conv.Data) { d.Reset() })
		fmt.Println("conversación reiniciada")
	case "/level":
		lvl := strings.ToUpper(arg)
		// The scale is the same in every language: validate it against English.
		if _, ok := conv.LevelOf("en", lvl); ok {
			_ = store.Update(func(d *conv.Data) { d.Profile.Level = lvl })
			fmt.Println("nivel:", lvl)
		} else {
			fmt.Println("usa A1, A2, B1, B2, C1 o C2")
		}
	case "/topic":
		if arg == "" {
			fmt.Println("uso: /topic cine")
			return
		}
		_ = store.Update(func(d *conv.Data) { d.AddTopic(arg) })
		fmt.Println("tema añadido")
	case "/forget":
		if arg == "" {
			fmt.Println("uso: /forget city")
			return
		}
		var removed bool
		if err := store.Update(func(d *conv.Data) { removed = d.DeleteFact(arg) }); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return
		}
		if removed {
			fmt.Printf("olvidado: %s\n", conv.NormalizeFactKey(arg))
		} else {
			fmt.Printf("no tenía nada guardado con la clave %q\n", arg)
		}
	case "/persona":
		dir := filepath.Join(filepath.Dir(cfg.Persona), "personas")
		if arg == "" {
			voice, review := tut.Personas()
			fmt.Printf("voz: %s · correcciones: %s\n", personaLabel(voice), personaLabel(review))
			names, err := conv.ListPersonas(dir)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				return
			}
			if len(names) == 0 {
				fmt.Printf("no hay personas en %s (copiá un .md ahí)\n", dir)
			} else {
				fmt.Printf("disponibles: %s · o pasá una ruta a un .md\n", strings.Join(names, ", "))
			}
			return
		}
		voicePath, reviewPath, err := resolvePersona(dir, arg)
		if err != nil {
			fmt.Println(err)
			return
		}
		voice, err := conv.LoadPersona(voicePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return
		}
		review, err := conv.LoadPersona(reviewPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return
		}
		if voicePath != "" && voice == "" {
			fmt.Printf("la persona %q está vacía (¿te falta el cuerpo después de ---?)\n", arg)
			return
		}
		tut.SetPersonas(voice, review)
		fmt.Printf("voz: %s · correcciones: %s\n", personaLabel(voice), personaLabel(review))
	case "/voice":
		if spk == nil {
			fmt.Println("estás sin voz: arrancaste con --mute")
			return
		}
		if arg == "" {
			cur := spk.Voice()
			if cur == "" {
				cur = "la del modelo"
			}
			fmt.Printf("voz: %s · usa /voice femenina, /voice masculina o un nombre\n", cur)
			return
		}
		if config.SingleVoiceModel(cfg.TTS) {
			fmt.Printf("%s tiene una sola voz (la del modelo): /voice no cambia nada acá\n", cfg.TTS)
			return
		}
		v := speech.ResolveVoice(arg)
		spk.SetVoice(v)
		if v != strings.TrimSpace(arg) {
			fmt.Printf("voz: %s (%s)\n", v, strings.ToLower(strings.TrimSpace(arg)))
		} else {
			fmt.Printf("voz: %s\n", v)
		}
	case "/status":
		status(ctx, cl, cfg, store, tut)
	default:
		fmt.Println("comando desconocido; prueba /help")
	}
}

func status(ctx context.Context, cl *ollama.Client, cfg config.Config, store *conv.Store, tut *tutor.Tutor) {
	if cfg.User != "" {
		fmt.Printf("usuario: %s · %s\n", cfg.User, cfg.StatePath)
	}
	fmt.Printf("chat: %s · %s\n", cfg.Provider, cfg.LLM)
	if !cfg.Remote() || !cfg.Mute {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if running, err := cl.Running(sctx); err == nil {
			var parts []string
			var total int64
			for _, m := range running {
				parts = append(parts, fmt.Sprintf("%s %.1f GiB", m.Name, gib(m.Size)))
				total += m.Size
			}
			fmt.Printf("modelos en memoria (Ollama): %s (total %.1f GiB)\n", strings.Join(parts, " · "), gib(total))
		}
	}
	d := store.View()
	nivel := d.Profile.Level
	if p, ok := conv.LevelOf(cfg.Target, nivel); ok {
		nivel += " (" + p.Name + ")"
	}
	fmt.Printf("conversación: %d turnos, resumen de %d palabras, nivel %s\n", len(d.Turns), len(strings.Fields(d.Summary)), nivel)
	// The three categories below are exactly the ones the next system prompt
	// will carry, so this line is the reason the tutor behaves differently.
	if top := d.TopMistakes(3); len(top) > 0 {
		parts := make([]string, len(top))
		for i, k := range top {
			parts[i] = fmt.Sprintf("%s ×%d", k, d.Profile.Mistakes[k])
		}
		fmt.Printf("errores recurrentes: %s\n", strings.Join(parts, " · "))
	} else {
		fmt.Println("errores recurrentes: todavía ninguno")
	}
	// The facts are the other half of the memory: what the tutor knows about
	// the learner as a person, not as a student. Mnemonics live apart because
	// they are teaching tricks, not things about you.
	var facts, mnemonics []string
	for _, k := range d.FactKeys() {
		if strings.HasPrefix(k, conv.MnemonicPrefix) {
			mnemonics = append(mnemonics, strings.TrimPrefix(k, conv.MnemonicPrefix)+"="+d.Profile.Facts[k])
			continue
		}
		facts = append(facts, k+"="+d.Profile.Facts[k])
	}
	if len(facts) > 0 {
		fmt.Printf("lo que sabe de vos: %s\n", strings.Join(facts, " · "))
	} else {
		fmt.Println("lo que sabe de vos: todavía nada")
	}
	if len(mnemonics) > 0 {
		fmt.Printf("mnemónicos: %s\n", strings.Join(mnemonics, " · "))
	}
	if n := tut.LastPromptTokens(); n > 0 {
		fmt.Printf("último prompt: %d de %d tokens\n", n, cfg.NumCtx)
	} else {
		fmt.Printf("último prompt: tamaño no informado (presupuesto %d tokens)\n", cfg.NumCtx)
	}
}
