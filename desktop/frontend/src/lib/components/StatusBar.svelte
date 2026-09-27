<script lang="ts">
  import { s } from "../state.svelte";
</script>

<div class="statusbar">
  <span class="badge" class:insert={s.mode === "INSERT"}>{s.mode}</span>
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
  </span>
  <span class="ver">atlas 1.0.0-dev</span>
</div>
