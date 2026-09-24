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

func TestRunner_PartialNeverRegressesToBlank(t *testing.T) {
	// Regression test for a real bug: a later decode pass over the same
	// growing segment can legitimately return "" (e.g. more accumulated
	// silence shifting the model's read of ambiguous audio), but that must
	// never blank out already-displayed good text.
	fake := &fakeTranscriber{result: "hello world"}
	clock := time.Unix(0, 0)
	cfg := noMinLength()
	r := NewRunner(cfg, fake, func() time.Time { return clock })

	events, err := r.Feed([]float32{0.1}, -10) // "hello world"
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hello world" {
		t.Fatalf("events = %+v, want one Partial 'hello world'", events)
	}

	fake.result = ""
	clock = clock.Add(cfg.DecodeInterval)
	events, err = r.Feed([]float32{0.1}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none -- an empty decode must not emit a blanking Partial", events)
	}

	// A subsequent real result must still diff correctly against the last
	// known non-empty text, not against "".
	fake.result = "hello world again"
	clock = clock.Add(cfg.DecodeInterval)
	events, err = r.Feed([]float32{0.1}, -10)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Text != "hello world again" || events[0].StableWords != 2 {
		t.Fatalf("events = %+v, want one Partial 'hello world again' with StableWords=2", events)
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

func TestRunner_SegmentDoesNotGrowDuringSilenceAfterUtterance(t *testing.T) {
	// Regression test for a real bug found running the live app: silence
	// following an utterance kept being appended to the segment forever
	// (Feed used to append unconditionally), so the buffer -- and the
	// per-decode-pass transcribe cost -- grew without bound the whole time
	// 0type sat listening to silence, pegging CPU and never producing
	// another update.
	fake := &fakeTranscriber{result: "hello world"}
	cfg := noMinLength()
	cfg.SilenceHangoverChunks = 2
	r := NewRunner(cfg, fake, nil)

	r.Feed([]float32{0.1, 0.2}, -10)                // speech
	r.Feed([]float32{0.1, 0.2}, -60)                // silence 1 (under hangover)
	events, err := r.Feed([]float32{0.1, 0.2}, -60) // silence 2 -> end of utterance
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Final {
		t.Fatalf("events = %+v, want one Final", events)
	}
	if got := r.SegmentSamples(); got != 0 {
		t.Fatalf("SegmentSamples() after finalize = %d, want 0", got)
	}

	for i := 0; i < 100; i++ {
		if _, err := r.Feed([]float32{0.1, 0.2}, -96); err != nil {
			t.Fatalf("Feed: %v", err)
		}
	}
	if got := r.SegmentSamples(); got != 0 {
		t.Fatalf("SegmentSamples() after 100 silent chunks post-utterance = %d, want 0 (unbounded growth bug)", got)
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

// The bug that lost dictation: a session ended right after speaking, inside
// the silence hangover, so the utterance was never finalized and the
// runner's buffer -- the only copy of what was said -- was thrown away.
func TestRunner_FlushFinalizesAnUnfinishedUtterance(t *testing.T) {
	asr := &fakeTranscriber{result: "the quick brown fox"}
	r := NewRunner(noMinLength(), asr, nil)

	loud := make([]float32, 1600)
	// Speech, then stop *before* the hangover elapses: no Final from Feed.
	for i := 0; i < 5; i++ {
		if _, err := r.Feed(loud, -20); err != nil {
			t.Fatalf("Feed: %v", err)
		}
	}
	// A couple of silent chunks, well short of SilenceHangoverChunks.
	for i := 0; i < 2; i++ {
		events, err := r.Feed(loud, -80)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		for _, ev := range events {
			if ev.Kind == Final {
				t.Fatal("test setup wrong: Final arrived before Flush")
			}
		}
	}

	events, err := r.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(events) != 1 || events[0].Kind != Final || events[0].Text != "the quick brown fox" {
		t.Fatalf("Flush() = %+v, want one Final with the utterance", events)
	}
	if r.SegmentSamples() != 0 {
		t.Errorf("segment still holds %d samples after Flush", r.SegmentSamples())
	}
}

func TestRunner_FlushWithNothingBufferedDoesNothing(t *testing.T) {
	asr := &fakeTranscriber{result: "should not be called"}
	r := NewRunner(noMinLength(), asr, nil)

	events, err := r.Flush()
	if err != nil || len(events) != 0 {
		t.Fatalf("Flush() = %+v, %v; want nothing", events, err)
	}
	if asr.calls != 0 {
		t.Errorf("Transcribe called %d times on an empty buffer", asr.calls)
	}
}

// A short utterance is still an utterance: the caller decided the audio is
// over, so the MinSamplesForPartial gate (which exists to stop the model
// hallucinating on tiny fragments *while listening*) must not apply.
func TestRunner_FlushIsNotGatedByMinSamplesForPartial(t *testing.T) {
	asr := &fakeTranscriber{result: "yes"}
	r := NewRunner(DefaultConfig(), asr, nil) // gate enabled

	if _, err := r.Feed(make([]float32, 800), -20); err != nil { // 50ms, far below the gate
		t.Fatalf("Feed: %v", err)
	}
	events, err := r.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(events) != 1 || events[0].Text != "yes" {
		t.Fatalf("Flush() = %+v, want the short utterance", events)
	}
}

func TestRunner_FlushEmptyTextIsSuppressed(t *testing.T) {
	r := NewRunner(noMinLength(), &fakeTranscriber{result: ""}, nil)
	if _, err := r.Feed(make([]float32, 1600), -20); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	events, err := r.Flush()
	if err != nil || len(events) != 0 {
		t.Fatalf("Flush() = %+v, %v; want no events for empty text", events, err)
	}
}

func TestRunner_FlushErrorPropagatesAndKeepsTheAudio(t *testing.T) {
	asr := &fakeTranscriber{result: "x"}
	r := NewRunner(noMinLength(), asr, nil)
	if _, err := r.Feed(make([]float32, 1600), -20); err != nil {
		t.Fatalf("Feed: %v", err)
	}

	asr.err = errors.New("boom")
	if _, err := r.Flush(); err == nil {
		t.Fatal("expected the transcribe error")
	}
	// The audio must still be there, so a retry can succeed rather than the
	// failure quietly destroying the only copy.
	if r.SegmentSamples() == 0 {
		t.Error("a failed Flush discarded the buffered audio")
	}
	asr.err = nil
	events, err := r.Flush()
	if err != nil || len(events) != 1 {
		t.Fatalf("retry Flush() = %+v, %v; want the utterance", events, err)
	}
}

// limitCfg is a small-number config for the segment-length limits: 100
// sample chunks, soft limit 1000 samples (10 chunks), hard limit 3000
// (30 chunks), a 2-chunk pause, and a hangover far longer than any of the
// tests' silences so only the length limits can end an utterance.
func limitCfg() Config {
	cfg := noMinLength()
	cfg.SilenceHangoverChunks = 1000
	cfg.SoftSegmentSamples = 1000
	cfg.HardSegmentSamples = 3000
	cfg.SoftPauseChunks = 2
	return cfg
}

func feedN(t *testing.T, r *Runner, n int, db float64) []Event {
	t.Helper()
	chunk := make([]float32, 100)
	var all []Event
	for i := 0; i < n; i++ {
		ev, err := r.Feed(chunk, db)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		all = append(all, ev...)
	}
	return all
}

func finals(events []Event) int {
	n := 0
	for _, e := range events {
		if e.Kind == Final {
			n++
		}
	}
	return n
}

// Past the soft limit a short pause is enough to end the segment, so a long
// dictation is cut where the speaker breathes rather than mid-word.
func TestRunner_SoftLimitFinalizesAtTheNextShortPause(t *testing.T) {
	r := NewRunner(limitCfg(), &fakeTranscriber{result: "part one"}, nil)

	// Speech right up to the soft limit: no pause yet, so no Final.
	if got := finals(feedN(t, r, 12, -20)); got != 0 {
		t.Fatalf("got %d finals with no pause, want 0", got)
	}
	// One silent chunk is not yet a pause...
	if got := finals(feedN(t, r, 1, -80)); got != 0 {
		t.Fatalf("finalized after a single silent chunk")
	}
	// ...the second is.
	if got := finals(feedN(t, r, 1, -80)); got != 1 {
		t.Fatalf("got %d finals at the pause, want 1", got)
	}
	if r.SegmentSamples() != 0 {
		t.Errorf("segment not reset after the forced final: %d samples", r.SegmentSamples())
	}
}

// Below the soft limit a short pause must not cut anything: that's normal
// speech rhythm, and the ordinary hangover handles real sentence ends.
func TestRunner_ShortPauseBelowTheSoftLimitDoesNotFinalize(t *testing.T) {
	r := NewRunner(limitCfg(), &fakeTranscriber{result: "x"}, nil)

	feedN(t, r, 4, -20)
	if got := finals(feedN(t, r, 3, -80)); got != 0 {
		t.Fatalf("a short pause in a short segment produced %d finals", got)
	}
}

// Someone who never pauses still gets a bounded segment.
func TestRunner_HardLimitFinalizesMidSpeech(t *testing.T) {
	r := NewRunner(limitCfg(), &fakeTranscriber{result: "long"}, nil)

	got := finals(feedN(t, r, 45, -20)) // 4500 samples of continuous speech
	if got != 1 {
		t.Fatalf("got %d finals over 4500 samples of unbroken speech, want 1", got)
	}
	if r.SegmentSamples() >= limitCfg().HardSegmentSamples {
		t.Errorf("segment is still %d samples, above the hard limit", r.SegmentSamples())
	}
}

// After a forced final the silence that follows must not become a segment
// of its own: the model answers silence with filler words.
func TestRunner_NothingIsAccumulatedInTheSilenceAfterAForcedFinal(t *testing.T) {
	r := NewRunner(limitCfg(), &fakeTranscriber{result: "part"}, nil)

	feedN(t, r, 12, -20)
	feedN(t, r, 2, -80) // pause -> forced final
	feedN(t, r, 6, -80) // ...then a long silence

	if r.SegmentSamples() != 0 {
		t.Errorf("silence after a forced final accumulated %d samples", r.SegmentSamples())
	}
	// Speech resuming must start a fresh segment normally.
	feedN(t, r, 3, -20)
	if r.SegmentSamples() != 300 {
		t.Errorf("segment after resuming = %d samples, want 300", r.SegmentSamples())
	}
}

// A failed decode at a forced boundary must not lose the audio; the next
// chunk retries.
func TestRunner_ForcedFinalErrorKeepsTheAudio(t *testing.T) {
	asr := &fakeTranscriber{result: "ok"}
	r := NewRunner(limitCfg(), asr, nil)
	feedN(t, r, 12, -20)

	asr.err = errors.New("boom")
	if _, err := r.Feed(make([]float32, 100), -80); err != nil {
		t.Fatalf("Feed at the not-yet-a-pause chunk: %v", err)
	}
	if _, err := r.Feed(make([]float32, 100), -80); err == nil {
		t.Fatal("expected the transcribe error at the forced boundary")
	}
	if r.SegmentSamples() == 0 {
		t.Fatal("audio discarded by a failed forced final")
	}
	asr.err = nil
	if got := finals(feedN(t, r, 1, -80)); got != 1 {
		t.Errorf("retry produced %d finals, want 1", got)
	}
}

// The defaults themselves have to be coherent, or the limits do nothing.
func TestDefaultConfigSegmentLimitsAreCoherent(t *testing.T) {
	c := DefaultConfig()
	if c.SoftSegmentSamples <= 0 || c.HardSegmentSamples <= c.SoftSegmentSamples {
		t.Errorf("soft=%d hard=%d: want 0 < soft < hard", c.SoftSegmentSamples, c.HardSegmentSamples)
	}
	if c.SoftPauseChunks < 1 || c.SoftPauseChunks >= c.SilenceHangoverChunks {
		t.Errorf("SoftPauseChunks=%d must be in [1, hangover=%d): a soft pause should be shorter than a real end of utterance",
			c.SoftPauseChunks, c.SilenceHangoverChunks)
	}
}
