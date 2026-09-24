package main

import "sync"

// session is one dictation, from the toggle that starts it to the
// clipboard write that ends it.
//
// It is its own type, rather than fields on app, because a session
// outlives the moment it's stopped: ending one has to wait for the last
// audio to be decoded, and by then the user may already have started the
// next. Shared fields would let the finishing session's late results
// overwrite the new one's; with a value per session, each keeps its own.
type session struct {
	stop chan struct{} // closed to ask the capture goroutine to finish
	done chan struct{} // closed by that goroutine once it has flushed and exited

	skip     chan struct{} // closed if the user presses again instead of waiting
	stopOnce sync.Once
	skipOnce sync.Once

	mu       sync.Mutex
	stopped  bool   // end() has been called
	finished bool   // the text has been delivered
	dictated string // finalized sentences, space-joined
	partial  string // the sentence still being spoken, as last decoded
}

func newSession() *session {
	return &session{stop: make(chan struct{}), done: make(chan struct{}), skip: make(chan struct{})}
}

// end asks the capture goroutine to finish. Safe to call more than once:
// it is reachable both from the toggle and from shutdown.
func (s *session) end() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		close(s.stop)
	})
}

// finishing reports whether the session has been stopped but its text has
// not been delivered yet: the window where the last words are being decoded.
func (s *session) finishing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped && !s.finished
}

func (s *session) markFinished() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

// skipWait tells a finishing session to stop waiting for the last decode
// and deliver what it already has. Safe to call more than once.
func (s *session) skipWait() {
	s.skipOnce.Do(func() { close(s.skip) })
}

// addFinal records a finished sentence and returns the whole session's
// text so far, for display. The in-progress sentence is cleared: it has
// just become this one.
func (s *session) addFinal(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dictated = appendSentence(s.dictated, text)
	s.partial = ""
	return s.dictated
}

// setPartial records the in-progress sentence and returns the whole
// session's text so far, for display.
func (s *session) setPartial(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = text
	return appendSentence(s.dictated, s.partial)
}

// result is everything that should end up on the clipboard: the finished
// sentences plus whatever was still in progress.
//
// Including the in-progress sentence is the whole point. Ending a session
// normally flushes it into a proper final decode first, so by the time
// this is read the partial has already been folded in and cleared -- but
// if that decode fails or takes too long, the last partial is what's left,
// and a slightly stale sentence is far better than losing it. The
// original bug was exactly that: only finished sentences were kept, so
// stopping right after speaking copied nothing at all.
func (s *session) result() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendSentence(s.dictated, s.partial)
}
