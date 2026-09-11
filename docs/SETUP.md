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

Not yet implemented (Phase 2). Will fetch a Parakeet TDT ONNX export
(`istupakov/parakeet-tdt-0.6b-v2-onnx` from HuggingFace) into
`~/.cache/0type/models/`.
