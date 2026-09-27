# Atlas 2.0 — desktop + web rewrite · DEBRIEF

Status: **draft for Caden — no decisions locked.** Written 2026-09-26.
Ask: full rewrite (still Go). Ship as a desktop app with vim keys in a real UI,
easy + pretty. Also reachable in a browser (phone / any device). Keep the
Discord organization shape (category → channel → thread), and track spawned
work (subagents, pi tasks) as it happens. Debrief first; framework decisions
after.

---

## 1. TL;DR

**Go core + ONE web frontend + three shells.**

- **Core — `atlasd`** (Go, on head): the hub grown up. state.db reads, hermes
  API client, Discord relay, spawned-work tracker, live streams (WS/SSE),
  auth, and it serves the frontend. Runs headless as a real systemd unit.
- **Frontend — one web app** (TS; framework TBD after spikes). All UI code
  lives here exactly once.
- **Shells:**
  1. **Browser / PWA** — phone + any device via tailnet. Costs nothing extra
     once the frontend exists. Day-one "desktop", too.
  2. **Wails desktop app** — native window wrapping the same frontend; tray,
     global hotkey, native notifications. Single ~15–25 MB Go binary.
  3. **TUI** — the existing Bubble Tea client, kept as a legacy client of the
     same atlasd API (frozen feature-wise).

The unlock that makes this cheap: **Wails v3 can compile the same app to a
headless server** (`-tags server`) — so "desktop app" and "web app" are two
build modes of one codebase, not two products. No UI duplication, ever.

This also deletes the roadmap we no longer need: the Discord mirror-relay +
native-formatting-parity work is dead (it existed to serve the phone story;
the web frontend serves it better). Only the approvals piece of the bridge
arc survives (unchanged conclusion).

## 2. Desktop shipping options (reviewed honestly)

| Option | Verdict |
|---|---|
| **Wails v3** (Go + system webview) | **Recommend.** Go-native, `~15–25 MB`, `60–120 MB` RAM, auto TS bindings, multi-window, tray, menus, server build. Linux stack 2026: GTK4 + WebKitGTK 6.0 default. v3 is beta (beta.23+, API "stable", production use reported) — pin versions. |
| PWA only (install from browser) | Keep as day-one desktop + phone story (free). No tray/hotkey/notifications. |
| Tauri 2 | Great shells, but Rust core — violates "still Go"; Go-as-sidecar = two runtimes. Out. |
| Electron | What hermes-desktop uses (verified: `apps/desktop` = Electron). Ships Chromium: 150–300 MB, 1–3 s startup. We want the lean inverse. Out. |
| Fyne / Gio (pure-Go UI) | Go everywhere, but: no credible web story (Gio WASM = big bundle, slow first load, IME pain), text-editing/chat rendering is on you, "pretty" gets expensive. Out — with the honest note that this is the ONE place we add a second language to honor "pretty + web + fast". |
| TUI in a window (embed a terminal) | Cheapest, but it's still a TUI. Contradicts "real UI". Out as primary. |

**Known Wails/Linux risks (with escape hatches):**
- NVIDIA + WebKitGTK hardware acceleration can white-screen → one-line
  `WebviewGpuPolicy: Never` (software render). Check in spike A.
- Tiling WMs (Hyprland): minimize/maximize are WM concerns — expected, fine.
- v3 beta API churn → pin; v2 fallback loses tray/server mode (then web would
  run on a plain Go server instead — still fine, just less tidy).
- NixOS packaging: `buildGoModule` + `gtk4`/`webkitgtk-6.0` buildInputs +
  vendored npm frontend build (fixed-output derivation). Standard, Debbie-able.

## 3. Vim in a real UI

