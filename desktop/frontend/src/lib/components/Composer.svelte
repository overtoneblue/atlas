<script lang="ts">
  import {
    s,
    actions,
    modelPickRows,
    modelPickVisible,
    paletteItems,
    paletteSelIndex,
    paletteVisible,
    slashHint,
    slashParse,
  } from "../state.svelte";
  import { preLiftComposer } from "../mobile-viewport";

  // usage line for the command being typed ("/compress [here [N] | …]")
  const hint = $derived(slashHint());

  let ta = $state<HTMLTextAreaElement | null>(null);
  let root = $state<HTMLElement | null>(null);

  // Phones: keep hints free of vim/desktop keybinds (touch only).
  const coarse = window.matchMedia("(pointer: coarse)").matches;

  // Keep the selected row visible while arrowing through the palette / model
  // picker (both lists can outgrow their box). block:"nearest" no-ops when
  // the row is already in view.
  $effect(() => {
    void s.paletteIdx;
    void s.modelPick?.idx;
    void s.draft;
    const sel = root?.querySelector<HTMLElement>(".prow.sel");
    sel?.scrollIntoView({ block: "nearest" });
  });

  $effect(() => {
    const shouldFocus = s.focus === "composer" && s.mode === "INSERT";
    if (!ta) return;
    if (shouldFocus) ta.focus();
    else ta.blur();
    // autosize
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
  });

  const MAX_IMG = 4 * 1024 * 1024; // raw bytes, matches atlasd's cap

  function onPaste(e: ClipboardEvent) {
    const items = e.clipboardData?.items;
    if (!items) return;
    const files: File[] = [];
    for (const it of items) {
      if (it.kind === "file" && it.type.startsWith("image/")) {
        const f = it.getAsFile();
        if (f) files.push(f);
      }
    }
    if (!files.length) return; // plain text paste passes through
    e.preventDefault();
    const room = 4 - s.attachments.length;
    if (room <= 0) {
      s.statusText = "max 4 images per message";
      return;
    }
    for (const f of files.slice(0, room)) {
      if (f.size > MAX_IMG) {
        s.statusText = `image too large (${(f.size / 1048576).toFixed(1)} MB, max 4 MB)`;
        continue;
      }
      const rd = new FileReader();
      rd.onload = () => actions.addAttachment(String(rd.result ?? ""));
      rd.onerror = () => (s.statusText = "could not read pasted image");
      rd.readAsDataURL(f);
    }
  }
</script>

<div
  bind:this={root}
  class="composer"
  class:active={s.focus === "composer"}
  class:insert={s.focus === "composer" && s.mode === "INSERT"}
  onclick={() => actions.setFocus("composer")}
  onpaste={onPaste}
  role="presentation"
>
  {#if modelPickVisible() && s.modelPick}
    <div class="palette modelpick">
      {#if s.modelPick.loading}
        <div class="phint">loading models…</div>
      {:else if s.modelPick.error}
        <div class="phint">models failed: {s.modelPick.error}</div>
      {:else}
        {#each modelPickRows(s.draft) as m, i}
          <div
            class="prow"
            class:sel={i === s.modelPick.idx}
            onmousedown={(e) => e.preventDefault()}
            onclick={() => actions.pickModelAt(i)}
            role="presentation"
          >
            <span class="pname">{m.name}{#if m.current}<span class="pcur"> ✓</span>{/if}</span>
            {#if m.recent}<span class="pkind recent">recent</span>{/if}
            <span class="pkind">{m.slug}</span>
            <span class="pdesc">{m.meta}</span>
          </div>
        {/each}
        {#if s.modelConfirm}
          <div class="mconfirm">{s.modelConfirm.message}</div>
          <div class="phint">enter again = switch anyway · esc cancels</div>
        {:else}
          <div class="phint">{modelPickRows(s.draft).length} models · type to filter (words match model or provider) · ↑↓ pick · enter switch · esc close</div>
        {/if}
      {/if}
    </div>
  {/if}
  {#if paletteVisible()}
    {@const items = paletteItems(s.draft)}
    {@const sel = paletteSelIndex()}
    {@const stage = slashParse(s.draft)?.stage}
    <div class="palette" class:argstage={stage === "arg"}>
      {#if hint}<div class="pusage">{hint}</div>{/if}
      {#each items as c, i}
        <div
          class="prow"
          class:sel={i === sel}
          class:cur={c.current}
          onmousedown={(e) => e.preventDefault()}
          onclick={() => void actions.runPaletteAt(i)}
          role="presentation"
        >
          <span class="pname">{c.name}{#if c.current}<span class="pcur"> ✓</span>{/if}</span>
          {#if c.kind === "skill"}<span class="pkind skill">skill</span>{:else if c.kind === "command"}<span class="pkind">cmd</span>{/if}
          <span class="pdesc">{c.desc}</span>
          {#if c.more}<span class="pmore" title="takes arguments — Tab">›</span>{/if}
        </div>
      {/each}
      <div class="phint">
        {stage === "arg" ? "↑↓ pick · tab complete · enter run · esc dismiss" : "↑↓ pick · tab complete (› = has options) · enter run · esc dismiss"}
      </div>
    </div>
  {:else if hint && s.focus === "composer" && s.mode === "INSERT"}
    <div class="palette usage-only"><div class="pusage">{hint}</div></div>
  {/if}
  <div class="cstack">
    {#if s.attachments.length}
      <div class="chips">
        {#each s.attachments as u, i}
          <span class="chip">
            <img src={u} alt="" />
            <button
              class="x"
              title="remove"
              onmousedown={(e) => e.preventDefault()}
              onclick={() => actions.removeAttachment(i)}>✕</button
            >
          </span>
        {/each}
      </div>
    {/if}
    <textarea
      bind:this={ta}
      bind:value={s.draft}
      oninput={() => actions.paletteReset()}
      onfocus={() => actions.setFocus("composer")}
      onmousedown={() => preLiftComposer()}
      rows="1"
      spellcheck="false"
      placeholder={s.open
        ? s.turnBusy[s.open.id]
          ? coarse
            ? "streaming…"
            : "streaming…   ctrl+c stops the turn"
          : coarse
            ? "message…"
            : "message…   enter sends · ctrl+v pastes images · esc normal"
        : coarse
          ? "open a workstream first"
          : "open a workstream to chat"}
    ></textarea>
  </div>
  {#if s.open && (!s.link.stream || s.link.serve === "down" || s.status === null)}
    <span class="cwarn" title="sends fail fast and hand your message back while this lasts">
      {s.status === null ? "atlasd offline" : !s.link.stream ? "reconnecting…" : "hermes-serve down"}
    </span>
  {/if}
  {#if s.open && s.turnBusy[s.open.id]}
    <button
      class="stop"
      onmousedown={(e) => e.preventDefault()}
      onclick={() => void actions.stopTurn()}
      title="stop turn (ctrl+c)">⏹</button
    >
  {:else}
    <button
      onmousedown={(e) => e.preventDefault()}
      onclick={() => void actions.send()}
      title="send (enter)"
      aria-label="send">⏎</button
    >
  {/if}
</div>
