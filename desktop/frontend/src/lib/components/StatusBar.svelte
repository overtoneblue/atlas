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
  <span class="dots">
    <span class:ok={s.status?.hub === true} class:bad={s.status !== null && s.status.hub === false}>hub</span>
    <span class:ok={s.status?.api === true} class:bad={s.status !== null && s.status.api === false}>api</span>
    <span class:ok={s.status?.serve === true} class:bad={s.status !== null && s.status.serve === false}>serve</span>
  </span>
  <span class="ver" onclick={verTap}>atlas {__ATLAS_VERSION__}</span>
</div>
{#if vpDebug}
  <div class="vpdbg">{vpText}</div>
{/if}
