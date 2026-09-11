package stream

import "testing"

func TestVAD_SpeechThenSilenceTriggersEndOfUtterance(t *testing.T) {
	v := NewVAD(-40, 3)

	if speech, eou := v.Update(-10); !speech || eou {
		t.Fatalf("Update(loud) = (%v,%v), want (true,false)", speech, eou)
	}
	for i := 0; i < 2; i++ {
		if speech, eou := v.Update(-60); speech || eou {
			t.Fatalf("Update(silent) call %d = (%v,%v), want (false,false) before hangover elapses", i, speech, eou)
		}
	}
	if speech, eou := v.Update(-60); speech || !eou {
		t.Fatalf("Update(silent) at hangover boundary = (%v,%v), want (false,true)", speech, eou)
	}
}

func TestVAD_SilenceBeforeSpeechNeverTriggers(t *testing.T) {
	v := NewVAD(-40, 2)
	for i := 0; i < 10; i++ {
		if speech, eou := v.Update(-60); speech || eou {
			t.Fatalf("Update(silence, no prior speech) call %d = (%v,%v), want (false,false)", i, speech, eou)
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
	if speech, eou := v.Update(-10); !speech || eou {
		t.Fatalf("Update(loud again) = (%v,%v), want (true,false)", speech, eou)
	}
}

func TestVAD_HangoverResetsAfterFiring(t *testing.T) {
	v := NewVAD(-40, 2)
	v.Update(-10) // speech
	v.Update(-60) // silence 1
	if _, eou := v.Update(-60); !eou {
		t.Fatal("expected end-of-utterance on 2nd silent chunk")
	}
	// Further silence, with no intervening speech, must not fire again.
	if _, eou := v.Update(-60); eou {
		t.Fatal("end-of-utterance fired again without new intervening speech")
	}
}
