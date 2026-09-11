// Package toggle implements 0type's show/hide mechanism: the running
// instance writes a pidfile and listens for SIGUSR1; `0type toggle` reads
// the pidfile and sends that signal. Wayland doesn't let an unfocused app
// grab a global hotkey itself, so the intended setup is a compositor
// keybinding (see docs/SETUP.md) running `0type toggle`.
package toggle

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func pidFilePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("toggle: resolve cache dir: %w", err)
	}
	return filepath.Join(dir, "0type", "0type.pid"), nil
}

// WritePIDFile records the current process's PID so Send can find it. The
// returned cleanup func removes the file and should be deferred.
func WritePIDFile() (cleanup func(), err error) {
	path, err := pidFilePath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("toggle: create pidfile dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("toggle: write pidfile: %w", err)
	}
	return func() { os.Remove(path) }, nil
}

// Send reads the running instance's PID from the pidfile and sends it
// SIGUSR1, asking it to toggle visibility.
func Send() error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("toggle: no running instance found (is 0type running?): %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("toggle: invalid pidfile contents %q: %w", data, err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("toggle: find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGUSR1); err != nil {
		return fmt.Errorf("toggle: signal pid %d: %w (stale pidfile? try restarting 0type)", pid, err)
	}
	return nil
}

// toggleChanBuffer gives the signal channel room for a few rapid presses
// in a row. Standard Unix signals (unlike real-time signals) are never
// queued by the kernel: if a second SIGUSR1 arrives while the first is
// still pending delivery, it's simply dropped, not merged or queued. A
// buffer of 1 (Go's usual signal.Notify recommendation for a single
// signal type) turned out to be too tight for how this is actually used
// -- toggling off then quickly back on is a completely ordinary thing to
// do, and a dropped second press silently ate the "back on" -- so this is
// larger to make that failure mode rare in practice, not because any of
// this is expected to be a high-frequency signal.
const toggleChanBuffer = 8

// OnToggle installs a signal handler that calls handler once for every
// SIGUSR1 the process receives, for the life of the program.
func OnToggle(handler func()) {
	ch := make(chan os.Signal, toggleChanBuffer)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			handler()
		}
	}()
}
