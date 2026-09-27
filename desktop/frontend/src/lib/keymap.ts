// The vim key router. One capture-phase listener on window; modal branches
// (help overlay → find bar → composer INSERT → visual selection → NORMAL)
// + a focus stack (tree / chat / composer). Counts (`3j`) accumulate in
// NORMAL and consume on the next motion. Actions live in state.svelte.ts;
// this file owns grammar only.

import { s, actions } from "./state.svelte";

export function installKeymap(): () => void {
  window.addEventListener("keydown", onKey, true);
  return () => window.removeEventListener("keydown", onKey, true);
}

// True when the user has text selected (textarea selection or page
// selection). Ctrl+C then means "copy", never "stop the turn".
function hasSelection(): boolean {
  const el = document.activeElement;
  if (el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) {
    return el.selectionStart !== el.selectionEnd;
  }
  return (window.getSelection()?.toString() ?? "") !== "";
}

function onKey(e: KeyboardEvent) {
  const key = e.key;

  // Help overlay: ? or esc closes; nothing else acts while it's up.
  if (s.helpOpen) {
    if (key === "?" || key === "Escape") {
      e.preventDefault();
      actions.closeHelp();
    }
    return;
  }

  // Find bar: the real <input> owns typing; we own the verbs.
  if (s.findOpen) {
    if (key === "Escape") {
      e.preventDefault();
      actions.findClose();
      return;
    }
    if (key === "Enter") {
      e.preventDefault();
      actions.findAccept();
      return;
    }
    if (key === "ArrowDown") {
      e.preventDefault();
      actions.findNext(1);
      return;
    }
    if (key === "ArrowUp") {
      e.preventDefault();
      actions.findNext(-1);
      return;
    }
    return; // everything else (typing, backspace, paste) flows to the input
  }

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
    if (e.ctrlKey && key === "c") {
      if (s.live && !hasSelection()) {
        e.preventDefault();
        void actions.stopTurn();
      }
      return;
    }
    return;
  }

  // Counts: leading digits accumulate (3j, 12k). The next key consumes them.
  if (/^[1-9]$/.test(key) || (key === "0" && s.pendingCount > 0)) {
    e.preventDefault();
    s.pendingCount = Math.min(999, s.pendingCount * 10 + Number(key));
    return;
  }
  const n = s.pendingCount || 1;
  s.pendingCount = 0;

  if (e.ctrlKey && !e.metaKey) {
    if (key === "c") {
      if (s.live && !hasSelection()) {
        e.preventDefault();
        void actions.stopTurn();
      }
      return; // otherwise a normal copy
    }
    if (key === "d") {
      e.preventDefault();
      actions.scrollChat(10 * n);
      return;
    }
    if (key === "u") {
      e.preventDefault();
      actions.scrollChat(-10 * n);
      return;
    }
    if (key === "Enter") {
      e.preventDefault();
      void actions.send();
      return;
    }
    return; // let other ctrl chords pass (devtools etc.)
  }

  // Visual selection: motions extend, y yanks, esc cancels.
  if (s.visual) {
    switch (key) {
      case "j":
      case "ArrowDown":
        e.preventDefault();
        actions.visualMove(n);
        return;
      case "k":
      case "ArrowUp":
        e.preventDefault();
        actions.visualMove(-n);
        return;
      case "g":
        e.preventDefault();
        actions.visualMove(-1e6);
        return;
      case "G":
        e.preventDefault();
        actions.visualMove(1e6);
        return;
      case "y":
        e.preventDefault();
        void actions.visualYank();
        return;
      case "Escape":
        e.preventDefault();
        actions.visualCancel();
        return;
      default:
        return; // everything else is inert while selecting
    }
  }

  switch (key) {
    case "j":
    case "ArrowDown":
      e.preventDefault();
      actions.move(n);
      break;
    case "k":
    case "ArrowUp":
      e.preventDefault();
      actions.move(-n);
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
    case "/":
      e.preventDefault();
      actions.findStart();
      break;
    case "n":
      e.preventDefault();
      actions.findNext(1);
      break;
    case "N":
      e.preventDefault();
      actions.findNext(-1);
      break;
    case "v":
      e.preventDefault();
      actions.visualStart(false);
      break;
    case "V":
      e.preventDefault();
      actions.visualStart(true);
      break;
    case "r":
      e.preventDefault();
      actions.toggleReasoning();
      break;
    case "?":
      e.preventDefault();
      actions.toggleHelp();
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
      actions.scrollChat(20 * n);
      break;
    case "PageUp":
      e.preventDefault();
      actions.scrollChat(-20 * n);
      break;
    default:
      break;
  }
}
