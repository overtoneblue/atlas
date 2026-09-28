// Mobile visual-viewport manager — the iOS keyboard bridge.
//
// On iOS, focusing the composer can do any of three things, unreliably:
//   (a) shrink the visual viewport (classic Safari tab behavior),
//   (b) pan the visual viewport (reported as offsetTop), or
//   (c) scroll the whole document — the "stale WebKit pan", common in
//       standalone PWAs, where the page visibly jumps up and stays there.
// Events for these are also unreliable in standalone mode. So this module:
//
//   1. PRE-LIFT — on composer mousedown (fires after touchend but BEFORE
//      iOS's pre-focus visibility check), move the composer to its
//      keyboard-open position so Safari never decides to scroll. Timing at
//      mousedown also means the lift lands right as the keyboard starts
//      rising — no dead air where the composer hangs in mid-air.
//   2. FOLLOW — every frame while anything is moving, size/position the app
//      surface from the visual viewport: height = closedH - keyboard,
//      translated to follow offsetTop. Before any live signal arrives in a
//      gesture, the remembered keyboard height keeps the pre-lift stable.
//   3. SNAP BACK — the mobile app surface is a fixed, non-scrolling
//      document; any window.scrollY is an iOS artifact, so we undo it and
//      express the same displacement as keyboard height instead.
//   4. WATCHDOG — a 120 ms tick (only while the rAF loop is idle) catches
//      pans whose events iOS swallowed.

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

// The running instance's frame-kicker (single install at a time).
let kicker: (() => void) | null = null;

/** Pre-lift the composer above where the keyboard will land. Call from
 *  mousedown — it fires before iOS runs the pre-focus visibility check,
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
  kicker?.();
}

export function installMobileViewport(): () => void {
  const mq = window.matchMedia(MQ);
  const vv = window.visualViewport;
  const root = document.documentElement.style;
  const cls = document.documentElement.classList;

  let closedH = Math.round(vv?.height ?? window.innerHeight);
  let kbCache = readKbCache();
  let sawSignal = false; // a real keyboard signal arrived in this gesture
  let focusT = 0; // when an editable last gained focus
  let settle: ReturnType<typeof setTimeout> | undefined;
  let lastH = "";
  let lastOy = "";
  let lastKbOpen = false;

  const apply = () => {
    if (!mq.matches || !vv) {
      if (lastH !== "off") {
        root.removeProperty("--app-h");
        root.removeProperty("--app-oy");
        lastH = "off";
        lastOy = "off";
      }
      if (lastKbOpen) {
        cls.remove("kb-open");
        lastKbOpen = false;
      }
      return;
    }
    if (vv.scale > 1.02) {
      // pinch-zoom: just fit the visible height; keyboard math is
      // meaningless while scaled
      const zh = `${Math.round(vv.height)}px`;
      if (zh !== lastH) {
        root.setProperty("--app-h", zh);
        lastH = zh;
      }
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
    const nextH = `${h}px`;
    const nextOy = `${vvPan}px`;
    if (nextH !== lastH) {
      root.setProperty("--app-h", nextH);
      lastH = nextH;
    }
    if (nextOy !== lastOy) {
      root.setProperty("--app-oy", nextOy);
      lastOy = nextOy;
    }

    // While the keyboard is up, the home-indicator inset is covered by the
    // keyboard — collapse it (CSS) so there is no black band above the keys.
    const kbOpen = kb > 60;
    if (kbOpen !== lastKbOpen) {
      cls.toggle("kb-open", kbOpen);
      lastKbOpen = kbOpen;
    }

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

  // Frame-perfect tracking while anything is moving: keyboard animations run
  // at ~60fps — a 120ms tick reads as stepping. The rAF loop runs while
  // events keep arriving (each event extends the window) and stops ~450ms
  // after the last one.
  let raf = 0;
  let rafUntil = 0;
  const kick = () => {
    rafUntil = Math.max(rafUntil, Date.now() + 450);
    if (!raf) {
      const loop = () => {
        apply();
        raf = Date.now() < rafUntil ? requestAnimationFrame(loop) : 0;
      };
      raf = requestAnimationFrame(loop);
    }
  };
  kicker = kick;

  const onFocus = (e: FocusEvent) => {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === "TEXTAREA" || t.tagName === "INPUT")) {
      sawSignal = false;
      focusT = Date.now();
    }
    kick();
  };

  // Watchdog: catches pans/resizes whose events iOS swallowed. Skipped
  // while the rAF loop is live (it is already applying every frame).
  const tick = setInterval(() => {
    if (!raf) apply();
  }, 120);
  apply();
  vv?.addEventListener("resize", kick);
  vv?.addEventListener("scroll", kick);
  window.addEventListener("scroll", kick, { passive: true });
  window.addEventListener("focusin", onFocus);
  window.addEventListener("focusout", onFocus);
  window.addEventListener("orientationchange", kick);
  mq.addEventListener("change", kick);

  return () => {
    clearInterval(tick);
    clearTimeout(settle);
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
    kicker = null;
    vv?.removeEventListener("resize", kick);
    vv?.removeEventListener("scroll", kick);
    window.removeEventListener("scroll", kick);
    window.removeEventListener("focusin", onFocus);
    window.removeEventListener("focusout", onFocus);
    window.removeEventListener("orientationchange", kick);
    mq.removeEventListener("change", kick);
    root.removeProperty("--app-h");
    root.removeProperty("--app-oy");
    cls.remove("kb-open");
  };
}
