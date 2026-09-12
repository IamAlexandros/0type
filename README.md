<div align="center">

# 0type

### Press a key. Talk. Press it again. Your words are on the clipboard.

<img src="docs/images/hero.png" width="720" alt="0type transcribing speech in a small bar at the bottom of the screen">

Voice typing for Linux that runs entirely on your own machine.<br>
No account, no subscription, no internet. Nothing leaves your computer.

</div>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/zalkanorr/0type/main/install.sh | sh
```

That's it. It installs 0type, downloads the speech model, starts it, and
offers to launch it when you log in. Then it tells you the one thing left
to do: pick a key to talk with.

<sub>Everything goes in `~/.local` — no sudo, nothing outside your home
folder. Uninstalling is deleting two directories.</sub>

<div align="center">

<a href="https://fetchlayer.dev"><img src="docs/images/sponsor-fetchlayer.png" width="820" alt="Sponsored by FetchLayer - every social platform, one structured API"></a>

0type is sponsored by **[FetchLayer](https://fetchlayer.dev)** — structured social
data from every platform, through one API.

</div>


## Using it

**Press your key.** A small bar appears at the bottom of the screen.

**Talk.** The words show up as you say them, not after you stop.

**Press it again.** The bar disappears and everything you said is on your
clipboard, ready to paste.

That's the whole thing. It works in any app — your editor, a browser, a
chat box — because it's just the clipboard.

Run `0type` on its own for a menu:

<div align="center">
<img src="docs/images/menu.png" width="480" alt="The 0type menu">
</div>

## Forty themes

<div align="center">
<img src="docs/images/themes.png" width="900" alt="All forty 0type themes">
</div>

Open the menu, pick **Theme**, and arrow through them — each one applies
instantly as you move, so you can see them all in about ten seconds.
Enter keeps it, Esc puts back the one you started with.

They're more than colours. The Game Boy says `READY` and `SAVED!`, DOS
answers with `1 file(s) copied.`, the Commodore 64 boots to `READY.`, and
the Tamagotchi has a little creature living in it. The fonts are real
too — genuine pixel type, a fourteen-segment calculator display, actual
handwriting on the Polaroid.

### Making your own

A theme is a single CSS file. Copy one you like from `themes/` into
`~/.config/0type/themes/`, change the colours, and select it in the menu.
Yours appears in the list next to the built-in ones.

```css
#zt-panel  { background-color: #1a1a21; border-radius: 26px; }
#zt-label  { color: #ecedf5; font-size: 15px; }
#zt-accent { color: #7a8cff; }

/* 0type-idle: whenever you're ready
 * 0type-copied: got it
 */
```

Preview it while you work, without touching your microphone:

```sh
0type ui --theme ./mine.css --text "hello there"
```

## Settings

Optional, in `~/.config/0type/config.toml`:

```toml
theme = "term"

[hooks]
on_copy = "wtype -"                      # type it out instead of copying
on_stop = "notify-send 0type 'copied'"   # ping me when it's done
```

**Hooks** are just shell commands. They get your text on stdin, run in the
background, and can never break or slow down dictation. Handy ones: type
straight into the focused window, append to a notes file, pipe it
somewhere else.

## Handy commands

| | |
| --- | --- |
| `0type` | the menu |
| `0type toggle` | start/stop talking — **this is the one to bind to a key** |
| `0type themes` | list every theme |
| `0type config` | show your settings and where they came from |
| `0type listen` | transcribe to the terminal, no window |

## Questions

**Does it work offline?** Yes, always. The model lives on your disk and
nothing is ever sent anywhere.

**What does it need?** A Linux desktop with GTK4 and ALSA — that's almost
any of them. Works on Wayland and X11, GNOME and KDE.

**How big is it?** The program is about 10MB. The speech model is 650MB
and downloads once.

**Is it fast?** Words appear about a second behind you, on CPU. No GPU
needed.

**Something's wrong.** Run `0type config` to see what it thinks your
settings are. If your key stops working, `0type toggle` from a terminal
will restart it and tell you what happened.

## For the curious

It's a single Go binary using NVIDIA's Parakeet speech model through ONNX
Runtime, drawing its window with GTK4 and Cairo. If you want the whole
story — how the streaming works, why the overlay is built the way it is,
and everything that broke on the way — that's in
[`docs/SETUP.md`](docs/SETUP.md).

Building it yourself takes two commands:

```sh
sudo dnf install golang gtk4-devel alsa-lib-devel libX11-devel \
                 fontconfig-devel onnxruntime-devel     # or your distro's equivalent
make build
```

## License

MIT. The bundled fonts are SIL Open Font License 1.1 — see
[`LICENSE`](LICENSE).
