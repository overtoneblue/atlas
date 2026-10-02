<script lang="ts">
  import { s, actions, settingsRows } from "../state.svelte";

  // The sheet is keyboard-first (j/k · h/l · enter · esc — keymap.ts owns
  // the keys while it's open); clicks work too, for the phone.
  const rows = $derived(settingsRows());

  let panel = $state<HTMLElement | null>(null);

  // keep the cursor row in view on long sheets (phones)
  $effect(() => {
    void s.settingsIdx;
    panel?.querySelector<HTMLElement>(".st-row.cur")?.scrollIntoView({ block: "nearest" });
  });

  function click(i: number, step: number) {
    s.settingsIdx = i;
    void actions.settingsAct(step);
  }
</script>

{#if s.settingsOpen}
  <div class="scrim st-scrim" onclick={() => actions.closeSettings()} role="presentation">
    <div class="st-panel" bind:this={panel} onclick={(e) => e.stopPropagation()} role="presentation">
      <div class="st-head">
        <span class="st-title">settings</span>
        {#if s.open}<span class="st-sub" title={s.open.id}>{s.open.title}</span>{/if}
        <span class="st-keys">j/k move · h/l change · enter act · esc close</span>
      </div>
      {#if s.settingsErr}<div class="st-err">{s.settingsErr}</div>{/if}
      {#each rows as r, i (r.id)}
        {#if i === 0 || rows[i - 1].section !== r.section}
          <div class="st-sec">{r.section}</div>
        {/if}
        <div
          class="st-row"
          class:cur={i === s.settingsIdx}
          class:ro={r.disabled}
          class:busy={s.settingsBusy !== "" && s.settingsBusy === r.id}
          onclick={() => click(i, 0)}
          role="presentation"
        >
          <span class="st-label">{r.label}</span>
          <span class="st-val {r.tone ?? ''}">
            {#if !r.disabled && (r.id === "reasoning" || r.id === "display")}
              <button class="st-step" onclick={(e) => (e.stopPropagation(), click(i, -1))} aria-label="previous">‹</button>
            {/if}
            {r.value}
            {#if !r.disabled && (r.id === "reasoning" || r.id === "display")}
              <button class="st-step" onclick={(e) => (e.stopPropagation(), click(i, 1))} aria-label="next">›</button>
            {/if}
          </span>
          {#if r.hint}<span class="st-hint">{r.hint}</span>{/if}
        </div>
      {/each}
    </div>
  </div>
{/if}
