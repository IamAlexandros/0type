package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/zalkanorr/0type/internal/asr"
	"github.com/zalkanorr/0type/internal/audio"
	"github.com/zalkanorr/0type/internal/config"
	"github.com/zalkanorr/0type/internal/modelstore"
	"github.com/zalkanorr/0type/internal/plugin"
	"github.com/zalkanorr/0type/internal/stream"
	"github.com/zalkanorr/0type/internal/theme"
	"github.com/zalkanorr/0type/internal/toggle"
	"github.com/zalkanorr/0type/internal/ui"
)

// copiedConfirmationHold is how long the "✓ Copied to clipboard" message
// stays up before the window plays its closing (fade+drop) animation --
// long enough to actually read, short enough not to feel like it's
// lingering.
const copiedConfirmationHold = 850 * time.Millisecond

// runApp is 0type's normal, no-subcommand entry point: it loads the model
// once, shows the floating overlay (initially hidden), and toggles
// visibility -- starting/stopping live mic capture and transcription along
// with it -- on SIGUSR1 (see internal/toggle and `0type toggle`).
func runApp(args []string) error {
	fs := flag.NewFlagSet("0type", flag.ContinueOnError)
	themeFlag := fs.String("theme", "", "theme name or path to a .css file (overrides the config file)")
	// Set by `0type toggle` when it has to start 0type itself: whoever
	// pressed the shortcut wants to dictate, not to read a menu.
	dictateFlag := fs.Bool("dictate", false, "start dictating as soon as the model is loaded, instead of opening the menu")
	// For starting 0type on login, or any time you want it resident and
	// out of the way: no menu, no microphone, nothing on screen until the
	// toggle shortcut asks for something.
	backgroundFlag := fs.Bool("background", false, "start hidden: no menu, no dictation, just wait for the toggle")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// A second `0type` must not load a second copy of the model (seconds
	// of startup, hundreds of MB); it asks the instance that's already
	// running to show its menu instead.
	if err := toggle.SendMenu(); err == nil {
		return nil
	}

	// Nothing answered, but an instance may still be starting up and not
	// yet listening. The lock is what makes that case visible -- without
	// it, every attempt during the model load looks like "nothing is
	// running" and starts yet another copy.
	release, err := toggle.Lock()
	if err != nil {
		if errors.Is(err, toggle.ErrAlreadyRunning) {
			fmt.Fprintln(os.Stderr, "0type is already starting up; it'll be ready in a few seconds")
			return nil
		}
		return err
	}
	defer release()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Resolve the theme before loading the model: the model takes seconds,
	// and a typo in a theme name should fail immediately rather than after
	// the wait.
	th, err := loadTheme(*themeFlag, cfg)
	if err != nil {
		return err
	}
	hooks, err := plugin.New(cfg.Hooks, plugin.WithLogger(log.Printf))
	if err != nil {
		return err
	}

	dir, err := modelstore.Dir(modelstore.ParakeetTDTv2.Name)
	if err != nil {
		return fmt.Errorf("resolve model cache dir: %w", err)
	}
	if err := asr.InitEnvironment(""); err != nil {
		return fmt.Errorf("init onnxruntime: %w", err)
	}
	defer asr.DestroyEnvironment()

	model, err := asr.LoadModel(dir)
	if err != nil {
		return fmt.Errorf("load model (did you run `0type setup`?): %w", err)
	}
	defer model.Close()

	win, err := ui.New("") // idle: the "Listening…" bar, see internal/ui's draw_content
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	applyTheme(win, th)

	cleanupPIDFile, err := toggle.WritePIDFile()
	if err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer cleanupPIDFile()

	a := &app{win: win, model: model, hooks: hooks, themeName: th.Name}
	toggle.OnToggle(a.handleToggle)
	toggle.OnMenu(func() { ui.RunOnMainThread(a.openMenu) })

	// Running `0type` with nothing else going on opens the menu, so the
	// program is discoverable without knowing any of its subcommands or
	// having set up a keybinding yet.
	switch {
	case *dictateFlag:
		ui.RunOnMainThread(a.showAndListen)
	case *backgroundFlag:
		// Nothing: stay resident and invisible until toggled.
	default:
		ui.RunOnMainThread(a.openMenu)
	}

	win.Run() // blocks until SIGINT/SIGTERM
	a.stopPipeline()
	hooks.Wait() // let an in-flight hook finish rather than killing it at exit
	return nil
}

