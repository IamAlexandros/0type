package ui

/*
#cgo pkg-config: fontconfig
#include <fontconfig/fontconfig.h>
#include <stdlib.h>

// register_app_font makes a font file available to this process only,
// without touching the user's installed fonts. Returns TRUE on success.
static int register_app_font(const char *path) {
	return FcConfigAppFontAddFile(NULL, (const FcChar8 *)path) == FcTrue;
}
*/
import "C"

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

// bundledFonts are fonts a built-in theme needs that no system is
// expected to have.
//
// Embedding rather than asking the user to install anything: a theme that
// only looks right if you first go and install a font isn't a theme
// that's really bundled, and writing into someone's ~/.local/share/fonts
// would change what every *other* program on their machine sees. These
// are registered with fontconfig for this process alone, so they're
// visible to 0type and nothing else.
//
// Press Start 2P is licensed under the SIL Open Font License 1.1; the
// license text ships alongside it in fonts/OFL.txt, as the license
// requires.
//
//go:embed fonts/*.ttf
var bundledFonts embed.FS

// fontCacheDir is where embedded fonts are materialized, because
// fontconfig takes a path, not bytes.
func fontCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("ui: locate cache dir: %w", err)
	}
	return filepath.Join(base, "0type", "fonts"), nil
}

// registerBundledFonts writes the embedded fonts to the cache directory
// (once) and registers them with fontconfig for this process. Errors are
// returned rather than fatal: a missing font means a theme falls back to
// its next choice, which is a cosmetic problem, not a reason to refuse to
// start.
func registerBundledFonts() error {
	dir, err := fontCacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ui: create font cache dir: %w", err)
	}

	entries, err := bundledFonts.ReadDir("fonts")
	if err != nil {
		return fmt.Errorf("ui: read embedded fonts: %w", err)
	}

	for _, entry := range entries {
		data, err := bundledFonts.ReadFile("fonts/" + entry.Name())
		if err != nil {
			return fmt.Errorf("ui: read embedded font %s: %w", entry.Name(), err)
		}
		// Name the cached copy after its content, so a font changed by an
		// upgrade is written afresh instead of a stale file being reused.
		sum := sha256.Sum256(data)
		name := hex.EncodeToString(sum[:8]) + "-" + entry.Name()
		path := filepath.Join(dir, name)

		if _, err := os.Stat(path); err != nil {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("ui: write font %s: %w", path, err)
			}
		}

		cpath := C.CString(path)
		ok := C.register_app_font(cpath) != 0
		C.free(unsafe.Pointer(cpath))
		if !ok {
			return fmt.Errorf("ui: fontconfig rejected %s", path)
		}
	}
	return nil
}
