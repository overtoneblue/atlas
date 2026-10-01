// Small formatting helpers: ages, clock times, and a compact markdown-lite
// renderer for chat bodies (fences, inline code, bold/italic, links).
// Full markdown comes with the renderer upgrade; this keeps M1 honest.

export function ageTag(ts: number | undefined): string {
  if (!ts) return "";
  const d = Date.now() / 1000 - ts;
  if (d < 60) return "now";
  if (d < 3600) return `${Math.floor(d / 60)}m`;
  if (d < 86400) return `${Math.floor(d / 3600)}h`;
  if (d < 7 * 86400) return `${Math.floor(d / 86400)}d`;
  if (d < 60 * 86400) return `${Math.floor(d / (7 * 86400))}w`;
  return `${Math.floor(d / (30 * 86400))}mo`;
}

export function timeHM(ts: number | undefined): string {
  if (!ts) return "";
  const dt = new Date(ts * 1000);
  return dt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function esc(x: string): string {
  return x.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escAttr(x: string): string {
  return esc(x).replace(/"/g, "&quot;");
}

// The link passes below run on text esc() already entity-escaped, so a full
// re-escape would double-encode URLs — but a raw " in a URL must still not
// break out of the href attribute.
function escQuotes(x: string): string {
  return x.replace(/"/g, "&quot;");
}

function imgHTML(src: string, alt: string): string {
  return (
    `<span class="mdimgwrap"><img class="mdimg" src="${escAttr(src)}" alt="${escAttr(alt)}" loading="lazy" />` +
    `<span class="mdimgtools">` +
    `<button class="mdbtn" data-imgact="copy" title="copy image" aria-label="copy image">⧉</button>` +
    `<button class="mdbtn" data-imgact="save" title="save image" aria-label="save image">⤓</button>` +
    `</span></span>`
  );
}

export function mdLite(src: string | undefined): string {
  if (!src) return "";
  const blocks: string[] = [];
  const stash = (html: string) => {
    blocks.push(html);
    return `\u0000${blocks.length - 1}\u0000`;
  };
  // escape first; placeholders use \u0000 so user text can't spoof them
  let s = esc(src);
  s = s.replace(/```([\w+-]*)\n([\s\S]*?)```/g, (_m, _lang, code: string) =>
    stash(`<pre class="code"><code>${code.replace(/\n$/, "")}</code></pre>`),
  );
  s = s.replace(/`([^`\n]+)`/g, (_m, code: string) => stash(`<code class="ic">${code}</code>`));
  // images: markdown ![](url) first, then MEDIA:<path> refs served by
  // atlasd's relay. Both go through the stash so later passes can't
  // corrupt their attributes, and they must run before the link passes
  // (which would otherwise eat the [..](..) part and leave stray "!").
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_m, alt: string, url: string) =>
    stash(imgHTML(url, alt)),
  );
  s = s.replace(/\bMEDIA:(?:"([^"]+)"|(\S+))/g, (m, quoted: string | undefined, bare: string | undefined) => {
    const path = (quoted ?? bare ?? "").replace(/[),;:]+$/, "");
    if (path === "" || path[0] !== "/") return m;
    const name = path.split("/").pop() || "image";
    return stash(imgHTML(`/media?path=${encodeURIComponent(path)}`, name));
  });
  // Persisted user-attachment refs (@image:<path> — the desktop's canonical
  // stored form, tui_gateway/session_history.py) render through the same relay.
  s = s.replace(
    /(^|[\s])@image:(`([^`\n]+)`|"([^"\n]+)"|'([^'\n]+)'|(\S+))/g,
    (_m, pre: string, _whole: string, bt?: string, dq?: string, sq?: string, bare?: string) => {
      const path = (bt ?? dq ?? sq ?? bare ?? "").replace(/[),;:]+$/, "");
      if (path === "" || path[0] !== "/") return _m;
      const name = path.split("/").pop() || "image";
      return pre + stash(imgHTML(`/media?path=${encodeURIComponent(path)}`, name));
    },
  );
  s = s.replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/(^|\W)\*([^*\n]+)\*(?=\W|$)/g, "$1<em>$2</em>");
  s = s.replace(
    /\[([^\]]+)\]\((https?:[^)\s]+)\)/g,
    (_m, label: string, url: string) =>
      `<a href="${escQuotes(url)}" target="_blank" rel="noreferrer">${label}</a>`,
  );
  s = s.replace(
    /(^|[\s(])(https?:\/\/[^\s<)]+)/g,
    (_m, pre: string, url: string) =>
      `${pre}<a href="${escQuotes(url)}" target="_blank" rel="noreferrer">${url}</a>`,
  );
  s = s.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => blocks[+i] ?? "");
  return s;
}

export function firstLine(s: string | undefined, max = 110): string {
  if (!s) return "";
  const line = s.split("\n").find((l) => l.trim() !== "") ?? "";
  return line.length > max ? line.slice(0, max - 1) + "…" : line;
}

// Monochrome marks, matching the app's glyph language (◍ ✓ ✗ ⊘ ↺ ◆ #) —
// color emoji broke the warm-dark terminal aesthetic and rendered
// differently per platform.
const TOOL_GLYPHS: Record<string, string> = {
  terminal: "$",
  close_terminal: "$",
  read_terminal: "$",
  patch: "✎",
  write_file: "✚",
  read_file: "▤",
  search_files: "⌕",
  delegate_task: "⤷",
  discord: "⬡",
  web_search: "◎",
  web_extract: "◎",
  skill_view: "✦",
  memory: "◉",
};

export function toolGlyph(name: string | undefined): string {
  return TOOL_GLYPHS[name ?? ""] ?? "·";
}
