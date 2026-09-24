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

	// SoftSegmentSamples and HardSegmentSamples bound how long a single
	// utterance may grow. Decoding cost is roughly linear in segment length
	// (about 0.15x realtime on CPU), and every partial re-decodes the whole
	// segment, so someone who talks for a minute without a pause used to
	// make each update take seconds -- and made stopping take as long,
	// since the last words are decoded at that point. Bounding the segment
	// bounds all of it.
	//
	// Past SoftSegmentSamples the segment is finalized at the next short
	// pause (SoftPauseChunks of silence, far shorter than the normal
	// hangover, since a breath is enough of a boundary here). Past
	// HardSegmentSamples it is finalized wherever it is, mid-word if need
	// be. Zero disables either limit.
	SoftSegmentSamples int
	HardSegmentSamples int
	SoftPauseChunks    int
}

// DefaultConfig returns reasonable defaults for 16kHz mic input processed
// in ~100ms chunks (see cmd/0type's chunkMS constant).
func DefaultConfig() Config {
	return Config{
		DecodeInterval:        1 * time.Second,
		SilenceThresholdDB:    -45,
		SilenceHangoverChunks: 8,    // ~800ms at 100ms chunks
		MinSamplesForPartial:  4800, // 300ms at 16kHz
		SoftSegmentSamples:    10 * 16000,
		HardSegmentSamples:    18 * 16000,
		SoftPauseChunks:       3, // ~300ms at 100ms chunks
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
	pauseRun     int // consecutive silent chunks inside the current segment
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

// segmentTooLong reports whether the current segment has hit a length
// limit and should be finalized now rather than waiting for the VAD.
func (r *Runner) segmentTooLong() bool {
	n := len(r.segment)
	if r.cfg.HardSegmentSamples > 0 && n >= r.cfg.HardSegmentSamples {
		return true
	}
	if r.cfg.SoftSegmentSamples > 0 && n >= r.cfg.SoftSegmentSamples {
		pause := r.cfg.SoftPauseChunks
		if pause < 1 {
			pause = 1
		}
		return r.pauseRun >= pause
	}
	return false
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
	r.pauseRun = 0
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

	isSpeech, active, endOfUtterance := r.vad.Update(chunkDB)
	if isSpeech {
		r.pauseRun = 0
	} else if active {
		r.pauseRun++
	}
	if active {
		r.segment = append(r.segment, chunk...)
	}

	// The VAD decides an utterance is over after a long silence; the runner
	// also ends one itself when it has grown too long (see Config).
	tooLong := !endOfUtterance && r.segmentTooLong()
	if (endOfUtterance || tooLong) && len(r.segment) > 0 {
		text, err := r.asr.Transcribe(r.segment)
		if err != nil {
			return events, err
		}
		r.segment = nil
		r.lastPartial = ""
		r.pauseRun = 0
		if tooLong {
			// The VAD still thinks it is mid-utterance, so the silence
			// that follows would keep being accumulated into a new segment
			// that is nothing but silence -- which the model may answer with
			// filler words. Start the next one from silence.
			r.vad.Reset()
		}
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
