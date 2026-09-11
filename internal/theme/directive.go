package theme

import (
	"fmt"
	"regexp"
	"strings"
)

// Marks are the brand marks a theme may select with the mark directive.
// This list must stay in step with internal/ui's Marks, which is what
// actually draws them; cmd/0type has a test that fails if they drift.
func Marks() []string { return []string{"mic", "pixel", "zero"} }

// DefaultMark is used by any theme that doesn't declare one.
const DefaultMark = "mic"

// markDirective matches `0type-mark: pixel`, which themes put inside a
// CSS comment.
//
// Why a directive and not a CSS property: which mark to draw isn't
// something CSS can express -- there's no property whose value is "a
// shape 0type knows how to paint", and the tricks that would smuggle one
// through (encoding the choice in a font-family or a min-width on a
// dummy widget) are unreadable in the theme file and untestable without a
// live GTK display. A named directive in a comment is ignored by CSS,
// obvious to whoever is editing the theme, and parseable in pure Go, so
// it can be tested like any other input.
var markDirective = regexp.MustCompile(`0type-mark\s*:\s*([A-Za-z0-9_-]+)`)

// parseDirectives extracts 0type's own settings from a theme's CSS.
// An unrecognized mark is an error rather than a silent fallback: a
// misspelled directive that quietly draws the default mark looks exactly
// like a theme that forgot to set one.
func parseDirectives(css []byte) (mark string, err error) {
	mark = DefaultMark
	m := markDirective.FindSubmatch(css)
	if m == nil {
		return mark, nil
	}

	got := strings.ToLower(string(m[1]))
	for _, known := range Marks() {
		if got == known {
			return got, nil
		}
	}
	return "", fmt.Errorf("unknown mark %q (supported: %s)", got, strings.Join(Marks(), ", "))
}
