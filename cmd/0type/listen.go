package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/IamAlexandros/0type/internal/asr"
	"github.com/IamAlexandros/0type/internal/audio"
	"github.com/IamAlexandros/0type/internal/modelstore"
	"github.com/IamAlexandros/0type/internal/stream"
)

// runListen captures live microphone audio and prints partial/final
// transcript lines to stdout as they're produced. It exists to prove the
// streaming pipeline works end-to-end from the terminal, before any UI
// code depends on it.
func runListen(args []string) error {
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

	mic, err := audio.OpenCapture(sampleRate, channels)
	if err != nil {
		return fmt.Errorf("open capture: %w", err)
	}
	defer mic.Close()

	runner := stream.NewRunner(stream.DefaultConfig(), model, nil)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	stop := make(chan struct{})
	go func() {
		<-sigCh
		close(stop)
	}()

	// Capture runs on its own goroutine so a slow decode pass (hundreds of
	// ms, see internal/asr's warm-inference benchmark) never blocks the
	// ALSA read loop -- doing that caused real, silent audio dropouts
	// (buffer overruns) exactly when a decode pass was in flight, which
	// corrupted transcripts. See internal/audio.StreamChunks.
	chunkSamples := sampleRate * channels * chunkMS / 1000
	chunks := audio.StreamChunks(mic, chunkSamples, stop)

	fmt.Println("listening... press Ctrl+C to stop")

	for c := range chunks {
		if c.Err != nil {
			return fmt.Errorf("read: %w", c.Err)
		}
		db := audio.DBFS(audio.RMS(c.Samples))

		events, err := runner.Feed(audio.Int16ToFloat32(c.Samples), db)
		if err != nil {
			return fmt.Errorf("transcribe: %w", err)
		}
		for _, ev := range events {
			if ev.Kind == stream.Final {
				fmt.Printf("\rfinal:   %s%s\n", ev.Text, clearToEOL)
			} else {
				fmt.Printf("\rpartial: %s%s", ev.Text, clearToEOL)
			}
		}
	}
	fmt.Println()
	return nil
}

// clearToEOL erases any leftover characters from a previous, longer line
// when overwriting it in place with \r.
const clearToEOL = "\033[K"
