package stream

import "strings"

// StablePrefixWords returns the number of leading words shared between prev
// and cur (split on whitespace). Used to distinguish "settled" text, which
// is unlikely to change on the next decode pass, from "tentative" text at
// the end of the current partial, which might still be revised as more
// audio context arrives.
func StablePrefixWords(prev, cur string) int {
	a := strings.Fields(prev)
	b := strings.Fields(cur)
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
