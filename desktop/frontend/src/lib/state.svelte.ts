// Central app state — Svelte 5 runes. ALL mutations flow through `actions`;
// components only read `s` and call actions. Ports the TUI's behaviors:
// stale-stows, folds, unread tracking, vim focus model — plus the v2 live
// turn machine (streamed deltas + tool activity, stop support), the parity
// layer (find, visual/yank, counts, image paste), and spawned-work rows
// (subagent runs + pi tasks nested under their chat).

import type {
  Catalog,
  Completion,
  ExecResult,
  Focus,
  HubNode,
  LiveTurn,
  Message,
  ModelOptions,
  ModelRow,
  Row,
  Session,
  SpawnItem,
  Status,
  TurnEvent,
} from "./types";
import * as api from "./api";
import { buildRows, buildSessionRows } from "./tree";
import { findRuntime } from "./find";
import { tick } from "svelte";

const LS_STALE = "atlas.hideStale";
const LS_READ = "atlas.lastRead";
const LS_OPEN = "atlas.lastOpen"; // session id that was open when the app last ran
const LS_BASE = "atlas.readBase"; // unread baseline (unix seconds)

function loadRead(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(LS_READ) || "{}");
  } catch {
    return {};
  }
}

// Activity older than the first run of this client counts as seen, so a
// fresh or upgraded client isn't a wall of unread dots for chats it never
// opened. Only NEW activity after the baseline marks a chat unread.
function loadBase(): number {
  const v = Number(localStorage.getItem(LS_BASE));
  if (v > 0) return v;
  const now = Date.now() / 1000;
  localStorage.setItem(LS_BASE, String(now));
  return now;
}

export const s = $state({
  status: null as Status | null,
  sections: [] as HubNode[],
  sessions: [] as Session[],
  rows: [] as Row[],
  cursor: 0,
  collapsed: [] as string[], // fold keys (stable name paths)
  hideStale: localStorage.getItem(LS_STALE) !== "0",
  hiddenCount: 0,
  postCount: 0,
  open: null as { profile: string; id: string; title: string } | null,
  messages: [] as Message[],
  loadingOpen: false,
  live: null as LiveTurn | null, // the streaming turn for the open session
  turnBusy: {} as Record<string, boolean>, // sessionID -> turn in flight
  focus: "tree" as Focus,
  mode: "NORMAL" as "NORMAL" | "INSERT",
  statusText: "starting…",
  draft: "",
  drafts: {} as Record<string, string>, // per-chat unsent drafts (in-memory)
  lastRead: loadRead(),
  readBase: loadBase(),
  autoScroll: 0, // bumped to force a scroll-to-bottom
  // find (transcript search; DOM ranges live in the Transcript component)
  findOpen: false,
  findQuery: "",
  findCount: 0,
  findCur: 0,
  findMsg: -1, // message index holding the current match (visual's anchor)
  // visual selection (message-index range)
  visual: false,
  visualAnchor: 0,
  visualCur: 0,
  // parity toggles
  helpOpen: false,
  // display density cycle: 0 full | 1 no reasoning | 2 exact tools | 3 quiet
  dispMode: 0,
  // slash-command surface (hermes-serve via atlasd)
  catalog: null as Catalog | null,
  catComplete: [] as Completion[], // live complete.slash items for catDraft
  catDraft: "", // the draft the completions belong to
  catSeq: 0, // debounce / stale-response guard
  // bumped to force the transcript back to the bottom (send, exec output)
  stickBump: 0,
  // mobile: off-canvas workstreams drawer
  navOpen: false,
  pendingCount: 0, // vim count prefix (3j)
  attachments: [] as string[], // pasted images awaiting send (data URLs)
  // spawned work: parent session id -> its subagent runs + pi tasks
  spawned: {} as Record<string, SpawnItem[]>,
  // the live-log viewer (one spawned run), null when hidden
  logView: null as { item: SpawnItem } | null,
  // transcript paging: tail-first reads load the newest page up front and
  // offset pages back through older history (incl. compaction-archived
  // display rows once the head endpoint supports include_compacted)
  olderOffset: 0,
  olderExhausted: false,
  loadingOlder: false,
  // image zoom preview (click a transcript image)
  lightbox: null as { src: string; alt: string } | null,
  // command palette (composer "/" affordance)
  paletteIdx: 0,
  paletteMoved: false, // user navigated the palette explicitly (arrows)
  paletteDismissed: null as string | null,
  // /model picker: providers → models, fetched from the serve for the open
  // session; the composer draft filters it while open.
  modelPick: null as null | ModelPickState,
});

let chatScroller: HTMLDivElement | null = null;
let lightboxOriginEl: HTMLElement | null = null;

// The Lightbox registers its animated close here so keymap dismissals play
// the same reverse-FLIP; the fallback is an instant unmount.
export const lightboxRuntime: { close: (() => void) | null } = { close: null };

const MSG_PAGE = 400;

function cleanMessages(ms: Message[]): Message[] {
  return ms
    .filter((m) => !!m.tool_name || m.role === "user" || m.role === "assistant")
    .filter((m) => (!!m.tool_name && !!m.content?.trim()) || !!m.content?.trim())
    .sort((a, b) => a.id - b.id);
}

