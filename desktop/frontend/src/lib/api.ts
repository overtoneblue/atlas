// The frontend's entire data layer — now transport-agnostic. The active
// transport is picked once (Wails legacy shell vs atlasd HTTP+SSE);
// everything above this file stays identical either way.

import type { HubTree, Message, Session, Status as StatusT } from "./types";
import { isWails } from "./transport";
import * as http from "./transport-http";
import * as wails from "./transport-wails";

const t = isWails() ? wails : http;

export const Status = (): Promise<StatusT> => t.Status();

export const GetTree = (): Promise<HubTree> => t.GetTree();

export const GetSessions = (limit: number): Promise<Session[]> => t.GetSessions(limit);

export const GetMessages = (
  profile: string,
  sessionID: string,
  limit: number,
): Promise<Message[]> => t.GetMessages(profile, sessionID, limit);

export const SendMessage = (
  profile: string,
  sessionID: string,
  text: string,
): Promise<void> => t.SendMessage(profile, sessionID, text);

// StopTurn interrupts the in-flight run for a session (best-effort: atlasd
// falls back to detaching the stream when the run id is not yet known).
export const StopTurn = (sessionID: string): Promise<void> => t.StopTurn(sessionID);

// The --open startup target (empty string when unset).
export const InitialSession = (): Promise<string> => t.InitialSession();
