//go:build integration

package asr

import (
	"os"
	"testing"
	"time"

	"github.com/IamAlexandros/0type/internal/audio"
	"github.com/IamAlexandros/0type/internal/modelstore"
	"github.com/IamAlexandros/0type/internal/wav"
)

// TestTranscribeScalesWithLength prints how decode time grows with the
// length of the utterance. Not an assertion -- a measurement, run by hand
// (go test -tags integration -run Scaling -v ./internal/asr) when deciding
// how long a segment the streaming runner may let grow before finalizing.
func TestTranscribeScalesWithLength(t *testing.T) {
	f, err := os.Open("../../testdata/hello.wav")
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := wav.Read(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	clip := audio.Int16ToFloat32(pcm.Samples)

	dir, err := modelstore.Dir(modelstore.ParakeetTDTv2.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitEnvironment(""); err != nil {
		t.Fatal(err)
	}
	defer DestroyEnvironment()
	model, err := LoadModel(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()

	model.Transcribe(clip) // warm up

	for _, repeats := range []int{1, 2, 4, 6, 9, 14, 20} {
		var wave []float32
		for i := 0; i < repeats; i++ {
			wave = append(wave, clip...)
		}
		start := time.Now()
		if _, err := model.Transcribe(wave); err != nil {
			t.Fatalf("%d repeats: %v", repeats, err)
		}
		secs := float64(len(wave)) / 16000
		took := time.Since(start)
		t.Logf("%5.1fs of audio -> %6.2fs to decode  (%.2fx realtime)", secs, took.Seconds(), took.Seconds()/secs)
	}
}
