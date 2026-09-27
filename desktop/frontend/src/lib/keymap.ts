// The vim key router. One capture-phase listener on window; a modal state
// machine (NORMAL / INSERT) + a focus stack (tree / chat / composer).
// This is the same design that shipped in the TUI's chatnav work — the
// actions differ per pane, the grammar doesn't.

import { s, actions } from "./state.svelte";

export function installKeymap(): () => void {
  window.addEventListener("keydown", onKey, true);
  return () => window.removeEventListener("keydown", onKey, true);
}

function onKey(e: KeyboardEvent) {
  const key = e.key;

  // Composer insert mode: let typing through, intercept only the
  // escape hatches. Enter sends, Shift+Enter is a newline.
  if (s.focus === "composer" && s.mode === "INSERT") {
    if (key === "Escape") {
      e.preventDefault();
      actions.escape();
      return;
    }
    if (key === "Tab") {
      e.preventDefault();
      actions.cycleFocus(e.shiftKey ? -1 : 1);
      return;
    }
    if (key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void actions.send();
      return;
    }
    return;
  }

  if (e.ctrlKey && !e.metaKey) {
    if (key === "d") {
      e.preventDefault();
      actions.scrollChat(10);
      return;
    }
    if (key === "u") {
      e.preventDefault();
      actions.scrollChat(-10);
      return;
    }
    if (key === "Enter") {
      e.preventDefault();
      void actions.send();
      return;
    }
    return; // let other ctrl chords pass (devtools etc.)
  }

  switch (key) {
    case "j":
    case "ArrowDown":
      e.preventDefault();
      actions.move(1);
      break;
    case "k":
    case "ArrowUp":
      e.preventDefault();
      actions.move(-1);
      break;
    case "g":
      e.preventDefault();
      actions.toTop();
      break;
    case "G":
      e.preventDefault();
      actions.toBottom();
      break;
    case "h":
      e.preventDefault();
      actions.foldAt(true);
      break;
    case "l":
      e.preventDefault();
      actions.foldAt(false);
      break;
    case "Tab":
      e.preventDefault();
      actions.cycleFocus(e.shiftKey ? -1 : 1);
      break;
    case "Enter":
      e.preventDefault();
      if (s.focus === "composer") void actions.send();
      else actions.enter();
      break;
    case ".":
    case ",":
      e.preventDefault();
      actions.toggleStale();
      break;
    case "i":
      if (s.focus === "composer") {
        e.preventDefault();
        s.mode = "INSERT";
      }
      break;
    case "Escape":
      e.preventDefault();
      actions.escape();
      break;
    case "PageDown":
      e.preventDefault();
      actions.scrollChat(20);
      break;
    case "PageUp":
      e.preventDefault();
      actions.scrollChat(-20);
      break;
    default:
      break;
  }
}
