# atlas

Keyboard-driven workstream client for **Hermes Agent** — the PC power tool of
the rich-desktop-hermes-app project. The phone stays on Discord (it mirrors the
same conversations); atlas is what you live in at the desk.

> The shape mirrors the Discord workspace 1:1: **categories → channels → forum
> posts → conversations (sessions)**. Same objects, rendered as a fast,
> vim-navigable TUI.

Status: **0.0.1 — skeleton.** UI shell + packaging. Live Hermes wiring next.

## Run

```console
# from the nixos-config repo root
$ nix build .#atlas && ./result/bin/atlas

# render one frame and exit (no TTY needed — used for screenshots/CI)
$ atlas --once --width 128 --height 40
```

Development (without nix):

```console
$ go build ./... && ./atlas
```

## Keys (v0)

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move in the workstream tree |
| `g` / `G` | jump to top / bottom |
| `tab` / `shift+tab` | cycle pane focus |
| `enter` | open the selected post |
| `?` | key help in the status bar |
| `q` / `ctrl+c` | quit |

## Layout

- **Left** — workstream tree: categories → channels → posts, unread badges.
- **Center** — transcript of the open conversation: streaming text, tool
  cards, inline approvals. (Demo content until the Hermes wiring lands.)
- **Right** — details, active runs, context meter.
- **Bottom** — composer + status bar.

## Development

Go module `atlas`, built with the charmbracelet stack (Bubble Tea + Lip Gloss —
same foundations as `head-dash`). `--once` renders a single frame so frames can
be captured and verified without a TTY.

Design notes: see the `rich-desktop-hermes-app` investigation
(categories/channels/posts are the real objects; Discord thread ↔ session is
1:1; TUI writes mirror into the thread).
