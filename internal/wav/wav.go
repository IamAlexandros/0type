// Package wav reads canonical PCM WAV files: just enough to load test
// fixtures and files handed to the `debug transcribe` command. It does not
// resample or handle compressed formats — 0type's own pipeline always
// produces 16kHz mono PCM directly from internal/audio, so this is
// intentionally minimal.
package wav

import (
	"encoding/binary"
	"fmt"
	"io"
)

// PCM holds decoded mono 16-bit PCM audio.
type PCM struct {
	SampleRate int
	Samples    []int16
}

// Read parses a canonical PCM WAV file (RIFF/WAVE, 16-bit, mono) from r.
func Read(r io.Reader) (*PCM, error) {
	var riffHdr [12]byte
	if _, err := io.ReadFull(r, riffHdr[:]); err != nil {
		return nil, fmt.Errorf("wav: read RIFF header: %w", err)
	}
	if string(riffHdr[0:4]) != "RIFF" || string(riffHdr[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav: not a RIFF/WAVE file")
	}

	var sampleRate, channels, bitsPerSample int
	var samples []int16

	for {
		var chunkHdr [8]byte
		if _, err := io.ReadFull(r, chunkHdr[:]); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("wav: read chunk header: %w", err)
		}
		chunkID := string(chunkHdr[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHdr[4:8])

		switch chunkID {
		case "fmt ":
			body := make([]byte, chunkSize)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil, fmt.Errorf("wav: read fmt chunk: %w", err)
			}
			if len(body) < 16 {
				return nil, fmt.Errorf("wav: fmt chunk too short (%d bytes)", len(body))
			}
			audioFormat := binary.LittleEndian.Uint16(body[0:2])
			if audioFormat != 1 {
				return nil, fmt.Errorf("wav: unsupported audio format %d (only PCM=1 supported)", audioFormat)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			sampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			bitsPerSample = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			if bitsPerSample != 16 {
				return nil, fmt.Errorf("wav: unsupported bits per sample %d (only 16 supported)", bitsPerSample)
			}
			if channels != 1 {
				return nil, fmt.Errorf("wav: unsupported channel count %d (only mono supported)", channels)
			}
			body := make([]byte, chunkSize)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil, fmt.Errorf("wav: read data chunk: %w", err)
			}
			samples = make([]int16, len(body)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(body[i*2 : i*2+2]))
			}
		default:
			if _, err := io.CopyN(io.Discard, r, int64(chunkSize)); err != nil {
				return nil, fmt.Errorf("wav: skip chunk %q: %w", chunkID, err)
			}
		}
		if chunkSize%2 == 1 {
			if _, err := io.CopyN(io.Discard, r, 1); err != nil {
				return nil, fmt.Errorf("wav: skip pad byte after chunk %q: %w", chunkID, err)
			}
		}
	}

	if samples == nil {
		return nil, fmt.Errorf("wav: no data chunk found")
	}
	return &PCM{SampleRate: sampleRate, Samples: samples}, nil
}
