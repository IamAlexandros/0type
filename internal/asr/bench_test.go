//go:build integration

package asr

import (
	"os"
	"testing"
	"time"

	"github.com/zalkanorr/0type/internal/audio"
	"github.com/zalkanorr/0type/internal/modelstore"
	"github.com/zalkanorr/0type/internal/wav"
)

// BenchmarkTranscribe_Warm measures per-call Transcribe latency once the
// model is already loaded, which is what matters for streaming (Phase 3):
// the daemon loads the model once and reuses it for the life of the
// process, so model-load time is a one-time startup cost, not a per-window
// cost.
func BenchmarkTranscribe_Warm(b *testing.B) {
	f, err := os.Open("../../testdata/hello.wav")
	if err != nil {
		b.Fatalf("open testdata/hello.wav: %v", err)
	}
	defer f.Close()
	pcm, err := wav.Read(f)
	if err != nil {
		b.Fatalf("parse testdata/hello.wav: %v", err)
	}
	waveform := audio.Int16ToFloat32(pcm.Samples)
	clipSeconds := float64(len(waveform)) / 16000.0

	dir, err := modelstore.Dir(modelstore.ParakeetTDTv2.Name)
	if err != nil {
		b.Fatalf("resolve model cache dir: %v", err)
	}
	if err := InitEnvironment(""); err != nil {
		b.Fatalf("init onnxruntime: %v", err)
	}
	defer DestroyEnvironment()

	loadStart := time.Now()
	model, err := LoadModel(dir)
	if err != nil {
		b.Fatalf("load model: %v", err)
	}
	defer model.Close()
	b.Logf("model load: %v", time.Since(loadStart))
	b.Logf("test clip length: %.2fs", clipSeconds)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := model.Transcribe(waveform); err != nil {
			b.Fatalf("transcribe: %v", err)
		}
	}
}
