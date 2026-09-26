# atlas

Keyboard-driven workstream client for **Hermes Agent** — the PC power tool of
the rich-desktop-hermes-app project. The phone stays on Discord (it mirrors the
same conversations); atlas is what you live in at the desk.

> The shape mirrors the Discord workspace 1:1: **categories → channels → forum
> posts → conversations (sessions)**. Same objects, rendered as a fast,
> vim-navigable TUI.

Status: **0.12.0 — a tree that stays clean.** Conversations idle 7+ days are
stowed away by default — a Discord-style shelf life without touching Discord —
and every section folds with `enter`, so a long-lived tree stays readable.
Post rows gained right-edge age tags ("3h", "2d", "1w"), non-Discord sources
read as names not slugs (CLI, API, Cron…), and the footer shows visible/total
counts while things are stowed. Folds survive the periodic refresh. Builds on
0.11 (vim-in-the-chat) and 0.10 (theming, panes).

## Mirror

`POST /mirror {session_id, role, content}` relays a single message (your side,
sent immediately on send). `POST /mirror_turn {session_id, since}` reconstructs
the finished turn from the session's messages — reasoning + reply, tool lines,
stats card, native `(i/n)` chunking. Posts are made by the bot, which the
gateway hard-drops on ingest — mirroring can never re-trigger a turn.
`GET /stats/card?session_id=…&since=…` exposes the rendered card for the TUI.

## Hub

`hub/atlas-hub.py` (stdlib python3) serves the workstream tree on
`127.0.0.1:8643` by combining the Hermes `state.db` (session ↔ thread
bindings, opened read-only) with the Discord REST API (server structure).
Run it on the Hermes host. Without it Atlas falls back to a flat session
list.

```console
$ HERMES_HOME=~/.hermes python3 hub/atlas-hub.py   # as the hermes user
```

## Run

```console
# from the nixos-config repo root
$ nix build .#atlas && ./result/bin/atlas

# render one frame and exit (no TTY needed — used for screenshots/CI)
$ atlas --once --width 128 --height 40
```

Development (without nix):

```console
$ go build -o atlas . && ./atlas
```

## Live mode

Atlas finds its API credentials in this order:

1. `ATLAS_API_KEY` / `ATLAS_API_URL` environment variables
2. `~/.config/atlas/env` (KEY=VALUE lines)
3. the active hermes home `.env` (when running as the hermes user)

Without a key it falls back to demo data.

## Keys (v0)

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move the tree · scroll the transcript (focused) |
| `tab` / `shift+tab` | focus: tree ⇄ transcript |
| `1` / `2` | toggle the tree (left) / rail (right) pane |
| `z` | focus mode — both panes away; `z` restores what was visible |
| `g` / `G` | first / last row · scroll top / bottom |
| `pgup` / `pgdn` | scroll a screenful (a real page now) |
| `ctrl+u` / `ctrl+d` | half page up / down |
| `enter` | open a post · fold / unfold a section |
| `.` / `,` | hide / show conversations idle 7+ days |
| `/` | search all sessions (tree) · find inside the chat (transcript) |
| `n` / `N` | next / previous find hit (after `/` in the chat) |
| `{` / `}` | jump to the previous / next message |
| `v` / `V` | visual select: lines / whole messages |
| `y` | yank the selection to the clipboard (OSC 52; raw markdown for `V`) |
| `Q` | quote the message (or selection) into the composer |
| `e` | expand / collapse long messages |
| `r` | show / hide reasoning |
| `i` | insert mode — write a message |
| `enter` (insert) | send it — runs a turn, streams the reply |
| `esc` | leave insert · cancel visual / find · detach · back to bottom |
| `x` | stop the running turn (while streaming) |
| `R` | refresh now — the tree also auto-refreshes every 10s |
| `?` | full keymap panel |
| `q` / `ctrl+c` | quit |

`atlas --open <session-id>` pins the app to one conversation at startup
(handy for testing).

## Layout

- **Left** — workstream tree: categories → channels → posts, unread badges.
- **Center** — transcript of the open conversation with the live streaming
  reply while a turn runs.
- **Right** — details, tree stats, mode.
- **Bottom** — composer (NORMAL / INSERT / STREAM) + status bar.

## Architecture

```
┌─────────────┐  chat/stream (SSE)  ┌───────────────────────────┐
│  atlas (Go) │ ──────────────────► │  hermes gateway           │
│  TUI client │ ◄────────────────── │  api_server :8642         │
└──────┬──────┘  sessions·messages  │  turns · runs · stops     │
       │                            └───────────────────────────┘
       │ tree · search · cards · mirror
       ▼
┌─────────────────┐  read-only  ┌───────────────┐   REST   ┌─────────┐
│ atlas-hub (py)  │ ──────────► │   state.db    │          │ Discord │
│ 127.0.0.1:8643  │             │   (hermes)    │ ───────► │   API   │
└─────────────────┘             └───────────────┘          └─────────┘
```

- The **gateway** owns the truth: sessions, transcripts, turns. Atlas sends
  turns through `POST /api/sessions/{id}/chat/stream` (SSE) and stops them via
  `POST /v1/runs/{id}/stop`.
- The **hub** joins the gateway's `state.db` (session ↔ Discord thread
  bindings, opened read-only) with the Discord REST API; it serves the
  workstream tree, full-text search, stats cards, and the mirror relay. Single
  stdlib-python file by design — it must run anywhere with zero deps.
- The TUI is a **pure client**: no state beyond read marks
  (`~/.config/atlas/state.json`) and credentials (`~/.config/atlas/env`).
  Closing it never affects the gateway; the phone stays on Discord against the
  same threads.

## Development

Go module `atlas`, charmbracelet stack (Bubble Tea + Lip Gloss — same
foundations as `head-dash`). Package layout:

- `main.go` — entry point and flags (`--once`, `--open`, `--version`)
- `internal/hermes/` — API client (`client.go`), SSE streaming (`stream.go`),
  hub client (`hub.go`) — the only package that talks to the network
- `internal/ui/` — Bubble Tea model (`model.go`), async commands (`commands.go`),
  tree building (`tree.go`), read state (`state.go`), rendering (`view.go`,
  `theme.go`), demo fallback (`demo.go`)
- `internal/config/` — on-disk paths under `~/.config/atlas`
- `hub/atlas-hub.py` — the local hub (stdlib python3)

```console
$ nix develop              # go toolchain
$ make check               # fmt + vet + tests (full pre-commit gate)
$ make build && ./atlas    # run it
$ make once                # render one frame (no TTY — screenshots/CI)
$ nix build                # package; runs the test suite in the sandbox
```

`--once` renders a single frame so screens can be captured and verified without
a TTY. Design notes: see the `rich-desktop-hermes-app` investigation
(categories/channels/posts are the real objects; Discord thread ↔ session is
1:1; TUI writes mirror into the thread).
