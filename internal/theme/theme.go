// Package theme resolves a theme name to the CSS that styles the overlay.
//
// Themes are plain GTK CSS. The ones 0type ships with are embedded in the
// binary rather than read from a themes/ directory next to it, so the
// overlay looks right no matter where the binary is run from -- an
// earlier version loaded "themes/default.css" relative to the working
// directory and was silently unstyled unless launched from the project
// checkout, which is exactly the kind of thing a GNOME keyboard shortcut
// does.
//
// A user theme is a .css file dropped in the user theme directory (see
// UserDir); one whose name matches a built-in shadows it, so a built-in
// can be tweaked by copying it out and editing, without patching 0type.
package theme

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed themes/*.css
var builtinFS embed.FS

const (
	// Default is the theme used when the config file names none.
	Default = "default"

	builtinDir = "themes"
	ext        = ".css"
)

// Theme is a resolved, ready-to-apply stylesheet.
type Theme struct {
	// Name is the name it was requested by ("default"), or the file's base
	// name when it was loaded from an explicit path.
	Name string
	// CSS is the stylesheet itself.
	CSS []byte
	// Mark names the brand mark the overlay should draw (see
	// parseDirectives). Always set; DefaultMark if the theme declares none.
	Mark string
	// Origin describes where it came from, for `0type themes` output and
	// for error messages that would otherwise leave the user guessing
	// which of several same-named files actually took effect.
	Origin string
}

// ErrNotFound is returned by Load when no theme by that name exists.
var ErrNotFound = errors.New("theme not found")

// Load resolves a theme by name, checking, in order: an explicit path (if
// name looks like one), the user theme directory, then the built-in
// themes. Returns ErrNotFound (wrapped) if none match.
func Load(name string) (*Theme, error) {
	if name == "" {
		name = Default
	}

	if looksLikePath(name) {
		css, err := os.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("theme: read %s: %w", name, err)
		}
		return newTheme(strings.TrimSuffix(filepath.Base(name), ext), css, name)
	}

	if err := validName(name); err != nil {
		return nil, err
	}

	if dir, err := UserDir(); err == nil {
		path := filepath.Join(dir, name+ext)
		css, err := os.ReadFile(path)
		if err == nil {
			return newTheme(name, css, path)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("theme: read %s: %w", path, err)
		}
	}

	css, err := builtinFS.ReadFile(builtinDir + "/" + name + ext)
	if err == nil {
		return newTheme(name, css, "built-in")
	}

	return nil, fmt.Errorf("theme %q: %w (try one of: %s)", name, ErrNotFound, strings.Join(builtinNames(), ", "))
}

// newTheme assembles a Theme from stylesheet bytes, reading 0type's own
// directives out of it.
func newTheme(name string, css []byte, origin string) (*Theme, error) {
	mark, err := parseDirectives(css)
	if err != nil {
		return nil, fmt.Errorf("theme %s (%s): %w", name, origin, err)
	}
	return &Theme{Name: name, CSS: css, Mark: mark, Origin: origin}, nil
}

// Entry is one theme available to load, as reported by List.
type Entry struct {
	Name   string
	Origin string
	// Overrides is true for a user theme that shadows a built-in of the
	// same name -- worth showing, because it explains why a built-in
	// doesn't look the way its documentation says it should.
	Overrides bool
}

// List reports every theme that can be loaded by name: the built-ins,
// plus any .css file in the user theme directory, sorted by name. A user
// theme replaces the built-in entry of the same name rather than
// appearing twice, matching what Load would actually resolve.
func List() []Entry {
	byName := map[string]Entry{}
	for _, name := range builtinNames() {
		byName[name] = Entry{Name: name, Origin: "built-in"}
	}

	if dir, err := UserDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		for _, path := range matches {
			name := strings.TrimSuffix(filepath.Base(path), ext)
			_, isBuiltin := byName[name]
			byName[name] = Entry{Name: name, Origin: path, Overrides: isBuiltin}
		}
	}

	out := make([]Entry, 0, len(byName))
	for _, e := range byName {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UserDir is where user-written themes live:
// $XDG_CONFIG_HOME/0type/themes (falling back to ~/.config/0type/themes).
func UserDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("theme: locate config dir: %w", err)
	}
	return filepath.Join(base, "0type", "themes"), nil
}

func builtinNames() []string {
	entries, err := fs.ReadDir(builtinFS, builtinDir)
	if err != nil {
		return nil // can't happen: the directory is embedded at build time
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ext))
	}
	sort.Strings(names)
	return names
}

// looksLikePath distinguishes `0type --theme ./my.css` from
// `0type --theme mydark`. A bare name is never treated as a path, so a
// stray file in the working directory can't shadow a real theme.
func looksLikePath(name string) bool {
	return strings.ContainsRune(name, filepath.Separator) || strings.HasSuffix(name, ext)
}

// validName rejects names that would escape the theme directory or hit
// the filesystem in surprising ways once joined onto a path.
func validName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("theme: invalid name %q", name)
	}
	return nil
}
