// Live-turn event plumbing: the Go side emits "atlas:turn" events over the
// Wails bridge (same bridge in desktop and server builds); this subscribes
// and hands each payload to the state machine.

import { Events } from "@wailsio/runtime";
import type { TurnEvent } from "./types";

export function installTurnEvents(onEvent: (ev: TurnEvent) => void): () => void {
  return Events.On("atlas:turn", (e) => {
    const data = e.data as TurnEvent;
    if (data && typeof data === "object" && typeof data.kind === "string") {
      onEvent(data);
    }
  });
}
