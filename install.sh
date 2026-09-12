#!/bin/sh
# 0type installer.
#
#   curl -fsSL https://raw.githubusercontent.com/IamAlexandros/0type/main/install.sh | sh
#
# Downloads the latest release, verifies its checksum, and installs into
# your home directory. No sudo, nothing outside these two paths:
#
#   ~/.local/share/0type   the program, its bundled ONNX Runtime, theme sources
#   ~/.local/bin/0type     a symlink onto your PATH
#
# Uninstall is `rm -rf` on those two. The speech model is downloaded
# separately by `0type setup` into ~/.cache/0type.
set -eu

REPO="IamAlexandros/0type"
PREFIX="${PREFIX:-$HOME/.local/share/0type}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

# Colors, but only when talking to a terminal -- this script is usually
# on the receiving end of a pipe, and escape codes in a log are noise.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); R=$(printf '\033[0m')
	GREEN=$(printf '\033[32m'); YELLOW=$(printf '\033[33m'); RED=$(printf '\033[31m')
else
	B=""; DIM=""; R=""; GREEN=""; YELLOW=""; RED=""
fi

say()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$GREEN" "$R" "$*"; }
warn() { printf '%s warning:%s %s\n' "$YELLOW" "$R" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$RED" "$R" "$*" >&2; exit 1; }

# --- what we can actually run on ----------------------------------------

case "$(uname -s)" in
Linux) ;;
Darwin) die "0type is Linux-only for now: it captures audio through ALSA and
       positions its window through X11. macOS needs a CoreAudio and a
       Quartz backend -- see the Platform support section of the README." ;;
*) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) ARCH="x86_64" ;;
*) die "no prebuilt release for $(uname -m); build from source instead:
       git clone https://github.com/$REPO && cd 0type && make build" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	die "need curl or wget"
fi

# --- runtime dependencies ------------------------------------------------
#
# Checked up front and reported together: finding out about a missing
# library from a dynamic linker error, after the install claimed success,
# is a worse experience than being told now.

missing=""
have_lib() {
	# ldconfig is the reliable check; fall back to looking in the usual
	# places if it isn't on PATH (some minimal images).
	if command -v ldconfig >/dev/null 2>&1; then
		ldconfig -p 2>/dev/null | grep -q "$1" && return 0
	fi
	for d in /usr/lib64 /usr/lib /usr/lib/x86_64-linux-gnu /lib64; do
		[ -e "$d/$1" ] && return 0
	done
	return 1
}
have_lib libgtk-4.so   || missing="$missing gtk4"
have_lib libasound.so  || missing="$missing alsa-lib"
have_lib libX11.so     || missing="$missing libX11"

if [ -n "$missing" ]; then
	warn "these runtime libraries look missing:$missing"
	say  "${DIM}    Fedora:  sudo dnf install gtk4 alsa-lib libX11${R}"
	say  "${DIM}    Debian:  sudo apt install libgtk-4-1 libasound2 libx11-6${R}"
	say  "${DIM}    Arch:    sudo pacman -S gtk4 alsa-lib libx11${R}"
	say  "${DIM}  Continuing anyway -- install them before running 0type.${R}"
fi

# --- find the latest release --------------------------------------------

step "Looking up the latest release of $REPO"
API="https://api.github.com/repos/$REPO/releases/latest"
TAG=$(fetch_stdout "$API" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true)
[ -n "$TAG" ] || die "couldn't find a release. If none has been published yet, build from source:
       git clone https://github.com/$REPO && cd 0type && make build && ./bin/0type setup"

TARBALL="0type-$TAG-linux-$ARCH.tar.gz"
BASE="https://github.com/$REPO/releases/download/$TAG"
say "    found $B$TAG$R"

TMP=$(mktemp -d)
# Clean up whatever happens, including a failure part-way through a
# download -- half an archive in /tmp helps nobody.
trap 'rm -rf "$TMP"' EXIT INT TERM

step "Downloading $TARBALL"
fetch "$BASE/$TARBALL" "$TMP/$TARBALL" || die "download failed: $BASE/$TARBALL"

