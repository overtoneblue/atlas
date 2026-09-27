// Smooth wheel scrolling — our own physics layer.
//
// Why we own it: Chromium exposes NO knobs for wheel-scroll animation. The
// Firefox `general.smoothScroll.msdPhysics.*` family has no Chromium
// counterpart (and on Linux, Chromium wheel scrolling is essentially
// stepped). But the scroller is OUR DOM — so we animate scrollTop ourselves
// and get exactly the Firefox feel, on every shell: Electron today, browser
// and phone later. Same code, same physics, no engine dependency.
//
// Model: wheel events move a virtual target; each frame the real position
// chases it with time-constant decay (alpha = 1 - e^(-rate*dt)) — the same
// family as Firefox's MSD springs, minus the oscillator. Time-based, so
// 240Hz and 60Hz settle identically.
//
// Caden's Firefox dials → our dials:
//   general.smoothScroll = true                    → this layer (enabled)
//   msdPhysics.springs / slowdown*                 → `rate` (settle speed)
//   mousewheel.default.delta_multiplier_y = 300    → `multiplier` (3)
//
// Tune live, no reload (devtools console — works in any shell):
//   atlasScroll.set({ multiplier: 4, rate: 18 })
//   atlasScroll.get()
// Persists in localStorage["atlas.scroll"].

export type ScrollTuning = {
  enabled: boolean;
  multiplier: number; // wheel delta scale
  rate: number; // settle rate, 1/s (higher = snappier)
};

const DEFAULTS: ScrollTuning = { enabled: true, multiplier: 3, rate: 14 };

function load(): ScrollTuning {
  try {
    const raw = localStorage.getItem("atlas.scroll");
    if (raw) return { ...DEFAULTS, ...(JSON.parse(raw) as Partial<ScrollTuning>) };
  } catch {
    /* fall through */
  }
  return { ...DEFAULTS };
}

let cfg: ScrollTuning = load();

export function getScrollTuning(): ScrollTuning {
  return { ...cfg };
}

export function setScrollTuning(patch: Partial<ScrollTuning>): ScrollTuning {
  cfg = { ...cfg, ...patch };
  try {
    localStorage.setItem("atlas.scroll", JSON.stringify(cfg));
  } catch {
    /* ignore */
  }
  return { ...cfg };
}

if (typeof window !== "undefined") {
  (window as unknown as Record<string, unknown>).atlasScroll = {
    get: getScrollTuning,
    set: setScrollTuning,
  };
}

const LINE_PX = 16; // DOM_DELTA_LINE → px
const SETTLE_EPS = 0.4; // px
const EXTERNAL_DRIFT = 1.5; // px — someone else moved the scroller: bail

export function installSmoothScroll(el: HTMLElement): () => void {
  let target = el.scrollTop;
  let expect = el.scrollTop;
  let raf = 0;
  let last = 0;

  const maxScroll = () => Math.max(0, el.scrollHeight - el.clientHeight);

  const step = (now: number) => {
    const cur = el.scrollTop;
    // If the position moved from under us (keyboard scrolls, scrollbar
    // drags, stick-to-bottom jumps), the glide is stale — abort silently.
    if (last !== 0 && Math.abs(cur - expect) > EXTERNAL_DRIFT) {
      raf = 0;
      last = 0;
      return;
    }
    const dt = Math.min(64, last ? now - last : 16.7) / 1000;
    last = now;
    const diff = target - cur;
    if (Math.abs(diff) < SETTLE_EPS) {
      el.scrollTop = target;
      raf = 0;
      last = 0;
      return;
    }
    const alpha = 1 - Math.exp(-Math.max(1, cfg.rate) * dt);
    el.scrollTop = cur + diff * alpha;
    expect = el.scrollTop;
    raf = requestAnimationFrame(step);
  };

  const onWheel = (e: WheelEvent) => {
    if (!cfg.enabled) return;
    if (e.ctrlKey || e.shiftKey) return; // zoom / horizontal intent: untouchable
    if (e.deltaMode === WheelEvent.DOM_DELTA_PAGE || e.deltaY === 0) return;
    // Opt-out hatch for any future nested scroller that wants native wheel.
    if ((e.target as HTMLElement | null)?.closest("[data-native-scroll]")) return;

    if (!raf) {
      target = el.scrollTop; // fresh gesture: anchor to reality
      expect = el.scrollTop;
    }
    const px = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? LINE_PX : 1;
    target = Math.max(0, Math.min(maxScroll(), target + e.deltaY * px * cfg.multiplier));
    e.preventDefault();
    if (!raf) {
      last = 0;
      raf = requestAnimationFrame(step);
    }
  };

  el.addEventListener("wheel", onWheel, { passive: false });
  return () => {
    el.removeEventListener("wheel", onWheel);
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
  };
}
