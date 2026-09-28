// Mobile visual-viewport manager — the iOS keyboard bridge.
//
// On iOS, focusing the composer can do any of three things, unreliably:
//   (a) shrink the visual viewport (classic Safari tab behavior),
//   (b) pan the visual viewport (reported as offsetTop), or
//   (c) scroll the whole document — the "stale WebKit pan", common in
//       standalone PWAs, where the page visibly jumps up and stays there.
// Events for these are also unreliable in standalone mode. So this module:
//
//   1. PRE-LIFT — on composer mousedown/touchstart (both fire BEFORE iOS's
//      pre-focus visibility check), move the composer to its keyboard-open
//      position so Safari never decides it needs to scroll at all.
//   2. FOLLOW — every tick, size/position the app surface from the visual
//      viewport: height = closedH - keyboard, translated to follow offsetTop.
//      Before any live signal arrives in a gesture, the remembered keyboard
//      height keeps the pre-lift position stable.
//   3. SNAP BACK — the mobile app surface is a fixed, non-scrolling
//      document; any window.scrollY is an iOS artifact, so we undo it and
//      express the same displacement as keyboard height instead.
//   4. WATCHDOG — a 120 ms tick catches pans whose events iOS swallowed.

const LS_KEY = "atlas.kbHeight";
const DEFAULT_KB = 320; // first-run guess (iPhone portrait, Face ID, + QuickType)
const MQ = "(max-width: 760px)";

function readKbCache(): number {
  try {
    const v = Number(localStorage.getItem(LS_KEY));
    if (Number.isFinite(v) && v > 60 && v < 520) return Math.round(v);
  } catch {
    /* private mode etc. */
  }
  return DEFAULT_KB;
}

function writeKbCache(v: number) {
  try {
    localStorage.setItem(LS_KEY, String(v));
  } catch {
    /* ignore */
  }
}

function focusedEditable(): boolean {
  const af = document.activeElement as HTMLElement | null;
  return !!af && (af.tagName === "TEXTAREA" || af.tagName === "INPUT");
}

/** Pre-lift the composer above where the keyboard will land. Call from
 *  mousedown/touchstart — both fire before iOS runs its visibility check,
 *  which is what makes this the only reliable prevention point. */
export function preLiftComposer() {
  if (!window.matchMedia(MQ).matches) return;
  // Touch only: with a mouse there is no keyboard to pre-empt, and a
  // coordinate-based click could miss the composer after it moves.
  if (!window.matchMedia("(pointer: coarse)").matches) return;
  const vv = window.visualViewport;
  if (!vv || vv.scale > 1.02) return;
  const closed = Math.max(vv.height, window.innerHeight);
  const h = Math.max(240, Math.round(closed - readKbCache()));
  document.documentElement.style.setProperty("--app-h", `${h}px`);
}

export function installMobileViewport(): () => void {
  const mq = window.matchMedia(MQ);
  const vv = window.visualViewport;
  const root = document.documentElement.style;

  let closedH = Math.round(vv?.height ?? window.innerHeight);
  let kbCache = readKbCache();
  let sawSignal = false; // a real keyboard signal arrived in this gesture
  let focusT = 0; // when an editable last gained focus
  let settle: ReturnType<typeof setTimeout> | undefined;

  const apply = () => {
    if (!mq.matches || !vv) {
      root.removeProperty("--app-h");
      root.removeProperty("--app-oy");
      return;
    }
    if (vv.scale > 1.02) {
      // pinch-zoom: just fit the visible height; keyboard math is
      // meaningless while scaled
      root.setProperty("--app-h", `${Math.round(vv.height)}px`);
      root.setProperty("--app-oy", "0px");
      return;
    }

    const visH = Math.round(vv.height);
    const vvPan = Math.max(0, Math.round(vv.offsetTop));
    const winScroll = Math.max(0, Math.round(window.scrollY));
    const shrink = Math.max(0, closedH - visH);
    const scrollKb = winScroll > 40 ? winScroll : 0; // doc-scrolled to reveal the input
    const focused = focusedEditable();

    if (shrink > 40 || scrollKb > 0) sawSignal = true;

    // Keyboard height estimate: live signals win; before the first signal of
    // a gesture, hold the remembered height so the pre-lift stays put.
    const optimistic = focused && !sawSignal && Date.now() - focusT < 2000;
    let kb =
      shrink > 40 || scrollKb > 0 || optimistic
        ? Math.max(shrink, scrollKb, focused ? kbCache : 0)
        : 0;
    kb = Math.min(kb, Math.round(closedH * 0.65));

    // Nothing keyboard-like anywhere: this viewport is the new "closed"
    // reference (first load, rotation, browser chrome changes).
    if (!focused && kb <= 40 && winScroll === 0 && vvPan === 0) closedH = visH;

    const h = Math.max(240, Math.round(closedH - kb));
    root.setProperty("--app-h", `${h}px`);
    root.setProperty("--app-oy", `${vvPan}px`);

    // The app surface is a fixed, non-scrolling document — any window scroll
    // is an iOS keyboard artifact. Undo it; the height above already
    // accounts for the same displacement.
    if (window.scrollY !== 0) window.scrollTo(0, 0);

    // Once the keyboard settles, remember its true height for next time.
    if (focused && shrink > 60) {
      clearTimeout(settle);
      settle = setTimeout(() => {
        const cur = Math.max(0, closedH - Math.round(vv.height));
        if (cur > 60 && Math.abs(cur - kbCache) > 6) {
          kbCache = cur;
          writeKbCache(cur);
          apply();
        }
      }, 350);
    }
  };

  const onFocus = (e: FocusEvent) => {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === "TEXTAREA" || t.tagName === "INPUT")) {
      sawSignal = false;
      focusT = Date.now();
    }
    apply();
  };

  const tick = setInterval(apply, 120);
  apply();
  vv?.addEventListener("resize", apply);
  vv?.addEventListener("scroll", apply);
  window.addEventListener("scroll", apply, { passive: true });
  window.addEventListener("focusin", onFocus);
  window.addEventListener("focusout", onFocus);
  window.addEventListener("orientationchange", apply);
  mq.addEventListener("change", apply);

  return () => {
    clearInterval(tick);
    clearTimeout(settle);
    vv?.removeEventListener("resize", apply);
    vv?.removeEventListener("scroll", apply);
    window.removeEventListener("scroll", apply);
    window.removeEventListener("focusin", onFocus);
    window.removeEventListener("focusout", onFocus);
    window.removeEventListener("orientationchange", apply);
    mq.removeEventListener("change", apply);
    root.removeProperty("--app-h");
    root.removeProperty("--app-oy");
  };
}
