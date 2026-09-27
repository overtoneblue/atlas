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

  onMount(() => {
    const uninstall = installKeymap();
    const uninstallTurns = installTurnEvents((ev) => actions.handleTurnEvent(ev));
    void actions.boot();
    const treeTimer = setInterval(() => void actions.refreshTree(), 10000);
    const spawnTimer = setInterval(() => void actions.refreshSpawned(), 4000);
    return () => {
      uninstall();
      uninstallTurns();
      clearInterval(treeTimer);
      clearInterval(spawnTimer);
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
</div>
