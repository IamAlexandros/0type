package audio

import (
	"errors"
	"testing"
	"time"
)

// fakeSource implements Source by replaying a fixed script of reads.
type fakeSource struct {
	chunks [][]int16
	err    error
	idx    int
}

func (f *fakeSource) ReadInt16(buf []int16) (int, error) {
	if f.idx >= len(f.chunks) {
		if f.err != nil {
			return 0, f.err
		}
		return 0, errors.New("fakeSource: script exhausted")
	}
	c := f.chunks[f.idx]
	f.idx++
	n := copy(buf, c)
	return n, nil
}

func (f *fakeSource) Close() error { return nil }

func TestStreamChunks_DeliversInOrder(t *testing.T) {
	src := &fakeSource{chunks: [][]int16{{1, 2}, {3, 4}, {5, 6}}, err: errors.New("done")}
	stop := make(chan struct{})
	defer close(stop)

	out := StreamChunks(src, 2, stop)

	want := [][]int16{{1, 2}, {3, 4}, {5, 6}}
	for i, w := range want {
		select {
		case c := <-out:
			if c.Err != nil {
				t.Fatalf("chunk %d: unexpected error %v", i, c.Err)
			}
			if len(c.Samples) != len(w) || c.Samples[0] != w[0] || c.Samples[1] != w[1] {
				t.Errorf("chunk %d = %v, want %v", i, c.Samples, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("chunk %d: timed out waiting for chunk", i)
		}
	}
}

func TestStreamChunks_ErrorClosesChannel(t *testing.T) {
	wantErr := errors.New("boom")
	src := &fakeSource{chunks: nil, err: wantErr}
	stop := make(chan struct{})
	defer close(stop)

	out := StreamChunks(src, 2, stop)

	select {
	case c := <-out:
		if c.Err != wantErr {
			t.Fatalf("Err = %v, want %v", c.Err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error chunk")
	}

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("channel should be closed after an error chunk")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestStreamChunks_StopEndsProducer(t *testing.T) {
	// A source that always succeeds so the producer would otherwise loop
	// forever; StreamChunks must still stop promptly once stop is closed.
	src := &foreverSource{}
	stop := make(chan struct{})
	out := StreamChunks(src, 2, stop)

	<-out // drain one chunk to know the goroutine is running
	close(stop)

	// Drain until closed (any buffered chunks, then a closed read).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // success: channel closed after stop
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel to close after stop")
		}
	}
}

type foreverSource struct{}

func (foreverSource) ReadInt16(buf []int16) (int, error) {
	for i := range buf {
		buf[i] = 0
	}
	return len(buf), nil
}

func (foreverSource) Close() error { return nil }
