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
sudo dnf install gtk4-devel libX11-devel onnxruntime-devel alsa-lib-devel
```

- `gtk4-devel` / `libX11-devel` — the floating overlay window, via a small
  hand-written cgo shim (`internal/ui`). See "Overlay positioning: why not
  layer-shell" below for why this needs libX11, not gtk4-layer-shell.
- `onnxruntime-devel` — not required to *build* (the Go ONNX Runtime binding
  dlopens `libonnxruntime.so` at runtime), but convenient to have installed on
  a dev machine so the shared library is present without a manual download.
- `alsa-lib-devel` — microphone capture via a small hand-written cgo shim
  (`internal/audio`).

## Overlay positioning: why not layer-shell

The original plan for `internal/ui` was gtk4-layer-shell (the Wayland
layer-shell protocol), which gives an app pixel-perfect anchoring and
guaranteed no-taskbar/no-alt-tab presence. That plan changed after actually
testing it: **Mutter (GNOME's compositor) has never implemented
wlr-layer-shell** — a long-standing (7+ year), still-open upstream issue
([mutter#973](https://gitlab.gnome.org/GNOME/mutter/-/issues/973)), true as
of the current Mutter release. Since GNOME/Mutter is a primary real-world
target, layer-shell was a dead end here, confirmed directly: on this dev
machine (GNOME Shell 48.8), `gtk_layer_init_for_window` fails outright
("compositor doesn't support Layer Shell"). wlroots compositors (Sway,
Hyprland, river) and KDE/KWin do support it, but GNOME does not.

Instead, `internal/ui` runs the GTK4 window through **Xwayland** (GDK's X11
backend, forced via `GDK_BACKEND=x11` set from Go before `gtk_init_check`)
and uses **plain Xlib** to mark the window override-redirect and position it
in screen coordinates — the same decades-old technique X11
launchers/OSDs/menus have always used to bypass window-manager placement and
decoration entirely. This turns out to be *more* portable than layer-shell
would have been: it works on GNOME as well as every wlroots compositor and
KDE, anywhere Xwayland is present (effectively everywhere on Linux desktops).
The tradeoff is running through an X11 compatibility layer rather than a
native Wayland surface — invisible in practice, but worth knowing.

Two gotchas hit while building this, both now handled in
`internal/ui/window.go`:

- **GTK doesn't finalize real window size synchronously.** Right after
  `gtk_widget_realize`, `gtk_widget_get_width/height` read `0x0` — the real
  size is only known after the main loop's first layout pass (empirically,
  well under 500ms; a 150ms delay is used with margin). Positioning happens
  in a delayed callback (`schedule_reposition`), not inline.
- **Scaled sessions mean GDK's logical size ≠ the raw X11 window size.**
  This dev machine runs at X11/Wayland scale factor 3 (3840x2400 physical
  output). GDK's own size accessors report *logical* pixels (e.g. 482x66),
  but raw Xlib calls like `XMoveWindow` need *physical* pixels (that window
  was actually 1446x198 = exactly 3x). Centering math must query the
  window's actual raw geometry via `XGetWindowAttributes` and use *that*,
  not GDK's logical size or an assumed width, or positioning will be off by
  the scale factor.

Verify positioning/no-taskbar behavior without relying on a screenshot
(useful in a headless/remote session where screen capture may not work at
all — confirmed on this dev machine, `grim`/`scrot` both return a black
image regardless of what's on screen):

```sh
./bin/0type ui &
DISPLAY=:0 xdotool search --onlyvisible "" | while read -r id; do
  DISPLAY=:0 xdotool getwindowpid "$id"   # match against the 0type pid
done
DISPLAY=:0 xdotool getwindowgeometry <id> # should show centered x, y=64
DISPLAY=:0 wmctrl -l                      # 0type must NOT appear here
```

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

## Streaming (live mic)

```sh
./bin/0type listen
```

Captures from the default mic and prints `partial:`/`final:` lines live as
you speak (Ctrl+C to stop). This is v1's streaming approximation (see
`internal/stream`): audio accumulates per-utterance, gets re-decoded every
~1s while you're speaking, and a simple dBFS-threshold VAD with a hangover
period decides when an utterance is over. True frame-level cache-aware
streaming (NVIDIA's lowest-latency mode) is a possible future upgrade, not
implemented here.

To debug the streaming/VAD pipeline without a live mic, replay a WAV file
through the exact same code path:

```sh
./bin/0type debug stream-file testdata/hello.wav
```

Known v1 limitation: very short segments can occasionally make the model
hallucinate a filler word (e.g. "Mm.") on an early partial. This is cosmetic
— it gets overwritten by the next decode pass and never affects the final
result — and is only partially mitigated by `Config.MinSamplesForPartial`
(see `internal/stream/stream.go`).

**Debugging note:** while building this, a real bug was found and fixed —
the original capture loop called the (hundreds-of-ms) decode pass
synchronously in the same loop as ALSA reads, which overran the capture
device's small hardware buffer and silently corrupted/truncated transcripts.
Audio capture now runs on its own goroutine (`audio.StreamChunks`),
decoupled from decode timing via a buffered channel. The regression test
for this lives at `internal/stream/integration_test.go`.

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
