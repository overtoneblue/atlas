#!/usr/bin/env bash
# preview-window.sh — render the Atlas desktop app on a HEADLESS host and
# capture the window as a PNG.
#
# The trick: Xvfb + mesa/llvmpipe software GL + WebKit's software-render
# escape hatches. No GPU, no physical display, no node0 needed — the dev
# loop for the Wails app runs entirely on head.
#
# Usage:  scripts/preview-window.sh [output.png] [settle-seconds]
# Requires: the desktop binary built once (`nix develop .#desktop` then
#           `cd desktop && task build`).

set -euo pipefail

OUT="${1:-/tmp/atlas-window.png}"
SETTLE="${2:-8}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/desktop/bin/atlas-desktop"

if [[ ! -x "$BIN" ]]; then
  echo "preview-window: $BIN not found — build it first:" >&2
  echo "  cd $ROOT && nix develop .#desktop -c bash -c 'cd desktop && task build'" >&2
  exit 1
fi

pkill -f "Xvfb :99" 2>/dev/null || true

nix shell nixpkgs#xvfb nixpkgs#imagemagick nixpkgs#mesa -c bash -c '
set -euo pipefail
MESA=$(nix build --print-out-paths --no-link nixpkgs#mesa)
export __EGL_VENDOR_LIBRARY_FILENAMES=$MESA/share/glvnd/egl_vendor.d/50_mesa.json
export LIBGL_DRIVERS_PATH=$MESA/lib/dri
export LIBGL_ALWAYS_SOFTWARE=1 GALLIUM_DRIVER=llvmpipe
export WEBKIT_DISABLE_DMABUF_RENDERER=1 WEBKIT_DISABLE_COMPOSITING_MODE=1

Xvfb :99 -screen 0 1400x900x24 >/tmp/xvfb-atlas.log 2>&1 &
XPID=$!
sleep 2
DISPLAY=:99 GDK_BACKEND=x11 "'"$BIN"'" >/tmp/atlas-app.log 2>&1 &
APID=$!
sleep "'"$SETTLE"'"
DISPLAY=:99 import -window root "'"$OUT"'"
kill $APID $XPID 2>/dev/null || true
echo "wrote '"$OUT"'"
'
