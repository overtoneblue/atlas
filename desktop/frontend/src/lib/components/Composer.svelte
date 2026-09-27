<script lang="ts">
  import { s, actions } from "../state.svelte";

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
</script>

<div
  class="composer"
  class:active={s.focus === "composer"}
  class:insert={s.focus === "composer" && s.mode === "INSERT"}
  onclick={() => actions.setFocus("composer")}
>
  <span class="cbadge">{s.focus === "composer" && s.mode === "INSERT" ? "INSERT" : "NORMAL"}</span>
  <textarea
    bind:this={ta}
    bind:value={s.draft}
    rows="1"
    spellcheck="false"
    placeholder={s.open
      ? "message…   enter sends · shift+enter newline · esc normal"
      : "open a workstream to chat"}
  ></textarea>
  <button onclick={() => void actions.send()} title="send">⏎</button>
</div>