export const actions = {
  registerChatScroller(el: HTMLDivElement | null) {
    chatScroller = el;
  },

  // Keyboard scrolling glides — native Chromium smooth scroll, same engine
  // as the wheel. Holding j/k re-targets mid-flight, so it reads as one
  // continuous motion.
  scrollChat(delta: number) {
    if (!chatScroller) return;
    chatScroller.scrollTo({ top: chatScroller.scrollTop + delta * 44, behavior: "smooth" });
  },

  scrollChatTo(pos: "top" | "bottom") {
    if (!chatScroller) return;
    chatScroller.scrollTo({ top: pos === "top" ? 0 : chatScroller.scrollHeight, behavior: "smooth" });
  },

  async boot() {
    try {
      s.status = await api.Status();
      // Live turns already in flight server-side: flag them now so the tree
      // shows ◍ and opening one attaches mid-stream.
      if (s.status?.turns?.length) {
        const busy = { ...s.turnBusy };
        for (const t of s.status.turns) busy[t.session] = true;
        s.turnBusy = busy;
      }
    } catch {
      s.status = null;
    }
    await this.refreshTree(true);
    void this.refreshSpawned();
    void this.loadCatalog();

    // Startup target, in order: --open (explicit) → the chat that was open
    // when the app last ran → the freshest chat overall. Never "first row":
    // the hub orders channels by Discord position, so that was simply
    // whatever sat atop the first channel — the same old chat every launch.
    // Focus and mode are left alone; the cursor is parked on the chat's row
    // so j/k/enter carry on from where you left off.
    let want = "";
    try {
      want = await api.InitialSession();
    } catch {
      /* binding missing or unset */
    }
    const target =
      (want ? lookupPost(want) : null) ?? lookupPost(rememberedOpen()) ?? freshestPost();
    if (target) {
      void this.openPost(target);
      const idx = s.rows.findIndex((r) => r.node.session_id === target.session_id);
      if (idx >= 0) s.cursor = idx;
    }
  },

  async refreshTree(initial = false) {
    try {
      const t = await api.GetTree();
      s.sections = t?.sections ?? [];
      s.sessions = [];
      if (initial) {
        const posts = countPosts(s.sections);
        s.statusText = `live · hub · ${posts} posts`;
      }
    } catch {
      try {
        const sess = await api.GetSessions(200);
        s.sessions = sess;
        s.sections = [];
        if (initial) s.statusText = `live · ${sess.length} sessions (hub unavailable)`;
      } catch (e2: unknown) {
        if (initial) s.statusText = "offline — " + errText(e2);
      }
    }
    this.rebuild(initial);
  },

  // refreshSpawned pulls the spawned-work index and rebuilds the tree only
  // when something changed (so the cursor doesn't twitch every poll).
  async refreshSpawned() {
    try {
      const list = await api.GetSpawned();
      const map: Record<string, SpawnItem[]> = {};
      for (const it of list.items ?? []) {
        if (!it.parent) continue; // cannot be placed under a chat
        if (!map[it.parent]) map[it.parent] = [];
        map[it.parent].push(it);
      }
      if (JSON.stringify(map) === JSON.stringify(s.spawned)) return;
      s.spawned = map;
      this.rebuild();
    } catch {
      /* hub down: keep the last known map */
    }
  },

  rebuild(initial = false) {
    const cur = s.rows[s.cursor]?.key ?? "";
    const now = Date.now() / 1000;
    const ctx = {
      collapsed: new Set(s.collapsed),
      hideStale: s.hideStale,
      now,
      spawned: s.spawned,
      keep: s.open?.id ?? null,
    };
    const built = s.sections.length
      ? buildRows(s.sections, ctx)
      : buildSessionRows(s.sessions, ctx);
    s.rows = built.rows;
    s.hiddenCount = built.hidden;
    s.postCount = built.total;
    let idx = s.rows.findIndex((r) => r.key === cur);
    if (idx < 0) idx = Math.min(Math.max(0, s.cursor), Math.max(0, s.rows.length - 1));
    s.cursor = initial ? 0 : idx;
  },

  // ---- tree navigation ------------------------------------------------

  move(delta: number) {
    if (s.focus === "chat") return this.scrollChat(delta * 3);
    if (s.focus !== "tree") return;
    const n = s.rows.length;
    if (!n) return;
    s.cursor = Math.max(0, Math.min(n - 1, s.cursor + delta));
  },

  toTop() {
    if (s.focus === "chat") return this.scrollChatTo("top");
    if (s.focus === "tree") s.cursor = 0;
  },

  toBottom() {
    if (s.focus === "chat") return this.scrollChatTo("bottom");
    if (s.focus === "tree") s.cursor = Math.max(0, s.rows.length - 1);
  },

  enter() {
    const r = s.rows[s.cursor];
    if (!r) return;
    if (r.node.kind === "spawn" && r.node.spawn) {
      this.openSpawn(r.node.spawn);
      return;
    }
    if (r.node.kind === "post" && r.node.session_id) {
      void this.openPost(r.node);
      return;
    }
    this.toggleFold(r.key);
  },

  clickRow(i: number) {
    s.cursor = i;
    this.enter();
  },

  openSpawn(it: SpawnItem) {
    s.logView = { item: it };
    this.setFocus("chat");
    s.statusText = `${it.kind} ${it.id} — esc closes`;
  },

  closeLog() {
    s.logView = null;
    this.setFocus("tree");
    s.statusText = "";
  },

  // ---- mobile nav (off-canvas workstreams drawer) -----------------------

  toggleNav() {
    s.navOpen = !s.navOpen;
  },

  closeNav() {
    if (s.navOpen) s.navOpen = false;
  },

  toggleFold(key: string) {
    const r = s.rows.find((x) => x.key === key);
    if (!r || r.node.kind === "post") return;
    if (!(r.node.children ?? []).length) return;
    if (s.collapsed.includes(key)) {
      s.collapsed = s.collapsed.filter((k) => k !== key);
    } else {
      s.collapsed = s.collapsed.concat([key]);
    }
    this.rebuild();
    const idx = s.rows.findIndex((x) => x.key === key);
    if (idx >= 0) s.cursor = idx;
    s.statusText = `${s.collapsed.includes(key) ? "folded" : "unfolded"} ${r.node.name}`;
  },

  foldAt(collapse: boolean) {
    const r = s.rows[s.cursor];
    if (!r || r.node.kind === "post" || r.node.kind === "spawn") return;
    if (!(r.node.children ?? []).length) return;
    const folded = s.collapsed.includes(r.key);
    if (collapse && !folded) this.toggleFold(r.key);
    if (!collapse && folded) this.toggleFold(r.key);
  },

  toggleStale() {
    s.hideStale = !s.hideStale;
    localStorage.setItem(LS_STALE, s.hideStale ? "1" : "0");
    this.rebuild();
    s.statusText = s.hideStale
      ? `hiding ${s.hiddenCount} chats idle 7d+ — ',' shows them`
      : "showing all chats";
  },

  // ---- transcript ------------------------------------------------------

  async openPost(node: HubNode) {
    const profile = node.profile ?? "default";
    const id = node.session_id ?? "";
    if (!id) return;
    const switching = !s.open || s.open.id !== id;
    // Leaving a chat: whatever arrived while it was open was seen, and an
    // unsent draft stays with the chat it was typed in.
    if (s.open && s.open.id !== id) {
      this.markRead(s.open.id);
      s.drafts[s.open.id] = s.draft;
    }
    s.open = { profile, id, title: node.name };
    if (switching) s.draft = s.drafts[id] ?? "";
    rememberOpen(id);
    // A stale chat opened on purpose (restored at boot, --open) must keep a
    // tree row for the cursor; rebuild() exempts the open chat from stowing.
    if (!s.rows.some((r) => r.node.session_id === id)) this.rebuild();
    s.loadingOpen = true;
    s.visual = false;
    s.findOpen = false;
    s.findMsg = -1;
    s.logView = null;
    s.pendingCount = 0;
    this.markRead(id);
    try {
      const msgs = await api.GetMessages(profile, id, MSG_PAGE, 0, "latest");
      if (s.open?.id !== id) return; // navigated away mid-flight
      s.messages = cleanMessages(msgs);
      s.olderOffset = msgs.length;
      s.olderExhausted = msgs.length === 0;
      s.paletteDismissed = null;
      s.modelPick = null;
      s.statusText = `${s.messages.length} messages · ${id}`;
      void this.attachTurn(id, profile);
    } catch (e: unknown) {
      // A stale failure must not clear the chat the user moved to; only the
      // chat this load belongs to may report it.
      if (s.open?.id !== id) return;
      s.messages = [];
      s.statusText = "transcript failed: " + errText(e);
    } finally {
      if (s.open?.id === id) {
        s.loadingOpen = false;
        s.autoScroll += 1;
      }
    }
  },

  async refreshTranscript() {
    const open = s.open;
    if (!open) return;
    try {
      const msgs = await api.GetMessages(open.profile, open.id, MSG_PAGE, 0, "latest");
      if (s.open?.id !== open.id) return;
      s.messages = cleanMessages(msgs);
      s.olderOffset = msgs.length;
      s.olderExhausted = msgs.length === 0;
      s.autoScroll += 1;
    } catch {
      /* keep the current transcript on refresh failure */
    }
  },

  // attachTurn hydrates a mid-turn session: when a turn is already
  // streaming server-side (sent from another client, or started before an
  // app reopen), the daemon replays the live buffer so this client shows
  // the whole turn so far, then continues on the event stream.
  async attachTurn(id: string, profile: string) {
    try {
      const t = await api.FetchTurn(id);
      if (!t?.active || s.open?.id !== id) return;
      s.live = {
        session: id,
        profile: t.profile ?? profile,
        segments: (t.segments ?? []).map((g) =>
          g.type === "tool"
            ? ({ type: "tool", name: g.name ?? "?", state: g.state ?? "running" } as const)
            : ({ type: "text", text: g.text ?? "" } as const),
        ),
        error: "",
      };
      s.turnBusy = { ...s.turnBusy, [id]: true };
      s.autoScroll += 1;
    } catch {
      /* daemon without the endpoint / no live turn: nothing to attach */
    }
  },

  // deletePost removes a chat for good ("✕" on a tree row, click twice to
  // confirm). Refused server-side while a turn is live in that session.
  async deletePost(sid: string, profile: string) {
    try {
      await api.DeleteSession(profile || "default", sid);
      s.statusText = `deleted ${sid}`;
      if (s.open?.id === sid) {
        s.open = null;
        s.messages = [];
        s.live = null;
      }
      forgetOpen(sid);
      delete s.drafts[sid];
      void this.refreshTree();
    } catch (e: unknown) {
      s.statusText = "delete failed: " + errText(e);
    }
  },

  // loadOlder pages back through history (offset paging from the tail).
  // The prepend grows the transcript at the top, so we restore the scroll
  // anchor once the DOM lands: scrollTop shifts by the height delta and
  // the view never jumps.
  async loadOlder() {
    const open = s.open;
    if (!open || s.loadingOlder || s.olderExhausted || s.loadingOpen) return;
    s.loadingOlder = true;
    const el = chatScroller;
    const prevH = el?.scrollHeight ?? 0;
    const prevTop = el?.scrollTop ?? 0;
    try {
      const msgs = await api.GetMessages(open.profile, open.id, MSG_PAGE, s.olderOffset, "latest");
      if (s.open?.id !== open.id) return; // navigated away mid-flight
      s.olderOffset += msgs.length;
      // A short page is normal at the archive boundary — only a truly
      // empty read means history is exhausted.
      if (msgs.length === 0) s.olderExhausted = true;
      const have = new Set(s.messages.map((m) => m.id));
      const older = cleanMessages(msgs).filter((m) => !have.has(m.id));
      if (older.length) {
        s.messages = [...older, ...s.messages];
        await tick();
        if (el) el.scrollTop = prevTop + (el.scrollHeight - prevH);
      }
    } catch (e: unknown) {
      if (s.open?.id === open.id) s.statusText = "older messages failed: " + errText(e);
    } finally {
      s.loadingOlder = false;
    }
  },

  // ---- find (transcript search) ---------------------------------------

  findStart() {
    if (s.logView) {
      s.statusText = "close the log view first (esc)";
      return;
    }
    if (!s.open) {
      s.statusText = "open a workstream to search";
      return;
    }
    s.findOpen = true;
    s.findQuery = "";
    s.findCount = 0;
    s.findCur = 0;
    s.pendingCount = 0;
    // vim-true: starting a search does NOT cancel a visual selection —
    // the match extends it (see syncFindMsg below).
  },

  findSetQuery(q: string) {
    s.findQuery = q;
    s.findCur = 0;
    findRuntime.recompute?.();
    if (s.findCount > 0) findRuntime.goto?.(0);
    syncFindMsg();
  },

  findAccept() {
    s.findOpen = false;
    if (s.findQuery && s.findCount > 0) {
      findRuntime.goto?.(s.findCur);
      syncFindMsg();
      s.statusText = `match ${s.findCur + 1}/${s.findCount} — n/N step`;
    } else {
      s.statusText = s.findQuery ? "no matches" : "";
    }
  },

  findClose() {
    s.findOpen = false;
    if (s.findQuery) s.statusText = `search: ${s.findQuery} · n/N step`;
  },

  findNext(dir: number) {
    if (!s.findQuery) {
      s.statusText = "no search — press / first";
      return;
    }
    if (!s.findCount) findRuntime.recompute?.();
    if (!s.findCount) {
      s.statusText = "no matches";
      return;
    }
    const n = s.findCount;
    s.findCur = ((s.findCur + dir) % n + n) % n;
    findRuntime.goto?.(s.findCur);
    syncFindMsg();
    s.statusText = `match ${s.findCur + 1}/${n}`;
  },

  // ---- visual mode -----------------------------------------------------

  visualStart(all: boolean) {
    if (!s.open || !s.messages.length) {
      s.statusText = "nothing to select";
      return;
    }
    s.findOpen = false;
    s.visual = true;
    if (all) {
      s.visualAnchor = 0;
      s.visualCur = s.messages.length - 1;
      s.statusText = `visual: whole conversation (${s.messages.length}) — y yank · esc cancel`;
    } else {
      // vim-true anchor: a completed find leaves the cursor at its match.
      const fromFind = !!(s.findQuery && s.findCount > 0 && s.findMsg >= 0);
      const i = fromFind
        ? Math.min(s.findMsg, s.messages.length - 1)
        : topVisibleMessage();
      s.visualAnchor = i;
      s.visualCur = i;
      s.statusText = fromFind
        ? `visual from match ${s.findCur + 1}/${s.findCount} — j/k · n/N extend · y yank · esc cancel`
        : "visual — j/k extend · y yank · esc cancel";
    }
    scrollRowIntoView(s.visualCur, "nearest");
  },

  visualMove(delta: number) {
    if (!s.visual) return;
    const n = s.messages.length;
    s.visualCur = Math.max(0, Math.min(n - 1, s.visualCur + delta));
    scrollRowIntoView(s.visualCur, "nearest");
  },

  visualCancel() {
    s.visual = false;
    s.statusText = "";
  },

  async visualYank() {
    if (!s.visual) return;
    const a = Math.min(s.visualAnchor, s.visualCur);
    const b = Math.max(s.visualAnchor, s.visualCur);
    const lines: string[] = [];
    for (let i = a; i <= b; i++) {
      const m = s.messages[i];
      if (!m || m.tool_name || m.role === "note" || !m.content?.trim()) continue;
      lines.push(`${roleName(m.role)}: ${m.content.trim()}`);
    }
    const text = lines.join("\n\n");
    s.visual = false;
    if (!text) {
      s.statusText = "nothing to yank";
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      s.statusText = `yanked ${lines.length} messages · ${text.length} chars`;
    } catch {
      fallbackClipboard(text);
      s.statusText = `yanked ${lines.length} messages (fallback copy)`;
    }
  },

  // ---- parity toggles --------------------------------------------------

  toggleHelp() {
    s.helpOpen = !s.helpOpen;
  },

  closeHelp() {
    s.helpOpen = false;
  },

  // cycleDisplay steps the transcript's activity density. One key (h with
  // chat focus, r, or /display) advances: full → reasoning hidden → exact
  // tool calls → quiet (messages only) → full.
  cycleDisplay() {
    s.dispMode = (s.dispMode + 1) % 4;
    s.statusText = [
      "display: full (reasoning + tool calls)",
      "display: reasoning hidden",
      "display: exact tool calls",
      "display: quiet — messages only",
    ][s.dispMode];
  },

  // loadCatalog warms the slash-command palette from hermes-serve.
  async loadCatalog() {
    try {
      s.catalog = await api.GetCommands();
    } catch {
      /* palette falls back to atlas-local commands */
    }
  },

  addAttachment(dataUrl: string) {
    if (!dataUrl.startsWith("data:image/")) return;
    if (s.attachments.length >= 4) {
      s.statusText = "max 4 images per message";
      return;
    }
    s.attachments = s.attachments.concat([dataUrl]);
    s.statusText = `image attached (${s.attachments.length}) — enter sends`;
  },

  removeAttachment(i: number) {
    s.attachments = s.attachments.filter((_, k) => k !== i);
  },

  // ---- live turns ------------------------------------------------------

  // send echoes the user message locally, then fires the turn; deltas and
  // lifecycle arrive on "atlas:turn" events. Attached images ride as
  // native vision parts; they're also persisted on the head side (best
  // effort) and referenced as MEDIA: lines so history re-renders them.
  async send() {
    if (!s.open) return;
    // TUI-parity dispatch: a draft that begins with "/" is a command, not a
    // message — route it (arguments included) even when the palette is
    // hidden or dismissed. Local atlas commands win; everything else goes
    // to hermes (e.g. "/model sonnet").
    const cmd = s.draft.trim();
    if (cmd.length > 1 && cmd.startsWith("/") && !cmd.startsWith("//")) {
      const parts = cmd.split(/\s+/);
      const first = parts[0].toLowerCase();
      const local = COMMANDS.find((c) => c.name.toLowerCase() === first);
      this.clearPalette();
      if (local) {
        local.run(parts.slice(1).join(" "));
        return;
      }
      void this.execCommand(cmd);
      return;
    }
    if (s.turnBusy[s.open.id]) {
      s.statusText = "a turn is already running — ctrl+c stops it";
      return;
    }
    const text = s.draft.trim();
    const imgs = s.attachments.slice();
    if (!text && !imgs.length) return;
    const open = s.open;
    const echoID = -Date.now();
    s.draft = "";
    s.attachments = [];

    let paths: string[] = [];
    if (imgs.length) {
      try {
        paths = await Promise.all(imgs.map((u) => api.AttachImage(u)));
      } catch {
        paths = []; // vision parts still deliver the images
      }
    }
    const refs = paths.filter(Boolean).map((p) => `MEDIA:${p}`);
    const wireText = [text, ...refs].filter((x) => x && x.length).join("\n\n");

    s.messages = s.messages.concat([
      {
        id: echoID,
        role: "user",
        content: text,
        tool_name: "",
        reasoning: "",
        timestamp: Date.now() / 1000,
        images: imgs.length ? imgs : undefined,
      },
    ]);
    s.autoScroll += 1;
    s.stickBump += 1;

    // Wire shape: plain string, or content parts when images are attached.
    const payload: string | unknown[] = imgs.length
      ? [
          ...(wireText ? [{ type: "text", text: wireText }] : []),
          ...imgs.map((u) => ({ type: "image_url", image_url: { url: u } })),
        ]
      : wireText;

    try {
      await api.SendMessage(open.profile, open.id, payload);
      s.statusText = imgs.length
        ? `sent with ${imgs.length} image(s) — streaming…`
        : "streaming…";
    } catch (e: unknown) {
      s.messages = s.messages.filter((m) => m.id !== echoID);
      s.draft = text;
      s.attachments = imgs;
      s.statusText = "send failed: " + errText(e);
    }
  },

  async stopTurn() {
    if (!s.open || !s.turnBusy[s.open.id]) return;
    s.statusText = "stopping…";
    try {
      await api.StopTurn(s.open.id);
    } catch (e: unknown) {
      s.statusText = "stop failed: " + errText(e);
    }
  },

  // restartDaemon replaces `systemctl restart atlasd`: ask the daemon to exit
  // (its supervisor relaunches it), wait until it has actually gone down and
  // come back, then reload — which also pulls in a freshly deployed bundle.
  // Refused while turns are live (their live view dies with the daemon; the
  // runs themselves continue on head) unless forced.
  async restartDaemon(force: boolean) {
    s.statusText = "restarting atlasd…";
    try {
      await api.RestartDaemon(force);
    } catch (e: unknown) {
      const msg = errText(e);
      s.statusText = "restart failed: " + msg;
      this.pushNote(`/restart — ${msg}`);
      return;
    }
    s.statusText = "atlasd restarting — waiting for it to come back…";
    const t0 = Date.now();
    let sawDown = false;
    while (Date.now() - t0 < 60_000) {
      await new Promise((r) => setTimeout(r, 400));
      try {
        await new Promise<void>((resolve, reject) => {
          const t = setTimeout(() => reject(new Error("timeout")), 1500);
          api.Status().then(
            () => (clearTimeout(t), resolve()),
            (e) => (clearTimeout(t), reject(e)),
          );
        });
        // Up. Trust it only after we saw it go down (the old process lingers
        // ~2s while it drains) — or after long enough that it can't be.
        if (sawDown || Date.now() - t0 > 12_000) {
          location.reload();
          return;
        }
      } catch {
        sawDown = true;
      }
    }
    s.statusText = "atlasd did not come back within 60s — check the service";
  },

  // ---- hermes slash commands (hermes-serve) ----------------------------

  // fetchCompletions pulls live fuzzy matches for a bare "/token" draft
  // (debounced; stale responses are dropped via catSeq).
  async fetchCompletions() {
    const draft = s.draft;
    if (!/^\/[a-z0-9_-]*$/i.test(draft) || !s.open) {
      s.catComplete = [];
      s.catDraft = "";
      return;
    }
    const seq = ++s.catSeq;
    await new Promise((r) => setTimeout(r, 90));
    if (seq !== s.catSeq || s.draft !== draft) return;
    try {
      const items = await api.CompleteSlash(draft, s.open.id);
      if (seq !== s.catSeq) return;
      s.catComplete = items;
      s.catDraft = draft;
    } catch {
      s.catComplete = [];
      s.catDraft = "";
    }
  },

  // execCommand runs one hermes command against the open session and folds
  // the result in: text renders as a local note row; dispatch directives
  // follow the desktop semantics (prefill fills the composer, send/skill
  // seed a turn, alias re-executes its target).
  async execCommand(command: string) {
    const open = s.open;
    if (!open) {
      s.statusText = "open a workstream first";
      return;
    }
    // /model with no arguments lands in the picker (arrows/enter/esc on the
    // list); every other command — /model with arguments included — execs
    // on the serve exactly as before.
    if (command.trim() === "/model") {
      void this.openModelPicker();
      return;
    }
    s.statusText = `running ${command}…`;
    try {
      const res = await api.ExecSlash(open.id, command);
      this.foldExec(res, command);
    } catch (e: unknown) {
      const msg = errText(e);
      s.statusText = `${command} failed: ` + msg;
      this.pushNote(`${command} — ${msg}`);
    }
  },

  foldExec(res: ExecResult, invoked: string) {
    if (res.type === "alias" && res.target) {
      void this.execCommand(res.target);
      return;
    }
    if (res.type === "prefill") {
      s.draft = res.message ?? res.display ?? "";
      this.setFocus("composer");
      s.statusText = `${invoked} → composer`;
      return;
    }
    if (res.type === "send" || res.type === "skill") {
      if (res.display) this.pushNote(res.display);
      const msg = res.message ?? "";
      if (msg) void this.sendRaw(msg);
      return;
    }
    const text = res.output ?? res.display ?? res.notice ?? "";
    if (text) this.pushNote(text);
    if (res.warning) s.statusText = res.warning;
    else if (!text) s.statusText = `${invoked} done`;
  },

  // pushNote appends a local-only output row (commands are ephemeral by
  // design: a transcript refresh drops them).
  pushNote(text: string) {
    s.messages = s.messages.concat([
      {
        id: -Date.now(),
        role: "note",
        content: text,
        tool_name: "",
        reasoning: "",
        timestamp: Date.now() / 1000,
      },
    ]);
    s.autoScroll += 1;
    s.stickBump += 1;
  },

  // sendRaw posts a message as a turn without touching the composer
  // (slashes that seed a prompt: /goal, skills, …).
  async sendRaw(text: string) {
    const open = s.open;
    if (!open || !text) return;
    if (s.turnBusy[open.id]) {
      s.statusText = "a turn is already running";
      return;
    }
    const echoID = -Date.now();
    s.messages = s.messages.concat([
      {
        id: echoID,
        role: "user",
        content: text,
        tool_name: "",
        reasoning: "",
        timestamp: Date.now() / 1000,
      },
    ]);
    s.autoScroll += 1;
    s.stickBump += 1;
    try {
      await api.SendMessage(open.profile, open.id, text);
      s.statusText = "streaming…";
    } catch (e: unknown) {
      s.messages = s.messages.filter((m) => m.id !== echoID);
      s.statusText = "send failed: " + errText(e);
    }
  },

  // handleTurnEvent folds one "atlas:turn" event into UI state.
  handleTurnEvent(ev: TurnEvent) {
    const sid = ev.session_id;
    if (ev.kind === "done") {
      s.turnBusy = { ...s.turnBusy, [sid]: false };
    } else if (ev.kind === "started" || ev.kind === "delta" || ev.kind === "tool") {
      if (!s.turnBusy[sid]) s.turnBusy = { ...s.turnBusy, [sid]: true };
    }

    const openHere = s.open?.id === sid;
    switch (ev.kind) {
      case "started":
        if (openHere) {
          s.live = { session: sid, profile: ev.profile ?? "default", segments: [], error: "" };
        }
        break;
      case "delta":
        if (openHere) {
          if (!s.live || s.live.session !== sid) {
            s.live = { session: sid, profile: ev.profile ?? "default", segments: [], error: "" };
          }
          const segs = s.live.segments;
          const last = segs[segs.length - 1];
          if (last && last.type === "text") last.text += ev.text ?? "";
          else segs.push({ type: "text", text: ev.text ?? "" });
          s.autoScroll += 1;
        }
        break;
      case "tool":
        if (openHere) {
          if (!s.live || s.live.session !== sid) {
            s.live = { session: sid, profile: ev.profile ?? "default", segments: [], error: "" };
          }
          const segs = s.live.segments;
          if (ev.tool_state === "done") {
            for (let i = segs.length - 1; i >= 0; i--) {
              const g = segs[i];
              if (g.type === "tool" && g.state === "running" && g.name === ev.tool) {
                g.state = "done";
                break;
              }
            }
          } else {
            segs.push({ type: "tool", name: ev.tool ?? "?", state: "running" });
          }
          s.autoScroll += 1;
        }
        break;
      case "error":
        if (openHere && s.live && s.live.session === sid) {
          s.live.error = ev.error ?? "error";
        }
        if (!openHere) s.statusText = `turn error in ${sid}: ${ev.error ?? "error"}`;
        break;
      case "done":
        if (openHere) {
          s.live = null;
          void this.refreshTranscript();
          s.statusText = ev.stopped
            ? "turn stopped"
            : ev.ok === false
              ? "turn ended with errors"
              : "turn complete";
        }
        break;
      default:
        break;
    }
  },

  isBusy(node: HubNode): boolean {
    return !!node.session_id && !!s.turnBusy[node.session_id];
  },

  // ---- read state ------------------------------------------------------

  markRead(sessionID: string) {
    s.lastRead = { ...s.lastRead, [sessionID]: Date.now() / 1000 };
    localStorage.setItem(LS_READ, JSON.stringify(s.lastRead));
  },

  isUnread(node: HubNode): boolean {
    if (node.kind !== "post" || !node.session_id) return false;
    if (node.session_id === s.open?.id) return false; // on screen right now
    const last = node.last_active ?? 0;
    if (last <= 0) return false;
    // Both sides are unix SECONDS (this compared last*1000 against seconds,
    // so every chat read as unread forever).
    return last > (s.lastRead[node.session_id] ?? s.readBase);
  },

  unreadCount(): number {
    let n = 0;
    for (const r of s.rows) if (this.isUnread(r.node)) n += 1;
    return n;
  },

  // ---- focus / modes ---------------------------------------------------

  setFocus(f: Focus) {
    s.focus = f;
    s.mode = f === "composer" ? "INSERT" : "NORMAL";
  },

  cycleFocus(dir: number) {
    const order: Focus[] = ["tree", "chat", "composer"];
    const idx = Math.max(0, order.indexOf(s.focus));
    const next = order[(idx + dir + order.length) % order.length];
    this.setFocus(next);
  },

  escape() {
    if (s.helpOpen) {
      s.helpOpen = false;
      return;
    }
    if (s.visual) {
      s.visual = false;
      return;
    }
    if (s.findOpen) {
      s.findOpen = false;
      return;
    }
    if (s.logView) {
      this.closeLog();
      return;
    }
    if (s.focus === "composer") {
      if (s.mode === "INSERT") {
        s.mode = "NORMAL";
        return;
      }
      s.focus = "tree";
      return;
    }
    if (s.focus !== "tree") {
      s.focus = "tree";
      return;
    }
    s.statusText = "";
  },

  // ---- image preview ---------------------------------------------------

  // Clicking a transcript image opens the centered zoom preview; the
  // origin element feeds the FLIP open/close animation.
  openLightbox(src: string, alt: string, origin: HTMLElement | null) {
    lightboxOriginEl = origin;
    s.lightbox = { src, alt };
  },

  // Animated dismissal: the Lightbox registers its reverse-FLIP close in
  // lightboxRuntime; the fallback unmounts instantly (reduced motion,
  // unmounted overlay).
  dismissLightbox() {
    if (s.lightbox && lightboxRuntime.close) lightboxRuntime.close();
    else s.lightbox = null;
  },

  takeLightboxOrigin(): HTMLElement | null {
    const el = lightboxOriginEl;
    lightboxOriginEl = null;
    return el;
  },

  closeLightbox() {
    s.lightbox = null;
  },

  // ---- command palette -------------------------------------------------

  paletteReset() {
    s.paletteIdx = 0;
    s.paletteMoved = false;
    void this.fetchCompletions();
  },

  paletteMove(dir: number) {
    const n = paletteItems(s.draft).length;
    if (!n) return;
    s.paletteMoved = true;
    s.paletteIdx = ((s.paletteIdx + dir) % n + n) % n;
  },

  paletteDismiss() {
    s.paletteDismissed = s.draft;
  },

  // Enter runs what the draft clearly points at: the selected row once the
  // user has navigated (arrows/click), else the first command the draft
  // prefixes (e.g. "/he" -> /help). A draft matching nothing by prefix is
  // dispatched to hermes as-is (it answers, e.g. the /models -> /model
  // hint) instead of silently running a fuzzy descendant like
  // /codex-runtime.
  runPalette() {
    const items = paletteItems(s.draft);
    if (!items.length) return;
    let it = s.paletteMoved
      ? items[Math.max(0, Math.min(s.paletteIdx, items.length - 1))]
      : undefined;
    if (!it) {
      const d = s.draft.trim().toLowerCase();
      it = items.find((x) => x.name.toLowerCase().startsWith(d));
    }
    if (!it) {
      const raw = s.draft.trim();
      this.clearPalette();
      if (raw.length > 1 && raw.startsWith("/")) void this.execCommand(raw);
      return;
    }
    this.runItem(it);
  },

  runPaletteAt(i: number) {
    const items = paletteItems(s.draft);
    const it = items[Math.max(0, Math.min(i, items.length - 1))];
    if (!it) return;
    this.runItem(it);
  },

  clearPalette() {
    s.draft = "";
    s.paletteIdx = 0;
    s.paletteMoved = false;
    s.paletteDismissed = null;
    s.catComplete = [];
    s.catDraft = "";
  },

  runItem(it: PaletteItem) {
    this.clearPalette();
    if (it.local) {
      it.local.run();
      return;
    }
    void this.execCommand(it.text ?? it.name);
  },

  // ---- model picker (/model) -------------------------------------------

  // openModelPicker backs bare /model: fetch the provider/model payload for
  // the open session and hand it to the composer as a selectable list. The
  // draft seeds to "/model " so typing narrows the rows.
  async openModelPicker() {
    const open = s.open;
    if (!open) {
      s.statusText = "open a workstream first";
      return;
    }
    s.modelPick = { session: open.id, rows: [], idx: 0, moved: false, loading: true, error: "" };
    s.draft = "/model ";
    this.setFocus("composer");
    s.statusText = "loading models…";
    try {
      const payload = await api.FetchModels(open.id);
      if (!s.modelPick || s.modelPick.session !== open.id) return; // closed or switched
      s.modelPick = { ...s.modelPick, rows: flattenModels(payload), loading: false };
      s.statusText = "";
    } catch (e: unknown) {
      if (!s.modelPick || s.modelPick.session !== open.id) return;
      s.modelPick = { ...s.modelPick, loading: false, error: errText(e) };
      s.statusText = "models failed: " + errText(e);
    }
  },

  pickerMove(dir: number) {
    const mp = s.modelPick;
    if (!mp || mp.loading) return;
    const n = modelPickRows(s.draft).length;
    if (!n) return;
    s.modelPick = { ...mp, moved: true, idx: ((mp.idx + dir) % n + n) % n };
  },

  pickerRun() {
    const rows = modelPickRows(s.draft);
    if (!rows.length) return;
    const mp = s.modelPick;
    if (!mp) return;
    const at = Math.max(0, Math.min(mp.moved ? mp.idx : 0, rows.length - 1));
    this.pickModel(rows[at]);
  },

  pickModelAt(i: number) {
    const rows = modelPickRows(s.draft);
    const row = rows[Math.max(0, Math.min(i, rows.length - 1))];
    if (row) this.pickModel(row);
  },

  pickerClose() {
    s.modelPick = null;
    s.draft = "";
    s.statusText = "";
  },

  // pickModel applies the desktop's exact switch contract: model first,
  // provider pinned with --provider, session-scoped — a pick in one chat
  // never rewrites the profile default. The result renders through the
  // normal exec fold (✓ confirmation, or the rejection's suggestions).
  pickModel(row: ModelRow) {
    const open = s.open;
    s.modelPick = null;
    s.draft = "";
    if (!open) return;
    const cmd = row.slug
      ? `/model ${row.name} --provider ${row.slug} --session`
      : `/model ${row.name} --session`;
    void this.execCommand(cmd);
  },
};

