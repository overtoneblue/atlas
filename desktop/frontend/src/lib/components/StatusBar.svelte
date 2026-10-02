<script lang="ts">
  import { s, actions } from "../state.svelte";

  // Triple-tap the version to toggle a live viewport readout — the remote
  // diagnostic for iOS keyboard misbehavior on the phone.
  let taps = 0;
  let tapT = 0;
  let vpDebug = $state(false);
  let vpText = $state("");

  function verTap() {
    const now = Date.now();
    taps = now - tapT < 600 ? taps + 1 : 1;
    tapT = now;
    if (taps >= 3) {
      taps = 0;
      vpDebug = !vpDebug;
    }
  }

  $effect(() => {
    if (!vpDebug) return;
    const id = setInterval(() => {
      const vv = window.visualViewport;
      const cs = getComputedStyle(document.documentElement);
      vpText =
        `vv ${Math.round(vv?.height ?? 0)}/${Math.round(vv?.offsetTop ?? 0)} s${(vv?.scale ?? 1).toFixed(2)}` +
        ` win ${window.innerHeight} sy ${Math.round(window.scrollY)}` +
        ` h ${cs.getPropertyValue("--app-h").trim() || "-"} oy ${cs.getPropertyValue("--app-oy").trim() || "-"}` +
        ` kb ${localStorage.getItem("atlas.kbHeight") ?? "-"}`;
    }, 250);
    return () => clearInterval(id);
  });

  // ---- live session chips (the official desktop's status-bar numbers) ----

  const si = $derived(s.open ? s.info[s.open.id] : undefined);
  const busy = $derived(!!(s.open && s.turnBusy[s.open.id]));

  // Model ids are long ("anthropic/claude-opus-5.5"): show the tail.
  function shortModel(m: string): string {
    const tail = m.includes("/") ? m.slice(m.lastIndexOf("/") + 1) : m;
    return tail.length > 28 ? tail.slice(0, 27) + "…" : tail;
  }

  function fmtTokens(n: number): string {
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1) + "M";
    if (n >= 1000) return Math.round(n / 1000) + "k";
    return String(n);
  }

  const ctxPct = $derived(si?.context_percent ?? 0);
  const ctxTone = $derived(ctxPct >= 85 ? "bad" : ctxPct >= 65 ? "warn" : "");

  // Link health: real reachability, not "a token is configured". The serve
  // edge comes live off the event stream; api/hub from the status poll.
  type Dot = { name: string; tone: "ok" | "bad" | "warn" | "off"; title: string };
  const dots = $derived.by((): Dot[] => {
    if (!s.status) return [{ name: "atlasd", tone: "bad", title: "atlasd unreachable" }];
    const L = s.status.links ?? {};
    const one = (k: string, cfg: boolean | undefined): Dot => {
      const l = L[k];
      if (l && !l.configured) return { name: k, tone: "off", title: `${k}: not configured` };
      let up = l ? l.up : !!cfg;
      if (k === "serve" && s.link.serve) up = s.link.serve === "up";
      return { name: k, tone: up ? "ok" : "bad", title: up ? `${k}: connected` : `${k}: down${l?.error ? " — " + l.error : ""}` };
    };
    const out = [one("serve", s.status.serve), one("api", s.status.api), one("hub", s.status.hub)];
    if (!s.link.stream) out.unshift({ name: "stream", tone: "warn", title: "event stream reconnecting" });
    return out;
  });
</script>

<div class="statusbar">
  <button class="mnav" onclick={() => actions.toggleNav()} title="workstreams">
    ☰ workstreams{#if actions.unreadCount() > 0}<span class="mnav-n">{actions.unreadCount()}</span>{/if}
  </button>
  <span class="badge mode" class:insert={s.mode === "INSERT"}>{s.mode}</span>
  <span class="focus dim">{s.focus}</span>
  {#if s.pendingCount > 0}<span class="badge count">{s.pendingCount}</span>{/if}
  {#if s.findOpen}<span class="badge find">FIND</span>{/if}
  {#if s.visual}<span class="badge vis">VISUAL</span>{/if}
  {#if s.helpOpen}<span class="badge help">HELP</span>{/if}
  <span class="msg">{s.statusText}</span>
  <span class="spacer"></span>

  {#if s.open && si}
    <span class="chips">
      {#if si.model}
        <button
          class="chip model"
          class:warn={!!si.drift}
          onclick={() => void actions.openModelPicker()}
          title={(si.drift ? "⚠ " + si.drift + "\n" : "") + `${si.model}${si.provider ? " · " + si.provider : ""}\nM switches model`}
        >
          {#if si.drift}<span class="chip-warn">⚠</span>{/if}{shortModel(si.model)}
        </button>
      {/if}
      {#if si.reasoning && si.reasoning !== "none"}
        <span class="chip" title="reasoning effort (S to change)">{si.reasoning}</span>
      {/if}
      {#if si.fast}<span class="chip ok" title="fast mode (priority tier)">fast</span>{/if}
      {#if si.context_max}
        <span
          class="chip ctx {ctxTone}"
          title={`context ${fmtTokens(si.context_used ?? 0)} / ${fmtTokens(si.context_max)}${si.context_estimated ? " (estimated)" : ""}`}
        >
          <span class="ctxbar"><span class="ctxfill" style={`width:${Math.min(100, Math.max(2, ctxPct))}%`}></span></span>
          {Math.round(ctxPct)}%
        </span>
      {/if}
      {#if si.tps}
        <span class="chip tps" class:live={busy} title={`rolling output throughput (last calls)${si.latency_s ? ` · ${si.latency_s.toFixed(1)}s avg latency` : ""}`}>
          {Math.round(si.tps)} tok/s
        </span>
      {/if}
    </span>
  {/if}

  <span class="dots">
    {#each dots as d (d.name)}
      <span class="dot {d.tone}" title={d.title}>{d.name}</span>
    {/each}
  </span>
  <span class="ver" onclick={verTap} role="presentation">atlas {__ATLAS_VERSION__}</span>
</div>
{#if vpDebug}
  <div class="vpdbg">{vpText}</div>
{/if}
