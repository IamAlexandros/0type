//go:build integration

// Excluded from the default `go test ./...` run for the same reason as
// internal/asr's integration test: it needs the real Parakeet model
// present locally (`0type setup`). See Makefile's test-integration target.
package stream

import (
	"os"
	"strings"
	"testing"

	"github.com/IamAlexandros/0type/internal/asr"
	"github.com/IamAlexandros/0type/internal/audio"
	"github.com/IamAlexandros/0type/internal/modelstore"
	"github.com/IamAlexandros/0type/internal/wav"
)

// TestRunner_GoldenFile_RealModel replays testdata/hello.wav through Runner
// in the same 100ms chunks and silence-padding that a real mic capture
// would produce, against the real ASR model, and checks that the resulting
// Final event matches testdata/hello.txt exactly.
//
// This is the regression test for a real bug found while building this
// package: the CLI capture loop originally called Transcribe() (hundreds
// of ms) synchronously in the same loop as ALSA reads, which caused
// buffer overruns and silently corrupted/truncated transcripts (see
// internal/audio.StreamChunks and cmd/0type/listen.go). That bug was in
// the CLI's capture loop, not here -- Runner itself was already correct in
// isolation, as this test demonstrates by driving it directly with no
// ALSA/mic involved at all.
func TestRunner_GoldenFile_RealModel(t *testing.T) {
	const sampleRate = 16000
	const chunkSamples = sampleRate / 10 // 100ms

	f, err := os.Open("../../testdata/hello.wav")
	if err != nil {
		t.Fatalf("open testdata/hello.wav: %v", err)
	}
	defer f.Close()
	pcm, err := wav.Read(f)
	if err != nil {
		t.Fatalf("parse testdata/hello.wav: %v", err)
	}
	if pcm.SampleRate != sampleRate {
		t.Fatalf("testdata/hello.wav sample rate = %d, want %d", pcm.SampleRate, sampleRate)
	}

	wantBytes, err := os.ReadFile("../../testdata/hello.txt")
	if err != nil {
		t.Fatalf("read testdata/hello.txt: %v", err)
	}
	want := strings.TrimSpace(string(wantBytes))

	dir, err := modelstore.Dir(modelstore.ParakeetTDTv2.Name)
	if err != nil {
		t.Fatalf("resolve model cache dir: %v", err)
	}
	if err := asr.InitEnvironment(""); err != nil {
		t.Fatalf("init onnxruntime: %v", err)
	}
	defer asr.DestroyEnvironment()

	model, err := asr.LoadModel(dir)
	if err != nil {
		t.Fatalf("load model (did you run `0type setup`?): %v", err)
	}
	defer model.Close()

	runner := NewRunner(DefaultConfig(), model, nil)

	// Pad with a second of silence on each end, mirroring the lead-in/
	// trail-off silence a real mic capture would have around speech.
	pad := make([]int16, sampleRate)
	samples := append(append(append([]int16{}, pad...), pcm.Samples...), pad...)

	var finals []string
	for off := 0; off < len(samples); off += chunkSamples {
		end := off + chunkSamples
		if end > len(samples) {
			end = len(samples)
		}
		chunk := samples[off:end]
		db := audio.DBFS(audio.RMS(chunk))

		events, err := runner.Feed(audio.Int16ToFloat32(chunk), db)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		for _, ev := range events {
			if ev.Kind == Final {
				finals = append(finals, ev.Text)
			}
		}
	}

	if len(finals) != 1 {
		t.Fatalf("got %d Final events %v, want exactly 1", len(finals), finals)
	}
	if finals[0] != want {
		t.Errorf("Final text = %q, want %q", finals[0], want)
	}
}
