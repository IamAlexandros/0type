// Package audio handles microphone capture and PCM sample utilities.
package audio

// Source produces interleaved int16 PCM samples, such as a live capture
// device. It exists so callers can be tested against a fake source instead
// of a real audio device.
type Source interface {
	ReadInt16(buf []int16) (int, error)
	Close() error
}
