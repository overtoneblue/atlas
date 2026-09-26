# PLAN — multiplexing: many workstreams, one window

Status: draft · 2026-09-26 · owner: Nolan

## The ask

Run several agent workstreams at once, in one Atlas instance: watch a stream
in one conversation while writing in another, pop a quick reply without
leaving what you're looking at, keep multiple agents' work visible.

## Where we are

One open session at a time (`openID`), one transcript pane, one stream
(`streamBuf`/`streamCancel`), composer pinned at the bottom of the middle
column. The tree can walk all sessions/profiles; opening another replaces the
transcript.

## The model (recommended): tmux-shaped

Atlas already reads like a visual tmux — lean into it.

- **Tab** = an open workstream (session + its own messages, scroll, stream
  state, composer draft). Tabs live in a bar on the status line; the tree
  stays the navigator that *feeds* tabs.
- **Pane** = a visible viewport of a tab. Start with one; add splits later.
- **Popup** = a transient overlay for *just replying* (lazygit-style) without
  changing your layout.
- **Activity** = a read-mostly digest of every session currently streaming +
  unread landings — "what is everything doing right now".

## Phasing

### Phase 1 — tabs + parallel streams (the real foundation)
The hard part is not the UI, it's the state: streaming must become
per-session.

- `openTabs []tabState` — each holds session id, profile, title, messages,
  scroll, `streamBuf`, `streamCancel`, draft input, find/visual state.
- Stream events arrive tagged with the session id; the UI routes them to the
  right tab (`streamEventMsg{session}`). A background tab streams silently
  and flags attention (unread dot / `*` in the tab bar).
- Mirror relay: `/mirror_turn` fires per *finished turn*, so parallel turns
  just queue — no gateway change needed for tabs.
- Keys (draft): `gt` / `gT` cycle tabs (vim), `1..9` jump, `t` new tab from
  the tree cursor, `X` close tab (confirm if streaming). `q` still quits.
  `esc`-cascade unchanged.
- Persist open tabs + active tab in `state.json`; restore on launch.

### Phase 2 — splits (see two transcripts at once)
- Middle column splits **vertically stacked** (top/bottom) — chat wraps to
  ~60 cols badly side-by-side; stacking keeps line lengths sane. Max 2–3.
- Focus model like tmux: `ctrl+w` prefix (`ctrl+w j/k/w`), the focused pane
  owns the seat of `focus` (tree ⇄ pane ⇄ rail).
- Tree `enter` opens into the *focused* pane instead of replacing.
- Render cost: each pane renders its own window slice; we already only build
  ~viewport-height lines per pane. Fine for the perf bar.

### Phase 3 — popup reply + activity
- `P` on a tree node (or a message): floating overlay with the tail of the
  conversation + composer. Send → mirror → close. Zero layout churn.
- Activity overlay (`@`?): every session streaming now, plus sessions with
  unread landings since last visit — `enter` to jump a tab there. This is the
  "multiple agent windows pop into one to reply" endgame: a single reply
  center.

## Interactions with existing decisions

- **Unread/read-marks**: tab bar uses the same read state; opening a tab
  marks read as opening does today.
- **Cards**: per tab (card state is per session already).
- **Profiles**: a tab is (profile, session) — no special casing; the tab bar
  shows profile color dot.
- **Perf**: extra tabs cost nothing until visible; splits cost one more
  viewport render each. The 10s tick refresh should target the *focused* tab
  only, with cheap unread checks for the rest.

## Open questions (for Caden)

1. Tab-cycle keys: `gt`/`gT` (vim) vs `ctrl+n`/`ctrl+p` (tmux-ish)?
2. Split direction preference — stacked only, or side-by-side too?
3. Do background streams auto-insert their finished turns into an "activity"
   digest, or is a tab dot enough?
4. Should a tab keep a *draft* (unsent composer text) per session? (cheap,
   recommended yes)
