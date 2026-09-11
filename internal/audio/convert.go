package audio

// Int16ToFloat32 converts int16 PCM samples to float32 samples normalized
// to [-1, 1], the format expected by downstream feature extraction and
// inference code.
func Int16ToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, s := range in {
		out[i] = float32(s) / 32768.0
	}
	return out
}
