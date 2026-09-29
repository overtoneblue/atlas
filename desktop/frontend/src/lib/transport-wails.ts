// transport-wails: the original Wails v3 bindings transport. Kept until
// the Wails shell is retired; selected only for `--mode wails*` builds.

import { DataService } from "../../bindings/atlas/desktop";
import { Events } from "@wailsio/runtime";
import type { HubTree, Message, Session, SpawnList, Status as StatusT, TurnEvent, TurnState } from "./types";
import type { Catalog, Completion, ExecResult } from "./types";

export async function Status(): Promise<StatusT> {
  return (await DataService.Status()) as StatusT;
}

export async function GetTree(): Promise<HubTree> {
  return (await DataService.GetTree()) as unknown as HubTree;
}

export async function GetSessions(limit: number): Promise<Session[]> {
  return (await DataService.GetSessions(limit)) as unknown as Session[];
}

// The legacy Wails shell is frozen until retirement: the new paging
// params (offset/order) are accepted for interface parity and ignored.
export async function GetMessages(
  profile: string,
  sessionID: string,
  limit: number,
  _offset = 0,
  _order: "latest" | "oldest" = "latest",
): Promise<Message[]> {
  return (await DataService.GetMessages(profile, sessionID, limit)) as unknown as Message[];
}

// The legacy Wails core speaks text only; content parts flatten to their
// text (image parts are dropped — this shell is frozen until retirement).
export async function SendMessage(
  profile: string,
  sessionID: string,
  message: string | unknown[],
): Promise<void> {
  await DataService.SendMessage(profile, sessionID, flatten(message));
}

// No image relay on the legacy shell.
export async function AttachImage(_dataUrl: string): Promise<string> {
  return "";
}

// Spawned work isn't surfaced in the legacy shell.
export async function FetchSpawned(): Promise<SpawnList> {
  return { items: [] };
}

export async function FetchSpawnLog(
  _kind: string,
  _id: string,
  _task: number,
  _lines: number,
): Promise<string> {
  return "";
}

// Slash-command surface isn't wired in the legacy shell; the palette
// falls back to atlas-local commands only.
export async function GetCommands(): Promise<Catalog> {
  return { pairs: [], categories: [], commands: {}, skills: {} };
}

export async function CompleteSlash(_text: string, _sessionID: string): Promise<Completion[]> {
  return [];
}

export async function ExecSlash(_sessionID: string, _command: string): Promise<ExecResult> {
  return { output: "slash commands need the atlasd transport" };
}

// No live-turn attach on the legacy shell (SSE-only surface).
export async function FetchTurn(_sessionID: string): Promise<TurnState> {
  return { active: false };
}

// Session deletion needs the atlasd transport.
export async function DeleteSession(_profile: string, _sessionID: string): Promise<void> {
  throw new Error("delete needs the atlasd shell");
}

export async function StopTurn(sessionID: string): Promise<void> {
  await DataService.StopTurn(sessionID);
}

export async function InitialSession(): Promise<string> {
  return ((await DataService.InitialSession()) as string) ?? "";
}

export function subscribeTurn(onEvent: (ev: TurnEvent) => void): () => void {
  return Events.On("atlas:turn", (e) => {
    const data = e.data as TurnEvent;
    if (data && typeof data === "object" && typeof data.kind === "string") {
      onEvent(data);
    }
  });
}

function flatten(message: string | unknown[]): string {
  if (typeof message === "string") return message;
  return message
    .map((p) => {
      const part = p as { type?: string; text?: string };
      return part?.type === "text" ? (part.text ?? "") : "";
    })
    .filter(Boolean)
    .join("\n");
}
