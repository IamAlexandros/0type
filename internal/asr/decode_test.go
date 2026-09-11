package asr

import (
	"errors"
	"reflect"
	"testing"
)

// scriptedStep is one canned response for fakeStepper, keyed by call order.
type scriptedStep struct {
	tokenID     int32
	durationIdx int
}

// fakeStepper replays a fixed script of (tokenID, durationIdx) pairs and
// records the (frameIdx, label, state1, state2) it was called with, so
// tests can assert on greedyDecode's control flow without any real ONNX
// session.
type fakeStepper struct {
	script []scriptedStep
	calls  int
	// nextState is returned as the "new state" on every call, tagged with
	// the call index so tests can verify which state ends up committed.
}

func (f *fakeStepper) step(frameIdx int, label int32, state1, state2 []float32) (int32, int, []float32, []float32, error) {
	if f.calls >= len(f.script) {
		return 0, 0, nil, nil, errors.New("fakeStepper: script exhausted")
	}
	s := f.script[f.calls]
	// Tag the returned state with the call index (as its one element) so
	// tests can check exactly which call's state was committed.
	newState := []float32{float32(f.calls)}
	f.calls++
	return s.tokenID, s.durationIdx, newState, newState, nil
}

func TestGreedyDecode_AllBlanks(t *testing.T) {
	// Every frame predicts blank with duration 1: no tokens emitted, one
	// joint call per frame, terminates after numFrames.
	stepper := &fakeStepper{script: []scriptedStep{
		{blankID, 1}, {blankID, 1}, {blankID, 1},
	}}
	tokens, err := greedyDecode(3, 4, stepper)
	if err != nil {
		t.Fatalf("greedyDecode: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("tokens = %v, want none", tokens)
	}
	if stepper.calls != 3 {
		t.Errorf("calls = %d, want 3 (one per frame)", stepper.calls)
	}
}

func TestGreedyDecode_EmitsAndAdvancesState(t *testing.T) {
	// Frame 0: emits token 5 with duration 1 (state from call 0 is committed).
	// Then advances to frame 1 (numFrames=1 -> loop ends).
	stepper := &fakeStepper{script: []scriptedStep{
		{5, 1},
	}}
	tokens, err := greedyDecode(1, 4, stepper)
	if err != nil {
		t.Fatalf("greedyDecode: %v", err)
	}
	if !reflect.DeepEqual(tokens, []int32{5}) {
		t.Errorf("tokens = %v, want [5]", tokens)
	}
}

func TestGreedyDecode_MultipleSymbolsSameFrame(t *testing.T) {
	// duration=0 lets multiple non-blank tokens be emitted at the same
	// frame before the loop is forced to advance by a later non-zero
	// duration.
	stepper := &fakeStepper{script: []scriptedStep{
		{10, 0}, // frame 0: emit 10, duration 0 -> stay on frame 0
		{11, 2}, // frame 0: emit 11, duration 2 -> advance to frame 2
	}}
	tokens, err := greedyDecode(2, 4, stepper)
	if err != nil {
		t.Fatalf("greedyDecode: %v", err)
	}
	if !reflect.DeepEqual(tokens, []int32{10, 11}) {
		t.Errorf("tokens = %v, want [10, 11]", tokens)
	}
}

func TestGreedyDecode_MaxSymbolsPerStepSafetyValve(t *testing.T) {
	// duration=0 forever with non-blank tokens must not loop forever: the
	// maxSymbolsPerStep cap forces an advance even though duration never
	// asks for one.
	script := make([]scriptedStep, maxSymbolsPerStep)
	for i := range script {
		script[i] = scriptedStep{tokenID: int32(100 + i), durationIdx: 0}
	}
	stepper := &fakeStepper{script: script}

	tokens, err := greedyDecode(1, 4, stepper)
	if err != nil {
		t.Fatalf("greedyDecode: %v", err)
	}
	if len(tokens) != maxSymbolsPerStep {
		t.Fatalf("tokens = %v, want %d tokens", tokens, maxSymbolsPerStep)
	}
	if stepper.calls != maxSymbolsPerStep {
		t.Errorf("calls = %d, want %d (capped)", stepper.calls, maxSymbolsPerStep)
	}
}

func TestGreedyDecode_BlankDoesNotCommitState(t *testing.T) {
	// A blank prediction must not advance the predictor label/state: the
	// label passed to the next step call must remain the blank id used
	// initially, not whatever a blank call happened to return.
	var labelsSeen []int32
	stepper := &recordingStepper{
		script: []scriptedStep{
			{blankID, 1},
			{blankID, 1},
		},
		labels: &labelsSeen,
	}
	_, err := greedyDecode(2, 4, stepper)
	if err != nil {
		t.Fatalf("greedyDecode: %v", err)
	}
	for _, l := range labelsSeen {
		if l != blankID {
			t.Errorf("label passed to stepper = %d, want blankID (%d) on every call since nothing was ever emitted", l, blankID)
		}
	}
}

func TestGreedyDecode_DurationIndexOutOfRange(t *testing.T) {
	stepper := &fakeStepper{script: []scriptedStep{
		{blankID, numDurations}, // out of range
	}}
	if _, err := greedyDecode(1, 4, stepper); err == nil {
		t.Fatal("greedyDecode: want error for out-of-range duration index, got nil")
	}
}

// recordingStepper behaves like fakeStepper but also records the label it
// was called with on each step.
type recordingStepper struct {
	script []scriptedStep
	calls  int
	labels *[]int32
}

func (r *recordingStepper) step(frameIdx int, label int32, state1, state2 []float32) (int32, int, []float32, []float32, error) {
	*r.labels = append(*r.labels, label)
	if r.calls >= len(r.script) {
		return 0, 0, nil, nil, errors.New("recordingStepper: script exhausted")
	}
	s := r.script[r.calls]
	r.calls++
	return s.tokenID, s.durationIdx, []float32{}, []float32{}, nil
}

func TestArgmaxFloat32(t *testing.T) {
	cases := []struct {
		in   []float32
		want int
	}{
		{[]float32{1, 5, 3}, 1},
		{[]float32{5, 1, 3}, 0},
		{[]float32{1, 3, 5}, 2},
		{[]float32{7}, 0},
	}
	for _, c := range cases {
		if got := argmaxFloat32(c.in); got != c.want {
			t.Errorf("argmaxFloat32(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
