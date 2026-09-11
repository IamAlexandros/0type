package stream

import "testing"

func TestVAD_SpeechThenSilenceTriggersEndOfUtterance(t *testing.T) {
	v := NewVAD(-40, 3)

	if speech, active, eou := v.Update(-10); !speech || !active || eou {
		t.Fatalf("Update(loud) = (%v,%v,%v), want (true,true,false)", speech, active, eou)
	}
	for i := 0; i < 2; i++ {
		if speech, active, eou := v.Update(-60); speech || !active || eou {
			t.Fatalf("Update(silent) call %d = (%v,%v,%v), want (false,true,false) before hangover elapses", i, speech, active, eou)
		}
	}
	if speech, active, eou := v.Update(-60); speech || active || !eou {
		t.Fatalf("Update(silent) at hangover boundary = (%v,%v,%v), want (false,false,true)", speech, active, eou)
	}
}

func TestVAD_SilenceBeforeSpeechNeverTriggers(t *testing.T) {
	v := NewVAD(-40, 2)
	for i := 0; i < 10; i++ {
		if speech, active, eou := v.Update(-60); speech || active || eou {
			t.Fatalf("Update(silence, no prior speech) call %d = (%v,%v,%v), want (false,false,false)", i, speech, active, eou)
		}
	}
}

func TestVAD_BriefDipDoesNotEndUtterance(t *testing.T) {
	// A silence run shorter than the hangover, followed by more speech,
	// must not trigger end-of-utterance -- this is what lets natural
	// pauses between words not fragment a sentence.
	v := NewVAD(-40, 5)
	v.Update(-10) // speech
	v.Update(-60) // 1 silent chunk, under hangover
	if speech, active, eou := v.Update(-10); !speech || !active || eou {
		t.Fatalf("Update(loud again) = (%v,%v,%v), want (true,true,false)", speech, active, eou)
	}
}

func TestVAD_HangoverResetsAfterFiring(t *testing.T) {
	v := NewVAD(-40, 2)
	v.Update(-10) // speech
	v.Update(-60) // silence 1
	if _, _, eou := v.Update(-60); !eou {
		t.Fatal("expected end-of-utterance on 2nd silent chunk")
	}
	// Further silence, with no intervening speech, must not fire again.
	if _, _, eou := v.Update(-60); eou {
		t.Fatal("end-of-utterance fired again without new intervening speech")
	}
}

func TestVAD_SilenceAfterUtteranceIsNeverActive(t *testing.T) {
	// Regression test for a real bug: after an utterance ends, further
	// silence must never be reported active=true, or a caller accumulating
	// audio only while active would grow its buffer without bound forever
	// (this exact scenario, live, pegged CPU and froze the displayed
	// transcript in place).
	v := NewVAD(-40, 2)
	v.Update(-10) // speech
	v.Update(-60) // silence 1
	if _, _, eou := v.Update(-60); !eou {
		t.Fatal("expected end-of-utterance on 2nd silent chunk")
	}
	for i := 0; i < 50; i++ {
		if speech, active, eou := v.Update(-96); speech || active || eou {
			t.Fatalf("Update(silence) call %d after utterance ended = (%v,%v,%v), want (false,false,false)", i, speech, active, eou)
		}
	}
}
