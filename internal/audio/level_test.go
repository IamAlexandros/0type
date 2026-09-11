package audio

import (
	"math"
	"testing"
)

func TestRMS(t *testing.T) {
	cases := []struct {
		name string
		buf  []int16
		want float64
	}{
		{"empty", nil, 0},
		{"silence", []int16{0, 0, 0, 0}, 0},
		{"full scale square wave", []int16{32767, -32768, 32767, -32768}, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RMS(c.buf)
			if math.Abs(got-c.want) > 0.001 {
				t.Errorf("RMS(%v) = %v, want %v", c.buf, got, c.want)
			}
		})
	}
}

func TestDBFS(t *testing.T) {
	if got := DBFS(0); got != silenceFloorDB {
		t.Errorf("DBFS(0) = %v, want floor %v", got, silenceFloorDB)
	}
	if got := DBFS(1.0); math.Abs(got-0) > 0.001 {
		t.Errorf("DBFS(1.0) = %v, want 0", got)
	}
	// -6dB is approximately half amplitude.
	if got := DBFS(0.5); math.Abs(got-(-6.02)) > 0.05 {
		t.Errorf("DBFS(0.5) = %v, want ~-6.02", got)
	}
}

func TestMeter(t *testing.T) {
	if got := Meter(0, 10); got != "##########" {
		t.Errorf("Meter(0, 10) = %q, want full bar", got)
	}
	if got := Meter(silenceFloorDB, 10); got != "----------" {
		t.Errorf("Meter(floor, 10) = %q, want empty bar", got)
	}
	// Values outside [-60, 0] must clamp rather than panic or overflow.
	if got := Meter(-1000, 5); got != "-----" {
		t.Errorf("Meter(-1000, 5) = %q, want empty bar", got)
	}
	if got := Meter(1000, 5); got != "#####" {
		t.Errorf("Meter(1000, 5) = %q, want full bar", got)
	}
}
