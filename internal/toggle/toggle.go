// Package toggle implements 0type's show/hide mechanism: the running
// instance writes a pidfile and listens for SIGUSR1; `0type toggle` reads
// the pidfile and sends that signal. Wayland doesn't let an unfocused app
// grab a global hotkey itself, so the intended setup is a compositor
// keybinding (see docs/SETUP.md) running `0type toggle`.
package toggle

import (
	"errors"
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

// ErrNotRunning means no live 0type instance was found -- distinct from
// a real failure (an unreadable pidfile, a signal that was refused), so
// callers can offer to start one instead of reporting an error.
var ErrNotRunning = errors.New("no running 0type instance")

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
		return 0, fmt.Errorf("toggle: %w (no pidfile at %s)", ErrNotRunning, path)
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
		return 0, fmt.Errorf("toggle: %w (stale pidfile for pid %d)", ErrNotRunning, pid)
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

// ErrAlreadyRunning means another instance holds the startup lock: it is
// either running, or still starting up.
var ErrAlreadyRunning = errors.New("0type is already running or starting")

func lockFilePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("toggle: resolve cache dir: %w", err)
	}
	return filepath.Join(dir, "0type", "0type.lock"), nil
}

// Lock takes an exclusive, process-lifetime lock that only one instance
// can hold, returning ErrAlreadyRunning if someone else has it.
//
// The pidfile can't do this job. It's written only once the model is
// loaded and the signal handlers are installed -- deliberately, since a
// pidfile is a promise that signals will be handled, and signalling a
// half-initialized process would terminate it (SIGUSR1's default action).
// That leaves a ~12 second window at startup in which the instance is
// real but invisible to everything that looks for a pidfile, and anything
// that starts 0type on demand will happily start another. Pressing a
// shortcut a few times while the model loads was enough to spawn a pile
// of instances, each loading its own copy of a 650MB model.
//
// flock is the right primitive here because the kernel drops it when the
// process dies, however it dies: there is no stale lock to clean up, and
// no PID to check for reuse.
func Lock() (release func(), err error) {
	path, err := lockFilePath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("toggle: create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("toggle: open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("toggle: lock %s: %w", path, err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Starting reports whether an instance holds the startup lock but hasn't
// published a pidfile yet -- i.e. it's loading the model right now.
func Starting() bool {
	if _, err := RunningPID(); err == nil {
		return false // past startup: it's ready
	}
	release, err := Lock()
	if err != nil {
		return errors.Is(err, ErrAlreadyRunning)
	}
	release() // nobody holds it, so nothing is starting
	return false
}