// ---- command palette registry ----------------------------------------
// Atlas-local UI commands (they shadow same-named hermes commands on
// purpose: find/visual/display act on the client). Everything else in the
// palette comes from the live hermes catalog + completion surface.

export interface Command {
  name: string;
  desc: string;
  run: (args?: string) => void;
}

export const COMMANDS: Command[] = [
  { name: "/help", desc: "keys + commands sheet", run: () => actions.toggleHelp() },
  { name: "/stop", desc: "stop the running turn", run: () => void actions.stopTurn() },
  { name: "/display", desc: "cycle display: reasoning · exact tools · quiet", run: () => actions.cycleDisplay() },
  { name: "/find", desc: "search the open transcript", run: () => actions.findStart() },
  { name: "/visual", desc: "start a selection at the top message", run: () => actions.visualStart(false) },
  { name: "/refresh", desc: "reload the transcript from head", run: () => void actions.refreshTranscript() },
  { name: "/stale", desc: "show/hide chats idle 7d+", run: () => actions.toggleStale() },
  { name: "/top", desc: "jump to the oldest loaded message", run: () => actions.scrollChatTo("top") },
  { name: "/bottom", desc: "jump to the newest message", run: () => actions.scrollChatTo("bottom") },
  {
    name: "/restart",
    desc: "restart the atlas daemon + reload the UI (/restart force: even mid-turn)",
    run: (a) => void actions.restartDaemon(/^(force|-f|--force)$/i.test((a ?? "").trim())),
  },
];

