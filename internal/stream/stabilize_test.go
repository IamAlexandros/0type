package stream

import "testing"

func TestStablePrefixWords(t *testing.T) {
	cases := []struct {
		prev, cur string
		want      int
	}{
		{"", "hello", 0},
		{"hello", "", 0},
		{"the quick brown", "the quick brown fox", 3},
		{"the quick brown", "the quick red fox", 2},
		{"hello world", "hello world", 2},
		{"a b c", "x y z", 0},
	}
	for _, c := range cases {
		if got := StablePrefixWords(c.prev, c.cur); got != c.want {
			t.Errorf("StablePrefixWords(%q, %q) = %d, want %d", c.prev, c.cur, got, c.want)
		}
	}
}
