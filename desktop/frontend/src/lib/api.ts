// The frontend's entire data layer — transport-agnostic. The active
// transport is picked at build time (Wails legacy shell vs atlasd
// HTTP+SSE); everything above this file stays identical either way.

import type { HubTree, Message, Session, SpawnList, Status as StatusT } from "./types";
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
  offset = 0,
  order: "latest" | "oldest" = "latest",
): Promise<Message[]> => t.GetMessages(profile, sessionID, limit, offset, order);

// message: plain text, or content parts (text + image_url) for vision.
export const SendMessage = (
  profile: string,
  sessionID: string,
  message: string | unknown[],
): Promise<void> => t.SendMessage(profile, sessionID, message);

// Persist a pasted image daemon-side; returns its path ("" when the shell
// has no relay). The path rides the message as a MEDIA: ref.
export const AttachImage = (dataUrl: string): Promise<string> => t.AttachImage(dataUrl);

// StopTurn interrupts the in-flight run for a session (best-effort: atlasd
// falls back to detaching the stream when the run id is not yet known).
export const StopTurn = (sessionID: string): Promise<void> => t.StopTurn(sessionID);

// The --open startup target (empty string when unset).
export const InitialSession = (): Promise<string> => t.InitialSession();

// Spawned work (subagent runs + pi tasks), for nesting under their chat.
export const GetSpawned = (): Promise<SpawnList> => t.FetchSpawned();

// Text tail of one spawned run's live log.
export const GetSpawnLog = (
  kind: string,
  id: string,
  task: number,
  lines: number,
): Promise<string> => t.FetchSpawnLog(kind, id, task, lines);
