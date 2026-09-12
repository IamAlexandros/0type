# 0type

A live voice-to-text overlay for Linux. Hit a key, talk, and watch the
words appear in a small bar at the bottom of the screen; hit the key
again and the whole thing is on your clipboard.

Everything runs locally — NVIDIA's Parakeet TDT speech model via ONNX
Runtime, no network, no account, no cloud. One Go binary, no Electron.

```
  ┌──────────────────────────────────────────────────┐
  │  🎙   the quick brown fox jumps           ▍▍▍    │
  └──────────────────────────────────────────────────┘
```

## Install

From a release tarball:

```sh
tar xzf 0type-*-linux-x86_64.tar.gz
cd 0type-*
./install.sh
0type setup        # downloads the speech model, ~650MB, once
```

From source (needs Go 1.25+, and GTK4/ALSA/ONNX Runtime development
packages — see [docs/SETUP.md](docs/SETUP.md)):

```sh
make build
./bin/0type setup
```

## Use

Run it:

```sh
0type
```

A menu appears in the middle of the screen: **Start dictation**, **Theme**,
**Close menu**, **Quit 0type**. Drive it with ↑/↓, Enter, and Esc.

The command returns your terminal straight away — 0type starts itself in
the background and stays there, so running `0type` again just brings the
menu back rather than loading the model a second time. *Close menu* puts
the menu away and leaves it running; *Quit 0type* stops it entirely, so
the next shortcut press has to load the model again.

To have it ready from login, autostart `0type --background`: resident and
invisible, with nothing on screen and the microphone closed until you
press the shortcut.

Then bind a key to `0type toggle` — on GNOME: *Settings → Keyboard →
Keyboard Shortcuts → Custom Shortcuts*, with the command set to the full
path (`~/.local/bin/0type toggle`).

Press it, speak, press it again. The bar disappears, and the session's
text is on your clipboard, ready to paste.

| Command | What it does |
| --- | --- |
| `0type` | open the menu, starting 0type if needed (add `--theme NAME`) |
| `0type --background` | start resident and hidden — for autostart |
| `0type toggle` | show/hide the running overlay — bind this to a key |
| `0type setup` | download the speech model |
| `0type themes` | list available themes |
| `0type config` | show the resolved config and where it came from |
| `0type ui --theme X` | preview a theme without the model or microphone |
| `0type listen` | print live transcription to stdout, no window |

## Configuration

Optional, at `~/.config/0type/config.toml`:

```toml
theme = "term"

[hooks]
on_copy = "wtype -"          # type the text into the focused window
on_stop = "notify-send 0type 'copied'"
```

Hooks are shell commands. They get the text on stdin and in
`$ZEROTYPE_TEXT`, run asynchronously, and can't break dictation — a hook
that fails or hangs is logged and killed. The hooks are `on_start`,
`on_final` (each finished sentence), `on_copy`, and `on_stop`.

## Themes

Eighteen are built in.

*Plain:* `default` (dark, glassy), `light`, `mono` (grayscale).

*Terminals:* `term` (green phosphor), `amber` (amber CRT), `dos` (CGA text
mode), `c64` (the Commodore boot screen).

*Desktops:* `win98`, `winxp` (an actual window, title bar and all),
`vista` (Aero glass), `macclassic` (System 6, black on white), `aqua`
(Mac OS X lozenge), `discord`.

*Devices:* `gameboy`, `nokia3310`, `tamagotchi`, `winamp`, `xbox`.

They set their own wording too, so the Game Boy says READY and SAVED!
where DOS says `C:\>` and `1 file(s) copied.`

The quickest way to switch is the menu: run `0type`, pick **Theme**, and
arrow through the list — each one applies live as you move, Enter keeps it
(saved to your config), Esc puts back the one you started with.

Themes are plain GTK CSS. To write one, copy a built-in from `themes/` in
the tarball (or `internal/theme/themes/` in the source) to
`~/.config/0type/themes/mine.css`, edit, and select it with
`theme = "mine"`. A file there shadows a built-in of the same name.

Besides the panel itself, a theme sets the colors the overlay draws with —
`#zt-accent`, `#zt-muted`, `#zt-success`, `#zt-meter`, `#zt-tile` — and
may set four things CSS can't express, via comment directives:
`0type-mark: mic|pixel|zero`, `0type-idle: READY`, `0type-copied: SAVED!`,
and its own sprite — up to 16×16, drawn in the comment itself:

```css
/* 0type-art:
 * ..#####..
 * .#.###.#.
 * .#######.
 * ..#...#..
 */
```

That's how the Tamagotchi gets a creature instead of a microphone, the
Xbox its jewel, Winamp its bolt, and DOS its prompt.

Five fonts are embedded in the binary and registered for 0type alone
(nothing is installed into your system): Press Start 2P, Silkscreen,
VT323, and Selawik regular/bold — all SIL Open Font License, with the
license texts in `licenses/` in the tarball.
Preview as you go with `0type ui --theme ./mine.css --text "hello"`.

## How it works

Audio comes off ALSA in 16kHz chunks, energy-gated by a simple VAD, and
fed to Parakeet in a growing window that's re-decoded about once a
second; successive decodes are reconciled by longest-common-prefix so
settled words stop moving. The overlay is a GTK4 window drawn with Cairo,
made an override-redirect X11 window through Xwayland — GNOME's Mutter
doesn't implement the Wayland layer-shell protocol that would be the
right way to do this.

See [docs/SETUP.md](docs/SETUP.md) for the full picture, including the
things that didn't work.
