package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/IamAlexandros/0type/internal/toggle"
)

// runToggle asks a running `0type` instance to show/hide its window. It
// exists as a separate, near-instant command because Wayland doesn't let
// an unfocused app register its own global hotkey -- bind this to a
// compositor keybinding instead (see docs/SETUP.md).
//
// If nothing is running, it starts 0type rather than failing. The command
// is almost always invoked from a keyboard shortcut, where nobody sees
// stdout or an exit code: "press the key, nothing happens, no explanation"
// is the worst possible failure, and it's reachable in completely ordinary
// ways -- quitting from the menu, a crash, or simply not having started
// 0type since logging in.
func runToggle(args []string) error {
	err := toggle.Send()
	if err == nil {
		return nil
	}
	if !errors.Is(err, toggle.ErrNotRunning) {
		return err
	}

	// Don't stampede: an instance may be part-way through its model load,
	// in which case it has no pidfile yet but is very much on its way.
	if toggle.Starting() {
		fmt.Fprintln(os.Stderr, "0type is still starting up; press again in a moment")
		return nil
	}

	if startErr := startDaemon("--dictate"); startErr != nil {
		return fmt.Errorf("0type wasn't running and couldn't be started: %w", startErr)
	}
	fmt.Fprintln(os.Stderr, "0type wasn't running; starting it (the speech model takes a few seconds to load)")
	return nil
}

// startDaemon launches the resident 0type process, detached, with
// whatever start-up behaviour the caller wants (`--dictate` from the
// toggle, because whoever pressed the shortcut wants to talk, not to read
// a menu; nothing at all for the plain menu).
func startDaemon(extra ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the 0type binary: %w", err)
	}

	cmd := exec.Command(exe, append([]string{"--daemon"}, extra...)...)
	// Its own session, so it outlives the shell (or the keybinding's
	// transient scope) that started it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull

	if err := cmd.Start(); err != nil {
		return err
	}
	// Nothing waits on it; it's meant to keep running after this command
	// exits, so release the child rather than leaving a zombie parent.
	return cmd.Process.Release()
}
