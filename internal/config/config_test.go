package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	cfg, err := Parse([]byte(`
# 0type config
theme = "mono"   # trailing comment

[hooks]
on_start = "notify-send 'listening'"
on_copy  = 'echo done'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Theme != "mono" {
		t.Errorf("Theme = %q, want mono", cfg.Theme)
	}
	if got := cfg.Hooks["on_start"]; got != "notify-send 'listening'" {
		t.Errorf("on_start = %q", got)
	}
	if got := cfg.Hooks["on_copy"]; got != "echo done" {
		t.Errorf("on_copy = %q", got)
	}
}

func TestParseEmptyIsDefaults(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Theme != "" {
		t.Errorf("Theme = %q, want empty (the default theme)", cfg.Theme)
	}
	if len(cfg.Hooks) != 0 {
		t.Errorf("Hooks = %v, want none", cfg.Hooks)
	}
}

// A `#` inside a quoted command is part of the command, not the start of
// a comment -- shell one-liners contain them.
func TestParseHashInsideString(t *testing.T) {
	cfg, err := Parse([]byte(`
[hooks]
on_stop = "printf '#%s' done"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Hooks["on_stop"]; got != "printf '#%s' done" {
		t.Errorf("on_stop = %q, want the `#` preserved", got)
	}
}

func TestParseEscapes(t *testing.T) {
	cfg, err := Parse([]byte(`theme = "a\\b\"c\td"`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Theme != "a\\b\"c\td" {
		t.Errorf("Theme = %q", cfg.Theme)
	}
}

// Every rejection below is something that would otherwise "work" by
// quietly doing nothing, which is the failure mode this parser exists to
// avoid.
func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unknown key":      `them = "mono"`,
		"unknown section":  "[hook]\non_start = \"x\"",
		"unquoted value":   `theme = mono`,
		"number value":     `theme = 12`,
		"array value":      `theme = ["a", "b"]`,
		"no equals":        `theme`,
		"empty key":        `= "mono"`,
		"unterminated":     `theme = "mono`,
		"bad section":      `[hooks`,
		"duplicate key":    "theme = \"a\"\ntheme = \"b\"",
		"bad escape":       `theme = "a\qb"`,
		"trailing garbage": `theme = "mono" oops`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", input)
			}
		})
	}
}

// The error should say which line is wrong; "invalid config" alone makes
// the user bisect their own file.
func TestParseErrorNamesTheLine(t *testing.T) {
	_, err := Parse([]byte("theme = \"mono\"\n\n[hooks]\non_start = oops\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %q does not name line 4", err)
	}
}

func TestLoadFromMissingFileIsDefaults(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Theme != "" || len(cfg.Hooks) != 0 {
		t.Errorf("missing file should yield defaults, got %+v", cfg)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty for a file that doesn't exist", cfg.Path)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`theme = "light"`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Theme != "light" {
		t.Errorf("Theme = %q, want light", cfg.Theme)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
}

func TestLoadFromMalformedFileNamesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("theme = oops"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected an error for a malformed file")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the offending file", err)
	}
}

func TestPathUsesXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")

	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "/tmp/xdg-test/0type/config.toml"; path != want {
		t.Errorf("Path() = %q, want %q", path, want)
	}
}
