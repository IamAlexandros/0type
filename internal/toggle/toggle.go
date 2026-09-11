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
// SIGUSR1, asking it to toggle dictation.
func Send() error { return send(syscall.SIGUSR1) }

// SendMenu asks the running instance to open its menu (SIGUSR2). This is
// what a second `0type` does instead of starting a whole second copy:
// the model takes seconds to load and hundreds of megabytes to hold, so
// there must only ever be one instance, and the natural meaning of
// running the command again is "show me the program".
func SendMenu() error { return send(syscall.SIGUSR2) }

func send(sig syscall.Signal) error {
	pid, err := RunningPID()
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("toggle: find process %d: %w", pid, err)
	}
	if err := proc.Signal(sig); err != nil {
		return fmt.Errorf("toggle: signal pid %d: %w (stale pidfile? try restarting 0type)", pid, err)
	}
	return nil
}

// RunningPID returns the PID of the running 0type instance, or an error
// if there isn't one. A pidfile left behind by a crashed instance is
// reported as "not running" rather than as a live process: signalling a
// PID that has since been reused by something else would be worse than
// starting a second copy.
func RunningPID() (int, error) {
	path, err := pidFilePath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("toggle: no running instance found (is 0type running?): %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("toggle: invalid pidfile contents %q: %w", data, err)
	}
	// Signal 0 checks the process exists and is ours without disturbing it.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, fmt.Errorf("toggle: find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, fmt.Errorf("toggle: no running instance (stale pidfile for pid %d): %w", pid, err)
	}
	return pid, nil
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
func OnToggle(handler func()) { on(syscall.SIGUSR1, handler) }

// OnMenu does the same for SIGUSR2, which asks for the menu.
func OnMenu(handler func()) { on(syscall.SIGUSR2, handler) }

func on(sig syscall.Signal, handler func()) {
	ch := make(chan os.Signal, toggleChanBuffer)
	signal.Notify(ch, sig)
	go func() {
		for range ch {
			handler()
		}
	}()
}
