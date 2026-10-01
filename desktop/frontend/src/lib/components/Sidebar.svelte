<script lang="ts">
  import { s, actions } from "../state.svelte";
  import { ageTag } from "../format";
  import type { Row } from "../types";

  // Fold carets live in the leading glyph slot for every foldable row
  // (category / channel / post-with-spawns); .tail is reserved for
  // age/state, so the same action always sits in the same column.
  function caret(r: Row): string {
    return s.collapsed.includes(r.key) ? "▸" : "▾";
  }

  function glyph(r: Row): string {
    if (r.node.kind === "spawn") {
      switch (r.node.spawn?.state) {
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
    const foldable = (r.node.children?.length ?? 0) > 0;
    switch (r.node.kind) {
      case "profile":
        return "◆";
      case "guild":
        return "≡";
      case "category":
        return caret(r);
      case "channel":
        return foldable ? caret(r) : "#";
      case "post":
        if (foldable) return caret(r); // spawned-work children
        return actions.isUnread(r.node) ? "●" : "·";
      default:
        return "·";
    }
  }

  function tail(r: Row): string {
    if (r.node.kind === "spawn") {
      const st = r.node.spawn?.state ?? "";
      return st === "running" ? "◍" : st;
    }
    if (r.node.kind === "post") {
      if (r.node.hidden) return "⊘";
      if (actions.isBusy(r.node)) return "◍";
      return ageTag(r.node.last_active);
    }
    return "";
  }

  let el = $state<HTMLElement | null>(null);
  $effect(() => {
    // keep the cursor row in view
    void s.cursor;
    void s.rows;
    el?.querySelector(".sel")?.scrollIntoView({ block: "nearest" });
  });
</script>

<aside
  class="pane sidebar"
  class:focused={s.focus === "tree"}
  class:open={s.navOpen}
  bind:this={el}
  onclick={() => actions.setFocus("tree")}
>
  <div class="pane-title">WORKSTREAMS</div>
  <div class="rows">
    {#if !s.rows.length}
      <div class="empty">
        <div class="big">◇</div>
        <div class="dim">nothing here yet — /category or /channel starts one</div>
      </div>
    {/if}
    {#each s.rows as r, i (r.key)}
      <div
        class="row {r.node.kind}"
        class:sel={i === s.cursor}
        class:unread={r.node.kind === "post" && actions.isUnread(r.node)}
        class:archived={r.node.kind === "post" && !!r.node.hidden}
        class:spawn-running={r.node.kind === "spawn" && r.node.spawn?.state === "running"}
        class:spawn-done={r.node.kind === "spawn" && r.node.spawn?.state === "done"}
        class:spawn-failed={r.node.kind === "spawn" && r.node.spawn?.state === "failed"}
        style={`--depth:${r.depth}`}
        onclick={() => {
          actions.setFocus("tree");
          actions.clickRow(i);
          if (
            (r.node.kind === "post" && r.node.session_id) ||
            (r.node.kind === "spawn" && r.node.spawn)
          ) {
            actions.closeNav();
          }
        }}
      >
        <span class="glyph">{glyph(r)}</span>
        <span class="label">{r.node.name}</span>
        <span class="tail" class:busy={r.node.kind === "post" && actions.isBusy(r.node)}>{tail(r)}</span>
        {#if r.node.kind === "profile" && r.node.profile}
          <button
            class="act"
            title="new category"
            onclick={(ev) => {
              ev.stopPropagation();
              actions.openNewCategory(r.node.profile ?? "default");
            }}>+</button
          >
        {/if}
        {#if r.node.native && r.node.kind === "channel"}
          <button
            class="act"
            title="new chat in this channel (c)"
            onclick={(ev) => {
              ev.stopPropagation();
              void actions.newChat(r.node.profile, r.node.id);
            }}>+</button
          >
          <button
            class="act"
            title="edit channel — name, category, guidelines"
            onclick={(ev) => {
              ev.stopPropagation();
              actions.openEditChannel(r.node);
            }}>✎</button
          >
        {/if}
        {#if r.node.native && r.node.kind === "category"}
          <button
            class="act"
            title="new channel in this category"
            onclick={(ev) => {
              ev.stopPropagation();
              actions.openNewChannel(r.node.id ?? "", r.node.profile ?? "default");
            }}>+</button
          >
          <button
            class="act"
            title="edit category"
            onclick={(ev) => {
              ev.stopPropagation();
              actions.openEditCategory(r.node);
            }}>✎</button
          >
        {/if}
        {#if r.node.kind === "post" && r.node.session_id}
          {#if r.node.source !== "discord"}
            <button
              class="act"
              title="move this chat to a channel"
              onclick={(ev) => {
                ev.stopPropagation();
                actions.openMovePicker(r.node);
              }}>→</button
            >
          {/if}
          <button
            class="del"
            title={r.node.hidden ? "restore to the tree" : "hide from atlas (H shows hidden)"}
            onclick={(ev) => {
              ev.stopPropagation();
              void actions.hidePost(r.node.session_id!, r.node.profile ?? "default", !r.node.hidden);
            }}
          >{r.node.hidden ? "↺" : "⊘"}</button>
        {/if}
      </div>
    {/each}
  </div>
  <div class="side-foot">
    {Math.max(0, s.postCount - s.hiddenCount - s.archivedCount)}/{s.postCount} posts{#if s.hiddenCount > 0}
      · {s.hiddenCount} stowed{/if}{#if s.archivedCount > 0}
      · {s.archivedCount} hidden{/if}{#if actions.unreadCount() > 0}
      · <em>{actions.unreadCount()} unread</em>{/if}
  </div>
</aside>

<style>
  .row .del {
    all: unset;
    flex: none;
    padding: 0 5px;
    border-radius: 4px;
    font-size: 0.78em;
    line-height: 1.5;
    color: inherit;
    opacity: 0;
    cursor: pointer;
  }
  .row:hover .del {
    opacity: 0.55;
  }
  .row .del:hover {
    opacity: 1;
    color: var(--fg-hi);
  }
  .row .act {
    all: unset;
    flex: none;
    margin-left: 6px;
    padding: 0 5px;
    border-radius: 4px;
    font-size: 0.78em;
    line-height: 1.5;
    color: inherit;
    opacity: 0;
    cursor: pointer;
  }
  .row:hover .act {
    opacity: 0.55;
  }
  .row .act:hover {
    opacity: 1;
    color: var(--fg-hi);
  }
  .row .act:focus-visible,
  .row .del:focus-visible {
    opacity: 1;
    outline: 1px solid var(--yellow);
    outline-offset: 0;
  }
  .row.spawn-running .glyph {
    color: var(--green);
  }
  .row.spawn-done .glyph {
    color: var(--dim2);
  }
  .row.spawn-failed .glyph {
    color: var(--red);
  }
  /* touch devices have no hover: keep the row actions visible */
  @media (pointer: coarse) {
    .row .act,
    .row .del {
      opacity: 0.5;
    }
  }
  .row.archived .del {
    opacity: 0.8;
  }
  .row.archived .label {
    opacity: 0.45;
  }
  .row.archived .glyph {
    color: var(--faint);
  }
</style>
