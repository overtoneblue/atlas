<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "../state.svelte";

  let filter = $state("");
  let filterEl: HTMLInputElement | null = $state(null);

  const m = $derived(s.modal);
  const store = $derived(actions.storeFor(m?.profile ?? "default"));
  const q = $derived(filter.trim().toLowerCase());

  onMount(() => filterEl?.focus());

  type PickRow = { id: string; name: string; cat: string };
  const rows = $derived.by((): PickRow[] => {
    const st = store;
    if (!st) return [];
    const catName = (cid: string | null) => st.categories.find((c) => c.id === cid)?.name ?? "";
    return st.channels
      .map((c) => ({ id: c.id, name: c.name, cat: catName(c.category_id) }))
      .filter((c) => !q || c.name.toLowerCase().includes(q) || c.cat.toLowerCase().includes(q));
  });

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      actions.closeModal();
    }
  }
</script>

<div class="scrim" onclick={() => actions.closeModal()} role="presentation">
  <div class="nf-panel" onclick={(e) => e.stopPropagation()} role="dialog" aria-label="move to channel">
    <div class="nf-title">move <span class="nf-dim">“{m?.title}”</span> to a channel</div>
    <input class="nf-input" bind:this={filterEl} bind:value={filter} placeholder="filter channels…" spellcheck="false" />
    <div class="mv-list">
      {#if m?.current}
        <div class="mv-item mv-detach" onclick={() => void actions.doMove(null)}>
          <span class="mv-glyph">✕</span><span>remove from its channel</span>
        </div>
      {/if}
      {#each rows as c (c.id)}
        <div class="mv-item" onclick={() => void actions.doMove(c.id)}>
          <span class="mv-glyph">{c.id === m?.current ? "✓" : "·"}</span>
          <span class="mv-cat">{c.cat || "no category"}</span>
          <span class="mv-name">{c.name}</span>
        </div>
      {/each}
      {#if !rows.length}
        <div class="mv-empty">
          {store ? "no channels yet — create one from the tree (+ on a category)" : "loading…"}
        </div>
      {/if}
    </div>
    <div class="nf-buttons">
      <button class="nf-btn" onclick={() => actions.closeModal()}>cancel</button>
    </div>
  </div>
</div>

<svelte:window on:keydown={onKey} />
