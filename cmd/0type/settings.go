package main

import (
	"fmt"
	"sort"

	"github.com/zalkanorr/0type/internal/config"
	"github.com/zalkanorr/0type/internal/plugin"
	"github.com/zalkanorr/0type/internal/theme"
)

// runThemes lists every theme that can be named in the config file or
// passed to --theme, and where each one comes from. Without this, a user
// theme that isn't being picked up (wrong directory, wrong extension) is
// invisible -- the overlay just looks unchanged.
func runThemes(args []string) error {
	dir, err := theme.UserDir()
	if err != nil {
		return err
	}

	for _, e := range theme.List() {
		note := e.Origin
		if e.Overrides {
			note += " (overrides the built-in)"
		}
		fmt.Printf("  %-10s %s\n", e.Name, note)
	}
	fmt.Printf("\nDrop a .css file in %s to add your own.\n", dir)
	fmt.Println("Select one with `theme = \"name\"` in the config file, or `0type --theme name`.")
	return nil
}

// runConfig reports the resolved configuration and the file it came from,
// so "0type is ignoring my settings" can be answered by looking rather
// than guessing.
func runConfig(args []string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.Path == "" {
		fmt.Printf("config file: %s (does not exist -- using defaults)\n", path)
	} else {
		fmt.Printf("config file: %s\n", cfg.Path)
	}

	themeName := cfg.Theme
	if themeName == "" {
		themeName = theme.Default + " (default)"
	}
	fmt.Printf("theme:       %s\n", themeName)

	fmt.Println("hooks:")
	if len(cfg.Hooks) == 0 {
		fmt.Println("  (none)")
	}
	names := make([]string, 0, len(cfg.Hooks))
	for name := range cfg.Hooks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %-9s %s\n", name, cfg.Hooks[name])
	}

	// Validate rather than just echo: an unknown hook name is exactly the
	// kind of thing someone runs this command to find.
	if _, err := plugin.New(cfg.Hooks); err != nil {
		return err
	}
	return nil
}
