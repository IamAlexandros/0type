package main

import "github.com/zalkanorr/0type/internal/toggle"

// runToggle asks a running `0type` instance to show/hide its window. It
// exists as a separate, near-instant command because Wayland doesn't let
// an unfocused app register its own global hotkey -- bind this to a
// compositor keybinding instead (see docs/SETUP.md).
func runToggle(args []string) error {
	return toggle.Send()
}
