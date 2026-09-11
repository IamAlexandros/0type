package toggle

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestWritePIDFileAndSend_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cleanup, err := WritePIDFile()
	if err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}
	defer cleanup()

	path, err := pidFilePath()
	if err != nil {
		t.Fatalf("pidFilePath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	if got := string(data); got != strconv.Itoa(os.Getpid()) {
		t.Errorf("pidfile contents = %q, want %q", got, strconv.Itoa(os.Getpid()))
	}

	received := make(chan struct{}, 1)
	OnToggle(func() {
		select {
		case received <- struct{}{}:
		default:
		}
	})

	if err := Send(); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnToggle handler to fire after Send")
	}
}

func TestSend_NoRunningInstance(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Send(); err == nil {
		t.Fatal("Send: want error when no pidfile exists, got nil")
	}
}

func TestSend_InvalidPIDFileContents(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := pidFilePath()
	if err != nil {
		t.Fatalf("pidFilePath: %v", err)
	}
	if err := os.MkdirAll(path[:len(path)-len("/0type.pid")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Send(); err == nil {
		t.Fatal("Send: want error for non-numeric pidfile contents, got nil")
	}
}
