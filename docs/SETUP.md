# 0type — Development Setup

## Go toolchain gotcha

On this dev machine (Fedora 42), `go` on `PATH` resolves to `/usr/bin/go.gcc`
(a gccgo 1.18 wrapper) — **not** the real Go toolchain. The real toolchain
lives at `/usr/lib/golang/bin/go` (go1.25.9). Always build via `make`, which
pins `GOROOT`/the `go` binary explicitly, rather than invoking bare `go`
directly. If you do need to invoke `go` by hand:

```sh
export GOROOT=/usr/lib/golang
export PATH=$GOROOT/bin:$PATH
go version   # must print go1.25.9 or newer, not gccgo
```

## System dependencies

```sh
sudo dnf install gtk4-devel gtk4-layer-shell-devel onnxruntime-devel alsa-lib-devel
```

- `gtk4-devel` / `gtk4-layer-shell-devel` — the floating overlay window and its
  Wayland layer-shell anchoring, via a small hand-written cgo shim
  (`internal/ui`). Note: a GTK**3** layer-shell package (`gtk-layer-shell`) may
  already be installed on this machine — it is not what we want; make sure the
  GTK**4** variant (`gtk4-layer-shell-0` in pkg-config) is installed too.
- `onnxruntime-devel` — not required to *build* (the Go ONNX Runtime binding
  dlopens `libonnxruntime.so` at runtime), but convenient to have installed on
  a dev machine so the shared library is present without a manual download.
- `alsa-lib-devel` — microphone capture via a small hand-written cgo shim
  (`internal/audio`).

Verify everything is resolvable via pkg-config:

```sh
make pkgcheck
```

## Building

```sh
make build     # -> bin/0type
make test      # go test ./...
```

## Model setup

```sh
./bin/0type setup
```

Downloads the int8-quantized Parakeet TDT 0.6B v2 model
(`istupakov/parakeet-tdt-0.6b-v2-onnx` from HuggingFace: encoder ~650MB,
decoder/joint ~9MB, feature extractor ~140KB) into
`~/.cache/0type/models/parakeet-tdt-0.6b-v2/`, verifying each file's SHA-256
against a hardcoded manifest (`internal/modelstore`). Safe to re-run — it
skips files that are already present and correct.

The full fp32 model is also available upstream (~2.4GB) but isn't used here;
int8 keeps the download small and runs entirely on CPU.

Try it:

```sh
./bin/0type debug transcribe testdata/hello.wav
# -> "The quick brown fox jumps over the lazy dog."
```

## ONNX Runtime API version gotcha

`go.mod` pins `github.com/yalue/onnxruntime_go` to **v1.17.0**, not latest.
Newer versions of that module bundle a newer `onnxruntime_c_api.h`
(`ORT_API_VERSION` 22+), which the system's `onnxruntime-devel` package
(ORT 1.20.1, API version 20) refuses to satisfy at runtime — it fails with
"requested API version [22] is not available". v1.13.0–v1.17.0 of the Go
module all target API version 20, matching ORT 1.20.x. If you upgrade the
system ONNX Runtime package, the Go module can likely be upgraded to match
(check `onnxruntime_c_api.h`'s `ORT_API_VERSION` in the module you're
considering against `pkg-config --modversion onnxruntime` — or, since
`onnxruntime_go` dlopens the library at runtime, just the `.so`'s own
version).
