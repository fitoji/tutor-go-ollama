// speakeasy: practice spoken English with an LLM, in the terminal.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"speakeasy/internal/config"
	"speakeasy/internal/conv"
	"speakeasy/internal/llm"
	"speakeasy/internal/ollama"
	"speakeasy/internal/openai"
	"speakeasy/internal/selftest"
	"speakeasy/internal/speech"
	"speakeasy/internal/tutor"
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

	// --selftest probes the real providers and exits before touching the
	// stored conversation or unloading anything from Ollama.
	if cfg.SelfTest {
		return selftest.Run(ctx, os.Stdout, selftest.Env{
			Cfg:      cfg,
			Ollama:   cl,
			Chat:     chat,
			Settings: settingsFor(cfg),
		})
	}

	if err := prepare(ctx, cl, cfg); err != nil {
		return err
	}

	store, err := conv.Open(cfg.StatePath)
	if err != nil {
		return err
	}
	tut := tutor.New(chat, store, settingsFor(cfg))

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

	fmt.Printf("speakeasy · %s: %s · voz=%s · ctx=%d · /help para ver comandos\n", cfg.Provider, cfg.LLM, ttsLabel(cfg, spk), cfg.NumCtx)
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
		if cfg.Corrections {
			corr, err := tut.Correct(turnCtx, line)
			if err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "[correcciones]", err)
			}
			for _, c := range corr {
				fmt.Printf("  ✎ %q → %q  (%s)\n", c.Original, c.Better, c.Note)
			}
		}
		tut.FoldAsync(ctx, func(err error) { fmt.Fprintln(os.Stderr, "\n[memoria]", err) })
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for {
		fmt.Print("\ntú> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case line == "/quit" || line == "/exit":
			finish()
			return nil
		case strings.HasPrefix(line, "/"):
			command(ctx, line, cl, cfg, store, tut)
		default:
			runTurn(line)
		}
	}
	finish()
	return nil
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
func settingsFor(cfg config.Config) tutor.Settings {
	return tutor.Settings{
		Model:      cfg.LLM,
		NumCtx:     cfg.NumCtx,
		KeepAlive:  cfg.KeepAlive,
		Think:      cfg.Think,
		ThinkField: !cfg.Remote(),
		NotesLang:  cfg.NotesLang,
	}
}

func ttsLabel(cfg config.Config, spk *speech.Speaker) string {
	if cfg.Mute || spk == nil {
		return "off"
	}
	return cfg.TTS
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

func command(ctx context.Context, line string, cl *ollama.Client, cfg config.Config, store *conv.Store, tut *tutor.Tutor) {
	fields := strings.Fields(line)
	arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	switch fields[0] {
	case "/help":
		fmt.Println(`/reset        empezar una conversación nueva (mantiene tu perfil)
/level B1     cambiar tu nivel (A2, B1, B2, C1)
/topic X      añadir un tema de interés
/status       modelo, memoria usada y estado de la conversación
/quit         salir`)
	case "/reset":
		_ = store.Update(func(d *conv.Data) { d.Reset() })
		fmt.Println("conversación reiniciada")
	case "/level":
		lvl := strings.ToUpper(arg)
		switch lvl {
		case "A2", "B1", "B2", "C1":
			_ = store.Update(func(d *conv.Data) { d.Profile.Level = lvl })
			fmt.Println("nivel:", lvl)
		default:
			fmt.Println("usa A2, B1, B2 o C1")
		}
	case "/topic":
		if arg == "" {
			fmt.Println("uso: /topic cine")
			return
		}
		_ = store.Update(func(d *conv.Data) { d.AddTopic(arg) })
		fmt.Println("tema añadido")
	case "/status":
		status(ctx, cl, cfg, store, tut)
	default:
		fmt.Println("comando desconocido; prueba /help")
	}
}

func status(ctx context.Context, cl *ollama.Client, cfg config.Config, store *conv.Store, tut *tutor.Tutor) {
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
	fmt.Printf("conversación: %d turnos, resumen de %d palabras, nivel %s\n", len(d.Turns), len(strings.Fields(d.Summary)), d.Profile.Level)
	if n := tut.LastPromptTokens(); n > 0 {
		fmt.Printf("último prompt: %d de %d tokens\n", n, cfg.NumCtx)
	} else {
		fmt.Printf("último prompt: tamaño no informado (presupuesto %d tokens)\n", cfg.NumCtx)
	}
}
