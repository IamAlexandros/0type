package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/IamAlexandros/0type/internal/asr"
	"github.com/IamAlexandros/0type/internal/audio"
	"github.com/IamAlexandros/0type/internal/config"
	"github.com/IamAlexandros/0type/internal/modelstore"
	"github.com/IamAlexandros/0type/internal/plugin"
	"github.com/IamAlexandros/0type/internal/stream"
	"github.com/IamAlexandros/0type/internal/theme"
	"github.com/IamAlexandros/0type/internal/toggle"
	"github.com/IamAlexandros/0type/internal/ui"
)

// copiedConfirmationHold is how long the "✓ Copied to clipboard" message
// stays up before the window plays its closing (fade+drop) animation --
// long enough to actually read, short enough not to feel like it's
// lingering.
const copiedConfirmationHold = 850 * time.Millisecond

// flushTimeout bounds how long ending a session waits for the last words
// to be decoded. A normal flush takes a fraction of a second; this is the
// backstop for a stuck decode, after which the last partial is copied.
const flushTimeout = 6 * time.Second

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
	// The resident process runs with this; you are not expected to type
	// it. Everything else here is a front end that talks to that process
	// and returns immediately -- see frontEnd.
	daemonFlag := fs.Bool("daemon", false, "run the resident process in the foreground (started for you by `0type`)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*daemonFlag {
		return frontEnd(*themeFlag, *dictateFlag, *backgroundFlag)
	}

	// Only one resident process, ever. See toggle.Lock for why the pidfile
	// can't enforce this by itself.
	release, err := toggle.Lock()
	if err != nil {
		if errors.Is(err, toggle.ErrAlreadyRunning) {
			return nil // another instance beat us to it
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

// frontEnd is what `0type` does when you run it: talk to the resident
// process, starting it first if there isn't one, and return immediately.
//
// Running the resident process in the foreground of the terminal you
// launched it from was wrong in the obvious way -- the shell sat there
// until you pressed Ctrl+C, and closing the terminal took 0type with it.
// A program you drive with a global shortcut should hand the terminal
// straight back.
func frontEnd(themeName string, dictate, background bool) error {
	if _, err := toggle.RunningPID(); err == nil {
		if background {
			fmt.Fprintln(os.Stderr, "0type is already running")
			return nil
		}
		if dictate {
			return toggle.Send()
		}
		return toggle.SendMenu()
	}

	if toggle.Starting() {
		fmt.Fprintln(os.Stderr, "0type is starting up; it'll be ready in a few seconds")
		return nil
	}

	var extra []string
	if themeName != "" {
		extra = append(extra, "--theme", themeName)
	}
	switch {
	case dictate:
		extra = append(extra, "--dictate")
	case background:
		extra = append(extra, "--background")
	}
	if err := startDaemon(extra...); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "starting 0type (the speech model takes a few seconds to load)")
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

	mu      sync.Mutex
	visible bool
	// cur is the current session, kept after it has been stopped so that
	// its delayed finish can tell whether a newer one has replaced it (and
	// so must leave the window alone). Nil until the first dictation.
	cur *session
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
	sess := newSession()
	a.mu.Lock()
	if a.visible {
		a.mu.Unlock()
		return
	}
	a.visible = true
	a.cur = sess
	a.mu.Unlock()

	a.win.SetText("") // back to idle for the new session
	a.win.Show()
	a.hooks.Fire(plugin.HookStart, "")
	go a.runPipeline(sess)
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
	sess := a.cur
	a.mu.Unlock()

	if sess == nil {
		a.win.Hide()
		return
	}
	sess.end()
	a.win.SetLevel(0) // the meter shouldn't keep dancing while we finish up

	// Don't read the text yet. The last thing said is usually still in the
	// runner's buffer, not finalized -- that only happens after ~800ms of
	// silence, and pressing the key right after you stop talking is well
	// inside that -- so the capture goroutine is about to decode it. Wait
	// for it, off the UI thread so the window stays responsive, but not
	// forever: if the decode hangs we still copy what we have.
	go func() {
		select {
		case <-sess.done:
		case <-time.After(flushTimeout):
			log.Printf("0type: finishing the last words took over %s; copying what was transcribed so far", flushTimeout)
		}
		ui.RunOnMainThread(func() { a.finishSession(sess) })
	}()
}

// finishSession puts a stopped session's text on the clipboard and plays
// the outro. It runs on the GTK main thread.
//
// The text is copied unconditionally, even if something else has taken the
// window in the meantime, because the clipboard is the deliverable: a user
// who stops dictating and immediately opens the menu, or starts again,
// must not lose what they just said. What's conditional is only the visual
// hand-off, which must not draw over whatever now owns the window.
func (a *app) finishSession(sess *session) {
	text := sess.result()

	if text != "" {
		ui.SetClipboard(text)
		// on_copy is the hook that can do something *else* with the result --
		// type it into the focused window, append it to a file -- so it fires
		// with the same text that just went to the clipboard, before on_stop
		// reports the session as over.
		a.hooks.Fire(plugin.HookCopy, text)
	}
	a.hooks.Fire(plugin.HookStop, text)

	if a.windowTaken(sess) {
		return
	}
	if text == "" {
		a.win.Hide() // nothing was said: no confirmation, just close
		return
	}

	a.win.ShowCopiedConfirmation()
	time.AfterFunc(copiedConfirmationHold, func() {
		ui.RunOnMainThread(func() {
			if a.windowTaken(sess) {
				return // something started during the hold -- don't close it out
			}
			a.win.Hide()
		})
	})
}

// windowTaken reports whether the overlay now belongs to something other
// than sess: a newer dictation, or the menu.
func (a *app) windowTaken(sess *session) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur != sess || a.visible || a.menu.open
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
	sess := a.cur
	a.mu.Unlock()
	if sess != nil {
		sess.end()
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
func (a *app) runPipeline(sess *session) {
	// Closed last, after the final flush, so hideAndStop knows every word
	// has been accounted for.
	defer close(sess.done)

	mic, err := audio.OpenCapture(sampleRate, channels)
	if err != nil {
		a.show(sess, fmt.Sprintf("mic error: %v", err))
		return
	}
	defer mic.Close()

	runner := stream.NewRunner(stream.DefaultConfig(), a.model, nil)
	chunkSamples := sampleRate * channels * chunkMS / 1000
	chunks := audio.StreamChunks(mic, chunkSamples, sess.stop)

	for c := range chunks {
		if c.Err != nil {
			a.show(sess, fmt.Sprintf("mic error: %v", c.Err))
			break // still flush what was heard before the mic failed
		}
		db := audio.DBFS(audio.RMS(c.Samples))
		level := (db + 60) / 50 // map -60..-10 dBFS onto 0..1 for the bar's live meter
		ui.RunOnMainThread(func() { a.win.SetLevel(level) })
		events, err := runner.Feed(audio.Int16ToFloat32(c.Samples), db)
		if err != nil {
			continue // transient decode error: keep listening, don't crash the session
		}
		for _, ev := range events {
			a.applyEvent(sess, ev)
		}
	}

	// The loop ends because the session was stopped. Whatever was said in
	// the last stretch is still buffered, undecoded as a sentence -- decode
	// it now, as a real final rather than relying on the last partial,
	// which lags by up to a second and so is missing the very last words.
	events, err := runner.Flush()
	if err != nil {
		log.Printf("0type: could not decode the last words: %v", err)
		return // result() falls back to the last partial
	}
	for _, ev := range events {
		a.applyEvent(sess, ev)
	}
}

// applyEvent folds one transcription event into the session and, if the
// session still owns the window, redraws it.
func (a *app) applyEvent(sess *session, ev stream.Event) {
	var display string
	if ev.Kind == stream.Final {
		display = sess.addFinal(ev.Text)
		a.hooks.Fire(plugin.HookFinal, ev.Text)
	} else {
		display = sess.setPartial(ev.Text)
	}
	a.show(sess, display)
}

// show draws text, but only while sess is still the current session: a
// session that is finishing in the background must not overwrite the
// display of one that has since started.
func (a *app) show(sess *session, text string) {
	ui.RunOnMainThread(func() {
		a.mu.Lock()
		current := a.cur == sess
		a.mu.Unlock()
		if current {
			a.win.SetText(text)
		}
	})
}

// appendSentence joins a newly finished (or in-progress) sentence onto the
// dictation accumulated so far, space-separated.
func appendSentence(dictated, sentence string) string {
	if sentence == "" {
		return dictated
	}
	if dictated == "" {
		return sentence
	}
	return dictated + " " + sentence
}
