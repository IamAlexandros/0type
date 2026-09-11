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
	// Art is a sprite the theme draws itself, one string per row, '#' for
	// a filled cell. Empty means use Mark instead. Three named marks were
	// never going to cover this: the right icon for a Tamagotchi is a
	// creature, for an Xbox a jewel, and no fixed enum guesses that.
	Art []string
}

// MaxArt is the largest sprite a theme may draw. Must match
// internal/ui.MaxArtSize, which is what actually renders it.
const MaxArt = 16

var (
	markDirective   = regexp.MustCompile(`0type-mark\s*:\s*([A-Za-z0-9_-]+)`)
	artDirective    = regexp.MustCompile(`0type-art\s*:`)
	artRow          = regexp.MustCompile(`^[.#]+$`)
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

	art, err := parseArt(css)
	if err != nil {
		return Directives{}, err
	}
	d.Art = art

	return d, nil
}

// parseArt reads the rows following a `0type-art:` directive. Each row is
// the run of '.' and '#' on its own line; the block ends at the first
// line that isn't one, so the closing `*/` terminates it naturally:
//
//	/* 0type-art:
//	 * ..###..
//	 * .#####.
//	 */
//
// A malformed block is an error rather than a partial sprite: ragged rows
// would silently draw something other than what the author laid out, and
// the whole point of this directive is that what you type is what you
// see.
func parseArt(css []byte) ([]string, error) {
	loc := artDirective.FindIndex(css)
	if loc == nil {
		return nil, nil
	}

	var rows []string
	for _, line := range strings.Split(string(css[loc[1]:]), "\n")[1:] {
		// Strip the comment furniture a CSS block comment puts at the
		// start of each line.
		cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if !artRow.MatchString(cleaned) {
			break
		}
		rows = append(rows, cleaned)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("0type-art: declared but no rows of '.' and '#' follow it")
	}
	if len(rows) > MaxArt {
		return nil, fmt.Errorf("0type-art: %d rows, maximum is %d", len(rows), MaxArt)
	}
	width := len(rows[0])
	if width > MaxArt {
		return nil, fmt.Errorf("0type-art: rows are %d cells wide, maximum is %d", width, MaxArt)
	}
	for i, row := range rows {
		if len(row) != width {
			return nil, fmt.Errorf("0type-art: row %d is %d cells wide, but row 1 is %d -- every row must be the same width", i+1, len(row), width)
		}
	}
	return rows, nil
}
