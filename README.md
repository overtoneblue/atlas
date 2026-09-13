# atlas

Keyboard-driven workstream client for **Hermes Agent** — the PC power tool of
the rich-desktop-hermes-app project. The phone stays on Discord (it mirrors the
same conversations); atlas is what you live in at the desk.

> The shape mirrors the Discord workspace 1:1: **categories → channels → forum
> posts → conversations (sessions)**. Same objects, rendered as a fast,
> vim-navigable TUI.

Status: **0.5.0 — native-parity mirror.** A turn sent from Atlas relays into
the thread exactly like a native one: reasoning in Discord's `-# 💭` subtext,
tool lines with emojis (`💻 terminal`, `🔧 patch([...])`), the reply, and the
`⚡ tok/s` stats card from the hermes-stats recorder — and Atlas renders the
same reasoning, tool previews and card in its own transcript.

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
| `j` / `k`, `↓` / `↑` | move in the workstream tree |
| `g` / `G` | jump to top / bottom |
| `tab` / `shift+tab` | cycle pane focus |
| `enter` | open the selected post |
| `i` | insert mode — write a message |
| `enter` (insert) | send it — runs a turn, streams the reply |
| `esc` | leave insert · detach a running stream |
| `R` | refresh tree + sessions |
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

## Development

Go module `atlas`, built with the charmbracelet stack (Bubble Tea + Lip Gloss —
same foundations as `head-dash`). `--once` renders a single frame so frames can
be captured and verified without a TTY.

Design notes: see the `rich-desktop-hermes-app` investigation
(categories/channels/posts are the real objects; Discord thread ↔ session is
1:1; TUI writes mirror into the thread).
