<script lang="ts">
  import { onMount } from "svelte";
  import { s, actions } from "../state.svelte";
  import { mdLite, timeHM, toolGlyph, firstLine } from "../format";
  import { findRuntime } from "../find";

  let scroller = $state<HTMLDivElement | null>(null);
  let findInput = $state<HTMLInputElement | null>(null);
  let stick = true;
  // Live DOM ranges for the current query. Component-local + non-reactive
  // by design: ranges are DOM objects, and only the count/current index
  // need to live in app state.
  let ranges: Range[] = [];

  // Phones: touch-only hint text (no vim keybinds).
  const coarse = window.matchMedia("(pointer: coarse)").matches;

  onMount(() => {
    return () => clearPaint();
  });

  // Registration must follow bind:this asynchronously — onMount can run
  // before the $state ref is populated, which silently left chatScroller
  // null (j/k + ctrl+d/u chat scrolling went nowhere). The effect
  // re-registers when the ref lands and unregisters on destroy.
  $effect(() => {
    actions.registerChatScroller(scroller);
    return () => actions.registerChatScroller(null);
  });

  $effect(() => {
    void s.autoScroll;
    void s.messages.length;
    if (!scroller) return;
    if (stick) queueMicrotask(() => scroller?.scrollTo({ top: scroller.scrollHeight }));
  });

  // ---- find: DOM range engine (query/cursor live in state) -------------

  $effect(() => {
    findRuntime.recompute = recompute;
    findRuntime.goto = goto;
    findRuntime.msgAt = msgAt;
    return () => {
      findRuntime.recompute = null;
      findRuntime.goto = null;
      findRuntime.msgAt = null;
    };
  });

  $effect(() => {
    // recompute on query change or transcript swap
    void s.findQuery;
    void s.messages;
    void s.open?.id;
    recompute();
  });

  $effect(() => {
    // repaint current-match when navigation moves
    void s.findCur;
    paintCur();
  });

  $effect(() => {
    if (s.findOpen && findInput) findInput.focus();
  });

  function collectTexts(root: Element): Text[] {
    const out: Text[] = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    let node: Node | null;
    while ((node = walker.nextNode())) out.push(node as Text);
    return out;
  }

  function recompute() {
    ranges = [];
    const q = s.findQuery.trim().toLowerCase();
    if (scroller && q) {
      for (const el of scroller.querySelectorAll<HTMLElement>("[data-mi]")) {
        for (const node of collectTexts(el)) {
          const text = node.data.toLowerCase();
          for (let i = text.indexOf(q); i !== -1; i = text.indexOf(q, i + q.length)) {
            const r = new Range();
            r.setStart(node, i);
            r.setEnd(node, i + q.length);
            ranges.push(r);
          }
        }
      }
    }
    s.findCount = ranges.length;
    if (s.findCur >= ranges.length) s.findCur = 0;
    paintAll();
  }

  function hlAPI(): { HL: unknown; css: { highlights?: { set(k: string, h: unknown): void; delete(k: string): void } } } | null {
    const HL = (window as unknown as { Highlight?: unknown }).Highlight;
    const css = CSS as unknown as { highlights?: { set(k: string, h: unknown): void; delete(k: string): void } };
    if (!HL || !css?.highlights) return null;
    return { HL, css };
  }

  function paintAll() {
    const api = hlAPI();
    if (!api) return; // older engines: counter still works
    api.css.highlights!.set("atlas-find", new (api.HL as new (...r: Range[]) => unknown)(...ranges));
    paintCur();
  }

  function paintCur() {
    const api = hlAPI();
    if (!api) return;
    const cur = ranges[s.findCur];
    if (cur) api.css.highlights!.set("atlas-find-cur", new (api.HL as new (...r: Range[]) => unknown)(cur));
    else api.css.highlights!.delete("atlas-find-cur");
  }

  function clearPaint() {
    const api = hlAPI();
    if (!api) return;
    api.css.highlights!.delete("atlas-find");
    api.css.highlights!.delete("atlas-find-cur");
  }

  function goto(i: number) {
    const r = ranges[i];
    if (!r || !scroller) return;
    const rr = r.getBoundingClientRect();
    const cr = scroller.getBoundingClientRect();
    scroller.scrollTop += rr.top - cr.top - cr.height / 3;
  }

  // Which message row a match lives in — the state layer uses this to
  // anchor `v` at the find cursor and to extend a selection with n/N.
  function msgAt(i: number): number {
    const r = ranges[i];
    if (!r) return -1;
    let el: Node | null = r.startContainer;
    while (el) {
      if (el instanceof HTMLElement && el.dataset.mi !== undefined) {
        return Number(el.dataset.mi);
      }
      el = el.parentNode;
    }
    return -1;
  }

  // ---- visuals ----------------------------------------------------------

  function onScroll() {
    if (!scroller) return;
    stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80;
    // near the top: page in older history (tail-first reads live in state)
    if (scroller.scrollTop < 240) void actions.loadOlder();
  }

  const authorName = () => {
    const p = s.open?.profile ?? "default";
    return p === "default" ? "Nolan" : p.charAt(0).toUpperCase() + p.slice(1);
  };
  const authorColor = () =>
    (s.open?.profile ?? "default") === "debbie" ? "var(--purple)" : "var(--yellow)";

  function inVisual(i: number): boolean {
    if (!s.visual) return false;
    const a = Math.min(s.visualAnchor, s.visualCur);
    const b = Math.max(s.visualAnchor, s.visualCur);
    return i >= a && i <= b;
  }

  // Pane-level click: transcript images open the zoom preview; anywhere
  // else just takes focus.
  function onPaneClick(e: MouseEvent) {
    const el = e.target instanceof Element ? e.target.closest(".mdimg") : null;
    if (el instanceof HTMLImageElement) {
      actions.openLightbox(el.src, el.alt, el);
      return;
    }
    actions.setFocus("chat");
  }
