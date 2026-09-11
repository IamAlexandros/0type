package stream

// VAD is a simple energy-threshold voice activity detector with a
// hangover period, so brief pauses between words don't trigger a
// premature end-of-utterance.
type VAD struct {
	thresholdDB    float64
	hangoverChunks int
	silenceRun     int
	speaking       bool
}

// NewVAD creates a VAD that treats a chunk as silent when its level is
// below thresholdDB, and signals end-of-utterance once hangoverChunks
// consecutive silent chunks have followed speech.
func NewVAD(thresholdDB float64, hangoverChunks int) *VAD {
	if hangoverChunks < 1 {
		hangoverChunks = 1
	}
	return &VAD{thresholdDB: thresholdDB, hangoverChunks: hangoverChunks}
}

// Update processes one chunk's level (in dBFS) and returns:
//   - isSpeech: whether this chunk itself is above the silence threshold.
//   - active: whether this chunk belongs to the current utterance --
//     either actively speaking, or within the trailing hangover grace
//     window. Callers should only accumulate audio into a transcription
//     buffer while active is true; every chunk during genuine
//     between-utterance silence has active false, so that silence never
//     grows such a buffer without bound.
//   - endOfUtterance: whether this update just crossed the
//     end-of-utterance boundary (was speaking, now silent for the full
//     hangover period).
func (v *VAD) Update(db float64) (isSpeech, active, endOfUtterance bool) {
	isSpeech = db >= v.thresholdDB
	if isSpeech {
		v.speaking = true
		v.silenceRun = 0
		return true, true, false
	}

	if !v.speaking {
		return false, false, false
	}

	v.silenceRun++
	if v.silenceRun >= v.hangoverChunks {
		v.speaking = false
		v.silenceRun = 0
		return false, false, true
	}
	return false, true, false
}
