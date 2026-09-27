// The transport seam: every shell gets the same calls plus one event
// stream. Two implementations exist:
//
//   - transport-wails: Wails v3 bindings (the legacy desktop shell)
//   - transport-http:  atlasd's HTTP + SSE API (Electron / browser shells)
//
// Picked at BUILD time via __ATLAS_WAILS__ (see vite.config.ts). Nothing
// above this file knows or cares which one is active.

import type { HubTree, Message, Session, SpawnList, Status, TurnEvent } from "./types";

export interface Transport {
  Status(): Promise<Status>;
  GetTree(): Promise<HubTree>;
  GetSessions(limit: number): Promise<Session[]>;
  GetMessages(profile: string, sessionID: string, limit: number): Promise<Message[]>;
  SendMessage(profile: string, sessionID: string, message: string | unknown[]): Promise<void>;
  AttachImage(dataUrl: string): Promise<string>;
  StopTurn(sessionID: string): Promise<void>;
  InitialSession(): Promise<string>;
  FetchSpawned(): Promise<SpawnList>;
  FetchSpawnLog(kind: string, id: string, task: number, lines: number): Promise<string>;
  subscribeTurn(onEvent: (ev: TurnEvent) => void): () => void;
}

// The transport is a build-time contract (see vite.config.ts): Wails builds
// set __ATLAS_WAILS__; everything else speaks the atlasd HTTP+SSE API.
// Never sniff at runtime — importing the @wailsio/runtime shim defines
// window._wails anywhere, which makes sniffing lie.

export const isWails = (): boolean => __ATLAS_WAILS__;
