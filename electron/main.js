// Atlas — Electron shell.
//
// A thin Chromium window over the atlasd bridge daemon. The daemon owns
// every head-service conversation and serves the web UI; this process only
// spawns it, waits for it to answer, and opens a window at its origin.
//
// No Node APIs reach the renderer, no app logic lives here — the whole
// shell is "start the daemon, show the page". Everything else (keybinds,
// state, transport) is the same web UI that runs in a browser.
//
// Env knobs:
//   ATLASD_BIN=path        atlasd binary (default: ../bin/atlasd)
//   ATLASD_PORT=8644       daemon port
//   ATLASD_EXTERNAL=1      use an already-running daemon; don't spawn
//   ATLAS_WEB_DIR=path     built web UI directory (default: ../desktop/frontend/dist)
//
// Wayland note: Electron defaults to XWayland. For native Wayland run
// with ELECTRON_OZONE_PLATFORM_HINT=auto electron .

const { app, BrowserWindow, shell } = require("electron");
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");
const http = require("node:http");

const PORT = Number(process.env.ATLASD_PORT || 8644);
const ORIGIN = `http://127.0.0.1:${PORT}`;

// Product identity: userData dir + window class association (matches the
// desktop entry's StartupWMClass, so launchers group/focus us correctly).
app.setName("Atlas");

// Native animated wheel scrolling. Measured on this exact build (Electron
// 43 / Chromium 150): wheel already animates natively — ~120px over ~270ms,
// ~12 frames per notch — and this switch changes nothing (A/B identical).
// Kept explicit so a future build can't silently regress to stepped
// scrolling. No app-side physics: the compositor owns scroll.
app.commandLine.appendSwitch("enable-smooth-scrolling");

let daemon = null;
let win = null;
let quitting = false;

function findAtlasd() {
  const candidates = [process.env.ATLASD_BIN, path.join(__dirname, "..", "bin", "atlasd")].filter(
    Boolean,
  );
  for (const c of candidates) {
    try {
      fs.accessSync(c, fs.constants.X_OK);
      return c;
    } catch {
      /* try next */
    }
  }
  return null;
}

function findWebDir() {
  const candidates = [
    process.env.ATLAS_WEB_DIR,
    path.join(__dirname, "..", "desktop", "frontend", "dist"),
  ].filter(Boolean);
  for (const c of candidates) {
    try {
      fs.accessSync(path.join(c, "index.html"));
      return c;
    } catch {
      /* try next */
    }
  }
  return null;
}

function pingDaemon(timeoutMs = 1500) {
  return new Promise((resolve) => {
    const req = http.get(`${ORIGIN}/api/status`, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.setTimeout(timeoutMs, () => {
      req.destroy();
      resolve(false);
    });
    req.on("error", () => resolve(false));
  });
}

function startDaemon() {
  if (process.env.ATLASD_EXTERNAL) {
    console.log(`atlas: using external atlasd on ${ORIGIN}`);
    return;
  }
  const bin = findAtlasd();
  if (!bin) {
    console.error("atlas: atlasd binary not found (build it or set ATLASD_BIN)");
    app.quit();
    return;
  }
  // --die-with-parent: kernel-enforced cleanup (PR_SET_PDEATHSIG) so the
  // daemon can never outlive this shell, even on hard kills.
  const args = ["--port", String(PORT), "--die-with-parent"];
  const web = findWebDir();
  if (web) {
    args.push("--web", web);
  } else {
    console.error("atlas: web UI not built (desktop/frontend/dist missing) — API only");
  }
  console.log(`atlas: spawning ${bin} ${args.join(" ")}`);
  daemon = spawn(bin, args, { stdio: ["ignore", "pipe", "pipe"] });
  daemon.stdout.on("data", (d) => process.stdout.write(`[atlasd] ${d}`));
  daemon.stderr.on("data", (d) => process.stderr.write(`[atlasd] ${d}`));
  daemon.on("error", (err) => {
    console.error(`atlas: failed to spawn atlasd: ${err.message}`);
    app.quit();
  });
  daemon.on("exit", (code) => {
    if (!quitting) {
      console.error(`atlas: atlasd exited unexpectedly (${code}) — port ${PORT} in use?`);
      app.quit();
    }
  });
}

function waitForDaemon(deadlineMs = 15000) {
  const start = Date.now();
  return new Promise((resolve) => {
    const tick = () => {
      const req = http.get(`${ORIGIN}/api/status`, (res) => {
        res.resume();
        if (res.statusCode === 200) return resolve(true);
        retry();
      });
      req.on("error", retry);
    };
    const retry = () => {
      if (Date.now() - start > deadlineMs) return resolve(false);
      setTimeout(tick, 250);
    };
    tick();
  });
}

async function main() {
  if (await pingDaemon()) {
    console.log("atlas: atlasd already running — reusing it");
  } else {
    startDaemon();
    if (!(await waitForDaemon())) {
      console.error("atlas: atlasd did not become ready in time");
      app.quit();
      return;
    }
  }

  win = new BrowserWindow({
    width: 1600,
    height: 1000,
    minWidth: 820,
    minHeight: 560,
    title: "Atlas",
    backgroundColor: "#000000",
    autoHideMenuBar: true,
    webPreferences: { contextIsolation: true, nodeIntegration: false },
  });
  win.loadURL(ORIGIN);

  // Links to the outside world open in the system browser, never in-app.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith("http")) void shell.openExternal(url);
    return { action: "deny" };
  });

  win.on("closed", () => {
    win = null;
  });
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (win) {
      if (win.isMinimized()) win.restore();
      win.focus();
    }
  });
  app.whenReady().then(main);
}

app.on("window-all-closed", () => app.quit());
app.on("will-quit", () => {
  quitting = true;
  if (daemon) daemon.kill("SIGTERM");
});

// Shell kills (terminal SIGTERM/SIGINT) go through the same path; the
// daemon additionally carries PR_SET_PDEATHSIG as a kernel backstop.
for (const sig of ["SIGTERM", "SIGINT"]) process.on(sig, () => app.quit());
process.on("exit", () => {
  if (daemon) daemon.kill("SIGTERM");
});
