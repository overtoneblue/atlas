// Clipboard / download helpers for transcript and lightbox image actions.

export function imageFileName(src: string, alt?: string): string {
  try {
    const u = new URL(src, location.href);
    const p = u.searchParams.get("path");
    const base = (p ?? u.pathname).split("/").pop() ?? "";
    if (base && /\.(png|jpe?g|gif|webp|bmp)$/i.test(base)) return base;
  } catch {
    /* fall through to the alt-derived name */
  }
  const tidy = (alt ?? "").trim().replace(/[^\w.-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);
  return (tidy || "image") + ".png";
}

export async function copyImageToClipboard(src: string): Promise<void> {
  const res = await fetch(src);
  if (!res.ok) throw new Error(`fetch ${res.status}`);
  const blob = await res.blob();
  await navigator.clipboard.write([new ClipboardItem({ [blob.type || "image/png"]: blob })]);
}

export function downloadImage(src: string, name: string): void {
  const a = document.createElement("a");
  a.href = src;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
}