// One palette row: local commands run client-side; hermes entries run via
// slash.exec on the daemon side.
export interface PaletteItem {
  name: string;
  desc: string;
  kind: "local" | "command" | "skill";
  local?: Command;
  text?: string; // the exact command text for hermes exec
}

// Browse ("/") lists the full catalog; typing narrows. The cap is only a
// DOM safety rail.
const PALETTE_CAP = 300;

export function paletteItems(draft: string): PaletteItem[] {
  const m = /^\/([a-z0-9_-]*)$/i.exec(draft);
  if (!m) return [];
  const f = m[1].toLowerCase();
  const out: PaletteItem[] = [];
  const seen = new Set<string>();
  for (const c of COMMANDS) {
    if (c.name.slice(1).toLowerCase().startsWith(f)) {
      out.push({ name: c.name, desc: c.desc, kind: "local", local: c });
      seen.add(c.name);
    }
  }
  // Live completion (fuzzy, ranked, skills included) for the current
  // draft; falls back to a prefix filter over the cached catalog.
  const comp = s.catDraft === draft ? s.catComplete : [];
  if (comp.length) {
    for (const it of comp) {
      const raw = it.text.trim();
      const name = raw.startsWith("/") ? raw : "/" + raw;
      if (name === "/" || seen.has(name)) continue;
      out.push({
        name,
        desc: it.meta || it.display || "",
        kind: it.kind === "skill" ? "skill" : "command",
        text: name,
      });
    }
  } else if (s.catalog) {
    for (const [name, desc] of s.catalog.pairs) {
      if (!name.toLowerCase().slice(1).startsWith(f) || seen.has(name)) continue;
      out.push({ name, desc, kind: "command", text: name });
    }
    for (const sk of Object.keys(s.catalog.skills ?? {})) {
      const name = sk.startsWith("/") ? sk : "/" + sk;
      if (!name.toLowerCase().slice(1).startsWith(f) || seen.has(name)) continue;
      out.push({ name, desc: "skill command", kind: "skill", text: name });
    }
  }
  return out.slice(0, PALETTE_CAP);
}

