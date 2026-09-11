package wav

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// encodeWAV builds a minimal canonical mono 16-bit PCM WAV file in memory,
// for use as a test fixture.
func encodeWAV(sampleRate int, samples []int16) []byte {
	var data bytes.Buffer
	for _, s := range samples {
		binary.Write(&data, binary.LittleEndian, s)
	}

	var fmtChunk bytes.Buffer
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(1))          // PCM
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(1))          // mono
	binary.Write(&fmtChunk, binary.LittleEndian, uint32(sampleRate)) // sample rate
	byteRate := sampleRate * 1 * 16 / 8
	binary.Write(&fmtChunk, binary.LittleEndian, uint32(byteRate))
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(2))  // block align
	binary.Write(&fmtChunk, binary.LittleEndian, uint16(16)) // bits per sample

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	riffSize := uint32(4 + (8 + fmtChunk.Len()) + (8 + data.Len()))
	binary.Write(&buf, binary.LittleEndian, riffSize)
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(fmtChunk.Len()))
	buf.Write(fmtChunk.Bytes())

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(data.Len()))
	buf.Write(data.Bytes())

	return buf.Bytes()
}

func TestRead(t *testing.T) {
	samples := []int16{0, 1000, -1000, 32767, -32768}
	raw := encodeWAV(16000, samples)

	pcm, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if pcm.SampleRate != 16000 {
		t.Errorf("SampleRate = %d, want 16000", pcm.SampleRate)
	}
	if !reflect.DeepEqual(pcm.Samples, samples) {
		t.Errorf("Samples = %v, want %v", pcm.Samples, samples)
	}
}

func TestRead_RejectsBadMagic(t *testing.T) {
	raw := encodeWAV(16000, []int16{0})
	raw[0] = 'X' // corrupt "RIFF" -> "XIFF"
	if _, err := Read(bytes.NewReader(raw)); err == nil {
		t.Fatal("Read: want error for bad RIFF magic, got nil")
	}
}

func TestRead_NoDataChunk(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(4))
	buf.WriteString("WAVE")
	if _, err := Read(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("Read: want error when no data chunk is present, got nil")
	}
}
