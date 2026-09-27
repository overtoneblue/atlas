// The transport seam: every shell gets the same seven calls plus one
// event stream. Two implementations exist:
//
//   - transport-wails: Wails v3 bindings (the legacy desktop shell)
//   - transport-http:  atlasd's HTTP + SSE API (Electron / browser shells)
//
// Picked once at runtime: the Wails runtime injects `window._wails`;
// anything else (atlasd-served window, vite dev, phone browser) gets HTTP.
// Nothing above this file knows or cares which one is active.

import type { HubTree, Message, Session, Status, TurnEvent } from "./types";

export interface Transport {
  Status(): Promise<Status>;
  GetTree(): Promise<HubTree>;
  GetSessions(limit: number): Promise<Session[]>;
  GetMessages(profile: string, sessionID: string, limit: number): Promise<Message[]>;
  SendMessage(profile: string, sessionID: string, text: string): Promise<void>;
  StopTurn(sessionID: string): Promise<void>;
  InitialSession(): Promise<string>;
  subscribeTurn(onEvent: (ev: TurnEvent) => void): () => void;
}

// The transport is a build-time contract (see vite.config.ts): Wails builds
// set __ATLAS_WAILS__; everything else speaks the atlasd HTTP+SSE API.
// Never sniff at runtime — importing the @wailsio/runtime shim defines
// window._wails anywhere, which makes sniffing lie.

export const isWails = (): boolean => __ATLAS_WAILS__;
