// Package asr runs the Parakeet TDT speech recognition pipeline: a log-mel
// feature extractor, a Conformer encoder, and a combined decoder+joint
// network, all via ONNX Runtime, tied together by TDT greedy decoding.
package asr

import "fmt"

const (
	blankID           = 1024
	vocabSize         = 1025
	numDurations      = 5
	maxSymbolsPerStep = 10
)

// durationValues maps a duration bin index (as predicted by the joint
// network) to the number of encoder frames to advance.
var durationValues = [numDurations]int{0, 1, 2, 3, 4}

// jointStepper runs one decoder+joint network step for a single encoder
// frame and predictor label, returning the predicted token id, the
// predicted duration bin index, and the predictor state that would result
// from having fed label into the prediction network. Committing that state
// is greedyDecode's decision: it is only kept when tokenID is non-blank,
// since the prediction network is only ever fed real (emitted) tokens.
type jointStepper interface {
	step(frameIdx int, label int32, state1, state2 []float32) (tokenID int32, durationIdx int, newState1, newState2 []float32, err error)
}

// greedyDecode runs TDT greedy decoding over numFrames encoder frames using
// stepper, returning the emitted token ids in order. stateSize is the
// flattened length of the predictor LSTM state vectors passed to stepper.
//
// Algorithm (validated against a reference Python implementation run
// against the real Parakeet TDT ONNX model, see docs/SETUP.md): at each
// encoder frame, repeatedly query the joint network for a token and a
// duration. A non-blank token is emitted and advances the predictor state;
// a blank does not. The loop advances to frame t+duration (at least 1)
// once a duration is non-zero, a blank was predicted, or the per-frame
// symbol cap is hit — the latter guards against a pathological duration=0
// non-blank stream looping forever on a single frame.
func greedyDecode(numFrames int, stateSize int, stepper jointStepper) ([]int32, error) {
	state1 := make([]float32, stateSize)
	state2 := make([]float32, stateSize)
	label := int32(blankID)
	var tokens []int32

	t := 0
	for t < numFrames {
		symbolsThisFrame := 0
		for {
			tokenID, durIdx, newState1, newState2, err := stepper.step(t, label, state1, state2)
			if err != nil {
				return nil, err
			}
			if durIdx < 0 || durIdx >= numDurations {
				return nil, fmt.Errorf("asr: duration index %d out of range [0,%d)", durIdx, numDurations)
			}
			duration := durationValues[durIdx]

			if tokenID != blankID {
				tokens = append(tokens, tokenID)
				label = tokenID
				state1, state2 = newState1, newState2
				symbolsThisFrame++
			}

			if duration > 0 || tokenID == blankID || symbolsThisFrame >= maxSymbolsPerStep {
				if duration < 1 {
					duration = 1
				}
				t += duration
				break
			}
		}
	}
	return tokens, nil
}

func argmaxFloat32(v []float32) int {
	best := 0
	for i := 1; i < len(v); i++ {
		if v[i] > v[best] {
			best = i
		}
	}
	return best
}
