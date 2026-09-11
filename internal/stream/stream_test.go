package stream

import (
	"errors"
	"testing"
	"time"
)

type fakeTranscriber struct {
	result string
	err    error
	calls  int
}

func (f *fakeTranscriber) Transcribe(waveform []float32) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.result, nil
}

// noMinLength returns DefaultConfig with MinSamplesForPartial disabled, for
// tests that feed tiny chunks and care about scheduling/stabilization
// logic rather than the length gate (see TestRunner_MinSamplesForPartial
// for that gate's own test).
func noMinLength() Config {
	cfg := DefaultConfig()
	cfg.MinSamplesForPartial = 0
	return cfg
}

func TestRunner_FirstChunkProducesImmediatePartial(t *testing.T) {
	fake := &fakeTranscriber{result: "hello"}
	clock := time.Unix(0, 0)
	r := NewRunner(noMinLength(), fake, func() time.Time { return clock })

	events, err := r.Feed([]float32{0.1, 0.2}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Partial || events[0].Text != "hello" {
		t.Fatalf("events = %+v, want one Partial 'hello'", events)
	}
}

func TestRunner_NoNewPartialUntilDecodeIntervalElapses(t *testing.T) {
	fake := &fakeTranscriber{result: "hello"}
	clock := time.Unix(0, 0)
	cfg := noMinLength()
	r := NewRunner(cfg, fake, func() time.Time { return clock })

	if _, err := r.Feed([]float32{0.1}, -10); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	clock = clock.Add(cfg.DecodeInterval / 2)
	events, err := r.Feed([]float32{0.1}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none before DecodeInterval elapses", events)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no extra decode before interval)", fake.calls)
	}
}

func TestRunner_SamePartialTextProducesNoDuplicateEvent(t *testing.T) {
	fake := &fakeTranscriber{result: "hello"}
	clock := time.Unix(0, 0)
	cfg := noMinLength()
	r := NewRunner(cfg, fake, func() time.Time { return clock })

	if _, err := r.Feed([]float32{0.1}, -10); err != nil { // first partial: "hello"
		t.Fatalf("Feed: %v", err)
	}
	clock = clock.Add(cfg.DecodeInterval)
	events, err := r.Feed([]float32{0.1}, -10) // still "hello"
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none when text is unchanged", events)
	}
}

func TestRunner_StableWordsReflectsUnchangedPrefix(t *testing.T) {
	fake := &fakeTranscriber{result: "the quick"}
	clock := time.Unix(0, 0)
	cfg := noMinLength()
	r := NewRunner(cfg, fake, func() time.Time { return clock })

	if _, err := r.Feed([]float32{0.1}, -10); err != nil { // "the quick"
		t.Fatalf("Feed: %v", err)
	}
	fake.result = "the quick brown"
	clock = clock.Add(cfg.DecodeInterval)
	events, err := r.Feed([]float32{0.1}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one Partial", events)
	}
	if events[0].StableWords != 2 {
		t.Errorf("StableWords = %d, want 2 (\"the quick\" unchanged)", events[0].StableWords)
	}
}

func TestRunner_EndOfUtteranceEmitsFinalAndResets(t *testing.T) {
	fake := &fakeTranscriber{result: "hello world"}
	clock := time.Unix(0, 0)
	cfg := noMinLength()
	cfg.SilenceHangoverChunks = 2
	r := NewRunner(cfg, fake, func() time.Time { return clock })

	if _, err := r.Feed([]float32{0.1}, -10); err != nil { // speech: first partial
		t.Fatalf("Feed: %v", err)
	}
	clock = clock.Add(cfg.DecodeInterval)
	if _, err := r.Feed([]float32{0.1}, -60); err != nil { // silence 1 (under hangover)
		t.Fatalf("Feed: %v", err)
	}
	clock = clock.Add(cfg.DecodeInterval)
	events, err := r.Feed([]float32{0.1}, -60) // silence 2 -> hangover reached
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Final || events[0].Text != "hello world" {
		t.Fatalf("events = %+v, want one Final 'hello world'", events)
	}

	// After a Final, the segment resets: the next speech chunk starts a
	// fresh utterance and produces a new immediate Partial.
	fake.result = "next"
	events, err = r.Feed([]float32{0.1}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Partial || events[0].Text != "next" {
		t.Fatalf("events after reset = %+v, want one Partial 'next'", events)
	}
}

func TestRunner_EmptyFinalTextIsSuppressed(t *testing.T) {
	fake := &fakeTranscriber{result: ""}
	cfg := noMinLength()
	cfg.SilenceHangoverChunks = 1
	r := NewRunner(cfg, fake, nil)

	events, err := r.Feed([]float32{0.1}, -60) // silence with no prior speech: no-op
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestRunner_TranscribeErrorPropagates(t *testing.T) {
	fake := &fakeTranscriber{err: errors.New("boom")}
	r := NewRunner(noMinLength(), fake, nil)
	if _, err := r.Feed([]float32{0.1}, -10); err == nil {
		t.Fatal("Feed: want error from Transcribe to propagate, got nil")
	}
}

func TestRunner_MinSamplesForPartial(t *testing.T) {
	fake := &fakeTranscriber{result: "hi"}
	cfg := DefaultConfig()
	cfg.MinSamplesForPartial = 5
	r := NewRunner(cfg, fake, nil)

	// Below the minimum: no decode pass at all, even though a Partial
	// would otherwise be immediately due.
	events, err := r.Feed(make([]float32, 4), -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none below MinSamplesForPartial", events)
	}
	if fake.calls != 0 {
		t.Fatalf("calls = %d, want 0 (gated by minimum length)", fake.calls)
	}

	// Crossing the minimum triggers the (still-immediate, first) decode.
	events, err = r.Feed(make([]float32, 2), -10) // segment now len 6 >= 5
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Partial || events[0].Text != "hi" {
		t.Fatalf("events = %+v, want one Partial 'hi' once minimum length is reached", events)
	}
}

func TestRunner_FinalNotGatedByMinSamplesForPartial(t *testing.T) {
	// An utterance VAD has decided is over must be reported even if it
	// never reached MinSamplesForPartial -- e.g. a short "no" or "yes".
	fake := &fakeTranscriber{result: "no"}
	cfg := DefaultConfig()
	cfg.MinSamplesForPartial = 100000 // never reached by the tiny chunk below
	cfg.SilenceHangoverChunks = 1
	r := NewRunner(cfg, fake, nil)

	r.Feed([]float32{0.1}, -10)                // brief speech, below the partial-length gate
	events, err := r.Feed([]float32{0.1}, -60) // immediate silence -> end of utterance
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Final || events[0].Text != "no" {
		t.Fatalf("events = %+v, want one Final 'no' despite never reaching MinSamplesForPartial", events)
	}
}
