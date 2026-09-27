#!/usr/bin/env bash
# node0 → head tunnels for the Atlas app:
#   8642 API · 8643 hub · 8645 → head's atlasd 8644 (image + paste relay;
#   the app points ATLAS_UPSTREAM at it). Idempotent: replaces the
#   previous Atlas tunnel. Safe to re-run any time.
set -euo pipefail

HOST="${ATLAS_HEAD_HOST:-head}"

# Replace any previous Atlas tunnel (never matches this script itself).
mapfile -t pids < <(pgrep -f 'ssh -N .*8642:127.0.0.1:8642' || true)
for pid in "${pids[@]}"; do
  [ "$pid" = "$$" ] && continue
  kill "$pid" 2>/dev/null || true
done
sleep 0.5

setsid ssh -N -o BatchMode=yes -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
  -L 8642:127.0.0.1:8642 -L 8643:127.0.0.1:8643 -L 8645:127.0.0.1:8644 \
  "$HOST" &

sleep 1
for p in 8642 8643 8645; do
  if timeout 1 bash -c "exec 3<>/dev/tcp/127.0.0.1/$p" 2>/dev/null; then
    echo "port $p: up"
  else
    echo "port $p: DOWN — check ssh to $HOST"
  fi
done
