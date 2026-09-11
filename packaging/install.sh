#!/bin/sh
# 0type installer.
#
# Installs into the user's home -- no root, no system directories, and
# nothing outside these two paths, so uninstalling is deleting them.
# The binary and its bundled ONNX Runtime library must stay together:
# 0type finds the library relative to its own location (see
# internal/asr's bundledLibraryPaths), which is why this copies the whole
# tree and then symlinks only the binary onto PATH.
set -eu

PREFIX="${PREFIX:-$HOME/.local/share/0type}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

SRC=$(cd "$(dirname "$0")" && pwd)

if [ ! -x "$SRC/bin/0type" ]; then
	echo "error: $SRC/bin/0type is missing -- run this script from inside the unpacked tarball" >&2
	exit 1
fi

echo "installing to $PREFIX"
mkdir -p "$PREFIX" "$BINDIR"
rm -rf "$PREFIX/bin" "$PREFIX/lib" "$PREFIX/themes"
cp -R "$SRC/bin" "$SRC/lib" "$SRC/themes" "$PREFIX/"
ln -sf "$PREFIX/bin/0type" "$BINDIR/0type"

echo
echo "installed:"
echo "  $PREFIX          program, bundled ONNX Runtime, theme sources"
echo "  $BINDIR/0type    on your PATH"

case ":$PATH:" in
*":$BINDIR:"*) ;;
*)
	echo
	echo "warning: $BINDIR is not on your PATH."
	echo "  add this to ~/.bashrc or ~/.zshrc:  export PATH=\"\$PATH:$BINDIR\""
	;;
esac

cat <<EOF

next steps:
  1. download the speech model (~650MB, once):
       0type setup

  2. start it (it stays running, hidden, until you toggle it):
       0type

  3. bind a key to show/hide it --
     GNOME: Settings > Keyboard > Keyboard Shortcuts > Custom Shortcuts
       command:  $BINDIR/0type toggle

  optional:
    0type themes    list themes (default, light, mono, term)
    0type config    show the config file's location and current settings
EOF
