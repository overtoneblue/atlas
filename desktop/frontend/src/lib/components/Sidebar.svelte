<script lang="ts">
  import { s, actions } from "../state.svelte";
  import { ageTag } from "../format";
  import type { Row } from "../types";

  function glyph(r: Row): string {
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
        }}
      >
        <span class="glyph">{glyph(r)}</span>
        <span class="label">{r.node.name}</span>
        <span class="tail" class:busy={r.node.kind === "post" && actions.isBusy(r.node)}>{tail(r)}</span>
      </div>
    {/each}
  </div>
  <div class="side-foot">
    {Math.max(0, s.postCount - s.hiddenCount)}/{s.postCount} posts{#if s.hiddenCount > 0}
      · {s.hiddenCount} stowed{/if}{#if actions.unreadCount() > 0}
      · <em>{actions.unreadCount()} unread</em>{/if}
  </div>
</aside>
