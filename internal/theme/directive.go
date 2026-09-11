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

// Defaults for the settings a theme may override.
const (
	DefaultMark   = "mic"
	DefaultIdle   = "Listening…"
	DefaultCopied = "Copied to clipboard"
)

// Directives are 0type's own per-theme settings, written as comments in
// the stylesheet.
//
// Why a directive and not CSS: none of these are things CSS can express.
// There's no property whose value is "a shape 0type knows how to paint",
// and the overlay's wording isn't a document it can style -- the strings
// are drawn with Cairo. The tricks that would smuggle them through
// (encoding a choice in a font-family, or a min-width on a dummy widget)
// are unreadable in the theme file and untestable without a live GTK
// display. A named directive in a comment is ignored by CSS, obvious to
// whoever is editing the theme, and parseable in pure Go, so it can be
// tested like any other input.
//
// The text ones matter more than they look: a Game Boy theme that says
// "Listening…" in a system sans-serif isn't a Game Boy theme. Wording is
// part of a pastiche, not decoration on top of one.
type Directives struct {
	Mark   string
	Idle   string // idle placeholder, e.g. "READY"
	Copied string // end-of-session confirmation
}

var (
	markDirective   = regexp.MustCompile(`0type-mark\s*:\s*([A-Za-z0-9_-]+)`)
	idleDirective   = regexp.MustCompile(`0type-idle\s*:([^\n*]*)`)
	copiedDirective = regexp.MustCompile(`0type-copied\s*:([^\n*]*)`)
)

// parseDirectives extracts 0type's own settings from a theme's CSS.
// An unrecognized mark is an error rather than a silent fallback: a
// misspelled directive that quietly draws the default mark looks exactly
// like a theme that forgot to set one.
func parseDirectives(css []byte) (Directives, error) {
	d := Directives{Mark: DefaultMark, Idle: DefaultIdle, Copied: DefaultCopied}

	if m := markDirective.FindSubmatch(css); m != nil {
		got := strings.ToLower(string(m[1]))
		var known bool
		for _, name := range Marks() {
			if got == name {
				known = true
			}
		}
		if !known {
			return Directives{}, fmt.Errorf("unknown mark %q (supported: %s)", got, strings.Join(Marks(), ", "))
		}
		d.Mark = got
	}

	if m := idleDirective.FindSubmatch(css); m != nil {
		if text := strings.TrimSpace(string(m[1])); text != "" {
			d.Idle = text
		}
	}
	if m := copiedDirective.FindSubmatch(css); m != nil {
		if text := strings.TrimSpace(string(m[1])); text != "" {
			d.Copied = text
		}
	}

	return d, nil
}
