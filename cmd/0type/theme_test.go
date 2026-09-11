package main

import (
	"testing"

	"github.com/zalkanorr/0type/internal/config"
	"github.com/zalkanorr/0type/internal/theme"
	"github.com/zalkanorr/0type/internal/ui"
)

// The set of marks a theme may name (internal/theme, which validates the
// directive) and the set the overlay can actually draw (internal/ui,
// which paints them) are declared in two places, because making the
// pure-Go theme package depend on the cgo/GTK one to share three strings
// costs more than it saves. This test is what keeps them honest: add a
// mark to one side only and it fails here.
func TestMarkListsAgree(t *testing.T) {
	fromTheme := theme.Marks()
	if len(fromTheme) != len(ui.Marks) {
		t.Fatalf("theme.Marks() = %v, ui.Marks = %v: different lengths", fromTheme, ui.Marks)
	}
	for i, name := range fromTheme {
		if ui.Mark(name) != ui.Marks[i] {
			t.Errorf("mark %d: theme has %q, ui has %q", i, name, ui.Marks[i])
		}
		if !ui.Mark(name).Valid() {
			t.Errorf("theme offers mark %q, which ui cannot draw", name)
		}
	}
}

// Every built-in theme must name a mark the overlay can draw -- a theme
// that ships broken is worse than one that doesn't ship.
func TestBuiltinThemesLoadAndDraw(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // ignore the developer's own themes

	for _, e := range theme.List() {
		th, err := theme.Load(e.Name)
		if err != nil {
			t.Errorf("Load(%q): %v", e.Name, err)
			continue
		}
		if !ui.Mark(th.Mark).Valid() {
			t.Errorf("theme %q selects mark %q, which ui cannot draw", e.Name, th.Mark)
		}
	}
}

func TestLoadThemeFlagOverridesConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{Theme: "mono"}

	th, err := loadTheme("term", cfg)
	if err != nil {
		t.Fatalf("loadTheme: %v", err)
	}
	if th.Name != "term" {
		t.Errorf("Name = %q, want term (the flag, not the config)", th.Name)
	}

	th, err = loadTheme("", cfg)
	if err != nil {
		t.Fatalf("loadTheme: %v", err)
	}
	if th.Name != "mono" {
		t.Errorf("Name = %q, want mono (from the config)", th.Name)
	}
}
