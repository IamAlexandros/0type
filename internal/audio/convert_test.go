package audio

import "testing"

func TestInt16ToFloat32(t *testing.T) {
	in := []int16{0, 32767, -32768}
	out := Int16ToFloat32(in)

	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	if out[0] != 0 {
		t.Errorf("out[0] = %v, want 0", out[0])
	}
	if out[1] < 0.999 || out[1] > 1.0 {
		t.Errorf("out[1] = %v, want ~1.0", out[1])
	}
	if out[2] != -1.0 {
		t.Errorf("out[2] = %v, want -1.0", out[2])
	}
}
