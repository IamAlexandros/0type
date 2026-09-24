package main

import "testing"

// The reported bug, at the level of the data it came from: someone talks
// and stops before a sentence is finalized. Only a partial exists. The
// result must still contain it.
func TestSessionResultKeepsTheUnfinishedSentence(t *testing.T) {
	s := newSession()
	s.setPartial("the quick brown fox")

	if got := s.result(); got != "the quick brown fox" {
		t.Fatalf("result() = %q, want the in-progress sentence (it used to be empty)", got)
	}
}

func TestSessionResultCombinesFinishedAndInProgress(t *testing.T) {
	s := newSession()
	s.addFinal("First sentence.")
	s.setPartial("and the second")

	if got, want := s.result(), "First sentence. and the second"; got != want {
		t.Errorf("result() = %q, want %q", got, want)
	}
}

// Once a sentence is finalized, its in-progress copy must be gone -- the
// final replaces it. Otherwise the clipboard would say everything twice.
func TestSessionFinalReplacesItsPartial(t *testing.T) {
	s := newSession()
	s.setPartial("hello wor")
	display := s.addFinal("Hello world.")

	if display != "Hello world." {
		t.Errorf("display after addFinal = %q", display)
	}
	if got := s.result(); got != "Hello world." {
		t.Errorf("result() = %q, want the final only, not final + partial", got)
	}
}

func TestSessionDisplayGrowsAcrossSentences(t *testing.T) {
	s := newSession()
	if got := s.addFinal("One."); got != "One." {
		t.Errorf("after first final: %q", got)
	}
	if got := s.setPartial("two"); got != "One. two" {
		t.Errorf("partial display: %q", got)
	}
	if got := s.addFinal("Two."); got != "One. Two." {
		t.Errorf("after second final: %q", got)
	}
}

func TestSessionEmptyResultIsEmpty(t *testing.T) {
	if got := newSession().result(); got != "" {
		t.Errorf("result() of an untouched session = %q, want empty", got)
	}
}

// end is reachable from both the toggle and shutdown; closing a channel
// twice panics.
func TestSessionEndIsIdempotent(t *testing.T) {
	s := newSession()
	s.end()
	s.end() // must not panic

	select {
	case <-s.stop:
	default:
		t.Error("stop channel not closed after end()")
	}
}

// "Finishing" is the window in which pressing the key again means "skip the
// wait" rather than "start a new session". It must be true from the moment
// the session is stopped until its text has been delivered -- and false
// before and after, or the key would stop starting sessions.
func TestSessionFinishingWindow(t *testing.T) {
	s := newSession()
	if s.finishing() {
		t.Error("a running session reports finishing")
	}
	s.end()
	if !s.finishing() {
		t.Error("a stopped session that hasn't delivered yet should report finishing")
	}
	s.markFinished()
	if s.finishing() {
		t.Error("a delivered session still reports finishing")
	}
}

func TestSessionSkipWaitIsIdempotent(t *testing.T) {
	s := newSession()
	s.skipWait()
	s.skipWait() // must not panic

	select {
	case <-s.skip:
	default:
		t.Error("skip channel not closed after skipWait()")
	}
}
