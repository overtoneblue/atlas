// Live-turn event plumbing — transport-agnostic. Wails shells receive
// "atlas:turn" over the bridge; atlasd shells receive the same payloads as
// SSE "turn" events. The subscription surface is identical.

import type { TurnEvent } from "./types";
import { isWails } from "./transport";
import * as http from "./transport-http";
import * as wails from "./transport-wails";

export function installTurnEvents(onEvent: (ev: TurnEvent) => void): () => void {
  return isWails() ? wails.subscribeTurn(onEvent) : http.subscribeTurn(onEvent);
}
