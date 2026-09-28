<script lang="ts">
  import { s, actions, paletteMatches, paletteVisible } from "../state.svelte";

  let ta = $state<HTMLTextAreaElement | null>(null);

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
  class="composer"
  class:active={s.focus === "composer"}
  class:insert={s.focus === "composer" && s.mode === "INSERT"}
  onclick={() => actions.setFocus("composer")}
  onpaste={onPaste}
  role="presentation"
>
  {#if paletteVisible()}
    <div class="palette">
      {#each paletteMatches(s.draft) as c, i}
        <div
          class="prow"
          class:sel={i === s.paletteIdx}
          onmousedown={(e) => e.preventDefault()}
          onclick={() => actions.runPaletteAt(i)}
          role="presentation"
        >
          <span class="pname">{c.name}</span>
          <span class="pdesc">{c.desc}</span>
        </div>
      {/each}
      <div class="phint">↑↓ pick · enter run · esc dismiss</div>
    </div>
  {/if}
  <span class="cbadge">{s.focus === "composer" && s.mode === "INSERT" ? "INSERT" : "NORMAL"}</span>
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
      rows="1"
      spellcheck="false"
      placeholder={s.open
        ? s.turnBusy[s.open.id]
          ? "streaming…   ctrl+c stops the turn"
          : "message…   enter sends · ctrl+v pastes images · esc normal"
        : "open a workstream to chat"}
    ></textarea>
  </div>
  {#if s.open && s.turnBusy[s.open.id]}
    <button class="stop" onclick={() => void actions.stopTurn()} title="stop turn (ctrl+c)">⏹</button>
  {:else}
    <button onclick={() => void actions.send()} title="send (enter)">⏎</button>
  {/if}
</div>
