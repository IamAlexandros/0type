package main

import (
	"fmt"
	"sync"

	"github.com/zalkanorr/0type/internal/asr"
	"github.com/zalkanorr/0type/internal/audio"
	"github.com/zalkanorr/0type/internal/modelstore"
	"github.com/zalkanorr/0type/internal/stream"
	"github.com/zalkanorr/0type/internal/toggle"
	"github.com/zalkanorr/0type/internal/ui"
)

// runApp is 0type's normal, no-subcommand entry point: it loads the model
// once, shows the floating overlay (initially hidden), and toggles
// visibility -- starting/stopping live mic capture and transcription along
// with it -- on SIGUSR1 (see internal/toggle and `0type toggle`).
func runApp(args []string) error {
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

	win, err := ui.New("") // idle: a pulsing dot until speech arrives, see internal/ui's slide_draw
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	win.LoadCSS("themes/default.css")

	cleanupPIDFile, err := toggle.WritePIDFile()
	if err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer cleanupPIDFile()

	a := &app{win: win, model: model}
	toggle.OnToggle(a.handleToggle)

	win.Run() // blocks until SIGINT/SIGTERM
	a.stopPipeline()
	return nil
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

	mu       sync.Mutex
	visible  bool
	stop     chan struct{}
	dictated string // finalized sentences this session, space-joined
}

func (a *app) handleToggle() {
	ui.RunOnMainThread(func() {
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
	stop := make(chan struct{})
	a.stop = stop
	a.mu.Unlock()

	a.win.SetText("") // back to idle for the new session
	a.win.Show()
	go a.runPipeline(stop)
}

// hideAndStop ends the session: stops capture, hides the window, and only
// then copies whatever was dictated to the clipboard -- once, not after
// every sentence -- so the user gets one clean paste of the whole session
// after they signal they're done.
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
	a.mu.Unlock()

	a.win.Hide()
	if stop != nil {
		close(stop)
	}
	if dictated != "" {
		ui.SetClipboard(dictated) // hideAndStop already runs on the GTK main thread (see handleToggle)
	}
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