</script>

<div
  class="pane chat"
  class:focused={s.focus === "chat"}
  onclick={onPaneClick}
>
  {#if s.open}
    <div class="chat-head">
      <span class="title">{s.open.title}</span>
      <span class="dim">{s.open.id}</span>
    </div>
    {#if s.findOpen}
      <div class="findbar">
        <span class="fmark">/</span>
        <input
          bind:this={findInput}
          value={s.findQuery}
          oninput={(e) => actions.findSetQuery((e.target as HTMLInputElement).value)}
          placeholder="search transcript — enter jumps · esc closes · n/N after"
          spellcheck="false"
          autocomplete="off"
        />
        <span class="fcount">
          {s.findQuery
            ? s.findCount
              ? `${s.findCur + 1} / ${s.findCount}`
              : "0 matches"
            : ""}
        </span>
      </div>
    {/if}
    <div class="scroller" bind:this={scroller} onscroll={onScroll}>
      {#if s.loadingOpen && s.messages.length === 0}
        <div class="pad dim">loading…</div>
      {/if}
      {#if s.loadingOlder}
        <div class="pad dim">loading older…</div>
      {/if}
      {#each s.messages as m, i (m.id)}
        {#if m.tool_name}
          <div class="tool" data-mi={i} class:vs={inVisual(i)}>
            <span class="tname">{toolGlyph(m.tool_name)} {m.tool_name}</span>
            <span class="snip">{firstLine(m.content)}</span>
          </div>
        {:else if m.role === "user"}
          <div
            class="msg user"
            data-mi={i}
            class:vs={inVisual(i)}
            class:vcur={s.visual && i === s.visualCur}
          >
            <span class="bar"></span>
            <div class="col">
              {#if m.images?.length}
                <div class="imgs">
                  {#each m.images as u}
                    <img class="mdimg" src={u} alt="" />
                  {/each}
                </div>
              {/if}
              {#if m.content}<div class="body">{@html mdLite(m.content)}</div>{/if}
            </div>
          </div>
        {:else}
          <div
            class="msg agent"
            data-mi={i}
            class:vs={inVisual(i)}
            class:vcur={s.visual && i === s.visualCur}
          >
            <div class="head">
              <span class="who" style={`color:${authorColor()}`}>{authorName()}</span>
              <span class="time">{timeHM(m.timestamp)}</span>
            </div>
            {#if s.showReasoning && m.reasoning}
              <div class="reason">{@html mdLite(m.reasoning)}</div>
            {/if}
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
      <div class="dim">{coarse ? "tap ☰ workstreams to begin" : "select a workstream · j/k move · enter open · ? help"}</div>
    </div>
  {/if}
</div>
