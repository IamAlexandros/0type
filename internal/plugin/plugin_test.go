package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestRunner builds a Runner whose log output the test can inspect.
func newTestRunner(t *testing.T, commands map[string]string, opts ...Option) (*Runner, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var logs []string
	opts = append(opts, WithLogger(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	}))

	r, err := New(commands, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), logs...)
	}
}

func TestFireRunsCommandWithTextOnStdin(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	r, _ := newTestRunner(t, map[string]string{"on_copy": "cat > " + out})

	r.Fire(HookCopy, "hello world")
	r.Wait()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not write its output file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("stdin = %q, want %q", got, "hello world")
	}
}

func TestFirePassesEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env.txt")
	r, _ := newTestRunner(t, map[string]string{"on_final": `printf '%s|%s' "$ZEROTYPE_HOOK" "$ZEROTYPE_TEXT" > ` + out})

	r.Fire(HookFinal, "a sentence")
	r.Wait()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "on_final|a sentence"; string(got) != want {
		t.Errorf("env = %q, want %q", got, want)
	}
}

func TestFireIgnoresUnboundHook(t *testing.T) {
	r, logs := newTestRunner(t, map[string]string{"on_copy": "true"})

	r.Fire(HookStart, "") // nothing bound to on_start
	r.Wait()

	if len(logs()) != 0 {
		t.Errorf("unbound hook logged: %v", logs())
	}
	if r.Has(HookStart) {
		t.Error("Has(HookStart) = true, want false")
	}
	if !r.Has(HookCopy) {
		t.Error("Has(HookCopy) = false, want true")
	}
}

// A failing hook must be reported but must not propagate: dictation keeps
// working even when the user's command is broken.
func TestFireSwallowsFailure(t *testing.T) {
	r, logs := newTestRunner(t, map[string]string{"on_stop": "echo boom >&2; exit 3"})

	r.Fire(HookStop, "text")
	r.Wait()

	got := logs()
	if len(got) != 1 {
		t.Fatalf("expected exactly one logged failure, got %v", got)
	}
	if !strings.Contains(got[0], "boom") {
		t.Errorf("log %q does not include the command's own output", got[0])
	}
}

func TestFireTimesOut(t *testing.T) {
	r, logs := newTestRunner(t, map[string]string{"on_stop": "sleep 10"}, WithTimeout(80*time.Millisecond))

	start := time.Now()
	r.Fire(HookStop, "")
	r.Wait()
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("hook was not killed at the timeout (took %s)", elapsed)
	}
	got := logs()
	if len(got) != 1 || !strings.Contains(got[0], "timed out") {
		t.Errorf("expected a timeout to be logged, got %v", got)
	}
}

// Fire must not block the caller: the session's UI thread calls it.
func TestFireIsAsynchronous(t *testing.T) {
	r, _ := newTestRunner(t, map[string]string{"on_start": "sleep 0.4"}, WithTimeout(2*time.Second))

	start := time.Now()
	r.Fire(HookStart, "")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Fire blocked for %s", elapsed)
	}
	r.Wait()
}

func TestNewRejectsUnknownHook(t *testing.T) {
	_, err := New(map[string]string{"on_finish": "true"})
	if err == nil {
		t.Fatal("expected an error for an unknown hook name")
	}
	if !strings.Contains(err.Error(), "on_final") {
		t.Errorf("error %q does not list the valid hook names", err)
	}
}

func TestNewWithNoCommandsReturnsNilRunner(t *testing.T) {
	for name, commands := range map[string]map[string]string{
		"nil":   nil,
		"empty": {},
		"blank": {"on_copy": "   "},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := New(commands)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if r != nil {
				t.Fatalf("New(%v) = %v, want nil", commands, r)
			}
			// A nil Runner must be safe to use, so callers don't branch.
			r.Fire(HookCopy, "x")
			r.Wait()
			if r.Has(HookCopy) {
				t.Error("nil Runner claims to have a hook")
			}
		})
	}
}

// A hook that *forks* must still be killed at the timeout. This is a
// separate case from TestFireTimesOut because a shell given a single
// simple command usually execs it, replacing itself -- so killing the
// shell happens to kill the sleep, and the bug stays hidden. Force a real
// child and the original implementation ran the full ten seconds:
// CommandContext killed only the shell, and CombinedOutput went on
// waiting for output pipes the surviving child still held open.
//
// It passed on the development machine and failed in CI, which is exactly
// the kind of difference this test exists to remove.
func TestFireTimesOutWhenTheHookForks(t *testing.T) {
	r, logs := newTestRunner(t, map[string]string{"on_stop": "sleep 10 & wait"}, WithTimeout(80*time.Millisecond))

	start := time.Now()
	r.Fire(HookStop, "")
	r.Wait()
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("forked hook was not killed at the timeout (took %s)", elapsed)
	}
	got := logs()
	if len(got) != 1 || !strings.Contains(got[0], "timed out") {
		t.Errorf("expected a timeout to be logged, got %v", got)
	}
}
