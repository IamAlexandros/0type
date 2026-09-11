package main

import (
	"fmt"

	"github.com/zalkanorr/0type/internal/ui"
)

// runUI shows the floating overlay window in its idle state (a pulsing
// dot, no live transcription wired in -- that's runApp). This is Phase
// 4's manual proof point: it exists to verify the window itself is
// anchored, styled, animates in, and behaves correctly on a real Wayland
// session before anything depends on it.
func runUI(args []string) error {
	win, err := ui.New("")
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	win.LoadCSS("themes/default.css")
	win.Show()
	win.Run()
	return nil
}
