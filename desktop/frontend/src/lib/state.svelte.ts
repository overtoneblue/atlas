// Central app state — Svelte 5 runes. ALL mutations flow through `actions`;
// components only read `s` and call actions. Ports the TUI's behaviors:
// stale-stows, folds, unread tracking, vim focus model — plus the v2 live
// turn machine (streamed deltas + tool activity, stop support), the parity
// layer (find, visual/yank, counts, image paste), and spawned-work rows
// (subagent runs + pi tasks nested under their chat).

import type {
  Catalog,
  ChannelStore,
  Completion,
  ExecResult,
  Focus,
  HubNode,
  LiveTurn,
  Message,
  ModalState,
  ModelOptions,
  ModelRow,
  Row,
  Session,
  SessionInfo,
  SpawnItem,
  Status,
  TurnEvent,
  TurnFailure,
} from "./types";
import * as api from "./api";
import { buildRows, buildSessionRows } from "./tree";
import { findRuntime } from "./find";
import { tick } from "svelte";

const LS_STALE = "atlas.hideStale";
const LS_READ = "atlas.lastRead";
const LS_DISP = "atlas.dispMode"; // transcript density, survives boots
const LS_HIDDEN_VIEW = "atlas.showHidden"; // show hidden (archived) chats
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

// Transcript density (0 full · 1 no reasoning · 2 exact tools · 3 quiet),
// persisted so the chosen view mode survives a restart. The default is 1
// (reasoning hidden) — the cleanest read, especially on the phone; whatever
// the user cycles to with `r` is what sticks.
function loadDispMode(): number {
  const raw = localStorage.getItem(LS_DISP);
  const v = Number(raw);
  // note: Number(null) is 0, so the missing-key case needs its own check —
  // otherwise the 1 (reasoning hidden) default is dead code.
  return raw !== null && Number.isInteger(v) && v >= 0 && v <= 3 ? v : 1;
}

export const s = $state({
  status: null as Status | null,
  sections: [] as HubNode[],
  sessions: [] as Session[],
  rows: [] as Row[],
  cursor: 0,
  sel: [] as string[], // marked row keys (multi-select)
  selAnchor: null as number | null, // shift+click anchor row index
  // live session snapshots (model · provider · reasoning · context · tok/s)
  info: {} as Record<string, SessionInfo>,
  // last failed turn per chat (the real cause behind Hermes' canned row)
  failures: {} as Record<string, TurnFailure>,
  // link state: the atlasd event stream itself + atlasd's serve socket
  link: { stream: true, serve: "" as "" | "up" | "down" },
  boot: "", // atlasd process id (a change = the daemon restarted)
  // settings panel (S · /settings)
  settingsOpen: false,
  settingsIdx: 0,
  settingsBusy: "", // row id with a request in flight
  settingsErr: "", // last failed change, shown inside the sheet
  collapsed: [] as string[], // fold keys (stable name paths)
  hideStale: localStorage.getItem(LS_STALE) !== "0",
  hiddenView: localStorage.getItem(LS_HIDDEN_VIEW) === "1",
  hiddenCount: 0,
  archivedCount: 0,
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
  dispMode: loadDispMode(),
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
  // native shape store cache (per profile) + the one modal slot (channel /
  // category form or the move-to-channel picker)
  stores: {} as Record<string, ChannelStore>,
  modal: null as ModalState | null,
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
  // a pending large-context/cost confirm from the switch contract: the next
  // enter on the SAME pick re-sends with confirm=true, esc backs out.
  modelConfirm: null as { cmd: string; session: string; message: string } | null,
});

let chatScroller: HTMLDivElement | null = null;
let lightboxOriginEl: HTMLElement | null = null;

// The Lightbox registers its animated close here so keymap dismissals play
// the same reverse-FLIP; the fallback is an instant unmount.
export const lightboxRuntime: { close: (() => void) | null } = { close: null };

const MSG_PAGE = 400;

// when each chat was last marked busy locally (non-reactive): the status
// poll may only clear a busy flag that is older than one poll round-trip,
// so a turn that just started can't be un-flagged by a stale response.
const busySince: Record<string, number> = {};
// chats whose runtime this client already warmed this daemon lifetime
const warmed = new Set<string>();

