// Package modelstore downloads and verifies the ONNX model files 0type's
// ASR pipeline needs, caching them under the user's XDG cache directory so
// they aren't bundled into the binary or repository.
package modelstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// File describes one file belonging to a model bundle.
type File struct {
	Name   string
	URL    string
	SHA256 string
}

// Bundle is a named, versioned collection of model files.
type Bundle struct {
	Name  string
	Files []File
}

// ParakeetTDTv2 is the int8-quantized Parakeet TDT 0.6B v2 model
// (istupakov/parakeet-tdt-0.6b-v2-onnx on HuggingFace): a log-mel feature
// extractor, a Conformer encoder, and a combined decoder+joint network.
var ParakeetTDTv2 = Bundle{
	Name: "parakeet-tdt-0.6b-v2",
	Files: []File{
		{
			Name:   "encoder-model.int8.onnx",
			URL:    "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/main/encoder-model.int8.onnx",
			SHA256: "3e0581fda6ab843888b51e56d7ee78b6d5bc3237ec113af1f732d1d5286aa155",
		},
		{
			Name:   "decoder_joint-model.int8.onnx",
			URL:    "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/main/decoder_joint-model.int8.onnx",
			SHA256: "a449f49acd68979d418651dd2dcb737cc0f1bf0225e009e29ee326354edbf7d3",
		},
		{
			Name:   "nemo128.onnx",
			URL:    "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/main/nemo128.onnx",
			SHA256: "a9fde1486ebfcc08f328d75ad4610c67835fea58c73ba57e3209a6f6cf019e9f",
		},
		{
			Name:   "vocab.txt",
			URL:    "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/main/vocab.txt",
			SHA256: "ec182b70dd42113aff6c5372c75cac58c952443eb22322f57bbd7f53977d497d",
		},
		{
			Name:   "config.json",
			URL:    "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v2-onnx/resolve/main/config.json",
			SHA256: "666903c76b9798caf2c210afd4f6cd60b08a8dbf9800ec8d7a3bc0d2148ac466",
		},
	},
}

// Dir returns the local cache directory for a named model bundle, creating
// it if necessary.
func Dir(bundleName string) (string, error) {
	cacheHome, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("modelstore: resolve cache dir: %w", err)
	}
	dir := filepath.Join(cacheHome, "0type", "models", bundleName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("modelstore: create cache dir: %w", err)
	}
	return dir, nil
}

// Progress is called once per file after EnsureDownloaded has confirmed it
// is present with a matching checksum, reporting whether it had to be
// downloaded or was already cached.
type Progress func(file string, downloaded bool)

// EnsureDownloaded makes sure every file in files exists under dir with a
// matching SHA-256 checksum, (re-)downloading any that are missing or
// don't match.
func EnsureDownloaded(dir string, files []File, progress Progress) error {
	for _, f := range files {
		path := filepath.Join(dir, f.Name)

		ok, err := verifyChecksum(path, f.SHA256)
		if err != nil {
			return fmt.Errorf("modelstore: verify %s: %w", f.Name, err)
		}
		if ok {
			if progress != nil {
				progress(f.Name, false)
			}
			continue
		}

		if err := download(path, f.URL); err != nil {
			return fmt.Errorf("modelstore: download %s: %w", f.Name, err)
		}
		ok, err = verifyChecksum(path, f.SHA256)
		if err != nil {
			return fmt.Errorf("modelstore: verify %s after download: %w", f.Name, err)
		}
		if !ok {
			os.Remove(path)
			return fmt.Errorf("modelstore: checksum mismatch for %s after download", f.Name)
		}
		if progress != nil {
			progress(f.Name, true)
		}
	}
	return nil
}

func verifyChecksum(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

func download(path, url string) error {
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}

	resp, err := http.Get(url)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
