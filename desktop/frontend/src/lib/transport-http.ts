// transport-http: atlasd's HTTP + SSE transport — the ones shells
// (Electron, browser, phone) use.
//
// Same-origin in production: the shell loads this UI from atlasd itself, so
// relative URLs just work. During vite dev the UI runs on :9245, so point
// VITE_ATLASD_URL at the daemon; atlasd allows loopback-origin CORS.

import type { HubTree, Message, Session, Status as StatusT, TurnEvent } from "./types";

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
): Promise<Message[]> {
  const q = `profile=${encodeURIComponent(profile)}&session=${encodeURIComponent(sessionID)}&limit=${limit}`;
  return get(`/api/messages?${q}`);
}

export async function SendMessage(
  profile: string,
  sessionID: string,
  text: string,
): Promise<void> {
  return post("/api/send", { profile, session: sessionID, text });
}

export async function StopTurn(sessionID: string): Promise<void> {
  return post("/api/stop", { session: sessionID });
}

export async function InitialSession(): Promise<string> {
  const v = await get<string | null>("/api/initial");
  return v ?? "";
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
