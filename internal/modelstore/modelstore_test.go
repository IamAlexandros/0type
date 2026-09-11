package modelstore

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestParakeetTDTv2Manifest guards against a transcription typo in the
// hardcoded checksum manifest silently shortening or corrupting a hash
// (which would otherwise only surface as a confusing "checksum mismatch"
// error after downloading hundreds of MB).
func TestParakeetTDTv2Manifest(t *testing.T) {
	if len(ParakeetTDTv2.Files) == 0 {
		t.Fatal("ParakeetTDTv2.Files is empty")
	}
	seen := map[string]bool{}
	for _, f := range ParakeetTDTv2.Files {
		if f.Name == "" || f.URL == "" {
			t.Errorf("file %+v has an empty Name or URL", f)
		}
		if !hexSHA256.MatchString(f.SHA256) {
			t.Errorf("file %s: SHA256 %q is not 64 lowercase hex characters", f.Name, f.SHA256)
		}
		if seen[f.Name] {
			t.Errorf("duplicate file name %q in manifest", f.Name)
		}
		seen[f.Name] = true
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello world")
	const want = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	ok, err := verifyChecksum(path, want)
	if err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
	if !ok {
		t.Error("verifyChecksum: want match for correct hash, got false")
	}

	ok, err = verifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
	if ok {
		t.Error("verifyChecksum: want mismatch for wrong hash, got true")
	}
}

func TestVerifyChecksum_MissingFile(t *testing.T) {
	ok, err := verifyChecksum(filepath.Join(t.TempDir(), "does-not-exist"), "irrelevant")
	if err != nil {
		t.Fatalf("verifyChecksum: want nil error for missing file, got %v", err)
	}
	if ok {
		t.Error("verifyChecksum: want false for missing file, got true")
	}
}

func TestDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, err := Dir("some-bundle")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Dir did not create %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}
