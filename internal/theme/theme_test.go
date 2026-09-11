package theme

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// userThemeDir points UserDir at a temp directory for the duration of a
// test, so tests never read or write the developer's real ~/.config.
func userThemeDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "0type", "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir theme dir: %v", err)
	}
	return dir
}

func TestLoadBuiltin(t *testing.T) {
	userThemeDir(t) // empty: nothing to shadow the built-ins

	for _, name := range []string{"default", "light", "mono", "term"} {
		th, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		if th.Name != name {
			t.Errorf("Load(%q).Name = %q", name, th.Name)
		}
		if th.Origin != "built-in" {
			t.Errorf("Load(%q).Origin = %q, want built-in", name, th.Origin)
		}
		// Every bundled theme must set every color the overlay draws with;
		// a missing one silently falls back to GTK's default rather than
		// anything the theme author chose (see internal/ui's LoadCSS).
		for _, sel := range []string{"#zt-panel", "#zt-label", "#zt-accent", "#zt-muted", "#zt-success", "#zt-meter", "#zt-tile"} {
			if !bytes.Contains(th.CSS, []byte(sel)) {
				t.Errorf("built-in theme %q does not style %s", name, sel)
			}
		}
	}
}

func TestLoadEmptyNameIsDefault(t *testing.T) {
	userThemeDir(t)

	th, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if th.Name != Default {
		t.Errorf("Load(\"\").Name = %q, want %q", th.Name, Default)
	}
}

func TestLoadUserThemeShadowsBuiltin(t *testing.T) {
	dir := userThemeDir(t)
	path := filepath.Join(dir, "default.css")
	if err := os.WriteFile(path, []byte("#zt-panel { color: red; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Origin != path {
		t.Errorf("Origin = %q, want the user file %q", th.Origin, path)
	}
	if !bytes.Contains(th.CSS, []byte("red")) {
		t.Error("loaded the built-in CSS, not the user's")
	}
}

func TestLoadUnknownName(t *testing.T) {
	userThemeDir(t)

	_, err := Load("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// The message should tell the user what they *can* pick, not just
	// that they picked wrong.
	if !strings.Contains(err.Error(), "default") {
		t.Errorf("error %q does not suggest available themes", err)
	}
}

func TestLoadExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.css")
	if err := os.WriteFile(path, []byte("#zt-panel { color: blue; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Name != "custom" {
		t.Errorf("Name = %q, want custom", th.Name)
	}
	if th.Origin != path {
		t.Errorf("Origin = %q, want %q", th.Origin, path)
	}
}

func TestLoadExplicitPathMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.css")); err == nil {
		t.Fatal("expected an error for a missing explicit path")
	}
}

// A bare name must never be resolved against the working directory: a
// stray default.css lying around shouldn't quietly replace the theme.
func TestLoadBareNameIgnoresWorkingDirectory(t *testing.T) {
	userThemeDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.css"), []byte("bogus"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	th, err := Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if th.Origin != "built-in" {
		t.Errorf("Origin = %q, want built-in", th.Origin)
	}
}

func TestLoadRejectsTraversal(t *testing.T) {
	userThemeDir(t)

	for _, name := range []string{"..", ".", "../../etc/passwd"} {
		if _, err := Load(name); err == nil {
			t.Errorf("Load(%q) succeeded, want an error", name)
		}
	}
}

func TestList(t *testing.T) {
	dir := userThemeDir(t)
	if err := os.WriteFile(filepath.Join(dir, "default.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := List()
	byName := map[string]Entry{}
	var names []string
	for _, e := range got {
		byName[e.Name] = e
		names = append(names, e.Name)
	}

	for _, want := range []string{"custom", "default", "light", "mono", "term"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("List() missing %q (got %v)", want, names)
		}
	}
	if len(byName) != len(got) {
		t.Errorf("List() has duplicate names: %v", names)
	}
	if !byName["default"].Overrides {
		t.Error("user default.css should be marked as overriding the built-in")
	}
	if byName["custom"].Overrides {
		t.Error("custom.css overrides no built-in")
	}
	if byName["light"].Origin != "built-in" {
		t.Errorf("light.Origin = %q, want built-in", byName["light"].Origin)
	}
	// Sorted, so `0type themes` output is stable between runs.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Errorf("List() is not sorted: %v", names)
			break
		}
	}
}

func TestParseDirectivesDefault(t *testing.T) {
	mark, err := parseDirectives([]byte("#zt-panel { color: red; }"))
	if err != nil {
		t.Fatalf("parseDirectives: %v", err)
	}
	if mark != DefaultMark {
		t.Errorf("mark = %q, want %q", mark, DefaultMark)
	}
}

func TestParseDirectivesMark(t *testing.T) {
	mark, err := parseDirectives([]byte("/* nice theme\n * 0type-mark: pixel\n */\n#zt-panel {}"))
	if err != nil {
		t.Fatalf("parseDirectives: %v", err)
	}
	if mark != "pixel" {
		t.Errorf("mark = %q, want pixel", mark)
	}
}

func TestParseDirectivesUnknownMark(t *testing.T) {
	_, err := parseDirectives([]byte("/* 0type-mark: sparkle */"))
	if err == nil {
		t.Fatal("expected an error for an unknown mark")
	}
	if !strings.Contains(err.Error(), "pixel") {
		t.Errorf("error %q does not list the valid marks", err)
	}
}

// The mark a theme selects has to survive being loaded, not just parsed.
func TestLoadCarriesMark(t *testing.T) {
	userThemeDir(t)

	term, err := Load("term")
	if err != nil {
		t.Fatalf("Load(term): %v", err)
	}
	if term.Mark != "pixel" {
		t.Errorf("term.Mark = %q, want pixel", term.Mark)
	}

	def, err := Load("default")
	if err != nil {
		t.Fatalf("Load(default): %v", err)
	}
	if def.Mark != DefaultMark {
		t.Errorf("default.Mark = %q, want %q", def.Mark, DefaultMark)
	}
}

func TestLoadRejectsBadDirective(t *testing.T) {
	dir := userThemeDir(t)
	path := filepath.Join(dir, "broken.css")
	if err := os.WriteFile(path, []byte("/* 0type-mark: banana */"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load("broken"); err == nil {
		t.Fatal("expected an error for an unknown mark directive")
	}
}
