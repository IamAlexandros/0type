// Package plugin runs user-configured shell commands at the moments that
// matter in a dictation session.
//
// "Plugin" here means a command line in the config file, not a loadable
// module: 0type's extension points are few and the interesting ones are
// one-liners ("notify me when it copies", "type the text into the focused
// window instead of the clipboard"), so a shell command with the text on
// stdin composes with everything the user already has installed and needs
// no plugin API, no ABI, and no versioning story.
//
// Hooks are strictly advisory: they run asynchronously and a hook that
// fails, hangs, or doesn't exist must never break or delay dictation, so
// every error is logged and swallowed and every command is killed at a
// timeout.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Hook identifies a moment in a session that a command can be attached to.
type Hook string

const (
	// HookStart fires when a dictation session starts (the overlay opens).
	// Text: empty.
	HookStart Hook = "on_start"
	// HookFinal fires each time a sentence is finalized, as it happens.
	// Text: that sentence.
	HookFinal Hook = "on_final"
	// HookCopy fires when the session's full dictation is put on the
	// clipboard. Text: everything dictated this session. This is the hook
	// to use to do something else with the result -- type it into the
	// focused window, append it to a file, send it somewhere.
	HookCopy Hook = "on_copy"
	// HookStop fires when the session ends, after any HookCopy. Text:
	// everything dictated this session, which is empty if nothing was said.
	HookStop Hook = "on_stop"
)

// All is every hook 0type fires, in the order a session triggers them.
var All = []Hook{HookStart, HookFinal, HookCopy, HookStop}

// DefaultTimeout bounds how long a hook command may run before it's
// killed. Generous for a notification or a `wtype` call, short enough
// that a hanging command can't pile up across a session.
const DefaultTimeout = 5 * time.Second

// Runner dispatches hook commands. The zero value is not usable; use New.
// A nil *Runner is, deliberately: it makes "no hooks configured" require
// no branching at the call sites.
type Runner struct {
	commands map[Hook]string
	timeout  time.Duration
	logf     func(format string, args ...any)

	wg sync.WaitGroup
}

// Option configures a Runner.
type Option func(*Runner)

// WithTimeout overrides DefaultTimeout.
func WithTimeout(d time.Duration) Option {
	return func(r *Runner) { r.timeout = d }
}

// WithLogger sets where hook failures are reported. Defaults to discarding
// them; the app passes log.Printf.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(r *Runner) { r.logf = logf }
}

// New builds a Runner from a hook-name -> command map (config's [hooks]
// section). Unknown hook names are an error rather than ignored: a
// misspelled hook that silently never fires is indistinguishable from a
// broken one. Returns nil, nil if no commands are configured.
func New(commands map[string]string, opts ...Option) (*Runner, error) {
	known := map[Hook]bool{}
	for _, h := range All {
		known[h] = true
	}

	parsed := map[Hook]string{}
	for name, cmd := range commands {
		if !known[Hook(name)] {
			return nil, fmt.Errorf("plugin: unknown hook %q (supported: %s)", name, strings.Join(hookNames(), ", "))
		}
		if strings.TrimSpace(cmd) == "" {
			continue // an empty command is how a user disables a hook without deleting the line
		}
		parsed[Hook(name)] = cmd
	}
	if len(parsed) == 0 {
		return nil, nil
	}

	r := &Runner{commands: parsed, timeout: DefaultTimeout, logf: func(string, ...any) {}}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Fire runs the command bound to hook, if any, passing text on stdin and
// in $ZEROTYPE_TEXT. It returns immediately -- the command runs on its
// own goroutine -- so a session is never blocked by a hook.
func (r *Runner) Fire(hook Hook, text string) {
	if r == nil {
		return
	}
	command, ok := r.commands[hook]
	if !ok {
		return
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := r.run(command, hook, text); err != nil {
			r.logf("0type: hook %s failed: %v", hook, err)
		}
	}()
}

// Wait blocks until every command started by Fire has finished or timed
// out. Used on shutdown so a hook isn't killed mid-flight by the process
// exiting, and by tests to observe effects.
func (r *Runner) Wait() {
	if r == nil {
		return
	}
	r.wg.Wait()
}

// Has reports whether a command is bound to hook.
func (r *Runner) Has(hook Hook) bool {
	if r == nil {
		return false
	}
	_, ok := r.commands[hook]
	return ok
}

func (r *Runner) run(command string, hook Hook, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	// `sh -c` rather than splitting the string ourselves: the command is
	// written by the user in a config file, in shell syntax, and pipes and
	// quoting are the point of allowing a command at all.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)

	// The timeout has to survive a hook that spawns children, and by
	// default it doesn't. CommandContext kills only the process it
	// started, and CombinedOutput waits for the output pipes to close --
	// which a surviving grandchild holds open. A hook like
	// `sh -c "sleep 10"` therefore ran to completion despite an 80ms
	// timeout, and one that backgrounded something would have blocked a
	// goroutine (and shutdown's Wait) indefinitely.
	//
	// So: put the hook in its own process group and kill the group, which
	// reaches the children; and set WaitDelay so that even a process that
	// somehow outlives the signal can't hold this call open past the
	// deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second

	cmd.Stdin = strings.NewReader(text)
	cmd.Env = append(cmd.Environ(),
		"ZEROTYPE_HOOK="+string(hook),
		"ZEROTYPE_TEXT="+text,
	)

	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("timed out after %s", r.timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(output) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		return err
	}
	return nil
}

func hookNames() []string {
	out := make([]string, 0, len(All))
	for _, h := range All {
		out = append(out, string(h))
	}
	sort.Strings(out)
	return out
}
