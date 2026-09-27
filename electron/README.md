# Atlas — Electron shell

A Chromium window over the [atlasd](../cmd/atlasd) bridge daemon. The daemon
speaks to head services and serves the web UI; this shell spawns it and shows
the page. See `main.js` for the env knobs (`ATLASD_BIN`, `ATLASD_PORT`,
`ATLASD_EXTERNAL`).

## Run (dev)

```sh
../scripts/electron-dev.sh
```

The script builds `bin/atlasd` on first use, then runs Electron from nixpkgs
(`nix shell nixpkgs#electron`). For native Wayland instead of XWayland:

```sh
ELECTRON_OZONE_PLATFORM_HINT=auto ../scripts/electron-dev.sh
```

## Notes

- No `npm install` here: Electron comes from nixpkgs; there are no JS deps.
- The renderer is the same web UI (`desktop/frontend`) that browsers get;
  it picks the HTTP+SSE transport automatically when `window._wails` is absent.