// Visible while the composer draft is a bare "/suffix" (no arguments yet)
// and not dismissed via esc for this exact draft.
export function paletteVisible(): boolean {
  return (
    s.focus === "composer" &&
    s.mode === "INSERT" &&
    s.paletteDismissed !== s.draft &&
    paletteItems(s.draft).length > 0
  );
}

// ---- model picker state ----------------------------------------------
// The /model picker reuses the palette's row look, but owns its own list:
// rows carry (model, provider) pairs so a pick dispatches the desktop's
// exact switch syntax without the user typing it.

export type ModelPickState = {
  session: string;
  rows: ModelRow[];
  idx: number;
  moved: boolean;
  loading: boolean;
  error: string;
};

// One row per (provider, model). The current provider sorts to the top so
// its models are reachable without filtering; the current model is marked.
function flattenModels(p: ModelOptions): ModelRow[] {
  const curModel = (p.model ?? "").trim();
  const curSlug = (p.provider ?? "").trim().toLowerCase();
  const providers = (p.providers ?? [])
    .slice()
    .sort((a, b) => Number(!!b.is_current) - Number(!!a.is_current));
  const rows: ModelRow[] = [];
  for (const prov of providers) {
    const slug = prov.slug ?? "";
    const meta = prov.name ?? slug;
    for (const m of prov.models ?? []) {
      const name = String(m);
      rows.push({
        name,
        slug,
        meta,
        current: curModel !== "" && name === curModel && (!curSlug || slug.toLowerCase() === curSlug),
      });
    }
  }
  return rows;
}

