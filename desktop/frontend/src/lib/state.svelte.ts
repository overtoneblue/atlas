// Central app state — Svelte 5 runes. ALL mutations flow through `actions`;
// components only read `s` and call actions. Ports the TUI's behaviors:
// stale-stows, folds, unread tracking, vim focus model — plus the v2 live
// turn machine (streamed deltas + tool activity, stop support), the parity
// layer (find, visual/yank, counts, image paste), and spawned-work rows
// (subagent runs + pi tasks nested under their chat).

import type {
  Focus,
  HubNode,
  LiveTurn,
  Message,
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

function loadRead(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(LS_READ) || "{}");
  } catch {
    return {};
  }
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
  lastRead: loadRead(),
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
  showReasoning: false,
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
  paletteDismissed: null as string | null,
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
    } catch {
      s.status = null;
    }
    await this.refreshTree(true);
    void this.refreshSpawned();

    // Open target: --open session when present, else the first visible post.
    let target: HubNode | null = null;
    try {
      const want = await api.InitialSession();
      if (want) {
        const row = s.rows.find((r) => r.node.session_id === want);
        if (row) target = row.node;
      }
    } catch {
      /* binding missing or unset */
    }
    if (!target) {
      target = s.rows.find((r) => r.node.kind === "post" && r.node.session_id)?.node ?? null;
    }
    if (target) {
      void this.openPost(target);
      const idx = s.rows.findIndex((r) => r.node.session_id === target!.session_id);
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
    s.open = { profile, id, title: node.name };
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
      s.olderExhausted = msgs.length < MSG_PAGE;
      s.paletteDismissed = null;
      s.statusText = `${s.messages.length} messages · ${id}`;
    } catch (e: unknown) {
      s.messages = [];
      s.statusText = "transcript failed: " + errText(e);
    } finally {
      s.loadingOpen = false;
      s.autoScroll += 1;
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
      s.olderExhausted = msgs.length < MSG_PAGE;
      s.autoScroll += 1;
    } catch {
      /* keep the current transcript on refresh failure */
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
      if (msgs.length < MSG_PAGE) s.olderExhausted = true;
      const have = new Set(s.messages.map((m) => m.id));
      const older = cleanMessages(msgs).filter((m) => !have.has(m.id));
      if (older.length) {
        s.messages = [...older, ...s.messages];
        await tick();
        if (el) el.scrollTop = prevTop + (el.scrollHeight - prevH);
      }
    } catch (e: unknown) {
      s.statusText = "older messages failed: " + errText(e);
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
      if (!m || m.tool_name || !m.content?.trim()) continue;
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

  toggleReasoning() {
    s.showReasoning = !s.showReasoning;
    s.statusText = s.showReasoning ? "reasoning shown" : "reasoning hidden";
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
    const last = node.last_active ?? 0;
    if (last <= 0) return false;
    return last * 1000 > (s.lastRead[node.session_id] ?? 0);
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
  },

  paletteMove(dir: number) {
    const n = paletteMatches(s.draft).length;
    if (!n) return;
    s.paletteIdx = ((s.paletteIdx + dir) % n + n) % n;
  },

  paletteDismiss() {
    s.paletteDismissed = s.draft;
  },

  runPalette() {
    this.runPaletteAt(s.paletteIdx);
  },

  runPaletteAt(i: number) {
    const cmds = paletteMatches(s.draft);
    const c = cmds[Math.max(0, Math.min(i, cmds.length - 1))];
    if (!c) return;
    s.draft = "";
    s.paletteIdx = 0;
    s.paletteDismissed = null;
    c.run();
  },
};

// ---- command palette registry ----------------------------------------
// Commands route through the same actions the keys use; names render with
// a leading slash and the palette filters on the typed suffix.

export interface Command {
  name: string;
  desc: string;
  run: () => void;
}

export const COMMANDS: Command[] = [
  { name: "/help", desc: "keys + commands sheet", run: () => actions.toggleHelp() },
  { name: "/stop", desc: "stop the running turn", run: () => void actions.stopTurn() },
  { name: "/reasoning", desc: "toggle reasoning blocks", run: () => actions.toggleReasoning() },
  { name: "/find", desc: "search the open transcript", run: () => actions.findStart() },
  { name: "/visual", desc: "start a selection at the top message", run: () => actions.visualStart(false) },
  { name: "/refresh", desc: "reload the transcript from head", run: () => void actions.refreshTranscript() },
  { name: "/stale", desc: "show/hide chats idle 7d+", run: () => actions.toggleStale() },
  { name: "/top", desc: "jump to the oldest loaded message", run: () => actions.scrollChatTo("top") },
  { name: "/bottom", desc: "jump to the newest message", run: () => actions.scrollChatTo("bottom") },
];

export function paletteMatches(draft: string): Command[] {
  const m = /^\/([a-z]*)$/i.exec(draft);
  if (!m) return [];
  const f = m[1].toLowerCase();
  return COMMANDS.filter((c) => c.name.slice(1).toLowerCase().startsWith(f));
}

// Visible while the composer draft is a bare "/suffix" (no arguments yet)
// and not dismissed via esc for this exact draft.
export function paletteVisible(): boolean {
  return (
    s.focus === "composer" &&
    s.mode === "INSERT" &&
    s.paletteDismissed !== s.draft &&
    paletteMatches(s.draft).length > 0
  );
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
