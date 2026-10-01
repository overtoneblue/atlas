// Tree visibility engine — a direct port of the TUI's rules:
//  · hidden chats (Hermes' own hidden flag — Atlas' archive) render only in
//    hidden view (`H`), and keep their row while they are the open chat
//  · posts idle 7+ days are stowed when hideStale is on (containers left
//    with nothing to show drop out entirely)
//  · the OPEN chat is never stowed (ctx.keep): a stale chat opened on purpose
//    — restored on boot, --open — must keep a row for the cursor to sit on
//  · folded containers stay visible but hide their children (the stale pass
//    ignores folds; folds apply afterwards)
//  · spawned work (subagent runs, pi tasks, debbie dispatches) nests as child
//    rows directly under its parent chat, state glyph included
// Keys are stable name paths (parent/child; native nodes use their id, since
// names may repeat) so folds survive tree refreshes.

import type { HubNode, Row, Session, SpawnItem } from "./types";

export const STALE_S = 7 * 24 * 3600; // seconds

export function isStale(node: HubNode, now: number): boolean {
  if (node.kind !== "post") return false;
  const ts = node.last_active ?? 0;
  if (ts <= 0) return false; // unknown activity never stows
  return now - ts > STALE_S;
}

export type TreeCtx = {
  collapsed: Set<string>;
  hideStale: boolean;
  hiddenView: boolean; // show chats archived via the hidden flag
  now: number;
  spawned: Record<string, SpawnItem[]>;
  keep?: string | null; // session id exempt from stowing (the open chat)
};

function spawnLabel(it: SpawnItem): string {
  const t = (it.title || it.id).replace(/\s+/g, " ").trim();
  const short = t.length > 64 ? t.slice(0, 61) + "…" : t;
  const kind = it.kind === "pi" ? "pi · " : it.kind === "debbie" ? "debbie · " : "subagent · ";
  return kind + short;
}

export function buildRows(
  sections: HubNode[],
  ctx: TreeCtx,
): { rows: Row[]; hidden: number; archived: number; total: number } {
  const rows: Row[] = [];
  let hidden = 0;
  let archived = 0;
  let total = 0;

  function project(node: HubNode, key: string, depth: number): { rows: Row[]; hidden: number; archived: number } {
    if (node.kind === "post") {
      total += 1;
      const kept = !!ctx.keep && node.session_id === ctx.keep;
      if (node.hidden && !ctx.hiddenView && !kept) return { rows: [], hidden: 0, archived: 1 };
      if (!node.hidden && ctx.hideStale && !kept && isStale(node, ctx.now)) {
        return { rows: [], hidden: 1, archived: 0 };
      }
      const out: Row[] = [{ node, key, depth }];
      const spawns = node.session_id ? ctx.spawned[node.session_id] : undefined;
      for (const it of spawns ?? []) {
        out.push({
          node: { kind: "spawn", name: spawnLabel(it), spawn: it },
          key: key + "/#" + it.id,
          depth: depth + 1,
        });
      }
      return { rows: out, hidden: 0, archived: 0 };
    }
    const kids = node.children ?? [];
    let kidRows: Row[] = [];
    let hid = 0;
    let arch = 0;
    for (const k of kids) {
      // Native rows key on their stable id: names are free text and can repeat
      // (two "Misc" categories), but row keys must stay unique.
      const seg = k.native && k.id ? k.id : k.name;
      const p = project(k, key + "/" + seg, depth + 1);
      kidRows = kidRows.concat(p.rows);
      hid += p.hidden;
      arch += p.archived;
    }
    if (kidRows.length === 0 && !node.native) {
      // nothing left to show (empty, or everything stowed/hidden).
      // Native categories/channels stay on screen while empty — that is how
      // a freshly created channel is visible (and fillable) right away.
      return { rows: [], hidden: hid, archived: arch };
    }
    const self: Row = { node, key, depth };
    if (ctx.collapsed.has(key)) return { rows: [self], hidden: hid, archived: arch };
    return { rows: [self].concat(kidRows), hidden: hid, archived: arch };
  }

  for (const s of sections) {
    const p = project(s, s.name, 0);
    rows.push(...p.rows);
    hidden += p.hidden;
    archived += p.archived;
  }
  return { rows, hidden, archived, total };
}

// Fallback: flat session list when the hub is down.
export function buildSessionRows(
  sessions: Session[],
  ctx: TreeCtx,
): { rows: Row[]; hidden: number; archived: number; total: number } {
  const category: HubNode = {
    kind: "category",
    name: "RECENT SESSIONS",
    children: sessions.map((s) => ({
      kind: "post" as const,
      name: s.title || s.id,
      session_id: s.id,
      profile: "default",
      last_active: s.last_active,
      message_count: s.message_count,
      hidden: s.hidden,
    })),
  };
  return buildRows([category], ctx);
}
