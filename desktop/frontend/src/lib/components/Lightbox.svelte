<script lang="ts">
  // Image preview overlay: click-to-zoom from the transcript, centered,
  // with a FLIP animation anchored to the clicked thumbnail (open) and
  // back to it (close). Reduced motion gets a plain crossfade.
  import { tick } from "svelte";
  import { s, actions, lightboxRuntime } from "../state.svelte";
  import { copyImageToClipboard, downloadImage, imageFileName } from "../imgtools";

  let imgEl = $state<HTMLImageElement | null>(null);
  let originEl: HTMLElement | null = null;
  let closing = false;

  const reduceMotion =
    typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;

  $effect(() => {
    const lb = s.lightbox;
    closing = false;
    if (!lb) return;
    originEl = actions.takeLightboxOrigin();
    void tick().then(() => {
      const origin = originEl?.getBoundingClientRect();
      if (!imgEl || !origin || reduceMotion) return;
      const t = imgEl.getBoundingClientRect();
      if (!t.width || !t.height) return;
      const sx = origin.width / t.width;
      const sy = origin.height / t.height;
      const dx = origin.left - t.left;
      const dy = origin.top - t.top;
      imgEl.style.transition = "none";
      imgEl.style.transformOrigin = "top left";
      imgEl.style.transform = `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`;
      requestAnimationFrame(() => {
        if (!imgEl) return;
        imgEl.style.transition = "transform 260ms cubic-bezier(0.2, 0.9, 0.27, 1)";
        imgEl.style.transform = "none";
      });
    });
  });

  // Reverse FLIP back to the thumbnail (which may have moved while the
  // overlay was up — we re-measure it), then unmount. Falls back to an
  // instant close when the origin is gone or motion is reduced.
  function close() {
    if (closing) return;
    const el = originEl;
    if (!imgEl || !el || !el.isConnected || reduceMotion) {
      actions.closeLightbox();
      return;
    }
    const o = el.getBoundingClientRect();
    const t = imgEl.getBoundingClientRect();
    if (!t.width || !t.height || !o.width || !o.height) {
      actions.closeLightbox();
      return;
    }
    closing = true;
    imgEl.style.transition = "transform 220ms cubic-bezier(0.3, 0.9, 0.3, 1)";
    imgEl.style.transform = `translate(${o.left - t.left}px, ${o.top - t.top}px) scale(${o.width / t.width}, ${o.height / t.height})`;
    setTimeout(() => actions.closeLightbox(), 240);
  }

  async function act(a: "copy" | "save") {
    const lb = s.lightbox;
    if (!lb) return;
    if (a === "save") {
      downloadImage(lb.src, imageFileName(lb.src, lb.alt));
      s.statusText = `saved ${imageFileName(lb.src, lb.alt)}`;
      return;
    }
    try {
      await copyImageToClipboard(lb.src);
      s.statusText = "image copied to clipboard";
    } catch {
      s.statusText = "copy failed — use save instead";
    }
  }

  $effect(() => {
    lightboxRuntime.close = s.lightbox ? close : null;
    return () => {
      lightboxRuntime.close = null;
    };
  });
</script>

{#if s.lightbox}
  <div class="lb-scrim" onclick={() => actions.dismissLightbox()} role="presentation">
    <img bind:this={imgEl} class="lb-img" src={s.lightbox.src} alt={s.lightbox.alt} />
    <div class="lb-tools">
      <button
        class="mdbtn"
        title="copy image"
        onclick={(e) => {
          e.stopPropagation();
          void act("copy");
        }}>⧉ copy</button
      >
      <button
        class="mdbtn"
        title="save image"
        onclick={(e) => {
          e.stopPropagation();
          void act("save");
        }}>⤓ save</button
      >
    </div>
  </div>
{/if}
