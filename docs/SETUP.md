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
image regardless of what's on screen; `import -window <id>` from
ImageMagick, targeting the specific window rather than the whole screen,
worked when those didn't):

```sh
./bin/0type ui &
DISPLAY=:0 xdotool search --onlyvisible "" | while read -r id; do
  DISPLAY=:0 xdotool getwindowpid "$id"   # match against the 0type pid
done
DISPLAY=:0 xdotool getwindowgeometry <id> # should show centered x, near the bottom
DISPLAY=:0 wmctrl -l                      # 0type must NOT appear here
DISPLAY=:0 import -window <id> out.png    # actually see it
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

## The full app: show/hide, and two more real bugs found live

```sh
./bin/0type &          # loads the model once, starts hidden, writes a pidfile
./bin/0type toggle      # show/hide the window -- starts/stops mic capture with it
```

For a global hotkey (Wayland doesn't let an unfocused app grab one itself):
GNOME Settings → Keyboard → Keyboard Shortcuts → Custom Shortcuts → add a
shortcut whose command is the **full path** to the binary plus `toggle`
(custom shortcuts don't reliably inherit your shell `PATH`), e.g.
`/home/you/projects/0type/bin/0type toggle`.

Testing the full pipeline against real audio (via a PipeWire loopback --
`pactl load-module module-null-sink`, set as the default source, `paplay`
a WAV into it) surfaced two further real bugs, both now covered by
regression tests:

- **A `Partial` could blank out already-good text.** A later decode pass
  over the same growing segment can legitimately return `""` (more
  accumulated silence/noise shifting the model's read of ambiguous audio),
  but `Feed` was emitting that as a `Partial` event anyway, overwriting
  correct displayed text with nothing. Fixed by only ever emitting `Partial`
  on non-empty text, mirroring how `Final` already worked. Regression test:
  `TestRunner_PartialNeverRegressesToBlank`.
- **The segment buffer grew without bound during silence.** `Feed` appended
  *every* chunk to the segment unconditionally, including chunks arriving
  while `VAD.speaking` was `false` (i.e. genuine between-utterance idle
  silence, not just the trailing hangover window). After a finalize+reset,
  any further silence — which is most of real listening time — kept getting
  appended to the now-empty segment forever, since there was no more speech
  left to ever trigger another end-of-utterance. Live, this pegged the CPU
  (~780% cumulative) within a couple of minutes and froze the displayed text
  in place, since each decode pass took longer than the last as the buffer
  grew. `VAD.Update` now returns a third `active` value (speaking, or still
  within the hangover grace window) and `Feed` only accumulates audio when
  `active` is true — genuine idle silence is discarded, not buffered.
  Regression tests: `TestVAD_SilenceAfterUtteranceIsNeverActive`,
  `TestRunner_SegmentDoesNotGrowDuringSilenceAfterUtterance`.

Both were invisible in isolated unit tests (which don't run long enough
against enough silence) and only surfaced running the real app against real
audio for more than a few seconds — worth remembering if something *feels*
fine in `go test` but not in practice.

## Theme design notes (bottom-anchored, glow, font)

Positioned at the bottom-center now (`bottomMarginPx` in `internal/ui`),
smaller than the original Phase 4 version.

**How the transcript animates.** The final design: text starts centered: as
more words come in it smoothly slides left, oldest words sliding off the
left edge first, newest words always visible on the right, and the panel
itself never resizes or jumps. Two earlier versions were tried and replaced
after live feedback:

1. A wrapping label that flashed/dimmed via a toggled CSS class +
   `transition: opacity` on every update. Felt bad, and wrapping meant the
   box kept resizing as text grew.
2. A fixed-width single-line label using `PANGO_ELLIPSIZE_START` (Pango
   truncates the text's *start*, not end). No flash, no resize -- but only
   ever an instant jump-cut to the new truncation, not the requested
   smooth sliding motion.

The real animation needs actual per-frame motion, which meant a genuine
animation loop, not a CSS/Pango property. That surfaced a GTK sizing trap
**twice** before landing on something that actually works, worth knowing if
you touch this code:

- First attempt: `GtkFixed` sized via `gtk_widget_set_size_request`, with
  the label positioned inside via `gtk_fixed_move` each frame. Looked
  right in isolation, but `GtkFixed`'s own measure() unions its children's
  *full* extent into its reported natural size regardless of
  `size_request` -- same "request is a floor, not a ceiling" trap hit
  elsewhere in this file (window sizing, label max-width-chars) -- so a
  long sentence grew the whole panel to fit it.
- Second attempt: wrapped that `GtkFixed` in a `GtkScrolledWindow` with
  `propagate-natural-width/height` off and scrollbars disabled
  (`GTK_POLICY_NEVER`), reasoning that a scrolled window is specifically
  built to decouple its own size from its child's. That assumption turned
  out to only be partially true in practice -- some sizing still leaked
  through (traced to the label's own *minimum* width, which for a
  non-ellipsizing label equals its full natural width) -- and forcing the
  label ellipsizable to shrink that minimum fixed the leak but broke the
  animation itself (the label then actually got allocated a *small* size
  and rendered a real Pango "…" instead of being positioned/clipped by us).

**What actually works:** skip GTK's container/child size negotiation
entirely. `internal/ui/window.go`'s `SlideState` is a `GtkDrawingArea`
sized only via `gtk_drawing_area_set_content_width/height` -- fixed,
always, because it has no children for anything to leak from -- with the
text painted directly via Cairo/Pango in a draw function
(`gtk_widget_create_pango_layout` for CSS-correct font, `gtk_widget_get_
color` for CSS-correct color) at an x offset that a per-frame
`GtkTickCallback` (`slide_tick`) eases toward a target (`slide_retarget`,
called from `SetText`) using frame-clock-timed exponential smoothing --
genuinely smooth, not an instant snap, and automatically continuous even
if the target changes again before a previous move finishes. Centered
while the text fits; once it doesn't, the target pins the text's right
edge to the viewport's right edge, so growth reads as the sentence sliding
left. The drawing area's own bounds are the only clip -- no ellipsis, no
container tricks, nothing to leak.

One rendering gotcha worth knowing if you touch `themes/*.css`: **GTK clips
`box-shadow` hard at the window's own edge.** `#zt-panel` used to fill the
window edge-to-edge, so its glow got cut off flat instead of fading out —
looked broken. Fixed with `margin: 44px` on `#zt-panel`, inset from the
window boundary, giving the shadow room to fall off before hitting the
clip edge. `internal/ui`'s `windowWidth` constant is a floor, not the
visual panel width, precisely because of this — the window's real natural
size ends up wider once that margin is included, and previous experience
in this file (see the label-wrapping gotcha in Phase 4's section) is why we
let natural sizing win rather than fighting it.

`#zt-label`'s `font-family` lists `"Inter"` first (matches Raycast's own
look) with a graceful fallback chain (`Cantarell`, `"Noto Sans"`, generic
`sans-serif`) — Inter isn't installed on this dev machine, so it currently
renders in Cantarell (GNOME's default UI font) at `font-weight: 600`; no
font is bundled or required. If you want Inter specifically, install a font
package that provides it and it'll be picked up automatically, no code
change needed.

## Intro animation and the idle state

**A real bug, not just a taste call:** the intro animation "wasn't seen
properly" because `Show()` set the panel's starting opacity to 0 only
*inside* the delayed callback (`schedule_show_animation`'s
`repositionDelayMs` wait), but made the window visible *before* that --
so for the whole delay the window was fully opaque at whatever position
it last had, then suddenly jumped to the animation's start state. The user
saw a flash-then-jump, not a fade. Fixed by setting opacity to 0
synchronously in `Show()`, before `widget_set_visible`, so the window is
invisible for the entire wait and the fade is the first thing ever seen.
Verified by capturing frames at 50ms/200ms/350ms/650ms after toggle-on:
50ms is fully blank (confirming no premature flash), later frames show the
fade progressing and then settled -- not an instant snap.

**Idle state:** the static "0type is listening…" label (and, in an
earlier version, a pulsing dot -- also didn't land well) is now a bigger,
bold, muted-gray "0type" wordmark (`slide_draw_idle` in
`internal/ui/window.go`), static rather than animated, closer in tone to
the panel's own dark background than to the bright transcript text so it
reads as a quiet watermark. `viewportHeightPx` was bumped from 22 to 36 to
give the bigger idle font room without clipping (both idle and transcript
content are vertically centered within whatever height this is, so
transcript rendering is unaffected). Shown via `ui.New("")` /
`SetText("")`; replaced by real sliding text the moment the first
`Partial` or `Final` arrives.

**Transcript text gradient:** `slide_draw_text` paints the sliding
transcript with a horizontal Cairo gradient instead of a flat color: muted
gray for roughly the first sixth of the box, sharpening to the normal
CSS-resolved text color from there to the right edge. Since older words
sit toward the left as the line slides (see `slide_retarget`), this reads
as those words quietly fading into the past rather than being cut off by
a hard clip edge. Only visible once text has grown past centered (a short,
centered phrase never reaches the gradient zone) -- confirmed via captured
frames showing a plain centered phrase with no visible gradient, then the
same session's growing sentence with a clearly graduated left edge once it
overflowed.

## Output: one continuously growing display, one clipboard write at close

0type doesn't expect you to select/copy the displayed text yourself, and
the display isn't meant to reset every time VAD decides one sentence ended
-- both were wrong in an earlier version, fixed after live feedback:

- **Display:** every event (`Partial` or `Final`) redraws the overlay as
  *everything finalized so far this session* plus *the sentence currently
  in progress*, combined into one string (`a.dictated` plus the live
  partial, in `cmd/0type/app.go`'s `runPipeline`) -- so the on-screen line
  keeps growing and sliding across the *whole* session, multiple sentences
  included, rather than snapping back to a blank slate each time one
  sentence finalizes.
- **Clipboard:** `ui.SetClipboard` (wrapping `gdk_clipboard_set_text` on
  the default display's `GdkClipboard`) is called exactly **once**, in
  `hideAndStop` -- i.e. only when the session actually ends (toggle-off) --
  with the full `a.dictated` accumulated over the session, not after every
  sentence. `a.dictated` resets to `""` at the start of each new session
  (`showAndListen`).

Verified end to end via the PipeWire loopback harness across two sentences
in one session: display confirmed to keep growing across the sentence
boundary (captured frames showing the first sentence's tail followed
immediately by the second sentence's partial, no reset in between),
clipboard confirmed empty (`xclip -selection clipboard -o` /
`xsel -b`) for the entire session and populated with both sentences,
space-joined, only after toggling off.

## Idle wordmark font fix, and the outro animation

**Idle wordmark was using the wrong font.** `slide_draw_idle` originally
built its `PangoFontDescription` via `pango_font_description_new()`, which
starts with *no family set at all* -- Pango falls back to its own generic
default rather than the CSS-resolved family (`"Inter"`/Cantarell/etc) used
everywhere else, so the bigger "0type" wordmark looked visibly
inconsistent with the rest of the UI. Fixed by copying the widget's actual
resolved font first (`pango_font_description_copy(pango_context_get_font_
description(gtk_widget_get_pango_context(area)))`) and overriding only the
size on that copy, plus a touch of letter-spacing for a more deliberate
wordmark feel. Verified visually against a silent loopback session (no
real speech to contaminate the idle state) -- font now matches the
transcript text's family and weight, just bigger.

**Outro animation:** closing used to just vanish the window instantly.
Now, in `hideAndStop` (`cmd/0type/app.go`): capture stops immediately
either way, but if there's nothing to copy the window just plays its
closing animation (`Window.Hide`, now the mirror of the intro -- fade out
while easing *down* by `showAnimRisePx`, via `hide_anim_tick` /
`start_hide_animation` in `internal/ui/window.go`) right away; if there
*is* something to copy, the clipboard is written, the display switches to
a solid "✓ Copied to clipboard" in soft accent green
(`ShowCopiedConfirmation` / `slide_draw_confirmation` -- no gradient, this
isn't sliding, it's a short-lived status message), held for
`copiedConfirmationHold` (850ms, via `time.AfterFunc`) so it's actually
readable, and only then does the same closing animation play.

Debugging note: an early attempt at verifying this over the PipeWire
loopback harness produced confusing, seemingly-wrong toggle sequences in
the app's own logs -- traced to leftover *backgrounded shell test
commands* from earlier iterations still pending and firing once a freshly
relaunched process became available (stale test-harness processes, not
stale application state). Re-run cleanly (every command foregrounded, no
`&`, explicit `pkill` between iterations) the sequence was exactly as
designed: `hideAndStop` logged the correct accumulated text, confirmation
shown, hold elapsed, hide triggered, and the window was confirmed
genuinely unmapped afterward (absent from
`xdotool search --onlyvisible`, not just visually faded).

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

## The bar layout (idle state rethink)

Centering a wordmark in an empty pill read as a splash screen, not a
tool, however it was set (plain, bold, tracked caps, display font -- all
tried). The bar is now laid out like Raycast's, with three regions that
stay put in every state (`internal/ui/window.go`, `slide_draw` and the
`draw_mark` / `draw_content` / `draw_bars` helpers):

- **Left, brand mark:** the "0" in a small rounded tile -- an icon-sized
  mark, in Adwaita Mono because its dotted zero reads as a *digit* (the
  name is a pun on zero; proportional faces draw an oval that reads as
  "O"). Turns into a green check during the "Copied" confirmation.
- **Center, content area:** muted "Listening…" placeholder while idle;
  the sliding transcript (centered until it overflows, then right-pinned,
  with a short left-edge fade) while dictating; "Copied to clipboard" at
  the end. Clipped to its own region so text never runs under the mark
  or the meter.
- **Right, level meter:** three thin bars driven by the *real* mic level
  (`Window.SetLevel`, fed per chunk from `runPipeline`, eased per frame in
  `slide_tick`). A live affordance that it's listening, not a decorative
  pulse.

Theme is Raycast-restrained (`themes/default.css`): a lifted near-black
translucent pill, neutral hairline, 1px inset top highlight for the glass
rim, depth from a layered black shadow rather than a colored glow; the
periwinkle accent survives only as a faint wash and the mark's "0".

## Theming (Phase 6)

Themes are GTK CSS files, resolved by `internal/theme`:

1. an explicit path (`--theme ./mine.css`, or anything containing `/` or
   ending `.css`),
2. `~/.config/0type/themes/<name>.css`,
3. a built-in, embedded in the binary with `go:embed`.

A user file shadows a built-in of the same name, so tweaking a bundled
theme is copy-edit-select rather than patching 0type. A bare name is
never resolved against the working directory — a stray `default.css`
lying around must not quietly become the theme.

**The embedding is a bug fix, not a nicety.** The previous code called
`win.LoadCSS("themes/default.css")` — a path relative to the *working
directory*. Launched from the project checkout it looked right; launched
from a GNOME keyboard shortcut (working directory `$HOME`) it silently
rendered unstyled. Embedding removes the failure mode entirely, and
`LoadCSS` now takes bytes.

### Making the drawn content themeable

The bar's contents are painted with Cairo, which knows nothing about CSS,
so every color was a constant compiled into the binary — a "theme" could
only restyle the panel *behind* the content. GTK4 has no API to read an
arbitrary CSS property off a widget, and `gtk_style_context_lookup_color`
(which would read `@define-color`) is deprecated.

What works: **color probes**. `new_color_probe` adds an invisible
`GtkLabel` to the panel per themeable color, named `#zt-accent`,
`#zt-muted`, `#zt-success`, `#zt-meter`, `#zt-tile`; the theme sets
`color` on those selectors, and the drawing code reads it back with the
non-deprecated `gtk_widget_get_color()`. Invisible children are skipped
during layout, so they cost nothing, but they must be in the window's
hierarchy or GTK never computes their style. Colors are read per-draw, so
a theme applied later takes effect without rebuilding anything.

Verified by rendering all four built-ins and confirming the *drawn* parts
(mark, meter, tile, idle text) change with the theme, not just the panel.

### The mark directive

Which brand mark to draw isn't expressible in CSS — there's no property
whose value is "a shape 0type knows how to paint". The tricks that would
smuggle one through (encoding the choice in a `font-family`, or a
`min-width` on a dummy widget) are unreadable in the theme file and
untestable without a live display. So themes declare it in a comment:

```css
/* 0type-mark: pixel */
```

CSS ignores it, `internal/theme` parses it in pure Go (so it's unit
tested), and an unknown value is a loud error rather than a silent
fallback to the default. Marks: `mic` (default), `pixel`, `zero`.

`theme.Marks()` and `ui.Marks` declare the same three names in two
packages — making the pure-Go theme package depend on the cgo/GTK one to
share three strings costs more than it saves. `cmd/0type`'s
`TestMarkListsAgree` fails if they drift.

### Pixel art

The `term` theme's 8-bit mic is a bitmap (`MARK_PIXEL_MIC`), not the
vector mark scaled down — shrinking a smooth path is precisely how you
lose the hard square pixels that make it read as 8-bit. Cells are 2.0
logical px so the grid lands on whole device pixels at this display's
scale factor 3.

Three drafts failed before one read as a microphone, which is worth
recording because the failures were not obvious on paper:

- 5-wide capsule + detached arms → a blob with floating dots.
- Narrow head + stem + wide foot → a chess pawn (head and stem fuse into
  one silhouette when the stem sits directly under the head).
- Head + cradle + equal-width base bar → furniture; two 5-wide bars with a
  stem between them read as a chair.

What works: cradle arms running *alongside* the head (not below it), and
a stand that narrows on the way down — cradle 5 wide, stem 1, foot 3.

Preview flags exist for exactly this loop: `0type ui --theme X
--text "..." --level 0.6 --confirm` renders any state without loading the
model or opening the microphone.

## Plugin hooks (Phase 7)

"Plugin" means a shell command in the config file, not a loadable module.
0type's extension points are few and the interesting ones are one-liners
(`on_copy = "wtype -"` types the dictation into the focused window), so a
command with the text on stdin composes with everything already installed
and needs no plugin API, ABI, or versioning story.

Hooks: `on_start`, `on_final` (per finished sentence), `on_copy`,
`on_stop`. Each gets the text on stdin and in `$ZEROTYPE_TEXT`, runs via
`sh -c` on its own goroutine, and is killed after 5s. Failures are logged
and swallowed — a hook must never break or delay dictation, which is what
the timeout and async tests pin down.

Unknown hook names are rejected at startup rather than ignored: a
misspelled hook that silently never fires is indistinguishable from a
broken one.

## Config file

`~/.config/0type/config.toml`, entirely optional — a missing file yields
the same defaults as an empty one. A *malformed* file is a hard error;
falling back to defaults because of a typo leaves the user staring at an
overlay that ignores their settings with no clue why.

The parser (`internal/config/toml.go`) handles the subset actually used:
comments, `[section]`, and `key = "value"`. It is deliberately strict —
numbers, arrays, unknown keys, and unknown sections are errors naming the
line. Rationale: the whole config surface is a handful of strings, so a
parser that *cannot* silently misread is worth more than one that accepts
every valid TOML document, and it keeps the dependency list at the one
library genuinely needed (ONNX Runtime). Files written to this subset are
valid TOML, so a real library could be swapped in later.

## Packaging (Phase 8)

`make dist` stages a tarball: the binary, ONNX Runtime, the theme CSS as
editable files, an installer, and the README.

Deliberately **not** a single static executable. Two of the three native
dependencies (GTK4, ALSA) are on every Linux desktop already and should
come from the system; the third (ONNX Runtime) is neither universally
installed nor version-stable, so it ships alongside and is found relative
to the binary at runtime (`bundledLibraryPaths`, searched *before* system
paths — a bundle that silently ran against the host's different version
would be a bundle in name only). Resolution follows the `install.sh`
symlink via `EvalSymlinks`, so `~/.local/bin/0type` still finds
`~/.local/share/0type/lib`.

The ~650MB model isn't in the tarball; `0type setup` fetches it once.
Result: 9.7MB.

Verified end-to-end rather than assumed: extract to a temp prefix, run
`install.sh`, execute the symlink from `/`, and confirm via
`/proc/<pid>/maps` that the *bundled* library is the one mapped.

## The menu (running `0type` with no arguments)

Running `0type` opens a menu in the middle of the screen — Start
dictation, Theme, Quit — driven with the arrow keys. The dictation bar
still lives at the bottom of the screen; the menu is centered, because
one is meant to stay out of the way of what you're typing into and the
other is the thing you're looking at.

It's the same window and the same drawing area, in a different mode, not
a second window: the panel's position, intro/outro animation, theming and
override-redirect setup are all attached to this one window, and a
separate menu window would have to reimplement every one of them to look
like it belonged to the same program.

### Getting keyboard input at all

This was the risky unknown, and worth probing before building anything on
top of it: an override-redirect window is invisible to the window manager
by design (that's what keeps the overlay out of the taskbar and
alt-tab), and the flip side is that nothing ever gives it keyboard focus.

Three things each had to be right, and each failed first:

1. **`XSetInputFocus` alone doesn't work**, because Mutter decides which
   X client holds focus and has no reason to pick a window it isn't
   managing. An `XGrabKeyboard` takes the keyboard regardless — the same
   thing every X11 popup menu does.
2. **Calling it too early is fatal.** `XSetInputFocus` on a window that
   isn't viewable yet is a `BadMatch`, and GDK's default X error handler
   turns that into an immediate exit. The window isn't viewable for the
   first frames after Show, which is exactly when a menu wants the
   keyboard, so the grab checks `map_state` first and the caller retries
   for a short while.
3. **GTK dropped the events anyway**, because a key controller defaults
   to the bubble phase, which propagates up from the focused widget — and
   this window deliberately contains nothing focusable. Installing the
   controller in the *capture* phase (top-down from the toplevel) needs
   no focus widget.

A grab can still legitimately fail if something else holds one (another
popup, a system dialog — a GNOME permission prompt did exactly this
during development). The menu closes itself in that case rather than
sitting there undismissable, and the grab is released whenever the window
hides.

Note for testing: don't verify this with `xdotool key`. Injecting
synthetic input via XTEST makes GNOME prompt for remote-desktop
permission, and while that prompt is up it holds a keyboard grab — which
both blocks the keys you were trying to send and makes 0type's own grab
fail. Press the keys by hand.

### Resizing the window between modes

The menu is taller than the bar, and simply growing the `GtkDrawingArea`
was not enough: the extra rows drew *outside* the panel's background,
past the bottom of the window. GTK settles a toplevel's size when the
surface is realized -- which `prepare_overlay` does at startup, while the
panel still holds a one-line bar -- and won't renegotiate it afterwards.
`gtk_window_set_default_size` doesn't move an already-realized window
either.

What works: measure the panel (`gtk_widget_measure` includes its CSS
margin, border and padding), then `XResizeWindow` the surface directly,
scaled by `gdk_surface_get_scale_factor`. GDK picks the new size up from
the resulting ConfigureNotify and GTK reallocates the panel to match.
That's consistent with how this window is handled everywhere else --
its position is already driven in raw X11 coordinates for the same
reason.

### Live theme preview

Selecting a theme in the menu applies it immediately, which turned up a
latent bug: `load_css` used to add a *new* `GtkCssProvider` on every
call, so stylesheets stacked. The newest wins wherever two themes set the
same property, but anything the old theme set and the new one doesn't
would linger forever. Invisible when the theme is only loaded once at
startup; immediately visible when switching live. There is now one
provider, reloaded in place.

Backing out of the theme list with Esc restores the theme that was active
when it was opened -- browsing shouldn't silently change your setup --
and Enter writes the choice to the config file, rewriting only the
`theme` line so comments and hooks survive (`config.SetTheme`).

### One instance

Running `0type` while it's already running sends SIGUSR2 to the existing
process (which shows the menu) and exits, rather than starting a second
copy: the model takes seconds to load and hundreds of MB to hold. A
pidfile left behind by a crashed instance is treated as "not running" --
signalling a PID that has since been reused by something else would be
worse than starting a second copy.

### The menu's keyboard grab breaks the global shortcut (and what's done about it)

Driving the menu needs an `XGrabKeyboard`. While that grab is held, the
compositor never sees key presses -- which means 0type's own GNOME
shortcut stops working, because it's Mutter that runs
`0type toggle`. Leaving a menu open on screen therefore breaks the main
way the program is used. This showed up immediately in practice: the app
was left running with its startup menu open, and the shortcut appeared
dead. (The toggle signal path was fine; `0type toggle` returned 0 and the
window appeared. The key was simply never reaching the compositor.)

Two mitigations, both of which are about limiting how long the grab can
be held rather than avoiding it:

- An idle menu closes itself after 20 seconds, releasing the keyboard.
- Any key the menu doesn't recognize closes it. A key meant for another
  window is swallowed by the grab regardless, so dismissing on the first
  unrecognized press costs one keystroke instead of leaving the keyboard
  captured until somebody finds the menu and presses Escape.

Note that this rules out "type to filter" in the menu without first
solving the focus problem differently -- e.g. making the window a normal
managed window while the menu is up, so Mutter focuses it and keeps
handling its own keybindings, instead of grabbing.