// Rows the picker shows for the current draft. The draft is always of the
// shape "/model <filter>" while the picker is open; anything after the
// command filters name/provider/meta matches.
export function modelPickRows(draft: string): ModelRow[] {
  const mp = s.modelPick;
  if (!mp) return [];
  const m = /^\/model\s*(.*)$/i.exec(draft);
  const q = (m ? m[1] : draft).trim().toLowerCase();
  const rows = q
    ? mp.rows.filter(
        (r) =>
          r.name.toLowerCase().includes(q) ||
          r.slug.toLowerCase().includes(q) ||
          r.meta.toLowerCase().includes(q),
      )
    : mp.rows;
  return rows.slice(0, PALETTE_CAP);
}

// The picker owns the composer while open (composer INSERT focus).
export function modelPickVisible(): boolean {
  return s.focus === "composer" && s.mode === "INSERT" && s.modelPick !== null;
}

// ---- last-open memory + startup target lookup ---------------------------
// The open chat is saved at OPEN time (not on quit): Electron can be killed
// at any moment, and localStorage flushes within moments of a write.

function rememberOpen(id: string) {
  try {
    localStorage.setItem(LS_OPEN, id);
  } catch {
    /* storage denied/full: restore is best-effort */
  }
}

function rememberedOpen(): string {
  return localStorage.getItem(LS_OPEN) ?? "";
}

