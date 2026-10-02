<script lang="ts">
  import { s, actions, COMMANDS } from "../state.svelte";

  const SECTIONS: { title: string; rows: [string, string][] }[] = [
    {
      title: "focus",
      rows: [
        ["tab / shift+tab", "cycle tree · chat · composer"],
        ["esc", "back to tree"],
        ["click", "focus a pane"],
      ],
    },
    {
      title: "tree",
      rows: [
        ["j / k   ·   3j", "move (counts work: 3j = three rows)"],
        ["g / G", "top / bottom"],
        ["enter", "open post · fold a section · open a spawned run's log"],
        ["c", "new chat · on a channel row: in that channel"],
        ["+ / ✎ (hover a row)", "new / edit channel or category"],
        ["→ (hover a chat)", "move it into a channel"],
        ["H", "show / hide hidden chats"],
        ["⊘ / ↺ (hover a row)", "hide a chat / restore it"],
        ["h / l", "collapse / expand"],
        [". / ,", "toggle stale chats (idle 7d+)"],
        ["◍ ✓ ✗ spawn rows", "subagent runs + pi tasks nested under their chat"],
        ["/", "search the open transcript"],
      ],
    },
    {
      title: "chat",
      rows: [
        ["j / k   ·   3j", "scroll lines"],
        ["ctrl+d / ctrl+u", "half-page down / up"],
        ["pgdn / pgup", "page down / up"],
        ["g / G", "top / bottom"],
        ["/  then  n / N", "find · next / previous match"],
        ["v / V", "visual from the find cursor · V = whole conversation"],
        ["/ n N in visual", "search mid-selection — the match extends it"],
        ["y", "yank selection to the real clipboard"],
        ["h / r", "cycle display: full · reasoning hidden · exact tools · quiet"],
        ["click an image", "zoom preview (esc or click closes)"],
      ],
    },
    {
      title: "composer",
      rows: [
        ["i  or  enter", "insert mode"],
        ["enter", "send"],
        ["shift+enter", "newline"],
        ["ctrl+v", "paste an image (up to 4, 4 MB each)"],
        ["/ at the start", "command palette — hermes commands + skills · ↑↓ · enter runs"],
        ["esc", "normal mode · back to tree"],
      ],
    },
    {
      title: "general",
      rows: [
        ["?", "this help"],
        ["S", "settings — model · reasoning · fast · display · connection"],
        ["M", "model picker for the open chat"],
        ["R", "retry a failed turn (only when one is shown)"],
        ["ctrl+c", "stop the running turn (while streaming)"],
      ],
    },
    {
      title: "settings sheet",
      rows: [
        ["j / k", "move"],
        ["h / l", "step a value (reasoning, display)"],
        ["enter", "act / toggle"],
        ["esc · q · S", "close"],
      ],
    },
    {
      title: "commands",
      rows: COMMANDS.map((c) => [c.name, c.desc] as [string, string]),
    },
  ];
</script>

{#if s.helpOpen}
  <div class="help-scrim" onclick={() => actions.closeHelp()} role="presentation">
    <div class="help" onclick={(e) => e.stopPropagation()} role="presentation">
      <div class="hhead">atlas — keys <span class="dim">? or esc to close</span></div>
      {#each SECTIONS as sec}
        <div class="hsec">
          <div class="htitle">{sec.title}</div>
          {#each sec.rows as [k, d]}
            <div class="hrow"><span class="hk">{k}</span><span class="hd">{d}</span></div>
          {/each}
        </div>
      {/each}
    </div>
  </div>
{/if}
