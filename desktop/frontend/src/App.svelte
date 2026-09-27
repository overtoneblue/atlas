<script lang="ts">
  import { onMount } from "svelte";
  import { actions } from "./lib/state.svelte";
  import { installKeymap } from "./lib/keymap";
  import { installTurnEvents } from "./lib/events";
  import Sidebar from "./lib/components/Sidebar.svelte";
  import Transcript from "./lib/components/Transcript.svelte";
  import Composer from "./lib/components/Composer.svelte";
  import StatusBar from "./lib/components/StatusBar.svelte";
  import Help from "./lib/components/Help.svelte";

  onMount(() => {
    const uninstall = installKeymap();
    const uninstallTurns = installTurnEvents((ev) => actions.handleTurnEvent(ev));
    void actions.boot();
    const timer = setInterval(() => void actions.refreshTree(), 10000);
    return () => {
      uninstall();
      uninstallTurns();
      clearInterval(timer);
    };
  });
</script>

<div class="app">
  <Sidebar />
  <main class="main">
    <Transcript />
    <Composer />
  </main>
  <StatusBar />
  <Help />
</div>
