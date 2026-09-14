#!/usr/bin/env python3
"""Preview renderer for Atlas screenshots: ANSI capture -> PNG.

Renders with the operator's real fonts and a warm base16 palette so previews
approximate the live terminal. Fonts are discovered in the nix store (Maple
Mono NF + Noto Emoji + DejaVu fallback); the palette below mirrors the
wezterm/stylix mapping. Override any font via ATLAS_PREVIEW_FONT_* env vars.

Usage: preview.py <in.ansi> <out.png> [fontsize=15]
"""
import glob
import os
import re
import sys

from PIL import Image, ImageDraw, ImageFont


def _find(pattern):
    for d in glob.glob(pattern):
        for p in glob.glob(d + "/**/*.ttf", recursive=True):
            return p
    return None


FONT_REG = os.environ.get("ATLAS_PREVIEW_FONT") or _find("/nix/store/*MapleMono-NF*/share/fonts/truetype") or _find("/nix/store/*dejavu*")
FONT_BOLD = (FONT_REG or "").replace("-Regular.ttf", "-Bold.ttf")
FONT_EMOJI = os.environ.get("ATLAS_PREVIEW_FONT_EMOJI") or _find("/nix/store/*noto-fonts-monochrome-emoji*")
FONT_FALLBACK = _find("/nix/store/*dejavu*")

# Warm base16 mapping (ansi[0..7] + brights[8..15]) matching the house theme.
PAL = [
    (0x00, 0x00, 0x00), (0xb0, 0x6a, 0x63), (0x72, 0x8b, 0x72), (0xaa, 0x95, 0x6d),
    (0x6b, 0x80, 0x78), (0x7d, 0x67, 0x7e), (0x74, 0x88, 0x86), (0xd8, 0xd0, 0xc0),
    (0x33, 0x2c, 0x26), (0xb0, 0x6a, 0x63), (0x72, 0x8b, 0x72), (0xaa, 0x95, 0x6d),
    (0x6b, 0x80, 0x78), (0x7d, 0x67, 0x7e), (0x74, 0x88, 0x86), (0xf4, 0xec, 0xdc),
]
CUBE = [0, 95, 135, 175, 215, 255]
DEFAULT_BG = (0x00, 0x00, 0x00)
DEFAULT_FG = (0xd8, 0xd0, 0xc0)

# Chars Maple Mono NF lacks but DejaVu Sans Mono covers (verified by glyph probe).
DEJAVU_SET = set("≡✳☰⌘▣▤▥∎⬢▫▪↳⏎⎋")
EMOJI_SET = (0x2328, 0x2699, 0x26A1, 0x26A0, 0x26D4, 0x2753, 0x23F0, 0x2705, 0x274C, 0x2728)

ANSI_RE = re.compile(r"\x1b\[([0-9;?]*)([a-zA-Z])")


