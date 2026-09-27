<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "../state.svelte";
  import * as api from "../api";

  let scroller = $state<HTMLDivElement | null>(null);
  let text = $state("");
  let stick = true;

  const item = $derived(s.logView?.item ?? null);

  async function pull() {
    const it = item;
    if (!it) return;
    try {
      text = await api.GetSpawnLog(it.kind, it.id, 0, 600);
    } catch {
      /* keep the last good tail */
    }
  }

  onMount(() => {
    void pull();
    const timer = setInterval(() => void pull(), 2000);
    actions.registerChatScroller(scroller);
    return () => {
      clearInterval(timer);
      actions.registerChatScroller(null);
    };
  });

  $effect(() => {
    void text;
    if (scroller && stick) queueMicrotask(() => scroller?.scrollTo({ top: scroller.scrollHeight }));
  });

  function onScroll() {
    if (!scroller) return;
    stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 60;
  }

  function glyph(state?: string): string {
    switch (state) {
      case "running":
        return "◍";
      case "done":
        return "✓";
      case "failed":
        return "✗";
      default:
        return "·";
    }
  }
</script>

{#if item}
  <div
    class="pane chat logview"
    class:focused={s.focus === "chat"}
    onclick={() => actions.setFocus("chat")}
  >
    <div class="chat-head">
      <span class="title">{glyph(item.state)} {item.kind} · {item.title}</span>
      <span class="dim">{item.state} · {item.id}</span>
    </div>
    <div class="scroller logtext" bind:this={scroller} onscroll={onScroll}>
      <pre>{text || "…"}</pre>
    </div>
    <div class="log-foot dim">esc closes · live tail (2s)</div>
  </div>
{/if}
