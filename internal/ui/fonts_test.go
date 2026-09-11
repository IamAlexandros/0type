package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled font must actually be in the binary: a theme asking for
// "Press Start 2P" silently falls back to a system font otherwise, and
// the failure looks like a styling mistake rather than a missing asset.
func TestBundledFontsArePresent(t *testing.T) {
	entries, err := bundledFonts.ReadDir("fonts")
	if err != nil {
		t.Fatalf("read embedded fonts: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no fonts embedded")
	}
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), "PressStart2P") {
			found = true
		}
		data, err := bundledFonts.ReadFile("fonts/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if len(data) < 1000 {
			t.Errorf("%s is %d bytes, too small to be a font", e.Name(), len(data))
		}
	}
	if !found {
		t.Error("Press Start 2P is missing; the gameboy theme depends on it")
	}
}

func TestRegisterBundledFontsWritesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	if err := registerBundledFonts(); err != nil {
		t.Fatalf("registerBundledFonts: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "0type", "fonts", "*.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no font was written to the cache directory")
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 1000 {
		t.Errorf("cached font is only %d bytes", info.Size())
	}

	// Registering twice must be harmless: New can be called again in the
	// same process, and the cached file is reused rather than rewritten.
	if err := registerBundledFonts(); err != nil {
		t.Fatalf("second registerBundledFonts: %v", err)
	}
}
