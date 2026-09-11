package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zalkanorr/0type/internal/audio"
)

const (
	sampleRate = 16000
	channels   = 1
	chunkMS    = 100
)

func runDebug(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("debug: missing subcommand (try: audio-levels)")
	}
	switch args[0] {
	case "audio-levels":
		return runDebugAudioLevels()
	default:
		return fmt.Errorf("debug: unknown subcommand %q", args[0])
	}
}

// runDebugAudioLevels opens the default microphone and prints a live dBFS
// meter to the terminal until interrupted. It exists to prove audio capture
// works standalone, before any ASR or UI code depends on it.
func runDebugAudioLevels() error {
	mic, err := audio.OpenCapture(sampleRate, channels)
	if err != nil {
		return fmt.Errorf("open capture: %w", err)
	}
	defer mic.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-sigCh
		close(done)
	}()

	buf := make([]int16, sampleRate*channels*chunkMS/1000)
	fmt.Println("listening... press Ctrl+C to stop")

	for {
		select {
		case <-done:
			fmt.Println()
			return nil
		default:
		}

		n, err := mic.ReadInt16(buf)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		db := audio.DBFS(audio.RMS(buf[:n]))
		fmt.Printf("\r%6.1f dBFS  [%s]", db, audio.Meter(db, 40))
	}
}
