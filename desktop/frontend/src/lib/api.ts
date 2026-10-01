// The frontend's entire data layer — transport-agnostic. The active
// transport is picked at build time (Wails legacy shell vs atlasd
// HTTP+SSE); everything above this file stays identical either way.

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
  TurnState,
} from "./types";
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

// Archive/unarchive a chat (Hermes hidden flag — reversible, nothing deleted).
export const HideSession = (profile: string, sessionID: string, hidden: boolean): Promise<void> =>
  t.HideSession(profile, sessionID, hidden);

// Mint a fresh chat in a profile (the /new command). With a channel id the
// chat is created inside that native channel and inherits its guidelines.
export const NewChat = (
  profile: string,
  channel = "",
): Promise<{ profile: string; session: string; warning?: string }> => t.NewChat(profile, channel);

// Native shape store: categories / channels / guidelines / assignments.
export const GetChannels = (profile: string): Promise<ChannelStore> => t.GetChannels(profile);

// One store mutation (create|update|delete|assign).
export const ChanOp = (body: Record<string, unknown>): Promise<Record<string, unknown>> =>
  t.ChanOp(body);

// Restart the atlasd this UI is served by (its supervisor relaunches it).
export const RestartDaemon = (force: boolean): Promise<void> => t.RestartDaemon(force);

// The --open startup target (empty string when unset).
export const InitialSession = (): Promise<string> => t.InitialSession();

// Spawned work (subagent runs + pi tasks), for nesting under their chat.
export const GetSpawned = (): Promise<SpawnList> => t.FetchSpawned();

// Slash-command surface (hermes-serve via the active transport).
export const GetCommands = (): Promise<Catalog> => t.GetCommands();
export const CompleteSlash = (text: string, sessionID: string): Promise<Completion[]> =>
  t.CompleteSlash(text, sessionID);
// Run one slash command against the open session (stored or runtime id).
export const ExecSlash = (sessionID: string, command: string): Promise<ExecResult> =>
  t.ExecSlash(sessionID, command);

// Model-picker payload (providers, models, current selection).
export const FetchModels = (sessionID: string): Promise<ModelOptions> => t.FetchModels(sessionID);

// Text tail of one spawned run's live log.
export const GetSpawnLog = (
  kind: string,
  id: string,
  task: number,
  lines: number,
): Promise<string> => t.FetchSpawnLog(kind, id, task, lines);

// Live-turn attach: snapshot of a mid-flight turn (see types.TurnState).
export const FetchTurn = (sessionID: string): Promise<TurnState> => t.FetchTurn(sessionID);

// Delete a chat (session + transcript) from its profile's store.
export const DeleteSession = (profile: string, sessionID: string): Promise<void> =>
  t.DeleteSession(profile, sessionID);