function forgetOpen(id: string) {
  if (localStorage.getItem(LS_OPEN) === id) localStorage.removeItem(LS_OPEN);
}

// Every post, stowed or not (the visible rows omit stale chats), falling
// back to the flat session list when the hub is down.
function allPosts(): HubNode[] {
  const out: HubNode[] = [];
  const walk = (n: HubNode) => {
    if (n.kind === "post" && n.session_id) out.push(n);
    for (const k of n.children ?? []) walk(k);
  };
  for (const sec of s.sections) walk(sec);
  if (!out.length) {
    for (const x of s.sessions) {
      out.push({
        kind: "post",
        name: x.title || x.id,
        session_id: x.id,
        profile: "default",
        last_active: x.last_active,
        message_count: x.message_count,
      });
    }
  }
  return out;
}

// A chat that no longer exists (deleted) resolves to null and the caller
// falls through to the next candidate.
function lookupPost(id: string): HubNode | null {
  if (!id) return null;
  return allPosts().find((p) => p.session_id === id) ?? null;
}

function freshestPost(): HubNode | null {
  let best: HubNode | null = null;
  for (const p of allPosts()) {
    if (!best || (p.last_active ?? 0) > (best.last_active ?? 0)) best = p;
  }
  return best;
}

function countPosts(sections: HubNode[]): number {
  let n = 0;
  const walk = (node: HubNode) => {
    if (node.kind === "post") n += 1;
    for (const k of node.children ?? []) walk(k);
  };
  for (const sec of sections) walk(sec);
  return n;
}

