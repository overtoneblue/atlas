// The frontend's entire data layer: thin wrappers over the Go DataService
// bindings. When the head-side daemon arrives, only this file changes —
// everything above it stays identical (that's the browser port story).

import { DataService } from "../../bindings/atlas/desktop";
import type { HubTree, Message, Session, Status } from "./types";

export async function Status(): Promise<Status> {
  return (await DataService.Status()) as Status;
}

export async function GetTree(): Promise<HubTree> {
  return (await DataService.GetTree()) as unknown as HubTree;
}

export async function GetSessions(limit: number): Promise<Session[]> {
  return (await DataService.GetSessions(limit)) as unknown as Session[];
}

export async function GetMessages(
  profile: string,
  sessionID: string,
  limit: number,
): Promise<Message[]> {
  return (await DataService.GetMessages(profile, sessionID, limit)) as unknown as Message[];
}

export async function SendMessage(
  profile: string,
  sessionID: string,
  text: string,
): Promise<void> {
  await DataService.SendMessage(profile, sessionID, text);
}

// StopTurn interrupts the in-flight run for a session (best-effort: the Go
// side falls back to detaching the stream when the run id is not yet known).
export async function StopTurn(sessionID: string): Promise<void> {
  await DataService.StopTurn(sessionID);
}

// The --open startup target (empty string when unset).
export async function InitialSession(): Promise<string> {
  return ((await DataService.InitialSession()) as string) ?? "";
}

