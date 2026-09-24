// Package stream turns a live sequence of audio chunks into incremental
// transcript events: it accumulates audio for the current utterance,
// periodically re-decodes it (v1's approximation of true streaming ASR --
// see docs/SETUP.md), and uses simple energy-based VAD to detect utterance
// boundaries and emit a final result.
package stream

import "time"

// Kind distinguishes a tentative, in-progress transcript update from a
// finalized one, emitted once an end-of-utterance boundary is detected.
type Kind int

const (
	Partial Kind = iota
	Final
)

// Event is one live transcript update.
type Event struct {
	Kind Kind
	Text string
	// StableWords is the number of leading words in Text that were also
	// present, in the same position, in the previous Partial's text --
	// i.e. words unlikely to change on the next decode pass. Only
	// meaningful for Partial events.
	StableWords int
}

// Transcriber is the minimal interface Runner needs from the ASR model, so
// the scheduling/stabilization logic here can be unit tested with a fake
// instead of a real model.
type Transcriber interface {
	Transcribe(waveform []float32) (string, error)
}

// Config controls windowing and end-of-utterance detection.
type Config struct {
	// DecodeInterval is the minimum time between decode passes while an
	// utterance is in progress.
	DecodeInterval time.Duration
	// SilenceThresholdDB is the dBFS level below which a chunk counts as
	// silence for VAD purposes.
	SilenceThresholdDB float64
	// SilenceHangoverChunks is how many consecutive silent chunks must
	// follow speech before an utterance is considered finished.
	SilenceHangoverChunks int
	// MinSamplesForPartial is the minimum accumulated segment length
	// before a Partial decode pass runs. Very short segments (a couple of
	// hundred ms) are prone to the model hallucinating filler words (e.g.
	// "Mm.") on ambiguous, context-free audio; below this length, Feed
	// simply waits for more audio rather than emitting a Partial. Final
	// decodes are never gated by this -- an utterance VAD has decided is
	// over is always reported, however short.
	MinSamplesForPartial int
}

// DefaultConfig returns reasonable defaults for 16kHz mic input processed
// in ~100ms chunks (see cmd/0type's chunkMS constant).
func DefaultConfig() Config {
	return Config{
		DecodeInterval:        1 * time.Second,
		SilenceThresholdDB:    -45,
		SilenceHangoverChunks: 8,    // ~800ms at 100ms chunks
		MinSamplesForPartial:  4800, // 300ms at 16kHz
	}
}

// Runner accumulates audio for the current utterance, periodically decodes
// it, and emits Partial/Final events as speech progresses and pauses are
// detected.
type Runner struct {
	cfg Config
	asr Transcriber
	vad *VAD
	now func() time.Time

	segment      []float32
	lastPartial  string
	lastDecodeAt time.Time
}

// SegmentSamples returns the number of samples accumulated for the
// current (not yet finalized) utterance. Mainly useful for diagnostics.
func (r *Runner) SegmentSamples() int {
	return len(r.segment)
}

// NewRunner creates a Runner. Pass nil for now to use time.Now; tests
// inject a deterministic clock instead.
func NewRunner(cfg Config, asr Transcriber, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	return &Runner{
		cfg: cfg,
		asr: asr,
		vad: NewVAD(cfg.SilenceThresholdDB, cfg.SilenceHangoverChunks),
		now: now,
	}
}

// Flush finishes whatever utterance is still in progress and returns it as
// a Final event, or no events if there was nothing to say. Call it when
// the audio source is going away for good.
//
// Without this, ending a session loses the last thing said: Feed only
// finalizes an utterance after SilenceHangoverChunks of silence, so
// stopping within that window (about 800ms by default, and pressing a key
// straight after you finish speaking is exactly when it happens) leaves
// the whole utterance sitting unfinalized in the buffer, where it was
// silently discarded.
//
// Like a Final from Feed, it is never gated by MinSamplesForPartial: the
// caller has decided the audio is over, so a short utterance is still
// reported. The runner is reset, so it may be reused afterwards.
func (r *Runner) Flush() ([]Event, error) {
	if len(r.segment) == 0 {
		return nil, nil
	}
	text, err := r.asr.Transcribe(r.segment)
	if err != nil {
		return nil, err
	}
	r.segment = nil
	r.lastPartial = ""
	r.lastDecodeAt = time.Time{}
	if text == "" {
		return nil, nil
	}
	return []Event{{Kind: Final, Text: text}}, nil
}

// Feed processes one chunk of audio (float32, normalized to [-1, 1]) with
// its measured level (dBFS): if VAD considers it part of the current
// utterance, it's appended to the accumulating segment (genuine
// between-utterance silence is discarded, not accumulated -- otherwise it
// would grow the segment without bound); a decode pass runs if one is due;
// and any resulting events are returned. There is at most one event per
// Feed call: either a Partial (new stabilized text from a decode pass) or
// a Final (the utterance just ended).
func (r *Runner) Feed(chunk []float32, chunkDB float64) ([]Event, error) {
	var events []Event

	_, active, endOfUtterance := r.vad.Update(chunkDB)
	if active {
		r.segment = append(r.segment, chunk...)
	}

	if endOfUtterance && len(r.segment) > 0 {
		text, err := r.asr.Transcribe(r.segment)
		if err != nil {
			return events, err
		}
		r.segment = nil
		r.lastPartial = ""
		// Zero, not r.now(): the next utterance must get an immediate
		// first partial regardless of how soon after this one it starts.
		r.lastDecodeAt = time.Time{}
		if text != "" {
			events = append(events, Event{Kind: Final, Text: text})
		}
		return events, nil
	}

	if len(r.segment) < r.cfg.MinSamplesForPartial {
		return events, nil
	}
	if r.now().Sub(r.lastDecodeAt) < r.cfg.DecodeInterval {
		return events, nil
	}

	text, err := r.asr.Transcribe(r.segment)
	if err != nil {
		return events, err
	}
	r.lastDecodeAt = r.now()
	if text == r.lastPartial {
		return events, nil
	}
	// A later decode pass over the same (growing) segment can legitimately
	// return "" -- e.g. more accumulated silence/noise shifting the
	// model's read of ambiguous audio -- but a Partial must never regress
	// already-displayed text to blank; just wait for the next pass rather
	// than emit anything. (Final already only fires on non-empty text;
	// this mirrors that for Partial.)
	if text == "" {
		return events, nil
	}
	stable := StablePrefixWords(r.lastPartial, text)
	r.lastPartial = text
	events = append(events, Event{Kind: Partial, Text: text, StableWords: stable})
	return events, nil
}