function cleanMessages(ms: Message[]): Message[] {
  return ms
    .filter((m) => m.display_kind !== "hidden") // channel-guideline seeds: model-facing only
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
      if (s.status?.boot) s.boot = s.status.boot;
      // Live turns already in flight server-side: flag them now so the tree
      // shows ◍ and opening one attaches mid-stream.
      if (s.status?.turns?.length) {
        const busy = { ...s.turnBusy };
        for (const t of s.status.turns) {
          busy[t.session] = true;
          busySince[t.session] = Date.now();
        }
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
      hiddenView: s.hiddenView,
      now,
      spawned: s.spawned,
      keep: s.open?.id ?? null,
    };
    const built = s.sections.length
      ? buildRows(s.sections, ctx)
      : buildSessionRows(s.sessions, ctx);
    s.rows = built.rows;
    s.hiddenCount = built.hidden;
    s.archivedCount = built.archived;
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
    if (!r || r.node.kind === "spawn") return;
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
    if (!r || r.node.kind === "spawn") return;
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

  // toggleHiddenView shows/hides chats archived via the hidden flag (`H`).
  toggleHiddenView() {
    s.hiddenView = !s.hiddenView;
    localStorage.setItem(LS_HIDDEN_VIEW, s.hiddenView ? "1" : "0");
    this.rebuild();
    s.statusText = s.hiddenView
      ? `showing hidden chats — ${s.archivedCount} archived, ↺ restores one`
      : s.archivedCount > 0
        ? `hiding ${s.archivedCount} archived chats — H shows them`
        : "no hidden chats";
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
      // with the model picker up, the composer holds "/model …" — the real
      // draft is the one the picker parked
      s.drafts[s.open.id] = s.modelPick ? pickerParkedDraft : s.draft;
      if (s.modelPick) {
        s.modelPick = null;
        s.modelConfirm = null;
        pickerParkedDraft = "";
      }
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
      const msgs = (await api.GetMessages(profile, id, MSG_PAGE, 0, "latest")) ?? [];
      if (s.open?.id !== id) return; // navigated away mid-flight
      s.messages = cleanMessages(msgs);
      s.olderOffset = msgs.length;
      s.olderExhausted = msgs.length === 0;
      s.paletteDismissed = null;
      s.modelPick = null;
      s.statusText = `${s.messages.length} messages · ${id}`;
      void this.attachTurn(id, profile);
      void this.loadInfo(profile, id);
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
      const msgs = (await api.GetMessages(open.profile, open.id, MSG_PAGE, 0, "latest")) ?? [];
      if (s.open?.id !== open.id) return;
      s.messages = cleanMessages(msgs);
      s.olderOffset = msgs.length;
      s.olderExhausted = msgs.length === 0;
      s.autoScroll += 1;
    } catch {
      /* keep the current transcript on refresh failure */
    }
  },

  // loadInfo pulls the chat's live snapshot (model · provider · reasoning ·
  // context · tok/s, or the stored route when unbound) and its remembered
  // last failure — so a reload still explains a failed turn.
  async loadInfo(profile: string, id: string) {
    try {
      const r = await api.FetchInfo(profile, id);
      if (r?.info) this.applyInfo(r.info);
      if (r?.last_failure && !s.turnBusy[id]) {
        s.failures = { ...s.failures, [id]: r.last_failure };
      }
      // The stored route would resume on a stale provider: bind now so the
      // daemon's route guard repairs it before anything is typed.
      if (r?.info?.drift && !r.info.live && s.open?.id === id) this.warmOpen();
    } catch {
      /* older daemon / offline: chips stay empty */
    }
  },

  applyInfo(si: SessionInfo) {
    if (!si?.session) return;
    const prev = s.info[si.session];
    // a stored-route description (the daemon marks those live:false) must
    // never overwrite a live snapshot; optimistic local patches carry no
    // live flag and always apply
    if (prev?.live && si.live === false) return;
    const next = { ...prev, ...si };
    // the daemon omits empty fields: a live snapshot without a drift note
    // means the route is fine now — an older stored-route warning must not
    // linger through the merge
    if (si.live) next.drift = si.drift ?? "";
    s.info = { ...s.info, [si.session]: next };
  },

  // warm binds the open chat's runtime the moment the composer takes focus:
  // the resume (an eager agent build) and the route guard run while you
  // type, so the first send neither waits for them nor hits a stale route.
  warmOpen() {
    const open = s.open;
    if (!open || warmed.has(open.id) || s.turnBusy[open.id]) return;
    if (s.info[open.id]?.live) return;
    warmed.add(open.id);
    api.Bind(open.profile, open.id).then(
      (si) => this.applyInfo(si),
      () => warmed.delete(open.id), // let the next focus try again
    );
  },

  // pollStatus refreshes link health and reconciles busy flags against the
  // daemon's live turns — the safety net for a "done" lost while the event
  // stream was down (a stuck busy flag blocks sending in that chat).
  async pollStatus() {
    let st: Status;
    try {
      st = await api.Status();
    } catch {
      s.status = null;
      return;
    }
    s.status = st;
    if (st.boot && s.boot && st.boot !== s.boot) {
      s.boot = st.boot;
      warmed.clear();
      void this.resync("atlasd restarted");
      return;
    }
    if (st.boot) s.boot = st.boot;
    this.reconcileBusy(st.turns ?? []);
  },

  reconcileBusy(turns: { session: string; profile: string }[]) {
    const live = new Set(turns.map((t) => t.session));
    const next = { ...s.turnBusy };
    let changed = false;
    for (const t of turns) {
      if (!next[t.session]) {
        next[t.session] = true;
        busySince[t.session] = Date.now();
        changed = true;
      }
    }
    for (const sid of Object.keys(next)) {
      if (!next[sid] || live.has(sid)) continue;
      if (Date.now() - (busySince[sid] ?? 0) < 8000) continue; // too fresh to judge
      next[sid] = false;
      changed = true;
      if (s.live?.session === sid && s.open?.id === sid) {
        s.live = null;
        void this.refreshTranscript();
        void this.loadInfo(s.open.profile, sid);
      }
    }
    if (changed) s.turnBusy = next;
  },

  // resync re-reads everything the event stream may have carried while it
  // was down (or after a daemon restart / dropped frames): live turns, the
  // open transcript or its live block, and the chat's info.
  async resync(why: string) {
    try {
      const st = await api.Status();
      s.status = st;
      if (st.boot) s.boot = st.boot;
      for (const sid of Object.keys(busySince)) busySince[sid] = 0;
      this.reconcileBusy(st.turns ?? []);
    } catch {
      return; // still down: the next stream/status edge retries
    }
    const open = s.open;
    if (!open) return;
    if (s.turnBusy[open.id]) await this.attachTurn(open.id, open.profile);
    else {
      if (s.live?.session === open.id) s.live = null;
      await this.refreshTranscript();
    }
    void this.loadInfo(open.profile, open.id);
    s.statusText = `resynced — ${why}`;
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

  // hidePost archives a chat via Hermes' own hidden flag: out of every list,
  // nothing deleted, and it stays fully resumable (H shows hidden chats in
  // Atlas; ↺ on a row brings one back). Optimistic — the row flips now, the
  // hub re-confirms on the next tree refresh.
  async hidePost(sid: string, profile: string, hidden: boolean) {
    const apply = (v: boolean) => {
      const walk = (n: HubNode) => {
        if (n.session_id === sid) n.hidden = v;
        for (const k of n.children ?? []) walk(k);
      };
      for (const sec of s.sections) walk(sec);
      const row = s.sessions.find((x) => x.id === sid);
      if (row) row.hidden = v;
      this.rebuild();
    };
    apply(hidden);
    try {
      await api.HideSession(profile || "default", sid, hidden);
      s.statusText = hidden ? "hidden — H shows hidden chats" : "restored to the tree";
    } catch (e: unknown) {
      apply(!hidden); // roll the optimistic flip back
      s.statusText = "hide failed: " + errText(e);
    }
  },

  // -- multi-select: ctrl+click toggles one row, shift+click selects the
  // visible range from the anchor; the bulk action applies per type (chats
  // hide, spawned run records delete). `x` toggles from the keyboard. --
  markable(r: (typeof s.rows)[number] | undefined): boolean {
    return (
      !!r &&
      ((r.node.kind === "post" && !!r.node.session_id) ||
        (r.node.kind === "spawn" && !!r.node.spawn))
    );
  },
  markToggle(i: number) {
    if (s.focus !== "tree") return;
    const r = s.rows[i];
    if (!this.markable(r)) return;
    s.cursor = i;
    s.sel = s.sel.includes(r.key) ? s.sel.filter((k) => k !== r.key) : s.sel.concat(r.key);
    s.selAnchor = i;
  },
  markRange(i: number) {
    const a = s.selAnchor ?? s.cursor;
    const lo = Math.min(a, i);
    const hi = Math.max(a, i);
    const keys = new Set(s.sel);
    for (let j = lo; j <= hi; j++) {
      const r = s.rows[j];
      if (this.markable(r)) keys.add(r.key);
    }
    s.sel = [...keys];
  },
  clearSel() {
    if (s.sel.length || s.selAnchor !== null) {
      s.sel = [];
      s.selAnchor = null;
    }
  },
  async bulkApply() {
    const targets = s.rows.filter((r) => s.sel.includes(r.key));
    let hid = 0;
    let del = 0;
    const failed: string[] = [];
    for (const r of targets) {
      try {
        if (r.node.kind === "spawn" && r.node.spawn) {
          await api.SpawnDelete(r.node.spawn.kind, r.node.spawn.id, r.node.spawn.profile ?? "default");
          del++;
        } else if (r.node.kind === "post" && r.node.session_id && !r.node.hidden) {
          await api.HideSession(r.node.profile ?? "default", r.node.session_id, true);
          hid++;
        }
      } catch {
        failed.push(r.node.name);
      }
    }
    this.clearSel();
    await this.refreshTree();
    void this.refreshSpawned();
    s.statusText =
      `bulk: hid ${hid} chat${hid === 1 ? "" : "s"} · deleted ${del} run record${del === 1 ? "" : "s"}` +
      (failed.length ? ` · ${failed.length} failed: ${failed.join(", ")}` : "");
  },
  async deleteSpawn(spawn: { kind: string; id: string; profile?: string }) {
    try {
      await api.SpawnDelete(spawn.kind, spawn.id, spawn.profile ?? "default");
      s.statusText = `deleted ${spawn.kind} run ${spawn.id}`;
      await this.refreshTree();
      void this.refreshSpawned();
    } catch (e: unknown) {
      s.statusText = `${spawn.kind} delete failed: ` + errText(e);
    }
  },

  // newChat mints a fresh chat through the daemon (serve session.create;
  // source "atlas" files it under the profile's Atlas channel) and opens an
  // empty composer on it. With channelId the chat is created inside that
  // native channel and inherits its guidelines as a hidden context row the
  // agent reads but the transcript never paints.
  async newChat(profile?: string, channelId?: string) {
    const p = (profile ?? "").trim() || s.open?.profile || "default";
    s.statusText = channelId ? "creating a chat in the channel…" : `creating a chat in ${p}…`;
    try {
      const r = await api.NewChat(p, channelId ?? "");
      await this.openPost({ kind: "post", name: "(new chat)", session_id: r.session, profile: p });
      this.setFocus("composer");
      if (r.warning) s.statusText = r.warning;
      else if (channelId) s.statusText = "new chat — the channel's guidelines are set for the agent";
      else s.statusText = `new chat in ${p} — it joins the tree with your first message`;
      void this.refreshTree(); // the seeded row lands under its channel now
    } catch (e: unknown) {
      s.statusText = "new chat failed: " + errText(e);
    }
  },

  // ---- native channels (categories · channels · guidelines) --------------

  async ensureStore(profile: string): Promise<ChannelStore | null> {
    const p = profile || "default";
    const hit = s.stores[p];
    if (hit) return hit;
    try {
      const st = await api.GetChannels(p);
      s.stores = { ...s.stores, [p]: st };
      return st;
    } catch (e: unknown) {
      s.statusText = "channels load failed: " + errText(e);
      return null;
    }
  },

  storeFor(profile: string): ChannelStore | null {
    return s.stores[profile || "default"] ?? null;
  },

  closeModal() {
    s.modal = null;
  },

  openNewCategory(profile: string) {
    s.modal = { kind: "category", mode: "new", profile: profile || "default", name: "" };
  },

  openEditCategory(node: HubNode) {
    s.modal = {
      kind: "category",
      mode: "edit",
      profile: node.profile ?? "default",
      id: node.id ?? "",
      name: node.name,
    };
  },

  openNewChannel(categoryId: string, profile: string) {
    s.modal = {
      kind: "channel",
      mode: "new",
      profile: profile || "default",
      name: "",
      template: "",
      categoryId: categoryId || "",
    };
    void this.ensureStore(profile);
  },

  openEditChannel(node: HubNode) {
    const p = node.profile ?? "default";
    s.modal = {
      kind: "channel",
      mode: "edit",
      profile: p,
      id: node.id ?? "",
      name: node.name,
      template: node.template ?? "",
      categoryId: node.category_id ?? "",
    };
    void this.ensureStore(p);
  },

  // saveModal persists the open form (category or channel create/update).
  async saveModal() {
    const m = s.modal;
    if (!m || m.kind === "move") return;
    try {
      if (m.kind === "category") {
        const name = (m.name ?? "").trim();
        if (!name) return;
        if (m.mode === "new") {
          await api.ChanOp({ action: "create", profile: m.profile, kind: "category", name });
        } else {
          await api.ChanOp({ action: "update", profile: m.profile, kind: "category", id: m.id, name });
        }
        s.statusText = `category ${name} ${m.mode === "new" ? "created" : "saved"}`;
      } else {
        const name = (m.name ?? "").trim();
        if (!name) return;
        let catId = (m.categoryId ?? "").trim();
        if (catId === "__new__") catId = "";
        const fresh = (m.newCategory ?? "").trim();
        if (fresh) {
          const r = await api.ChanOp({ action: "create", profile: m.profile, kind: "category", name: fresh });
          const item = r.item as { id?: string } | undefined;
          if (item?.id) catId = item.id;
        }
        if (m.mode === "new") {
          await api.ChanOp({
            action: "create",
            profile: m.profile,
            kind: "channel",
            name,
            template: m.template ?? "",
            category_id: catId || null,
          });
        } else {
          await api.ChanOp({
            action: "update",
            profile: m.profile,
            kind: "channel",
            id: m.id,
            name,
            template: m.template ?? "",
            category_id: catId || null,
          });
        }
        s.statusText =
          `channel ${name} ${m.mode === "new" ? "created" : "saved"}` +
          ((m.template ?? "").trim() ? " — guidelines ride every new chat" : "");
      }
      s.modal = null;
      void this.ensureStore(m.profile);
      void this.refreshTree();
    } catch (e: unknown) {
      s.statusText = "save failed: " + errText(e);
      if (s.modal) s.modal.error = errText(e); // visible inside the form, not under the scrim
    }
  },

  // deleteModal removes the open category/channel record. Chats are never
  // touched: a channel's chats fall back to their source grouping.
  async deleteModal() {
    const m = s.modal;
    if (!m || m.kind === "move") return;
    try {
      await api.ChanOp({ action: "delete", profile: m.profile, kind: m.kind, id: m.id });
      s.modal = null;
      s.statusText = `${m.kind} deleted — chats are untouched`;
      void this.ensureStore(m.profile);
      void this.refreshTree();
    } catch (e: unknown) {
      s.statusText = "delete failed: " + errText(e);
      if (s.modal) s.modal.error = errText(e);
    }
  },

  openMovePicker(node: HubNode) {
    if (!node.session_id) return;
    const p = node.profile ?? "default";
    s.modal = {
      kind: "move",
      mode: "edit",
      profile: p,
      session: node.session_id,
      title: node.name,
      current: node.channel_id ?? null,
    };
    void this.ensureStore(p);
  },

  // doMove re-files a chat into a native channel (null detaches it).
  async doMove(channelId: string | null) {
    const m = s.modal;
    if (!m || m.kind !== "move" || !m.session) return;
    try {
      await api.ChanOp({ action: "assign", profile: m.profile, session: m.session, channel_id: channelId });
      s.statusText = channelId ? "moved to channel" : "removed from channel";
      s.modal = null;
      void this.refreshTree();
    } catch (e: unknown) {
      s.statusText = "move failed: " + errText(e);
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
      const msgs = (await api.GetMessages(open.profile, open.id, MSG_PAGE, s.olderOffset, "latest")) ?? [];
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
    localStorage.setItem(LS_DISP, String(s.dispMode));
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

    busySince[open.id] = Date.now();
    try {
      await api.SendMessage(open.profile, open.id, payload);
      s.statusText = imgs.length
        ? `sent with ${imgs.length} image(s) — streaming…`
        : "streaming…";
    } catch (e: unknown) {
      // Never lose a message: the echo goes, the draft (and images) come
      // back, and the reason is pinned in the transcript — not just a
      // status-bar line that the next event overwrites.
      s.messages = s.messages.filter((m) => m.id !== echoID);
      if (s.open?.id === open.id) {
        s.draft = text;
        s.attachments = imgs;
      } else {
        s.drafts[open.id] = text;
      }
      const msg = errText(e);
      s.statusText = "not sent: " + msg;
      if (s.open?.id === open.id) this.pushNote(`not sent — ${msg}\nyour message is back in the composer`);
    }
  },

  // retryTurn re-runs the chat's last user message after a failure, via
  // Hermes' own /retry (rewinds the failed turn, then hands the message
  // back as a send) — falling back to resending the last user message.
  async retryTurn() {
    const open = s.open;
    if (!open) return;
    if (s.turnBusy[open.id]) {
      s.statusText = "a turn is already running";
      return;
    }
    s.statusText = "retrying…";
    try {
      const res = await api.ExecSlash(open.id, "/retry");
      if (res.type === "send" && res.message) {
        await this.sendRaw(res.message);
        return;
      }
      this.foldExec(res, "/retry");
    } catch (e: unknown) {
      const last = [...s.messages].reverse().find((m) => m.role === "user" && m.content?.trim());
      if (!last) {
        s.statusText = "retry failed: " + errText(e);
        return;
      }
      await this.sendRaw(last.content);
    }
  },

  dismissFailure(id: string) {
    if (!s.failures[id]) return;
    const f = { ...s.failures };
    delete f[id];
    s.failures = f;
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
    busySince[open.id] = Date.now();
    try {
      await api.SendMessage(open.profile, open.id, text);
      s.statusText = "streaming…";
    } catch (e: unknown) {
      s.messages = s.messages.filter((m) => m.id !== echoID);
      const msg = errText(e);
      s.statusText = "not sent: " + msg;
      if (s.open?.id === open.id) this.pushNote(`not sent — ${msg}`);
    }
  },

  // handleTurnEvent folds one "atlas:turn" event into UI state.
  handleTurnEvent(ev: TurnEvent) {
    // stream / daemon-level frames first (no session)
    switch (ev.kind) {
      case "hello":
        if (ev.link === "up" || ev.link === "down") s.link.serve = ev.link;
        if (ev.boot && s.boot && ev.boot !== s.boot) {
          s.boot = ev.boot;
          warmed.clear();
          void this.resync("atlasd restarted");
        } else if (ev.boot) s.boot = ev.boot;
        return;
      case "stream":
        s.link.stream = ev.link === "up";
        if (ev.link === "up") void this.resync("event stream reconnected");
        else s.statusText = "lost the atlasd event stream — reconnecting…";
        return;
      case "resync":
        void this.resync("missed live events");
        return;
      case "link":
        s.link.serve = ev.link === "up" ? "up" : "down";
        if (ev.link === "up") {
          warmed.clear(); // serve restarted: runtimes are new, re-warm on focus
          void this.pollStatus();
        }
        return;
    }

    const sid = ev.session_id;
    if (!sid) return;
    if (ev.kind === "done") {
      s.turnBusy = { ...s.turnBusy, [sid]: false };
    } else if (ev.kind === "started" || ev.kind === "delta" || ev.kind === "tool") {
      if (!s.turnBusy[sid]) {
        s.turnBusy = { ...s.turnBusy, [sid]: true };
        busySince[sid] = Date.now();
      }
    }

    const openHere = s.open?.id === sid;
    switch (ev.kind) {
      case "info":
        if (ev.info) this.applyInfo({ ...ev.info, session: sid });
        if (openHere && s.live?.session === sid && ev.info?.tps) s.live.tps = ev.info.tps;
        return;
      case "note":
        // daemon-authored notices: route repairs, busy-elsewhere folds
        if (openHere && ev.text) this.pushNote(ev.text);
        else if (ev.text) s.statusText = ev.text;
        return;
      case "started":
        if (s.failures[sid]) {
          const f = { ...s.failures };
          delete f[sid];
          s.failures = f;
        }
        if (openHere) {
          s.live = { session: sid, profile: ev.profile ?? "default", segments: [], error: "", tps: s.info[sid]?.tps };
          s.autoScroll += 1; // the live block lands below the echo — follow it down
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
      case "reasoning":
        if (openHere) {
          if (!s.live || s.live.session !== sid) {
            s.live = { session: sid, profile: ev.profile ?? "default", segments: [], error: "" };
          }
          const rsegs = s.live.segments;
          const rlast = rsegs[rsegs.length - 1];
          if (rlast && rlast.type === "reasoning") rlast.text += ev.text ?? "";
          else rsegs.push({ type: "reasoning", text: ev.text ?? "" });
          s.autoScroll += 1;
        }
        break;
      case "stats":
        // legacy frame (pre-info daemons): only the throughput reading
        if (ev.tps && !s.info[sid]?.live) this.applyInfo({ session: sid, tps: ev.tps, latency_s: ev.latency_s });
        if (openHere && s.live && s.live.session === sid && ev.tps) s.live.tps = ev.tps;
        break;
      case "error":
        if (openHere && s.live && s.live.session === sid) {
          s.live.error = ev.error ?? "error";
          s.autoScroll += 1;
        }
        if (!openHere) s.statusText = `turn failed in another chat: ${ev.error ?? "error"}`;
        break;
      case "done": {
        // A failed turn keeps its real cause on screen as a failure card —
        // the transcript refresh only brings back Hermes' canned row.
        if (ev.ok === false && !ev.stopped) {
          const err = ev.error || (openHere ? s.live?.error : "") || "the turn failed";
          s.failures = {
            ...s.failures,
            [sid]: { error: err, code: ev.code, at: Date.now() / 1000 },
          };
        }
        if (openHere) {
          s.live = null;
          void this.refreshTranscript();
          void this.refreshTree(); // a new chat's first row appears now
          s.statusText = ev.stopped
            ? "turn stopped"
            : ev.ok === false
              ? "turn failed — R retries · M picks another model"
              : "turn complete";
          s.stickBump += 1;
        }
        break;
      }
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
    if (f === "composer") this.warmOpen();
  },

  // ---- settings panel (S · /settings) ----------------------------------
  // A keyboard-first sheet over the same session-scoped contracts the
  // official desktop's settings use (model / reasoning / fast on the live
  // runtime) plus Atlas' own view toggles and connection health.

  openSettings() {
    s.settingsOpen = true;
    s.settingsIdx = 0;
    s.settingsErr = "";
    s.helpOpen = false;
    // opened from the palette (INSERT): release the textarea, or keys the
    // sheet doesn't own would type into the composer behind it
    if (s.mode === "INSERT") s.mode = "NORMAL";
    const o = s.open;
    if (o) void this.loadInfo(o.profile, o.id);
    void this.pollStatus();
  },

  closeSettings() {
    s.settingsOpen = false;
    s.settingsBusy = "";
  },

  settingsMove(dir: number) {
    const n = settingsRows().length;
    if (!n) return;
    s.settingsIdx = (((s.settingsIdx + dir) % n) + n) % n;
  },

  // step: -1 / +1 = h/l (←/→); 0 = enter (activate / step forward)
  async settingsAct(step: number) {
    const row = settingsRows()[s.settingsIdx];
    if (!row || row.disabled || s.settingsBusy) return;
    const open = s.open;
    switch (row.id) {
      case "model":
        this.closeSettings();
        void this.openModelPicker();
        return;
      case "reasoning": {
        if (!open) return;
        const cur = (s.info[open.id]?.reasoning || "medium").toLowerCase();
        const i = Math.max(0, REASONING_LEVELS.indexOf(cur));
        const next = REASONING_LEVELS[(i + (step || 1) + REASONING_LEVELS.length) % REASONING_LEVELS.length];
        await this.applySetting(open, "reasoning", next, () => {
          this.applyInfo({ session: open.id, reasoning: next });
        });
        return;
      }
      case "fast": {
        if (!open) return;
        const on = !!s.info[open.id]?.fast;
        const next = on ? "normal" : "fast";
        await this.applySetting(open, "fast", next, () => {
          this.applyInfo({ session: open.id, fast: !on, service_tier: on ? "" : "priority" });
        });
        return;
      }
      case "display":
        if (step < 0) s.dispMode = (s.dispMode + 2) % 4; // one back (cycle adds one)
        this.cycleDisplay();
        return;
      case "stale":
        this.toggleStale();
        return;
      case "hidden":
        this.toggleHiddenView();
        return;
      case "resync":
        void this.resync("manual resync");
        return;
      case "restart":
        this.closeSettings();
        void this.restartDaemon(false);
        return;
      default:
        return;
    }
  },

  async applySetting(open: { profile: string; id: string }, key: string, value: string, optimistic: () => void) {
    s.settingsBusy = key;
    s.settingsErr = "";
    s.statusText = `${key} → ${value}…`;
    try {
      await api.SetConfig(open.profile, open.id, key, value);
      optimistic(); // serve's session.info follows on the stream and wins
      s.statusText = `${key} → ${value} (this chat)`;
    } catch (e: unknown) {
      // e.g. "fast mode is not available for this model" — shown in the
      // sheet itself (the status bar sits under the scrim)
      const msg = errText(e).replace(/^rpc \d+:\s*/, "");
      s.settingsErr = `${key}: ${msg}`;
      s.statusText = `${key} failed: ` + msg;
    } finally {
      s.settingsBusy = "";
    }
  },

  cycleFocus(dir: number) {
    const order: Focus[] = ["tree", "chat", "composer"];
    const idx = Math.max(0, order.indexOf(s.focus));
    const next = order[(idx + dir + order.length) % order.length];
    this.setFocus(next);
  },

  escape() {
    if (s.settingsOpen) {
      this.closeSettings();
      return;
    }
    if (s.sel.length || s.selAnchor !== null) {
      this.clearSel();
      return;
    }
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
    if (s.modelPick) {
      // already open but the composer lost focus (a click elsewhere): bring
      // it back instead of doing nothing
      this.setFocus("composer");
      return;
    }
    // the picker borrows the composer: an in-progress message is parked and
    // handed back on close/pick (it used to be wiped)
    pickerParkedDraft = s.draft.startsWith("/model") ? "" : s.draft;
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
    s.modelConfirm = null; // moving targets: the pending confirm was for another row
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
    s.modelConfirm = null;
    s.draft = pickerParkedDraft;
    pickerParkedDraft = "";
    s.statusText = "";
  },

  // pickModel applies the desktop's exact switch contract: model first,
  // provider pinned with --provider, session-scoped — a pick in one chat
  // never rewrites the profile default. It routes through the confirm-capable
  // config.set contract: when a selection guard fires (mid-session switch on
  // a large cached context, expensive model, data-policy), the picker stays
  // open with the warning and the next enter re-sends with confirm=true —
  // the slash path used to flatten that warning into dead text.
  async pickModel(row: ModelRow) {
    const open = s.open;
    if (!open) {
      s.modelPick = null;
      s.draft = "";
      return;
    }
    const cmd = row.slug
      ? `/model ${row.name} --provider ${row.slug} --session`
      : `/model ${row.name} --session`;
    const value = cmd.replace(/^\/model\s+/, "");
    const confirmed = s.modelConfirm?.cmd === value && s.modelConfirm?.session === open.id;
    s.statusText = "switching model…";
    try {
      const r = await api.SetModel(open.id, value, confirmed);
      if (r.confirm_required) {
        s.modelConfirm = { cmd: value, session: open.id, message: r.confirm_message ?? r.warning ?? "" };
        s.statusText = "large-context switch — enter again to confirm · esc cancels";
        return; // picker stays open; second enter applies
      }
      s.modelConfirm = null;
      s.modelPick = null;
      s.draft = pickerParkedDraft;
      pickerParkedDraft = "";
      rememberModel(row);
      // optimistic chip update; serve's session.info follows and wins
      this.applyInfo({ session: open.id, model: row.name, provider: row.slug || undefined, drift: "" });
      if (s.failures[open.id]) s.statusText = `model → ${row.name} · R retries the failed turn`;
      else s.statusText = `model → ${r.value ?? row.name}`;
    } catch (e: unknown) {
      s.statusText = "model switch failed: " + errText(e);
    }
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
  { name: "/settings", desc: "model · reasoning · fast · display · connection (S)", run: () => actions.openSettings() },
  { name: "/resync", desc: "re-read live turns, transcript and links from atlasd", run: () => void actions.resync("manual resync") },
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
  { name: "/new", desc: "new chat in this profile · /new <profile> for another", run: (a) => void actions.newChat(a) },
  {
    name: "/hide",
    desc: "hide the open chat (H shows hidden chats)",
    run: () => {
      const o = s.open;
      if (o) void actions.hidePost(o.id, o.profile, true);
    },
  },
  {
    name: "/channel",
    desc: "new channel — name · category · guidelines",
    run: () => void actions.openNewChannel("", s.open?.profile ?? "default"),
  },
  {
    name: "/category",
    desc: "new category (groups channels in the tree)",
    run: () => void actions.openNewCategory(s.open?.profile ?? "default"),
  },
];

// ---- settings sheet model ---------------------------------------------
// Rows are derived from live state every render (no copies to go stale).
// Chat rows act on the open chat's runtime through serve's session-scoped
// config.set — a pick in one chat never rewrites config.yaml.

export const REASONING_LEVELS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];
export const DISPLAY_NAMES = ["full", "reasoning hidden", "exact tools", "quiet"];

export type SettingsRow = {
  id: string;
  section: string;
  label: string;
  value: string;
  hint?: string;
  tone?: "" | "ok" | "bad" | "warn" | "accent";
  disabled?: boolean;
};

function linkRow(id: string, label: string): SettingsRow {
  const l = s.status?.links?.[id];
  let up = l?.up;
  if (id === "serve" && s.link.serve) up = s.link.serve === "up";
  if (!s.status) return { id: "link-" + id, section: "connection", label, value: "atlasd unreachable", tone: "bad", disabled: true };
  if (l && !l.configured) return { id: "link-" + id, section: "connection", label, value: "not configured", tone: "warn", disabled: true };
  return {
    id: "link-" + id,
    section: "connection",
    label,
    value: up ? "connected" : "down",
    hint: up ? undefined : l?.error,
    tone: up ? "ok" : "bad",
    disabled: true,
  };
}

export function settingsRows(): SettingsRow[] {
  const rows: SettingsRow[] = [];
  const o = s.open;
  const si = o ? s.info[o.id] : undefined;
  if (o) {
    rows.push({
      id: "model",
      section: "this chat",
      label: "model",
      value: si?.model ? `${si.model}${si.provider ? " · " + si.provider : ""}` : "—",
      hint: si?.drift ? "⚠ " + si.drift : "enter opens the picker (M anywhere)",
      tone: si?.drift ? "warn" : "accent",
    });
    rows.push({
      id: "reasoning",
      section: "this chat",
      label: "reasoning",
      value: si?.reasoning || "default",
      hint: "h / l steps the effort · session-scoped",
    });
    rows.push({
      id: "fast",
      section: "this chat",
      label: "fast mode",
      value: si?.fast ? "on" : "off",
      hint: "priority tier where the provider supports it",
      tone: si?.fast ? "ok" : "",
    });
  }
  rows.push({ id: "display", section: "view", label: "transcript", value: DISPLAY_NAMES[s.dispMode] ?? "full", hint: "h / l cycles (r anywhere)" });
  rows.push({ id: "stale", section: "view", label: "stale chats (7d+)", value: s.hideStale ? "stowed" : "shown" });
  rows.push({ id: "hidden", section: "view", label: "hidden chats", value: s.hiddenView ? "shown" : "stowed" });
  rows.push({
    id: "link-stream",
    section: "connection",
    label: "event stream",
    value: s.link.stream ? "live" : "reconnecting…",
    tone: s.link.stream ? "ok" : "bad",
    disabled: true,
  });
  rows.push(linkRow("serve", "hermes-serve"));
  rows.push(linkRow("api", "hermes api"));
  rows.push(linkRow("hub", "atlas hub"));
  rows.push({
    id: "daemon",
    section: "connection",
    label: "atlasd",
    value: s.status ? `${s.status.version ?? "?"} · up ${fmtUptime(s.status.uptime_s ?? 0)}` : "unreachable",
    tone: s.status ? "" : "bad",
    disabled: true,
  });
  rows.push({ id: "resync", section: "actions", label: "resync now", value: "enter", hint: "re-read live turns, transcript, links" });
  rows.push({ id: "restart", section: "actions", label: "restart atlasd", value: "enter", hint: "refused while a turn is running" });
  return rows;
}

function fmtUptime(sec: number): string {
  if (sec < 90) return `${Math.round(sec)}s`;
  if (sec < 5400) return `${Math.round(sec / 60)}m`;
  if (sec < 172800) return `${(sec / 3600).toFixed(1)}h`;
  return `${Math.round(sec / 86400)}d`;
}

// Failure hint: what to do about a failed turn, keyed off serve's
// error_surface code (stable) with a text sniff as the fallback.
export function failureHint(code: string | undefined, text: string): string {
  const c = (code ?? "").toLowerCase();
  const t = text.toLowerCase();
  if (c === "send_failed") return "the message never reached hermes — check the connection, then retry";
  if (c.includes("not_found") || c.includes("model") || /\b404\b/.test(t))
    return "the provider rejected this model/route — pick another with M (or retry if it was a blip)";
  if (c.includes("auth") || /\b40[13]\b/.test(t)) return "provider auth failed — the credential for this provider needs attention";
  if (c.includes("rate") || /\b429\b/.test(t)) return "rate limited — give it a moment, then retry";
  if (c.includes("context") || t.includes("context length") || t.includes("too long"))
    return "the conversation is too large for this model — /compress or switch to a longer-context model";
  if (c.includes("timeout") || t.includes("timed out")) return "the provider timed out — retry";
  if (/\b5\d\d\b/.test(t) || c.includes("server") || c.includes("overload")) return "provider-side error — retry, or switch provider with M";
  return "R retries · M switches model";
}

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

// The picker borrows the composer; the user's draft waits here meanwhile.
let pickerParkedDraft = "";

// Recently picked (model, provider) pairs float to the top of the picker —
// the "easy model picker": the two or three models you actually switch
// between are always one keystroke away.
const LS_RECENT_MODELS = "atlas.recentModels";
const RECENT_MAX = 6;

function recentModels(): { name: string; slug: string }[] {
  try {
    const v = JSON.parse(localStorage.getItem(LS_RECENT_MODELS) || "[]");
    return Array.isArray(v) ? v.filter((x) => x && typeof x.name === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

function rememberModel(row: ModelRow) {
  const next = [{ name: row.name, slug: row.slug }, ...recentModels().filter((r) => !(r.name === row.name && r.slug === row.slug))];
  try {
    localStorage.setItem(LS_RECENT_MODELS, JSON.stringify(next.slice(0, RECENT_MAX)));
  } catch {
    /* storage denied: recents are a nicety */
  }
}

// One row per (provider, model): recents first (marked), then the current
// provider, then the rest; providers serve reports as unauthenticated are
// dropped (picking one can only fail). The current model is marked.
function flattenModels(p: ModelOptions): ModelRow[] {
  const curModel = (p.model ?? "").trim();
  const curSlug = (p.provider ?? "").trim().toLowerCase();
  const providers = (p.providers ?? [])
    .filter((prov) => prov.authenticated !== false)
    .slice()
    .sort((a, b) => Number(!!b.is_current) - Number(!!a.is_current));
  const all: ModelRow[] = [];
  for (const prov of providers) {
    const slug = prov.slug ?? "";
    const meta = prov.name ?? slug;
    for (const m of prov.models ?? []) {
      const name = String(m);
      all.push({
        name,
        slug,
        meta,
        current: curModel !== "" && name === curModel && (!curSlug || slug.toLowerCase() === curSlug),
      });
    }
  }
  const rec = recentModels();
  const key = (r: { name: string; slug: string }) => r.slug + "\u0000" + r.name;
  const recKeys = new Set(rec.map(key));
  const head: ModelRow[] = [];
  for (const r of rec) {
    const hit = all.find((x) => key(x) === key(r));
    if (hit) head.push({ ...hit, recent: true });
  }
  return head.concat(all.filter((x) => !recKeys.has(key(x))));
}

// Rows the picker shows for the current draft. The draft is always of the
// shape "/model <filter>" while the picker is open; anything after the
// command filters — every whitespace-separated token must match the model,
// provider slug or provider name ("opus or" → opus via openrouter).
export function modelPickRows(draft: string): ModelRow[] {
  const mp = s.modelPick;
  if (!mp) return [];
  const m = /^\/model\s*(.*)$/i.exec(draft);
  const toks = (m ? m[1] : draft).trim().toLowerCase().split(/\s+/).filter(Boolean);
  const rows = toks.length
    ? mp.rows.filter((r) => {
        const hay = `${r.name} ${r.slug} ${r.meta}`.toLowerCase();
        return toks.every((t) => hay.includes(t));
      })
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
