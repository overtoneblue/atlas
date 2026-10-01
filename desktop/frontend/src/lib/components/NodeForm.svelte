<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "../state.svelte";

  // Local field state, seeded when the form mounts; written back on save.
  let name = $state(s.modal?.name ?? "");
  let template = $state(s.modal?.template ?? "");
  let categoryId = $state(s.modal?.categoryId ?? "");
  let newCategory = $state("");
  let confirmDel = $state(false);
  let nameEl: HTMLInputElement | null = $state(null);

  const m = $derived(s.modal);
  const cats = $derived(actions.storeFor(m?.profile ?? "default")?.categories ?? []);

  onMount(() => nameEl?.focus());

  function save() {
    if (!s.modal) return;
    s.modal.name = name;
    s.modal.template = template;
    s.modal.categoryId = categoryId;
    s.modal.newCategory = newCategory;
    void actions.saveModal();
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      actions.closeModal();
      return;
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      save();
    }
  }

  function nameKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      save();
    }
  }
</script>

<div class="scrim" onclick={() => actions.closeModal()} role="presentation">
  <div class="nf-panel" onclick={(e) => e.stopPropagation()} role="dialog" aria-label="channel form">
    <div class="nf-title">
      {m?.mode === "new" ? "new" : "edit"} {m?.kind}{#if m?.kind === "channel" && m?.mode === "edit" && m?.name}<span class="nf-dim"> · {m.name}</span>{/if}
    </div>
    <div class="nf-row">
      <span class="nf-lab">name</span>
      <input
        class="nf-input"
        bind:this={nameEl}
        bind:value={name}
        onkeydown={nameKey}
        placeholder={m?.kind === "channel" ? "channel name" : "category name"}
        spellcheck="false"
      />
    </div>
    {#if m?.kind === "channel"}
      <div class="nf-row">
        <span class="nf-lab">category</span>
        <select class="nf-select" bind:value={categoryId}>
          <option value="">— none —</option>
          {#each cats as c (c.id)}
            <option value={c.id}>{c.name}</option>
          {/each}
          <option value="__new__">+ new category…</option>
        </select>
        {#if categoryId === "__new__"}
          <input class="nf-input" bind:value={newCategory} placeholder="new category name" spellcheck="false" />
        {/if}
      </div>
      <div class="nf-row">
        <span class="nf-lab">guidelines · context</span>
        <textarea
          class="nf-ta"
          bind:value={template}
          placeholder="Read by the agent at the start of every new chat in this channel — the Discord forum-guidelines equivalent. Never shown in the transcript."
        ></textarea>
      </div>
      <div class="nf-hint">
        New chats inherit this the moment they are created; edits apply from then on. Existing chats keep theirs.
      </div>
    {/if}
    <div class="nf-buttons">
      {#if m?.mode === "edit"}
        <button
          class="nf-btn danger nf-left"
          onclick={() => (confirmDel ? void actions.deleteModal() : (confirmDel = true))}
          onmouseleave={() => (confirmDel = false)}
        >{confirmDel ? "sure?" : "delete"}</button>
      {/if}
      <button class="nf-btn" onclick={() => actions.closeModal()}>cancel</button>
      <button class="nf-btn primary" onclick={save} disabled={!name.trim()}>save</button>
    </div>
  </div>
</div>

<svelte:window on:keydown={onKey} />
