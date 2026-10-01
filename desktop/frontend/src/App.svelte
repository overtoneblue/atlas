<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "./lib/state.svelte";
  import { installKeymap } from "./lib/keymap";
  import { installTurnEvents } from "./lib/events";
  import { installMobileViewport } from "./lib/mobile-viewport";
  import Sidebar from "./lib/components/Sidebar.svelte";
  import Transcript from "./lib/components/Transcript.svelte";
  import Composer from "./lib/components/Composer.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import Help from "./lib/components/Help.svelte";
  import LogView from "./lib/components/LogView.svelte";
  import Lightbox from "./lib/components/Lightbox.svelte";
  import NodeForm from "./lib/components/NodeForm.svelte";
  import MovePicker from "./lib/components/MovePicker.svelte";

  onMount(() => {
    const uninstall = installKeymap();
    const uninstallTurns = installTurnEvents((ev) => actions.handleTurnEvent(ev));
    void actions.boot();
    const treeTimer = setInterval(() => void actions.refreshTree(), 10000);
    const spawnTimer = setInterval(() => void actions.refreshSpawned(), 4000);

    // Phones: keep the app surface glued to the iOS keyboard — pre-lift,
    // follow, snap-back, watchdog (see lib/mobile-viewport.ts).
    const disposeViewport = installMobileViewport();

    return () => {
      uninstall();
      uninstallTurns();
      clearInterval(treeTimer);
      clearInterval(spawnTimer);
      disposeViewport();
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
  {#if s.modal && (s.modal.kind === "channel" || s.modal.kind === "category")}
    <NodeForm />
  {/if}
  {#if s.modal?.kind === "move"}
    <MovePicker />
  {/if}
</div>
