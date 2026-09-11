package asr

import (
	"fmt"
	"os"
	"path/filepath"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	lstmLayers = 2
	lstmHidden = 640
)

// candidateLibraryPaths are checked, in order, for the ONNX Runtime shared
// library if InitEnvironment isn't given one explicitly. These cover the
// Fedora package path used during development plus a couple of common
// alternate locations; if none exist, ONNX Runtime's own default dlopen
// search (LD_LIBRARY_PATH, ldconfig cache) is used instead.
//
// Paths relative to the binary itself come first (see
// bundledLibraryPaths): a release tarball ships its own copy of the
// library, and a bundle that silently ran against whatever different
// version the host happened to have installed would be a bundle in name
// only.
var candidateLibraryPaths = []string{
	"/usr/lib64/libonnxruntime.so",
	"/usr/lib/libonnxruntime.so",
	"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
	"/usr/local/lib/libonnxruntime.so",
}

// bundledLibraryPaths returns the locations a release tarball may have
// put libonnxruntime, relative to the running binary: lib/ beside it, and
// ../lib for the usual bin/ + lib/ layout.
func bundledLibraryPaths() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved // an installer may have symlinked this onto $PATH
	}
	dir := filepath.Dir(exe)
	return []string{
		filepath.Join(dir, "lib", "libonnxruntime.so"),
		filepath.Join(filepath.Dir(dir), "lib", "libonnxruntime.so"),
	}
}

// InitEnvironment configures and initializes the ONNX Runtime environment.
// It must be called once before loading any Model, and DestroyEnvironment
// should be called on shutdown. If libPath is empty, the first existing
// path in candidateLibraryPaths is used.
func InitEnvironment(libPath string) error {
	if libPath == "" {
		libPath = findLibrary()
	}
	if libPath != "" {
		ort.SetSharedLibraryPath(libPath)
	}
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("asr: initialize onnxruntime (tried library %q): %w", libPath, err)
	}
	return nil
}

// DestroyEnvironment releases the ONNX Runtime environment initialized by
// InitEnvironment.
func DestroyEnvironment() error {
	return ort.DestroyEnvironment()
}

