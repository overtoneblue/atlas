// transport-http: atlasd's HTTP + SSE transport — the one shells
// (Electron, browser, phone) use.
//
// Same-origin in production: the shell loads this UI from atlasd itself, so
// relative URLs just work. During vite dev the UI runs on :9245, so point
// VITE_ATLASD_URL at the daemon; atlasd allows loopback-origin CORS.

import type {
  Catalog,
  ChannelStore,
  Completion,
  ExecResult,
  HubTree,
  Message,
  ModelOptions,
  Session,
  SpawnList,
  Status as StatusT,
  TurnEvent,
  TurnState,
} from "./types";

const BASE: string = import.meta.env.VITE_ATLASD_URL ?? "";

async function errText(res: Response): Promise<string> {
  try {
    const j = (await res.json()) as { error?: string };
    if (j && typeof j.error === "string") return j.error;
  } catch {
    /* fall through */
  }
  return `${res.status} ${res.statusText}`;
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`);
  if (!res.ok) throw new Error(await errText(res));
  return (await res.json()) as T;
}

async function post(path: string, body: unknown): Promise<void> {
  const res = await fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await errText(res));
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await errText(res));
  return (await res.json()) as T;
}

async function del(path: string): Promise<void> {
  const res = await fetch(`${BASE}${path}`, { method: "DELETE" });
  if (!res.ok) throw new Error(await errText(res));
}

export async function Status(): Promise<StatusT> {
  return get("/api/status");
}

export async function GetTree(): Promise<HubTree> {
  return get("/api/tree");
}

export async function GetSessions(limit: number): Promise<Session[]> {
  return get(`/api/sessions?limit=${limit}`);
}

export async function GetMessages(
  profile: string,
  sessionID: string,
  limit: number,
  offset = 0,
  order: "latest" | "oldest" = "latest",
): Promise<Message[]> {
  // Tail-first reads: the newest page loads up front (order=latest pages
  // back from the end), offset pages through older history — including
  // compaction-archived display rows (include_compacted; the head API
  // ignores it gracefully until its endpoint supports it).
  const q =
    `profile=${encodeURIComponent(profile)}&session=${encodeURIComponent(sessionID)}` +
    `&limit=${limit}&offset=${offset}&order=${order}&include_compacted=1`;
  return get(`/api/messages?${q}`);
}

// Live-turn snapshot: what (if anything) is streaming in a session right
// now. A client attaching mid-turn paints the whole turn so far, then
// continues on the event stream.
export async function FetchTurn(sessionID: string): Promise<TurnState> {
  return get(`/api/turn?session=${encodeURIComponent(sessionID)}`);
}

// Delete a session (and its transcript) from its profile's store.
export async function DeleteSession(profile: string, sessionID: string): Promise<void> {
  const q = `profile=${encodeURIComponent(profile)}&session=${encodeURIComponent(sessionID)}`;
  await del(`/api/session?${q}`);
}

// message is either plain text or a content-parts array (text + image_url
// parts) — a native vision payload the Hermes API understands.
export async function SendMessage(
  profile: string,
  sessionID: string,
  message: string | unknown[],
): Promise<void> {
  return post("/api/send", { profile, session: sessionID, message });
}

// AttachImage persists a pasted image on the daemon side and returns its
// path; the path rides the turn as a MEDIA: ref so history re-renders it.
export async function AttachImage(dataUrl: string): Promise<string> {
  const r = await postJSON<{ path?: string }>("/api/attach", { data: dataUrl });
  return r.path ?? "";
}

export async function StopTurn(sessionID: string): Promise<void> {
  return post("/api/stop", { session: sessionID });
}

// Archive/unarchive a chat via Hermes' hidden flag (nothing is deleted;
// hidden chats stay resumable and can be shown/restored from Atlas).
export async function HideSession(profile: string, sessionID: string, hidden: boolean): Promise<void> {
  return post("/api/hide", { profile, session: sessionID, hidden });
}

// Mint a fresh chat in a profile; the daemon returns its stored id. With a
// channel the chat is created inside it and inherits its guidelines.
export async function NewChat(
  profile: string,
  channel = "",
): Promise<{ profile: string; session: string; warning?: string }> {
  return postJSON<{ profile: string; session: string; warning?: string }>(
    "/api/new",
    channel ? { profile, channel } : { profile },
  );
}

// Native shape store: categories / channels / guidelines / assignments.
export async function GetChannels(profile: string): Promise<ChannelStore> {
  return get(`/api/channels?profile=${encodeURIComponent(profile)}`);
}

// One store mutation (create|update|delete|assign).
export async function ChanOp(body: Record<string, unknown>): Promise<Record<string, unknown>> {
  return postJSON<Record<string, unknown>>("/api/chan", body);
}

// Model switch via the confirm-capable route. A selection guard (large
// cached context, cost, data-policy) answers confirm_required + a message;
// re-send with confirm=true to apply after the user confirms.
export async function SetModel(
  session: string,
  value: string,
  confirm: boolean,
): Promise<{
  value?: string;
  warning?: string;
  confirm_required?: boolean;
  confirm_message?: string;
  scope?: string;
}> {
  return postJSON("/api/model", { session, value, confirm });
}

// Ask atlasd to exit so its supervisor (systemd) relaunches it — the in-app
// `systemctl restart atlasd`. The custom header forces a CORS preflight, so a
// stray web page can't fire it blind. 409 while turns are live unless forced.
export async function RestartDaemon(force: boolean): Promise<void> {
  const res = await fetch(`${BASE}/api/restart`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Atlas-Action": "restart" },
    body: JSON.stringify({ force }),
  });
  if (!res.ok) throw new Error(await errText(res));
}

export async function InitialSession(): Promise<string> {
  const v = await get<string | null>("/api/initial");
  return v ?? "";
}

// Spawned work across the suite (hub scan: delegation ledger + live logs +
// pi task quartet).
export async function FetchSpawned(): Promise<SpawnList> {
  return get("/api/spawned");
}

// Slash-command catalog from hermes-serve (daemon-cached).
export async function GetCommands(): Promise<Catalog> {
  return get("/api/commands");
}

// Live fuzzy slash/skill completions for the composer draft.
export async function CompleteSlash(text: string, sessionID: string): Promise<Completion[]> {
  const q = `text=${encodeURIComponent(text)}&session=${encodeURIComponent(sessionID)}`;
  const r = await get<{ items?: Completion[] }>(`/api/complete?${q}`);
  return r.items ?? [];
}

// Run one slash command against the open session (stored or runtime id).
export async function ExecSlash(sessionID: string, command: string): Promise<ExecResult> {
  return postJSON<ExecResult>("/api/exec", { session: sessionID, command });
}

// Model-picker payload for a session: providers, their models, and the
// current selection (the serve binds it to the session's live runtime).
export async function FetchModels(sessionID: string): Promise<ModelOptions> {
  return get(`/api/models?session=${encodeURIComponent(sessionID)}`);
}

// Text tail of one spawned run's live log (the hub shapes the path).
export async function FetchSpawnLog(
  kind: string,
  id: string,
  task: number,
  lines: number,
): Promise<string> {
  const q = `kind=${encodeURIComponent(kind)}&id=${encodeURIComponent(id)}&task=${task}&lines=${lines}`;
  const r = await get<{ text?: string }>(`/api/spawn-log?${q}`);
  return r.text ?? "";
}

export function subscribeTurn(onEvent: (ev: TurnEvent) => void): () => void {
  const es = new EventSource(`${BASE}/api/events`);
  const handler = (e: MessageEvent) => {
    try {
      const d = JSON.parse(e.data) as TurnEvent;
      if (d && typeof d.kind === "string") onEvent(d);
    } catch {
      /* ignore malformed frames */
    }
  };
  es.addEventListener("turn", handler as EventListener);
  return () => es.close();
}