// loadTheme resolves which stylesheet to use: the --theme flag if given,
// otherwise the config file's, otherwise the default.
func loadTheme(flagValue string, cfg *config.Config) (*theme.Theme, error) {
	name := cfg.Theme
	if flagValue != "" {
		name = flagValue
	}
	return theme.Load(name)
}

// applyTheme puts a resolved theme on screen: its stylesheet, plus the
// brand mark it selected (which is 0type's own directive rather than a
// CSS property -- see internal/theme).
func applyTheme(win *ui.Window, th *theme.Theme) {
	win.LoadCSS(th.CSS)
	win.SetMark(ui.Mark(th.Mark))
	win.SetMarkArt(th.Art) // a theme's own sprite, if it drew one
	win.SetIdleText(th.Idle)
	win.SetCopiedText(th.Copied)
}

// app owns the live-capture pipeline's lifecycle: started on show, stopped
// on hide, so 0type never listens while its window isn't visible. It also
// owns the current session's accumulated dictation: every finished
// sentence (stream.Final) is appended to it, the display always shows the
// *whole session so far* (not just the current sentence) sliding as one
// continuously growing line, and the complete result is copied to the
// clipboard once, when the session ends (hideAndStop) -- not after each
// sentence.
type app struct {
	win   *ui.Window
	model *asr.Model
	hooks *plugin.Runner // nil when no hooks are configured; safe to call

	// menu and themeName are only touched on the GTK main thread (see
	// menuState), unlike the dictation fields below, which are shared with
	// the capture goroutine and guarded by mu.
	menu      menuState
	themeName string

	mu       sync.Mutex
	visible  bool
	stop     chan struct{}
	dictated string // finalized sentences this session, space-joined
	// generation increments every time a new session starts (showAndListen).
	// hideAndStop's delayed "hide after the confirmation hold" callback
	// captures the generation it was scheduled under and checks it's still
	// current before actually hiding -- otherwise a quick toggle-off then
	// toggle-back-on within the hold window would have that stale delayed
	// hide fire later and close the *new* session's window out from under it.
	generation int
}

func (a *app) handleToggle() {
	ui.RunOnMainThread(func() {
		// The toggle key means "dictate" even when the menu is up -- it's
		// the shortcut people reach for, and making it a no-op because a
		// menu happens to be open would feel broken.
		if a.menu.open {
			a.closeMenu()
			a.showAndListen()
			return
		}
		a.mu.Lock()
		show := !a.visible
		a.mu.Unlock()
		if show {
			a.showAndListen()
		} else {
			a.hideAndStop()
		}
	})
}

func (a *app) showAndListen() {
	a.mu.Lock()
	if a.visible {
		a.mu.Unlock()
		return
	}
	a.visible = true
	a.dictated = ""
	a.generation++
	stop := make(chan struct{})
	a.stop = stop
	a.mu.Unlock()

	a.win.SetText("") // back to idle for the new session
	a.win.Show()
	a.hooks.Fire(plugin.HookStart, "")
	go a.runPipeline(stop)
}