func findLibrary() string {
	for _, p := range append(bundledLibraryPaths(), candidateLibraryPaths...) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Model wraps the three ONNX graphs that make up the Parakeet TDT pipeline:
// a log-mel feature extractor, a Conformer encoder, and a combined
// decoder+joint network.
type Model struct {
	mel   *ort.DynamicAdvancedSession
	enc   *ort.DynamicAdvancedSession
	dj    *ort.DynamicAdvancedSession
	vocab []string
}

// LoadModel opens the ONNX sessions and vocabulary from dir, as produced by
// internal/modelstore.
func LoadModel(dir string) (*Model, error) {
	vocab, err := loadVocab(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		return nil, fmt.Errorf("asr: load vocab: %w", err)
	}

	mel, err := ort.NewDynamicAdvancedSession(
		filepath.Join(dir, "nemo128.onnx"),
		[]string{"waveforms", "waveforms_lens"},
		[]string{"features", "features_lens"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("asr: load feature extractor: %w", err)
	}

	enc, err := ort.NewDynamicAdvancedSession(
		filepath.Join(dir, "encoder-model.int8.onnx"),
		[]string{"audio_signal", "length"},
		[]string{"outputs", "encoded_lengths"},
		nil,
	)
	if err != nil {
		mel.Destroy()
		return nil, fmt.Errorf("asr: load encoder: %w", err)
	}

	dj, err := ort.NewDynamicAdvancedSession(
		filepath.Join(dir, "decoder_joint-model.int8.onnx"),
		[]string{"encoder_outputs", "targets", "target_length", "input_states_1", "input_states_2"},
		[]string{"outputs", "prednet_lengths", "output_states_1", "output_states_2"},
		nil,
	)
	if err != nil {
		mel.Destroy()
		enc.Destroy()
		return nil, fmt.Errorf("asr: load decoder/joint: %w", err)
	}

	return &Model{mel: mel, enc: enc, dj: dj, vocab: vocab}, nil
}

// Close releases all ONNX Runtime sessions held by the model.
func (m *Model) Close() {
	m.mel.Destroy()
	m.enc.Destroy()
	m.dj.Destroy()
}

// Transcribe runs the full pipeline (features -> encoder -> greedy decode)
// over a mono 16kHz float32 waveform normalized to [-1, 1], returning the
// decoded text.
func (m *Model) Transcribe(waveform []float32) (string, error) {
	if len(waveform) == 0 {
		return "", nil
	}

	wIn, err := ort.NewTensor(ort.NewShape(1, int64(len(waveform))), waveform)
	if err != nil {
		return "", fmt.Errorf("asr: waveform tensor: %w", err)
	}
	defer wIn.Destroy()
	lenIn, err := ort.NewTensor(ort.NewShape(1), []int64{int64(len(waveform))})
	if err != nil {
		return "", fmt.Errorf("asr: waveform length tensor: %w", err)
	}
	defer lenIn.Destroy()

	melOut := make([]ort.Value, 2)
	if err := m.mel.Run([]ort.Value{wIn, lenIn}, melOut); err != nil {
		return "", fmt.Errorf("asr: feature extraction: %w", err)
	}
	defer melOut[0].Destroy()
	defer melOut[1].Destroy()

	encOut := make([]ort.Value, 2)
	if err := m.enc.Run([]ort.Value{melOut[0], melOut[1]}, encOut); err != nil {
		return "", fmt.Errorf("asr: encoder: %w", err)
	}
	defer encOut[0].Destroy()
	defer encOut[1].Destroy()

	encTensor, ok := encOut[0].(*ort.Tensor[float32])
	if !ok {
		return "", fmt.Errorf("asr: encoder: unexpected output tensor type %T", encOut[0])
	}
	encLensTensor, ok := encOut[1].(*ort.Tensor[int64])
	if !ok {
		return "", fmt.Errorf("asr: encoder: unexpected length tensor type %T", encOut[1])
	}

	shape := encTensor.GetShape() // [1, encoderDim, T]
	if len(shape) != 3 {
		return "", fmt.Errorf("asr: encoder: unexpected output rank %d (shape %v)", len(shape), shape)
	}
	channels := int(shape[1])
	numFrames := int(encLensTensor.GetData()[0])

	stepper := &onnxJointStepper{dj: m.dj, encData: encTensor.GetData(), channels: channels, numFrames: numFrames}
	ids, err := greedyDecode(numFrames, lstmLayers*lstmHidden, stepper)
	if err != nil {
		return "", fmt.Errorf("asr: decode: %w", err)
	}
	return decodeTokens(ids, m.vocab), nil
}

// onnxJointStepper implements jointStepper against the real decoder_joint
// ONNX session, operating on the pre-computed encoder output.
type onnxJointStepper struct {
	dj        *ort.DynamicAdvancedSession
	encData   []float32 // flat [1, channels, numFrames], row-major
	channels  int
	numFrames int
}

func (s *onnxJointStepper) step(frameIdx int, label int32, state1, state2 []float32) (int32, int, []float32, []float32, error) {
	frame := make([]float32, s.channels)
	for c := 0; c < s.channels; c++ {
		frame[c] = s.encData[c*s.numFrames+frameIdx]
	}

	encFrame, err := ort.NewTensor(ort.NewShape(1, int64(s.channels), 1), frame)
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: encoder frame tensor: %w", err)
	}
	defer encFrame.Destroy()

	targets, err := ort.NewTensor(ort.NewShape(1, 1), []int32{label})
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: targets tensor: %w", err)
	}
	defer targets.Destroy()

	targetLen, err := ort.NewTensor(ort.NewShape(1), []int32{1})
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: target length tensor: %w", err)
	}
	defer targetLen.Destroy()

	st1, err := ort.NewTensor(ort.NewShape(lstmLayers, 1, lstmHidden), append([]float32(nil), state1...))
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: state1 tensor: %w", err)
	}
	defer st1.Destroy()
	st2, err := ort.NewTensor(ort.NewShape(lstmLayers, 1, lstmHidden), append([]float32(nil), state2...))
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: state2 tensor: %w", err)
	}
	defer st2.Destroy()

	outputs := make([]ort.Value, 4)
	if err := s.dj.Run([]ort.Value{encFrame, targets, targetLen, st1, st2}, outputs); err != nil {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: run: %w", err)
	}
	defer outputs[0].Destroy()
	defer outputs[1].Destroy()
	defer outputs[2].Destroy()
	defer outputs[3].Destroy()

	logitsTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: unexpected logits tensor type %T", outputs[0])
	}
	newState1Tensor, ok := outputs[2].(*ort.Tensor[float32])
	if !ok {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: unexpected state1 tensor type %T", outputs[2])
	}
	newState2Tensor, ok := outputs[3].(*ort.Tensor[float32])
	if !ok {
		return 0, 0, nil, nil, fmt.Errorf("asr: joint: unexpected state2 tensor type %T", outputs[3])
	}

	logits := logitsTensor.GetData() // [vocabSize + numDurations]
	tokenID := int32(argmaxFloat32(logits[:vocabSize]))
	durIdx := argmaxFloat32(logits[vocabSize : vocabSize+numDurations])

	newState1 := append([]float32(nil), newState1Tensor.GetData()...)
	newState2 := append([]float32(nil), newState2Tensor.GetData()...)

	return tokenID, durIdx, newState1, newState2, nil
}