def c256(n):
    if n < 16:
        return PAL[n]
    if n < 232:
        n -= 16
        return (CUBE[n // 36], CUBE[(n // 6) % 6], CUBE[n % 6])
    v = 8 + 10 * (n - 232)
    return (v, v, v)


def parse_line(line):
    runs, i, fg, bg, bold = [], 0, DEFAULT_FG, DEFAULT_BG, False

    def flush(s):
        if s:
            runs.append((s, fg, bg, bold))

    buf = []
    while i < len(line):
        m = ANSI_RE.match(line, i)
        if m:
            flush("".join(buf)); buf = []
            params, cmd = m.group(1), m.group(2)
            i = m.end()
            if cmd != "m":
                continue
            if params == "":
                params = "0"
            ps = [int(x) if x else 0 for x in params.split(";")]
            j = 0
            while j < len(ps):
                p = ps[j]
                if p == 0:
                    fg, bg, bold = DEFAULT_FG, DEFAULT_BG, False
                elif p == 1:
                    bold = True
                elif p == 22:
                    bold = False
                elif p == 39:
                    fg = DEFAULT_FG
                elif p == 49:
                    bg = DEFAULT_BG
                elif p == 38 and j + 1 < len(ps):
                    if ps[j + 1] == 5 and j + 2 < len(ps):
                        fg = c256(ps[j + 2]); j += 2
                    elif ps[j + 1] == 2 and j + 4 < len(ps):
                        fg = (ps[j + 2], ps[j + 3], ps[j + 4]); j += 4
                    j += 1
                elif p == 48 and j + 1 < len(ps):
                    if ps[j + 1] == 5 and j + 2 < len(ps):
                        bg = c256(ps[j + 2]); j += 2
                    elif ps[j + 1] == 2 and j + 4 < len(ps):
                        bg = (ps[j + 2], ps[j + 3], ps[j + 4]); j += 4
                    j += 1
                elif 30 <= p <= 37:
                    fg = PAL[p - 30]
                elif 90 <= p <= 97:
                    fg = PAL[p - 90 + 8]
                elif 40 <= p <= 47:
                    bg = PAL[p - 40]
                elif 100 <= p <= 107:
                    bg = PAL[p - 100 + 8]
                j += 1
            continue
        ch = line[i]
        if ch == "\r" or ch == "\n":
            flush("".join(buf)); buf = []
        elif ch != "\ufe0f":
            buf.append(ch)
        i += 1
    flush("".join(buf))
    return runs


def glyph_font(ch, fmain, femoji, ffall):
    """Per-character font: maple -> emoji -> dejavu fallback."""
    o = ord(ch)
    if ch in DEJAVU_SET:
        return ffall or fmain
    if 0x1F000 <= o <= 0x1FAFF or o in EMOJI_SET:
        return femoji
    return fmain


def main():
    src, dst = sys.argv[1], sys.argv[2]
    size = int(sys.argv[3]) if len(sys.argv) > 3 else 15
    with open(src, "r", errors="replace") as f:
        lines = f.read().split("\n")

    if not FONT_REG:
        sys.exit("no font found (set ATLAS_PREVIEW_FONT)")
    freg = ImageFont.truetype(FONT_REG, size)
    fbold = ImageFont.truetype(FONT_BOLD if os.path.exists(FONT_BOLD) else FONT_REG, size)
    femoji = ImageFont.truetype(FONT_EMOJI, int(size * 0.92)) if FONT_EMOJI else freg
    ffall = ImageFont.truetype(FONT_FALLBACK, size) if FONT_FALLBACK else None
    cw = freg.getlength("X")
    lh = int(size * 1.4)

    vis = [sum(len(s) for s, _, _, _ in parse_line(l)) for l in lines]
    w = int(max(vis, default=0) * cw) + 10
    h = len(lines) * lh + 6
    img = Image.new("RGB", (w, h), DEFAULT_BG)
    d = ImageDraw.Draw(img)

    for ln, line in enumerate(lines):
        x = 2
        y = ln * lh + 2
        for s, fg, bg, bold in parse_line(line):
            if bg != DEFAULT_BG:
                d.rectangle([x - 0.5, y - 1, x + cw * len(s) + cw, y + lh - 2], fill=bg)
            fmain = fbold if bold else freg
            segs, cur, cur_font = [], [], None
            for ch in s:
                fnt = glyph_font(ch, fmain, femoji, ffall)
                if cur and fnt is not cur_font:
                    segs.append(("".join(cur), cur_font)); cur = []
                cur.append(ch); cur_font = fnt
            if cur:
                segs.append(("".join(cur), cur_font))
            for seg, fnt in segs:
                if seg.strip() or any(c != " " for c in seg):
                    d.text((x, y), seg, font=fnt, fill=fg)
                x += cw * len(seg)
    img.save(dst)
    print(f"wrote {dst} {w}x{h}")


if __name__ == "__main__":
    main()
