package main

import (
	"fmt"

	"github.com/zalkanorr/0type/internal/ui"
)

// runUI shows the floating overlay window with static content. This is
// Phase 4's manual proof point: it doesn't wire in live transcription yet
// (that's runListen/Phase 5) -- it exists to verify the layer-shell window
// itself is anchored, styled, and behaves correctly on a real Wayland
// session before anything depends on it.
func runUI(args []string) error {
	win, err := ui.New("0type is listening…")
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	win.LoadCSS("themes/default.css")
	win.Show()
	win.Run()
	return nil
}
