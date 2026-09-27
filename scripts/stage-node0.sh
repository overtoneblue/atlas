#!/usr/bin/env bash
# stage-node0.sh — stage the Atlas desktop app onto node0.
#
# node0 is Caden's Hyprland desktop. This script copies the repo over the
# same SSH identity the `desktop` wrapper uses (transport only) and builds
# the desktop package on node0. The build is hermetic and deterministic:
# node0 produces the exact same store path as a head-side build.
#
# After staging, launch it (first run) with:
#   desktop bash -lc 'setsid nohup ~/atlas/result/bin/atlas-desktop >/tmp/atlas-app.log 2>&1 &'
#
# Runtime needs (already in place on node0):
#   ~/.config/atlas/env          — ATLAS_API_URL/ATLAS_HUB_URL/ATLAS_API_KEY
#   ssh -N -L 8642:127.0.0.1:8642 -L 8643:127.0.0.1:8643 head   (tunnel to head)
set -euo pipefail

KEY="${DESKTOP_SSH_KEY:-/home/overtoneblue/hermes-recovery/hermes-ssh/desktop_ed25519}"
KNOWN="${DESKTOP_KNOWN_HOSTS:-/home/overtoneblue/.ssh/known_hosts}"
SRC="${ATLAS_SRC:-/srv/atlas}"
SSH=(ssh -i "$KEY" -o BatchMode=yes -o IdentitiesOnly=yes
     -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$KNOWN"
     -o ConnectTimeout=15 overtoneblue@node0)

echo "==> node0 reachability"
"${SSH[@]}" hostname

echo "==> copying $SRC -> node0:~/atlas (build junk excluded)"
tar -C "$SRC" \
  --exclude='./result' --exclude='./result-*' \
  --exclude='./desktop/bin' \
  --exclude='./desktop/frontend/dist' \
  --exclude='./desktop/frontend/node_modules' \
  --exclude='./desktop/.task' \
  -czf - . | "${SSH[@]}" 'mkdir -p ~/atlas && tar xzf - -C ~/atlas'

echo "==> building .#atlas-desktop on node0 (detached; first run fetches deps)"
"${SSH[@]}" 'bash -lc "cd ~/atlas && setsid nohup nix build .#atlas-desktop --print-out-paths > /tmp/atlas-node0-build.log 2>&1 < /dev/null & echo build-kicked"'

echo "==> staged. watch: desktop tail -5 /tmp/atlas-node0-build.log"
echo "==> binary: node0:~/atlas/result/bin/atlas-desktop"
