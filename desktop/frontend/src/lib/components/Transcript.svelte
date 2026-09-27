<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "../state.svelte";
  import { mdLite, timeHM, toolGlyph, firstLine } from "../format";

  let scroller = $state<HTMLDivElement | null>(null);
  let stick = true;

  onMount(() => {
    actions.registerChatScroller(scroller);
    return () => actions.registerChatScroller(null);
  });

  $effect(() => {
    void s.autoScroll;
    void s.messages.length;
    if (!scroller) return;
    if (stick) queueMicrotask(() => scroller?.scrollTo({ top: scroller.scrollHeight }));
  });

  function onScroll() {
    if (!scroller) return;
    stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80;
  }

  const authorName = () => {
    const p = s.open?.profile ?? "default";
    return p === "default" ? "Nolan" : p.charAt(0).toUpperCase() + p.slice(1);
  };
  const authorColor = () =>
    (s.open?.profile ?? "default") === "debbie" ? "var(--purple)" : "var(--yellow)";
</script>

<div
  class="pane chat"
  class:focused={s.focus === "chat"}
  onclick={() => actions.setFocus("chat")}
>
  {#if s.open}
    <div class="chat-head">
      <span class="title">{s.open.title}</span>
      <span class="dim">{s.open.id}</span>
    </div>
    <div class="scroller" bind:this={scroller} onscroll={onScroll}>
      {#if s.loadingOpen && s.messages.length === 0}
        <div class="pad dim">loading…</div>
      {/if}
      {#each s.messages as m (m.id)}
        {#if m.tool_name}
          <div class="tool">
            <span class="tname">{toolGlyph(m.tool_name)} {m.tool_name}</span>
            <span class="snip">{firstLine(m.content)}</span>
          </div>
        {:else if m.role === "user"}
          <div class="msg user"><span class="bar"></span><span class="body">{m.content}</span></div>
        {:else}
          <div class="msg agent">
            <div class="head">
              <span class="who" style={`color:${authorColor()}`}>{authorName()}</span>
              <span class="time">{timeHM(m.timestamp)}</span>
            </div>
            <div class="body">{@html mdLite(m.content)}</div>
          </div>
        {/if}
      {/each}
      {#if s.live && s.live.session === s.open.id}
        <div class="msg agent live">
          <div class="head">
            <span class="who" style={`color:${authorColor()}`}>{authorName()}</span>
            <span class="time live-tag">● live</span>
          </div>
          {#each s.live.segments as seg}
            {#if seg.type === "tool"}
              <div class="tool">
                <span class="tname">{toolGlyph(seg.name)} {seg.name}</span>
                <span class="snip">{seg.state === "done" ? "✓" : "…"}</span>
              </div>
            {:else}
              <div class="body">{@html mdLite(seg.text)}</div>
            {/if}
          {/each}
          {#if s.live.error}<div class="body live-err">{s.live.error}</div>{/if}
          {#if s.live.segments.length === 0}<div class="body dim">…thinking</div>{/if}
        </div>
      {/if}
      {#if s.messages.length === 0 && !s.loadingOpen && !s.live}
        <div class="pad dim">empty conversation</div>
      {/if}
    </div>
  {:else}
    <div class="empty">
      <div class="big">◆ atlas</div>
      <div class="dim">select a workstream · j/k move · enter open</div>
    </div>
  {/if}
</div>
