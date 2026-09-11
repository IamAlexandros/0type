// Package config loads 0type's optional config file.
//
// The file is optional by design: 0type must work with nothing
// configured at all, so a missing file is not an error -- it yields the
// same defaults a file with no keys would. A malformed file *is* an
// error, and a loud one: silently falling back to defaults because of a
// typo would leave the user staring at an overlay that ignores their
// theme with no clue why.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is 0type's user configuration, fully populated with defaults
// even when no config file exists.
type Config struct {
	// Theme names the stylesheet to apply; see internal/theme for how a
	// name is resolved. Empty means the default theme.
	Theme string
	// Hooks maps a hook name to a shell command run when that moment in a
	// dictation session happens. Hook *names* are validated where they're
	// given meaning, by internal/plugin.NewRunner, so that this package
	// doesn't have to know the set of hooks that exists.
	Hooks map[string]string
	// Path is the file this was loaded from, or "" if none existed. Shown
	// by `0type config` so "my settings aren't applying" is answerable
	// without guessing which file 0type actually read.
	Path string
}

// Default returns the configuration used when there's no config file.
func Default() *Config {
	return &Config{Theme: "", Hooks: map[string]string{}}
}

// knownKeys are the top-level keys Parse accepts. Anything else is
// rejected rather than ignored: a misspelled key that's silently dropped
// is indistinguishable, from the user's side, from one that doesn't work.
var knownKeys = map[string]bool{"theme": true}

// Load reads the config file from its standard location. A missing file
// returns Default() and no error; an unreadable or malformed one returns
// an error.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config file from an explicit path.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse reads config content that's already in memory.
func Parse(data []byte) (*Config, error) {
	sections, err := parseTOML(data)
	if err != nil {
		return nil, err
	}

	cfg := Default()
	for key, value := range sections[""] {
		if !knownKeys[key] {
			return nil, fmt.Errorf("unknown key %q (supported: %s)", key, strings.Join(sortedKeys(knownKeys), ", "))
		}
		if key == "theme" {
			cfg.Theme = value
		}
	}

	for name, section := range sections {
		switch name {
		case "", "hooks":
		default:
			return nil, fmt.Errorf("unknown section [%s] (supported: [hooks])", name)
		}
		if name == "hooks" {
			for key, value := range section {
				cfg.Hooks[key] = value
			}
		}
	}

	return cfg, nil
}

// Path is the config file's standard location:
// $XDG_CONFIG_HOME/0type/config.toml (falling back to ~/.config).
func Path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locate config dir: %w", err)
	}
	return filepath.Join(base, "0type", "config.toml"), nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SetTheme persists a theme choice to the config file, creating it if
// necessary. It rewrites only the `theme` line, leaving comments and
// every other setting exactly as the user wrote them -- a settings menu
// that reformatted the file, or dropped the comments explaining someone's
// own hooks, would make the menu something you'd avoid using.
func SetTheme(name string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create config dir: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}

	updated := replaceThemeLine(string(existing), name)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// replaceThemeLine returns content with its top-level `theme = "..."`
// assignment set to name, adding one if there wasn't one. Only the
// top-level key is touched: a line inside a [section] is a different key
// that happens to share a name.
func replaceThemeLine(content, name string) string {
	assignment := fmt.Sprintf("theme = %q", name)

	lines := strings.Split(content, "\n")
	inSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(trimmed, "[") {
			inSection = true
			continue
		}
		if inSection {
			continue
		}
		if key, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "theme" {
			lines[i] = assignment
			return strings.Join(lines, "\n")
		}
	}

	if content == "" {
		return assignment + "\n"
	}
	// No theme line: put one at the top, before any [section] would
	// capture it.
	return assignment + "\n" + content
}
