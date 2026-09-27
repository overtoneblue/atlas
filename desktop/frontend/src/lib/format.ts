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

export function mdLite(src: string | undefined): string {
  if (!src) return "";
  const blocks: string[] = [];
  // escape first; placeholders use \u0000 so user text can't spoof them
  let s = esc(src);
  s = s.replace(/```([\w+-]*)\n([\s\S]*?)```/g, (_m, _lang, code: string) => {
    blocks.push(`<pre class="code"><code>${code.replace(/\n$/, "")}</code></pre>`);
    return `\u0000${blocks.length - 1}\u0000`;
  });
  s = s.replace(/`([^`\n]+)`/g, "<code class=\"ic\">$1</code>");
  s = s.replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/(^|\W)\*([^*\n]+)\*(?=\W|$)/g, "$1<em>$2</em>");
  s = s.replace(
    /\[([^\]]+)\]\((https?:[^)\s]+)\)/g,
    '<a href="$2" target="_blank" rel="noreferrer">$1</a>',
  );
  s = s.replace(
    /(^|[\s(])(https?:\/\/[^\s<)]+)/g,
    '$1<a href="$2" target="_blank" rel="noreferrer">$2</a>',
  );
  s = s.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => blocks[+i] ?? "");
  return s;
}

export function firstLine(s: string | undefined, max = 110): string {
  if (!s) return "";
  const line = s.split("\n").find((l) => l.trim() !== "") ?? "";
  return line.length > max ? line.slice(0, max - 1) + "…" : line;
}

const TOOL_GLYPHS: Record<string, string> = {
  terminal: "💻",
  close_terminal: "🖥️",
  read_terminal: "🖥️",
  patch: "🔧",
  write_file: "📝",
  read_file: "📖",
  search_files: "🔍",
  delegate_task: "⤷",
  discord: "⚙️",
  web_search: "🌐",
  web_extract: "🌐",
  skill_view: "📚",
  memory: "🧠",
};

export function toolGlyph(name: string | undefined): string {
  return TOOL_GLYPHS[name ?? ""] ?? "⚙️";
}
