# PLAN — Atlas on the web

Status: draft · 2026-09-26 · owner: Nolan

## The ask

Eventually port Atlas to a webpage/webapp — the same workstream client in a
browser, keeping the vim keyboard model.

## The good news: the architecture is already right

Atlas is a client/server system whether we call it that or not:

```
  head ── hermes API (:8642, SSE chat + session state)
       └─ atlas-hub (:8643, profile-aware tree/search/mirror/stats)
                 ▲
   ┌─────────────┼─────────────────────┐
   Go TUI      Discord mirror       web client  ← the port is a THIRD client,
   (node0/ssh) (phone)                 (this plan) not a rewrite
```

The hub owns session↔Discord bindings, the mirror relay, and all data
shaping. A web client consumes the same two upstreams. Nothing about the hub
model changes; it just gains a third consumer.

## What the hub needs first (shared wins)

1. **SSE `/events`** — stream tree deltas, run completions, unread counts.
   The TUI currently polls every 10s; the web want live, and the TUI can drop
   to a slower reconcile poll. (Pure stdlib streaming response; no deps.)
2. **Send + stop endpoints** — thin proxies to the API server's
   `chat/stream` + stop, *relayed as SSE* with the same event vocabulary the
   TUI consumes (buffered text, tool notes, done). One protocol, two clients.
3. **Auth for the web** — keep the hub on localhost. Serve the web app via
   nginx on head (already have the TLS/ACME setup), bound **tailnet-only**,
   with the bearer token exchanged once for an HttpOnly cookie. No session
   state beyond that.
4. **Paged transcript fetch** — `?limit/&before` on messages so the web
   opens long sessions without shipping megabytes.

## The client: TypeScript SPA (recommended)

Options weighed:

- **A. TypeScript SPA (Vite + Svelte/React).** Native DOM text: real
  selection, IME, copy/paste, **markdown with images**, link previews — the
  web's genuine advantages. The vim model is re-implemented as a small
  keyboard state machine (~one module), fed by a keymap table we already
  maintain.
- **B. Go WASM reusing `internal/ui`.** Tempting (scroll/find/selection math
  is pure Go) but Bubble Tea can't run in a browser; we'd ship a DOM adapter
  around the view-model, a 3–4 MB wasm bundle, and fight IME/selection UX.
  Big cost for logic we can re-derive from the keymap table.
- **C. Server-rendered htmx.** Wrong fit: streaming turns + vim interactions
  are exactly what htmx is worst at.

**Recommendation: A.** Keep the *interaction model* shared — not the code:
the keymap table and behavior specs live in this repo (`docs/KEYMAP.md` +
shared JSON test fixtures: state × keys → expected action) so both clients
are tested against the same scenarios. Parity stays honest without contorting
either stack.

## Phases

1. **Hub hardening** (above) — pays off in the TUI immediately.
2. **Read-only web v0**: pick a session (tree or search), render transcript
   with markdown, live-update via SSE, keyboard-scroll. Ship behind tailnet.
3. **Compose + stream**: composer, send → turn → streaming render with stop —
   reusing the exact TUI semantics (`r` reasoning toggle, cards, approvals
   surfaced the same way).
4. **Vim layer + visual mode**: the same keymap — `/` find, `{`/`}`, `v`/`V`
   selection, `y` yank to *real* system clipboard (no OSC 52 needed),
   `Q` quote. Native selection coexists (mouse-vs-keys, both fine).
5. **Multiplexing parity**: tabs/panes land in CSS grid for free — which is
   why `PLAN-multiplexing.md` should be settled in the TUI first; the web
   inherits the model once it's proven.

## Packaging

- Repo gains `web/` (the SPA) + a served bundle; `nix build .#web` produces
  the static bundle; hub or nginx serves it (`atlas.<tailnet>` on head).
- The TUI stays the primary daily driver; the web is for anywhere-with-a-
  browser + media-rich reading. Discord stays the phone story.

## Risks / notes

- Markdown + HTML in the web transcript: sanitize (agents can emit arbitrary
  markdown; treat as untrusted-ish).
- Token in URL? No — cookie only, tailnet bind; same posture as other head
  services (LAN/tailnet-only, no public exposure).
- Long-session perf: virtualize the list beyond ~2k nodes; hub paging (4).
- Shared fixtures (keymap parity) must be written when the web vim layer
  starts — cheap then, priceless later.
