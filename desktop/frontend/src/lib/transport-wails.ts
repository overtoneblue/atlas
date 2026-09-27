// transport-wails: the original Wails v3 bindings transport. Kept until
// the Wails shell is retired; selected only when window._wails is present.

import { DataService } from "../../bindings/atlas/desktop";
import { Events } from "@wailsio/runtime";
import type { HubTree, Message, Session, Status as StatusT, TurnEvent } from "./types";

export async function Status(): Promise<StatusT> {
  return (await DataService.Status()) as StatusT;
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
