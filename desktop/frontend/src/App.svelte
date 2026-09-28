<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "./lib/state.svelte";
  import { installKeymap } from "./lib/keymap";
  import { installTurnEvents } from "./lib/events";
  import Sidebar from "./lib/components/Sidebar.svelte";
  import Transcript from "./lib/components/Transcript.svelte";
  import Composer from "./lib/components/Composer.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import Help from "./lib/components/Help.svelte";
  import LogView from "./lib/components/LogView.svelte";
  import Lightbox from "./lib/components/Lightbox.svelte";

  onMount(() => {
    const uninstall = installKeymap();
    const uninstallTurns = installTurnEvents((ev) => actions.handleTurnEvent(ev));
    void actions.boot();
    const treeTimer = setInterval(() => void actions.refreshTree(), 10000);
    const spawnTimer = setInterval(() => void actions.refreshSpawned(), 4000);

    // Phones: track the visual viewport so the composer stays above the
    // on-screen keyboard (iOS shrinks visualViewport, not the layout).
    const mq = window.matchMedia("(max-width: 760px)");
    const vv = window.visualViewport;
    const applyH = () => {
      if (mq.matches && vv) {
        document.documentElement.style.setProperty("--app-h", `${Math.round(vv.height)}px`);
      } else {
        document.documentElement.style.removeProperty("--app-h");
      }
    };
    applyH();
    vv?.addEventListener("resize", applyH);
    vv?.addEventListener("scroll", applyH);
    mq.addEventListener("change", applyH);

    return () => {
      uninstall();
      uninstallTurns();
      clearInterval(treeTimer);
      clearInterval(spawnTimer);
      vv?.removeEventListener("resize", applyH);
      vv?.removeEventListener("scroll", applyH);
      mq.removeEventListener("change", applyH);
      document.documentElement.style.removeProperty("--app-h");
    };
  });
</script>

<div class="app">
  <Sidebar />
  <main class="main">
    {#if s.logView}
      <LogView />
    {:else}
      <Transcript />
    {/if}
    <Composer />
  </main>
  <StatusBar />
  <Help />
  <div
    class="navscrim"
    class:open={s.navOpen}
    onclick={() => actions.closeNav()}
    role="presentation"
  ></div>
  <Lightbox />
</div>
