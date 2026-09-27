// Central app state — Svelte 5 runes. ALL mutations flow through `actions`;
// components only read `s` and call actions. Ports the TUI's behaviors:
// stale-stows, folds, auto-open-first-post, unread tracking, vim focus model.

import type { Focus, HubNode, Message, Row, Session, Status } from "./types";
import * as api from "./api";
import { buildRows, buildSessionRows } from "./tree";

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
  focus: "tree" as Focus,
  mode: "NORMAL" as "NORMAL" | "INSERT",
  statusText: "starting…",
  draft: "",
  lastRead: loadRead(),
  autoScroll: 0, // bumped to force a scroll-to-bottom
});

let chatScroller: HTMLDivElement | null = null;

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

  scrollChat(delta: number) {
    if (chatScroller) chatScroller.scrollTop += delta * 44;
  },

  scrollChatTo(pos: "top" | "bottom") {
    if (!chatScroller) return;
    chatScroller.scrollTo({ top: pos === "top" ? 0 : chatScroller.scrollHeight });
  },

  async boot() {
    try {
      s.status = await api.Status();
    } catch {
      s.status = null;
    }
    await this.refreshTree(true);
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
    if (initial) {
      const first = s.rows.find((r) => r.node.kind === "post" && r.node.session_id);
      if (first) {
        void this.openPost(first.node);
        const idx = s.rows.findIndex((r) => r.key === first.key);
        if (idx >= 0) s.cursor = idx;
      }
    }
  },

  rebuild(initial = false) {
    const cur = s.rows[s.cursor]?.key ?? "";
    const now = Date.now() / 1000;
    const ctx = { collapsed: new Set(s.collapsed), hideStale: s.hideStale, now };
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
    if (!r || r.node.kind === "post") return;
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
    this.markRead(id);
    try {
      const msgs = await api.GetMessages(profile, id, 400);
      if (s.open?.id !== id) return; // navigated away mid-flight
      s.messages = cleanMessages(msgs);
      s.statusText = `${s.messages.length} messages · ${id}`;
    } catch (e: unknown) {
      s.messages = [];
      s.statusText = "transcript failed: " + errText(e);
    } finally {
      s.loadingOpen = false;
      s.autoScroll += 1;
    }
  },

  async send() {
    if (!s.open) return;
    const text = s.draft.trim();
    if (!text) return;
    const open = s.open;
    s.draft = "";
    s.statusText = "sending…";
    try {
      await api.SendMessage(open.profile, open.id, text);
      s.statusText = "turn complete";
    } catch (e: unknown) {
      s.statusText = "send failed: " + errText(e);
      s.draft = text;
    }
    try {
      const msgs = await api.GetMessages(open.profile, open.id, 400);
      if (s.open?.id === open.id) {
        s.messages = cleanMessages(msgs);
        s.autoScroll += 1;
      }
    } catch {
      /* keep current transcript on refresh failure */
    }
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
};

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
