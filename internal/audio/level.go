package audio

import "math"

// silenceFloorDB is the value DBFS returns for silence (rms == 0) instead
// of -Inf, so callers can render it without special-casing -Inf.
const silenceFloorDB = -96.0

// RMS returns the root-mean-square amplitude of buf, normalized to [0, 1]
// where 1.0 represents full-scale int16 amplitude.
func RMS(buf []int16) float64 {
	if len(buf) == 0 {
		return 0
	}
	var sumSq float64
	for _, s := range buf {
		v := float64(s) / 32768.0
		sumSq += v * v
	}
	return math.Sqrt(sumSq / float64(len(buf)))
}

// DBFS converts a normalized RMS amplitude (as returned by RMS) to decibels
// relative to full scale, floored at silenceFloorDB.
func DBFS(rms float64) float64 {
	if rms <= 0 {
		return silenceFloorDB
	}
	db := 20 * math.Log10(rms)
	if db < silenceFloorDB {
		return silenceFloorDB
	}
	return db
}

// Meter renders a fixed-width ASCII bar for a dBFS value, clamping the
// [-60, 0] dB display range to the bar's [0, width] extent.
func Meter(db float64, width int) string {
	const minDB, maxDB = -60.0, 0.0
	frac := (db - minDB) / (maxDB - minDB)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * float64(width))
	bar := make([]byte, width)
	for i := range bar {
		if i < filled {
			bar[i] = '#'
		} else {
			bar[i] = '-'
		}
	}
	return string(bar)
}