// hideAndStop ends the session: stops capture immediately, then copies
// whatever was dictated to the clipboard once (not after every sentence)
// and shows a brief confirmation before the window actually closes -- so
// the user gets clear feedback that their dictation made it to the
// clipboard, not just a window disappearing. If nothing was dictated,
// the window just plays its closing animation right away.
func (a *app) hideAndStop() {
	a.mu.Lock()
	if !a.visible {
		a.mu.Unlock()
		return
	}
	a.visible = false
	stop := a.stop
	a.stop = nil
	dictated := a.dictated
	gen := a.generation
	a.mu.Unlock()

	if stop != nil {
		close(stop)
	}

	if dictated == "" {
		a.hooks.Fire(plugin.HookStop, "")
		a.win.Hide()
		return
	}

	ui.SetClipboard(dictated) // hideAndStop already runs on the GTK main thread (see handleToggle)
	// on_copy is the hook that can do something *else* with the result --
	// type it into the focused window, append it to a file -- so it fires
	// with the same text that just went to the clipboard, before on_stop
	// reports the session as over.
	a.hooks.Fire(plugin.HookCopy, dictated)
	a.hooks.Fire(plugin.HookStop, dictated)
	a.win.ShowCopiedConfirmation()
	time.AfterFunc(copiedConfirmationHold, func() {
		ui.RunOnMainThread(func() {
			a.mu.Lock()
			stale := a.generation != gen
			a.mu.Unlock()
			if stale {
				return // a new session started before the hold elapsed -- don't close it out
			}
			a.win.Hide()
		})
	})
}

// isVisible reports whether a dictation session is on screen.
func (a *app) isVisible() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.visible
}

// stopPipeline is called once on shutdown, after the GTK main loop
// returns, to stop any in-flight capture goroutine cleanly.
func (a *app) stopPipeline() {
	a.mu.Lock()
	stop := a.stop
	a.stop = nil
	a.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// runPipeline owns one live-capture session end to end: opening the mic,
// streaming chunks through a fresh stream.Runner, and updating the
// display and a.dictated as speech is recognized. Every event (Partial or
// Final) redraws the display as "everything finalized so far this
// session" plus "the sentence currently in progress" combined into one
// string, so the on-screen line keeps growing and sliding across the
// whole session instead of resetting each time a sentence finalizes.
// Mic open/close both happen in this goroutine (see
// internal/audio.StreamChunks's doc comment) to avoid a close-while-
// reading race with a handle shared across goroutines.
func (a *app) runPipeline(stop chan struct{}) {
	mic, err := audio.OpenCapture(sampleRate, channels)
	if err != nil {
		text := fmt.Sprintf("mic error: %v", err)
		ui.RunOnMainThread(func() { a.win.SetText(text) })
		return
	}
	defer mic.Close()

	runner := stream.NewRunner(stream.DefaultConfig(), a.model, nil)
	chunkSamples := sampleRate * channels * chunkMS / 1000
	chunks := audio.StreamChunks(mic, chunkSamples, stop)

	for c := range chunks {
		if c.Err != nil {
			text := fmt.Sprintf("mic error: %v", c.Err)
			ui.RunOnMainThread(func() { a.win.SetText(text) })
			return
		}
		db := audio.DBFS(audio.RMS(c.Samples))
		level := (db + 60) / 50 // map -60..-10 dBFS onto 0..1 for the bar's live meter
		ui.RunOnMainThread(func() { a.win.SetLevel(level) })
		events, err := runner.Feed(audio.Int16ToFloat32(c.Samples), db)
		if err != nil {
			continue // transient decode error: keep listening, don't crash the session
		}
		for _, ev := range events {
			var display string
			if ev.Kind == stream.Final {
				a.mu.Lock()
				a.dictated = appendSentence(a.dictated, ev.Text)
				display = a.dictated
				a.mu.Unlock()
				a.hooks.Fire(plugin.HookFinal, ev.Text)
			} else {
				a.mu.Lock()
				display = appendSentence(a.dictated, ev.Text)
				a.mu.Unlock()
			}
			text := display
			ui.RunOnMainThread(func() { a.win.SetText(text) })
		}
	}
}

// appendSentence joins a newly finished (or in-progress) sentence onto the
// dictation accumulated so far, space-separated.
func appendSentence(dictated, sentence string) string {
	if dictated == "" {
		return sentence
	}
	return dictated + " " + sentence
}
