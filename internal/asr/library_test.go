package asr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A release tarball ships its own copy of ONNX Runtime next to the
// binary, and running against a different version that happens to be
// installed on the host instead would make the bundle meaningless -- so
// the binary-relative locations must be searched, and searched first.
func TestBundledLibraryPathsAreBinaryRelative(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot determine test binary path: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)

	got := bundledLibraryPaths()
	if len(got) == 0 {
		t.Fatal("bundledLibraryPaths() is empty")
	}
	for _, p := range got {
		if !strings.HasPrefix(p, filepath.Dir(dir)) {
			t.Errorf("%q is not relative to the binary at %q", p, dir)
		}
		if filepath.Base(p) != "libonnxruntime.so" {
			t.Errorf("%q does not name the library", p)
		}
	}

	// bin/ + lib/ is what the tarball's install layout produces.
	want := filepath.Join(filepath.Dir(dir), "lib", "libonnxruntime.so")
	var found bool
	for _, p := range got {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("bundledLibraryPaths() = %v, missing the ../lib layout %q", got, want)
	}
}

func TestBundledPathsComeFirst(t *testing.T) {
	all := append(bundledLibraryPaths(), candidateLibraryPaths...)
	if len(all) <= len(candidateLibraryPaths) {
		t.Fatal("no bundled paths were prepended")
	}
	// The first system path must appear only after every bundled one.
	firstSystem := -1
	for i, p := range all {
		if p == candidateLibraryPaths[0] {
			firstSystem = i
			break
		}
	}
	if firstSystem != len(bundledLibraryPaths()) {
		t.Errorf("system paths start at index %d, want %d (bundled paths must be searched first)",
			firstSystem, len(bundledLibraryPaths()))
	}
}