- One **capture-phase key router** + modal state machine:
  NORMAL / INSERT / VISUAL / FIND / COMMAND. Counts, `gg`/`G`, `{`/`}`,
  `ctrl+u`/`ctrl+d`, `/` find with highlight + `n`/`N`, `v`/`V` visual +
  `y` yank — to the **real clipboard** (`navigator.clipboard`; OSC 52
  retires), `Q` quote, `r` reasoning, `.`/`,` stale-stow toggle, `enter`
  fold/open, `1`/`2`/`z` pane focus, `?` help, `:` + `ctrl+k` command palette.
- **Own focus stack**, not DOM tab order: panes = tree / chat / composer;
  `tab`/`esc` cycle; mouse coexists (click = focus + cursor).
- Composer: modal insert/normal like today, on a real textarea underneath
  (IME, spellcheck, undo for free); keep motions pragmatic, grow later.
- Parity: ONE shared keymap table (action → keys) consumed by web + TUI,
  with fixtures — already the plan in PLAN-webui.md.
- Perf: virtualized message list; rAF-batched stream deltas; 60 fps target on
  5k-message transcripts.

## 4. Pretty

- Design tokens from the current house theme → CSS custom properties; house +
  light themes day one.
- What the real UI unlocks immediately: inline images, real markdown (tables,
  code blocks + syntax highlighting), links, emoji, smooth scrolling, subtle
  motion, ::selection styling (our reverse-video convention survives).
- The TUI's visual language carries: density, accent bars, per-profile color.

## 5. IA + spawned-work tracking (mandated)

- Shape stays: **profile → category → channel → thread → conversation.**
  Now owned by us — no Discord quirks to mirror; pins, tags, filters, saved
  views become cheap.
- Spawned work is first-class: **subagent runs, pi tasks, courier dispatches**
  nested under their parent conversation (and/or an activity view), with live
  status (● running / ✓ / ✗) and the transcript/log one keystroke away.
  Data feeds all mapped in the investigation notes: `async_delegations` +
  live transcript files + pi `tasks/*` quartet.

## 6. Rewrite map (Go)

**Reuse:** internal/hermes client + SSE handling; config; theme palette;
tree semantics (stale/folds/unread/age); keymap table; metrics/cards parsing;
hub logic (port Python → Go).
**New:** `atlasd` (HTTP + WS, auth, static hosting, tracker); `web/` frontend;
Wails shell; tests.
**Layout:** `cmd/{atlasd, atlas-desktop, atlas}` · `internal/…` · `web/`.
`hub/atlas-hub.py` retires at atlasd parity.
**Versioning:** this is the **1.0** line.

## 7. De-risk spikes — BEFORE locking framework decisions

- **A · Wails v3 × frontend × NixOS × Hyprland** (node0, heads-up first):
  hello → tray → hotkey → notification; GTK4 rendering under Hyprland;
  NVIDIA GPU-policy fallback check. Deliverable: screenshot + build recipe.
- **B · Server build:** same app `-tags server` on head → phone browser over
  tailnet renders it. Proves the one-codebase-two-shells claim end-to-end.
- **C · Vim router + virtual list:** 5k-message transcript at 60 fps, find +
  visual + yank working in the browser.

≈ one day total; each kills a named risk with evidence.

## 8. Proposed sequencing (after spikes + decisions)

1. **atlasd** port (Go hub v2 + WS/SSE + tracker + auth + static) — also
   finally kills the hub-durability gap (systemd unit).
2. **Web v1** (tree / chat / composer / vim / theme) — phone works day one
   over tailnet; PWA install.
3. **Desktop shell** (Wails) + native niceties; node0 package (heads-up rule).
4. TUI frozen as legacy client; Python hub retired at parity.
5. Approvals bridge last (unchanged).

## 9. Open questions for Caden

1. Frontend flavor — Svelte 5 (my lean) vs Solid vs React? Decide after
   spike A/C, or you call it now.
2. Wails v3 beta acceptable? (My lean: yes — v2 loses tray/server mode.)
3. TUI fate: freeze (my lean), maintain, or retire?
4. Desktop server URL: tailnet-first with LAN fallback — ok?
5. Spikes on node0 soon? (Heads-up before anything launches, per standing rule.)