# Checksums are verified when the release publishes them, and their
# absence is reported rather than passed over quietly: "no checksum" and
# "checksum matched" should never look the same from here.
if fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" 2>/dev/null; then
	step "Verifying checksum"
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$TMP" && grep " $TARBALL\$" SHA256SUMS | sha256sum -c - >/dev/null) \
			|| die "checksum mismatch -- refusing to install $TARBALL"
		say "    ok"
	else
		warn "sha256sum not found; skipping verification"
	fi
else
	warn "this release publishes no SHA256SUMS; skipping verification"
fi

step "Installing to $PREFIX"
tar -xzf "$TMP/$TARBALL" -C "$TMP"
DIR=$(find "$TMP" -maxdepth 1 -type d -name '0type-*' | head -1)
[ -n "$DIR" ] || die "unexpected archive layout in $TARBALL"

mkdir -p "$PREFIX" "$BINDIR"
rm -rf "$PREFIX/bin" "$PREFIX/lib" "$PREFIX/themes" "$PREFIX/licenses"
cp -R "$DIR/bin" "$DIR/lib" "$DIR/themes" "$PREFIX/"
[ -d "$DIR/licenses" ] && cp -R "$DIR/licenses" "$PREFIX/"
ln -sf "$PREFIX/bin/0type" "$BINDIR/0type"

say "    installed $B$TAG$R"

# --- the speech model ----------------------------------------------------
#
# Downloaded here rather than left as a "now run this" instruction: it is
# the difference between one command and a checklist, and 0type cannot
# transcribe a word without it. It's idempotent -- files already present
# are checksummed and skipped -- so re-running the installer is cheap.

if [ "${ZEROTYPE_SKIP_MODEL:-}" = "1" ]; then
	warn "skipping the model download (ZEROTYPE_SKIP_MODEL=1); run '0type setup' before using it"
else
	step "Downloading the speech model (~650MB, once)"
	"$PREFIX/bin/0type" setup || die "model download failed -- rerun '0type setup' to resume; finished files are kept"
fi

# --- start it, and offer to keep it started ------------------------------

AUTOSTART="$HOME/.config/autostart/0type.desktop"
install_autostart() {
	mkdir -p "$(dirname "$AUTOSTART")"
	cat >"$AUTOSTART" <<DESKTOP
[Desktop Entry]
Type=Application
Name=0type
Comment=Live voice-to-text overlay
Exec=$BINDIR/0type --background
Terminal=false
X-GNOME-Autostart-enabled=true
DESKTOP
}

# curl | sh leaves stdin pointing at the script, so an interactive
# question has to come from the terminal directly. When there isn't one
# (CI, a Dockerfile), don't ask and don't assume: just say what wasn't
# done.
if [ -r /dev/tty ] && [ -t 2 ]; then
	printf '%s==>%s Start 0type automatically when you log in? [Y/n] ' "$GREEN" "$R"
	read -r reply </dev/tty || reply=""
	case "$reply" in
	[Nn]*) say "    skipped -- create $AUTOSTART later if you change your mind" ;;
	*) install_autostart; say "    yes: $AUTOSTART" ;;
	esac
else
	say "${DIM}    (not a terminal, so not asking about autostart -- see the README)${R}"
fi

step "Starting 0type"
"$BINDIR/0type" --background >/dev/null 2>&1 || true

say ""
say "${B}Done. 0type is running.${R}"
say ""

case ":$PATH:" in
*":$BINDIR:"*) ;;
*)
	warn "$BINDIR is not on your PATH"
	say  "${DIM}  add to ~/.bashrc or ~/.zshrc:  export PATH=\"\$PATH:$BINDIR\"${R}"
	say  ""
	;;
esac

cat <<EOF
${B}One thing left:${R} pick a key to talk with.

  GNOME:  Settings > Keyboard > Keyboard Shortcuts > Custom Shortcuts
  KDE:    System Settings > Shortcuts > Custom Shortcuts

  Command:  ${B}$BINDIR/0type toggle${R}

Then press it, talk, and press it again -- your words are on the
clipboard. Run ${B}0type${R} any time for the menu and forty themes.

${DIM}Everything runs on your machine. No account, no telemetry.${R}
EOF
