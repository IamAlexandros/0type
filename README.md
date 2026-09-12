<div align="center">

# 0type

**Press a key. Talk. Press it again. The text is on your clipboard.**

A live voice-to-text overlay for Linux that runs entirely on your own
machine — no account, no network, no telemetry. One Go binary, no
Electron.

<img src="docs/images/hero.png" width="720" alt="0type transcribing speech in a small bar at the bottom of the screen">

</div>

```sh
curl -fsSL https://raw.githubusercontent.com/zalkanorr/0type/main/install.sh | sh
0type setup     # downloads the speech model, ~650MB, once
```

Installs into `~/.local/share/0type` and symlinks `~/.local/bin/0type`.
No sudo, nothing else touched. Uninstalling is `rm -rf` on those two.

---

## What it does

Words appear **as you speak them**, not after you stop. Audio is captured
in 16 kHz chunks, gated by a simple energy VAD, and fed to NVIDIA's
Parakeet TDT model in a growing window that's re-decoded about once a
second; successive decodes are reconciled by longest-common-prefix, so
settled words stop moving while the tail keeps updating.

The transcript starts centred and slides left as it grows, never wrapping
and never clearing. When you toggle off, the whole session goes to the
clipboard at once — one copy, not one per sentence — and the bar tells
you so before it disappears.

## Use

Run `0type`. A menu opens in the middle of the screen; the command hands
your terminal straight back, and 0type stays resident in the background.

<div align="center">
<img src="docs/images/menu.png" width="480" alt="The 0type menu: Start dictation, Theme, Close menu, Quit 0type">
</div>

Then bind a key to `~/.local/bin/0type toggle` — on GNOME: *Settings →
Keyboard → Keyboard Shortcuts → Custom Shortcuts*. That's the shortcut
you'll actually use; the menu is for everything else.

| Command | |
| --- | --- |
| `0type` | open the menu (starts 0type if it isn't running) |
| `0type toggle` | start/stop dictating — **bind this to a key** |
| `0type --background` | start resident and hidden; for autostart on login |
| `0type setup` | download the speech model |
| `0type themes` | list themes |
| `0type config` | show the resolved config and where it came from |
| `0type ui --theme X` | preview a theme without the model or microphone |
| `0type listen` | live transcription to stdout, no window |

## Themes

**Forty built in.** Pick one from the menu and arrow through the list —
each applies live as you move, Enter keeps it, Esc puts back the one you
started with.

<div align="center">
<img src="docs/images/themes.png" width="900" alt="All forty 0type themes">
</div>

A theme is one plain GTK CSS file. Copy a built-in out of `themes/` to
`~/.config/0type/themes/mine.css`, edit, and select it with
`theme = "mine"` — a file there shadows a built-in of the same name.

Besides the panel, a theme sets the colours the overlay *draws* with
(`#zt-accent`, `#zt-muted`, `#zt-success`, `#zt-meter`, `#zt-tile`), and
four things CSS can't express, via comment directives:

```css
/* 0type-idle: READY
 * 0type-copied: SAVED!
 * 0type-mark: pixel
 * 0type-art:
 * ..#####..
 * .#.###.#.
 * .#######.
 * ..#...#..
 */
```

That's how the Game Boy says `READY` instead of `Listening…`, DOS reports
`1 file(s) copied.`, and the Tamagotchi gets a creature instead of a
microphone. Preview as you write:
`0type ui --theme ./mine.css --text "hello"`.

Seven fonts ship inside the binary and are registered for 0type's process
alone — nothing is installed into your system: Press Start 2P, Silkscreen,
VT323, Caveat, DSEG14 (a real fourteen-segment display face), and Selawik
regular/bold. All SIL Open Font License; texts in `licenses/`.

## Configuration

Optional, at `~/.config/0type/config.toml`. A missing file is fine; a
*malformed* one is a loud error naming the line, rather than silently
falling back to defaults.

```toml
theme = "term"

[hooks]
on_copy = "wtype -"                        # type it into the focused window
on_stop = "notify-send 0type 'copied'"
```

### Plugins are shell commands

`on_start`, `on_final` (each finished sentence), `on_copy`, `on_stop`.
Each gets the text on stdin and in `$ZEROTYPE_TEXT`, runs asynchronously,
and is killed after 5s — a hook that fails or hangs is logged and can
never break dictation. An unknown hook name is rejected at startup rather
than silently never firing.

There's no plugin API, ABI or versioning story on purpose: the useful
extensions here are one-liners, and a shell command composes with
everything you already have.

## How it works

| | |
| --- | --- |
| **Speech** | NVIDIA Parakeet TDT 0.6B v2, int8, via ONNX Runtime — no Python or PyTorch at runtime |
| **Audio** | hand-written cgo binding to ALSA; capture on its own goroutine so a decode pass can't cause dropouts |
| **UI** | GTK4 window drawn entirely with Cairo, made override-redirect through Xlib |
| **Hotkey** | pidfile + `SIGUSR1`, because Wayland won't let an unfocused app grab a global key |

The overlay is an X11 override-redirect window rather than a Wayland
layer-shell surface because GNOME's Mutter doesn't implement layer-shell.
That turned out to be *more* portable, not less: the same code works on
GNOME, KDE and wlroots compositors, and natively on X11.

[`docs/SETUP.md`](docs/SETUP.md) has the full engineering log, including
the things that didn't work and why.

## Platform support

| | |
| --- | --- |
| **Linux / Wayland** | ✅ what it's developed on (GNOME, via Xwayland) |
| **Linux / X11** | ✅ should work unchanged — the overlay is already pure Xlib |
| **BSD** | one audio backend away; everything else is portable |
| **macOS / Windows** | needs a new window + audio layer; the engine, themes and sprites are portable Go |

Everything platform-bound lives in four files (ALSA capture, the X11 parts
of the window, fontconfig registration, and the signal/lock plumbing). The
streaming, decoding, theming and plugin packages are pure Go with no cgo
at all.

## Build from source

Needs Go 1.25+ and GTK4 / ALSA / X11 / fontconfig development packages:

```sh
# Fedora
sudo dnf install golang gtk4-devel alsa-lib-devel libX11-devel \
                 fontconfig-devel onnxruntime-devel
make build && ./bin/0type setup
```

`make test` runs the suite; `make dist` builds the release tarball. See
[`docs/SETUP.md`](docs/SETUP.md) for the toolchain notes.

## License

Code under the MIT license. Bundled fonts are SIL Open Font License 1.1,
with their license texts in `internal/ui/fonts/` and shipped in
`licenses/`. The speech model is downloaded at setup time and carries its
own license from its publisher.
