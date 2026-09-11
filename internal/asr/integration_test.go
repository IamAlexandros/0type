//go:build integration

// This file is excluded from the default `go test ./...` run (see
// Makefile: `make test` vs `make test-integration`) because it needs the
// real, several-hundred-MB Parakeet model to be present in the local model
// cache (run `0type setup` first) and takes a few seconds to run.
package asr

import (
	"os"
	"strings"
	"testing"

	"github.com/zalkanorr/0type/internal/audio"
	"github.com/zalkanorr/0type/internal/modelstore"
	"github.com/zalkanorr/0type/internal/wav"
)

// TestTranscribe_GoldenFile runs the full pipeline (feature extraction,
// encoder, TDT greedy decode) against testdata/hello.wav and checks the
// output against testdata/hello.txt. This is the single end-to-end
// correctness check for the whole ASR pipeline; everything else in this
// package is tested with fakes.
func TestTranscribe_GoldenFile(t *testing.T) {
	wavBytes, err := os.Open("../../testdata/hello.wav")
	if err != nil {
		t.Fatalf("open testdata/hello.wav: %v", err)
	}
	defer wavBytes.Close()

	pcm, err := wav.Read(wavBytes)
	if err != nil {
		t.Fatalf("parse testdata/hello.wav: %v", err)
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

	if err := InitEnvironment(""); err != nil {
		t.Fatalf("init onnxruntime: %v", err)
	}
	defer DestroyEnvironment()

	model, err := LoadModel(dir)
	if err != nil {
		t.Fatalf("load model (did you run `0type setup`?): %v", err)
	}
	defer model.Close()

	got, err := model.Transcribe(audio.Int16ToFloat32(pcm.Samples))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got != want {
		t.Errorf("Transcribe() = %q, want %q", got, want)
	}
}
