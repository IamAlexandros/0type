package main

import "testing"

func TestAppendSentence(t *testing.T) {
	cases := []struct {
		dictated, sentence, want string
	}{
		{"", "Hello.", "Hello."},
		{"Hello.", "How are you?", "Hello. How are you?"},
		{"One. Two.", "Three.", "One. Two. Three."},
	}
	for _, c := range cases {
		if got := appendSentence(c.dictated, c.sentence); got != c.want {
			t.Errorf("appendSentence(%q, %q) = %q, want %q", c.dictated, c.sentence, got, c.want)
		}
	}
}

// An empty sentence adds nothing. Without this, joining "Hello." with an
// empty in-progress sentence produced "Hello. " -- a stray trailing space
// on every clipboard write where nothing was mid-sentence.
func TestAppendSentenceEmptySentence(t *testing.T) {
	if got := appendSentence("Hello.", ""); got != "Hello." {
		t.Errorf("appendSentence(%q, \"\") = %q, want %q", "Hello.", got, "Hello.")
	}
	if got := appendSentence("", ""); got != "" {
		t.Errorf("appendSentence(\"\", \"\") = %q, want empty", got)
	}
}
