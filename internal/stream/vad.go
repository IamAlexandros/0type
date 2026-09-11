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

// Update processes one chunk's level (in dBFS) and returns whether it
// counts as speech, and whether this update just crossed the
// end-of-utterance boundary (was speaking, now silent for the full
// hangover period).
func (v *VAD) Update(db float64) (isSpeech bool, endOfUtterance bool) {
	isSpeech = db >= v.thresholdDB
	if isSpeech {
		v.speaking = true
		v.silenceRun = 0
		return true, false
	}

	if !v.speaking {
		return false, false
	}

	v.silenceRun++
	if v.silenceRun >= v.hangoverChunks {
		v.speaking = false
		v.silenceRun = 0
		return false, true
	}
	return false, false
}
