#!/usr/bin/env bash
# kbd-smoke.sh — headless keyboard smoke test for the Atlas desktop app.
#
# Starts the app on a private Xvfb display, sends a key sequence with
# xdotool, and captures screenshots before/after so keybindings can be
# verified visually without a physical display.
#
# Usage:  scripts/kbd-smoke.sh <out-prefix> [key ...]
#   Keys are xdotool key names: j k period G comma Return Tab
#
# Example: scripts/kbd-smoke.sh /tmp/atlas-kbd j j j period
set -euo pipefail

PREFIX="${1:-/tmp/atlas-kbd}"
shift || true
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/desktop/bin/atlas-desktop"

if [[ ! -x "$BIN" ]]; then
  echo "kbd-smoke: $BIN not found — build first:" >&2
  echo "  nix develop $ROOT#desktop -c bash -c 'cd desktop && task build'" >&2
  exit 1
fi

nix shell nixpkgs#xvfb nixpkgs#imagemagick nixpkgs#mesa nixpkgs#xdotool -c bash -s -- "$BIN" "$PREFIX" "$@" <<'EOS'
set -euo pipefail
BIN="$1"; PREFIX="$2"; shift 2

MESA=$(nix build --print-out-paths --no-link nixpkgs#mesa)
export __EGL_VENDOR_LIBRARY_FILENAMES=$MESA/share/glvnd/egl_vendor.d/50_mesa.json
export LIBGL_DRIVERS_PATH=$MESA/lib/dri
export LIBGL_ALWAYS_SOFTWARE=1 GALLIUM_DRIVER=llvmpipe
export WEBKIT_DISABLE_DMABUF_RENDERER=1 WEBKIT_DISABLE_COMPOSITING_MODE=1

# private display — no pkill, no collisions with other runs
DISP=":9$(( RANDOM % 9 ))"
Xvfb "$DISP" -screen 0 1600x1000x24 >/tmp/xvfb-atlas.log 2>&1 &
XPID=$!
sleep 2
DISPLAY="$DISP" GDK_BACKEND=x11 "$BIN" >/tmp/atlas-app.log 2>&1 &
APID=$!
sleep 12

WID=$(DISPLAY="$DISP" xdotool search --name "^Atlas$" | head -1)
echo "window id: $WID on $DISP"
DISPLAY="$DISP" import -window root "${PREFIX}-before.png"

if [ "$#" -gt 0 ]; then
  DISPLAY="$DISP" xdotool windowfocus --sync "$WID" 2>/dev/null || true
  sleep 0.5
  DISPLAY="$DISP" xdotool key --delay 300 --window "$WID" "$@"
  sleep 2
fi

DISPLAY="$DISP" import -window root "${PREFIX}-after.png"
kill "$APID" "$XPID" 2>/dev/null || true
echo "captured ${PREFIX}-before.png ${PREFIX}-after.png"
EOS
