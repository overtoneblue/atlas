<script lang="ts">
  import { s, actions } from "../state.svelte";
  import { ageTag } from "../format";
  import type { Row } from "../types";

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
    switch (r.node.kind) {
      case "profile":
        return "◆";
      case "guild":
        return "≡";
      case "category":
        return s.collapsed.includes(r.key) ? "▸" : "▾";
      case "channel":
        return "#";
      default:
        return actions.isUnread(r.node) ? "●" : "·";
    }
  }

  function tail(r: Row): string {
    if (r.node.kind === "spawn") {
      const st = r.node.spawn?.state ?? "";
      return st === "running" ? "◍" : st;
    }
    if (r.node.kind === "post") {
      if (actions.isBusy(r.node)) return "◍";
      return ageTag(r.node.last_active);
    }
    if (r.node.kind !== "category" && (r.node.children?.length ?? 0) > 0) {
      return s.collapsed.includes(r.key) ? "▸" : "▾";
    }
    return "";
  }

  let el = $state<HTMLElement | null>(null);
  let confirmDel = $state<string | null>(null);
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
    {#each s.rows as r, i (r.key)}
      <div
        class="row {r.node.kind}"
        class:sel={i === s.cursor}
        class:unread={r.node.kind === "post" && actions.isUnread(r.node)}
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
        {#if r.node.kind === "post" && r.node.session_id}
          <button
            class="del"
            class:armed={confirmDel === r.node.session_id}
            title={confirmDel === r.node.session_id
              ? "click again to delete this chat"
              : "delete chat"}
            onclick={(ev) => {
              ev.stopPropagation();
              if (confirmDel === r.node.session_id) {
                const sid = r.node.session_id!;
                confirmDel = null;
                void actions.deletePost(sid, r.node.profile ?? "default");
              } else {
                confirmDel = r.node.session_id!;
              }
            }}
            onmouseleave={() => (confirmDel = null)}
          >{confirmDel === r.node.session_id ? "sure?" : "✕"}</button>
        {/if}
      </div>
    {/each}
  </div>
  <div class="side-foot">
    {Math.max(0, s.postCount - s.hiddenCount)}/{s.postCount} posts{#if s.hiddenCount > 0}
      · {s.hiddenCount} stowed{/if}{#if actions.unreadCount() > 0}
      · <em>{actions.unreadCount()} unread</em>{/if}
  </div>
</aside>

<style>
  .row .del {
    all: unset;
    flex: none;
    margin-left: 8px;
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
    color: #e5484d;
  }
  .row .del.armed {
    opacity: 1;
    color: #e5484d;
    font-weight: 600;
  }
</style>
