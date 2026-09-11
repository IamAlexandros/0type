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