function errText(e: unknown): string {
  if (e == null) return "unknown error";
  if (typeof e === "string") return e;
  const m = (e as { message?: string }).message;
  return m ?? String(e);
}

// The topmost visibly-fully-or-partially message row: the `v` anchor when
// no find cursor exists.
function topVisibleMessage(): number {
  if (!chatScroller) return 0;
  const cr = chatScroller.getBoundingClientRect();
  for (const el of chatScroller.querySelectorAll<HTMLElement>("[data-mi]")) {
    if (el.getBoundingClientRect().bottom >= cr.top + 8) {
      return Number(el.dataset.mi ?? 0);
    }
  }
  return Math.max(0, s.messages.length - 1);
}

// After any find jump: remember the match's message (visual's anchor seed)
// and, while a selection is active, extend it to the match — vim acts the
// same way (`/`, `n`, `N` all work mid-visual).
function syncFindMsg() {
  const mi = findRuntime.msgAt?.(s.findCur);
  if (mi == null || mi < 0) return;
  s.findMsg = mi;
  if (s.visual) {
    s.visualCur = Math.max(0, Math.min(s.messages.length - 1, mi));
    scrollRowIntoView(s.visualCur, "nearest");
  }
}

function scrollRowIntoView(i: number, block: ScrollLogicalPosition = "center") {
  const el = document.querySelector<HTMLElement>(`[data-mi="${i}"]`);
  el?.scrollIntoView({ block, behavior: "auto" });
}

function roleName(role: string): string {
  if (role === "user") return "Caden";
  const p = s.open?.profile ?? "default";
  return p === "default" ? "Nolan" : p.charAt(0).toUpperCase() + p.slice(1);
}

function fallbackClipboard(text: string) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand("copy");
  } catch {
    /* nothing left to try */
  }
  ta.remove();
}
