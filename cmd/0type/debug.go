package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zalkanorr/0type/internal/asr"
	"github.com/zalkanorr/0type/internal/audio"
	"github.com/zalkanorr/0type/internal/modelstore"
	"github.com/zalkanorr/0type/internal/stream"
	"github.com/zalkanorr/0type/internal/wav"
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
	case "transcribe":
		return runDebugTranscribe(args[1:])
	case "stream-file":
		return runDebugStreamFile(args[1:])
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
	stop := make(chan struct{})
	go func() {
		<-sigCh
		close(stop)
	}()

	chunkSamples := sampleRate * channels * chunkMS / 1000
	chunks := audio.StreamChunks(mic, chunkSamples, stop)
	fmt.Println("listening... press Ctrl+C to stop")

	for c := range chunks {
		if c.Err != nil {
			return fmt.Errorf("read: %w", c.Err)
		}
		db := audio.DBFS(audio.RMS(c.Samples))
		fmt.Printf("\r%6.1f dBFS  [%s]", db, audio.Meter(db, 40))
	}
	fmt.Println()
	return nil
}

// runDebugTranscribe runs the full offline ASR pipeline over a fixed 16kHz
// mono WAV file and prints the resulting text. It exists to prove the
// ONNX Runtime + Parakeet pipeline works end-to-end, before any streaming
// or UI code depends on it.
func runDebugTranscribe(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("debug transcribe: missing <wav file> argument")
	}

	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open %s: %w", args[0], err)
	}
	defer f.Close()

	pcm, err := wav.Read(f)
	if err != nil {
		return fmt.Errorf("parse wav: %w", err)
	}
	if pcm.SampleRate != sampleRate {
		return fmt.Errorf("expected a %dHz mono WAV, got %dHz (resample first, e.g. `ffmpeg -i in.wav -ar %d -ac 1 out.wav`)", sampleRate, pcm.SampleRate, sampleRate)
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

	waveform := audio.Int16ToFloat32(pcm.Samples)
	text, err := model.Transcribe(waveform)
	if err != nil {
		return fmt.Errorf("transcribe: %w", err)
	}
	fmt.Println(text)
	return nil
}

// runDebugStreamFile replays a fixed 16kHz mono WAV file through the same
// stream.Runner used by `listen`, in the same 100ms chunks, against the
// real ASR model -- but with no ALSA/mic capture involved at all. It
// exists to debug the streaming/VAD/stabilization pipeline in isolation
// from live audio capture, using a known-correct input.
func runDebugStreamFile(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("debug stream-file: missing <wav file> argument")
	}

	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open %s: %w", args[0], err)
	}
	defer f.Close()

	pcm, err := wav.Read(f)
	if err != nil {
		return fmt.Errorf("parse wav: %w", err)
	}
	if pcm.SampleRate != sampleRate {
		return fmt.Errorf("expected a %dHz mono WAV, got %dHz", sampleRate, pcm.SampleRate)
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

	runner := stream.NewRunner(stream.DefaultConfig(), model, nil)

	chunkSamples := sampleRate * channels * chunkMS / 1000
	samples := pcm.Samples
	// Pad with a second of silence on each end, mirroring the lead-in/
	// trail-off silence a real mic capture would have around speech.
	pad := make([]int16, sampleRate)
	samples = append(append(append([]int16{}, pad...), samples...), pad...)

	for off := 0; off < len(samples); off += chunkSamples {
		end := off + chunkSamples
		if end > len(samples) {
			end = len(samples)
		}
		chunk := samples[off:end]
		db := audio.DBFS(audio.RMS(chunk))
		fmt.Printf("chunk[%5d:%5d] db=%6.1f\n", off, end, db)

		events, err := runner.Feed(audio.Int16ToFloat32(chunk), db)
		if err != nil {
			return fmt.Errorf("transcribe: %w", err)
		}
		for _, ev := range events {
			if ev.Kind == stream.Final {
				fmt.Printf("  -> final:   %q\n", ev.Text)
			} else {
				fmt.Printf("  -> partial: %q (stable=%d)\n", ev.Text, ev.StableWords)
			}
		}
	}
	return nil
}
