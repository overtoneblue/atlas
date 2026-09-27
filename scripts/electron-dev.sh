#!/usr/bin/env bash
# Run the Atlas Electron shell (dev): builds atlasd on first use, warns if
# the head tunnel is down, then launches. Everything runs locally; head
# services arrive via the existing 8642/8643 tunnel.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${ATLASD_BIN:-$ROOT/bin/atlasd}"

if [ ! -x "$BIN" ]; then
  echo "+ building atlasd → $BIN"
  nix shell nixpkgs#go -c bash -c "cd \"$ROOT\" && CGO_ENABLED=0 go build -o \"$BIN\" ./cmd/atlasd"
fi

for p in 8642 8643; do
  if ! timeout 1 bash -c "exec 3<>/dev/tcp/127.0.0.1/$p" 2>/dev/null; then
    echo "warning: 127.0.0.1:$p not answering — head tunnel up? (ssh -N -L $p:127.0.0.1:$p head)" >&2
  fi
done

exec nix shell nixpkgs#electron -c electron "$ROOT/electron" "$@"
