#!/usr/bin/env python3
"""Draw boxes, arrows, numbered badges, text labels and blurred regions on a picture from a JSON spec.

The input picture is never changed; the result goes to a new file.
"""
from __future__ import annotations

import argparse
import json
import math
import os
import subprocess
import sys

try:
    from PIL import Image, ImageDraw, ImageFilter, ImageFont
except ImportError:
    Image = None

SPEC_HELP = """\
Spec: {"units": "px" or "fraction", "color": "#e11d48", "items": [...]}
units: px (default) reads positions in pixels of the input picture; fraction reads 0..1 of its width and height.
Items (each may carry "color" and "width" for the line width):
  {"type": "box", "x": 10, "y": 20, "w": 200, "h": 80, "label": "text or number"}
  {"type": "arrow", "from": [x, y], "to": [x, y], "label": "optional text at the tail"}
  {"type": "number", "x": 40, "y": 40, "n": 1}            numbered badge centered on the point
  {"type": "text", "x": 10, "y": 10, "text": "caption"}   text on a filled strip at the point
  {"type": "blur", "x": 10, "y": 20, "w": 200, "h": 80, "radius": 12}
  {"type": "pixelate", "x": 10, "y": 20, "w": 200, "h": 80, "block": 12}
A box label sits above the box, or inside its top edge when the box touches the top of the picture."""

FONT_PATHS = [
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
    "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
    "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    "/System/Library/Fonts/STHeiti Medium.ttc",
    "/System/Library/Fonts/Helvetica.ttc",
]


def candidate_fonts(need_cjk):
    seen = []
    if need_cjk:
        for command in (["fc-match", "-f", "%{file}", ":lang=zh"], ["fc-list", "-f", "%{file}\\n", ":lang=zh"]):
            try:
                out = subprocess.run(command, capture_output=True, text=True, timeout=20)
            except FileNotFoundError:
                continue
            seen.extend(line.strip() for line in out.stdout.splitlines() if line.strip())
    seen.extend(FONT_PATHS)
    return [p for p in seen if os.path.exists(p)]


def covers(font, text):
    """True when the font draws every character of text with a real glyph (not the missing-glyph box)."""
    missing = bytes(font.getmask("\U0010FFFF"))
    return all(c.isspace() or bytes(font.getmask(c)) != missing for c in text)


def load_font(size, text_blob):
    need_cjk = any(ord(c) > 0x2E7F for c in text_blob)
    for path in candidate_fonts(need_cjk)[:40]:
        try:
            font = ImageFont.truetype(path, size)
        except OSError:
            continue
        if covers(font, text_blob):
            return font
    if text_blob.isascii():
        return ImageFont.load_default()
    sys.exit("no installed font covers every character of the labels; install a CJK font in user space or use ASCII labels")


def color_of(item, spec):
    return item.get("color", spec.get("color", "#e11d48"))


def coords(item, spec, size):
    scale_x, scale_y = (size[0], size[1]) if spec.get("units") == "fraction" else (1, 1)
    out = dict(item)
    for key, scale in (("x", scale_x), ("w", scale_x), ("y", scale_y), ("h", scale_y)):
        if key in out:
            out[key] = out[key] * scale
    for key in ("from", "to"):
        if key in out:
            out[key] = (out[key][0] * scale_x, out[key][1] * scale_y)
    return out


def text_strip(draw, font, xy, text, fill, anchor_bottom=False):
    left, top, right, bottom = draw.textbbox((0, 0), text, font=font)
    width, height = right - left + 12, bottom - top + 8
    x, y = xy
    if anchor_bottom:
        y -= height
    draw.rectangle([x, y, x + width, y + height], fill=fill)
    draw.text((x + 6 - left, y + 4 - top), text, font=font, fill="white")
    return width, height


def draw_arrow(draw, start, end, color, width):
    draw.line([start, end], fill=color, width=width)
    angle = math.atan2(end[1] - start[1], end[0] - start[0])
    head = max(12, width * 4)
    points = [end]
    for turn in (math.radians(155), -math.radians(155)):
        points.append((end[0] + head * math.cos(angle + turn), end[1] + head * math.sin(angle + turn)))
    draw.polygon(points, fill=color)


def box_of(item, size):
    left, top = max(0, int(item["x"])), max(0, int(item["y"]))
    right, bottom = min(size[0], int(item["x"] + item["w"])), min(size[1], int(item["y"] + item["h"]))
    if right <= left or bottom <= top:
        sys.exit("region %r lies outside the picture %dx%d" % (item, size[0], size[1]))
    return left, top, right, bottom


def render(image, spec):
    size = image.size
    scale = max(size) / 1000.0
    line_default = max(3, round(3 * scale))
    blob = "".join(str(i.get("label", "")) + str(i.get("text", "")) for i in spec["items"])
    font = load_font(max(14, round(22 * scale)), blob)
    badge_font = load_font(max(14, round(24 * scale)), "0123456789")
    canvas = image.convert("RGBA")
    # Pixel work (blur, pixelate) goes first so the outlines drawn later stay sharp.
    for raw in spec["items"]:
        item = coords(raw, spec, size)
        if item["type"] == "blur":
            region = box_of(item, size)
            patch = canvas.crop(region).filter(ImageFilter.GaussianBlur(item.get("radius", 12) * max(1, scale)))
            canvas.paste(patch, region[:2])
        elif item["type"] == "pixelate":
            region = box_of(item, size)
            patch = canvas.crop(region)
            block = max(2, int(item.get("block", 12) * max(1, scale)))
            small = patch.resize((max(1, patch.width // block), max(1, patch.height // block)), Image.BILINEAR)
            canvas.paste(small.resize(patch.size, Image.NEAREST), region[:2])
    draw = ImageDraw.Draw(canvas)
    for raw in spec["items"]:
        item = coords(raw, spec, size)
        kind, color = item["type"], color_of(raw, spec)
        width = int(item.get("width", line_default))
        if kind == "box":
            left, top, right, bottom = box_of(item, size)
            draw.rectangle([left, top, right, bottom], outline=color, width=width)
            label = item.get("label")
            if label not in (None, ""):
                label = str(label)
                above = top - (font.size if hasattr(font, "size") else 16) - 10
                if above >= 0:
                    text_strip(draw, font, (left, top), label, color, anchor_bottom=True)
                else:
                    text_strip(draw, font, (left + width, top + width), label, color)
        elif kind == "arrow":
            draw_arrow(draw, item["from"], item["to"], color, width)
            if item.get("label"):
                text_strip(draw, font, (item["from"][0], item["from"][1]), str(item["label"]), color, anchor_bottom=True)
        elif kind == "number":
            radius = max(14, round(18 * scale))
            cx, cy = item["x"], item["y"]
            draw.ellipse([cx - radius, cy - radius, cx + radius, cy + radius], fill=color, outline="white", width=2)
            text = str(item["n"])
            left, top, right, bottom = draw.textbbox((0, 0), text, font=badge_font)
            draw.text((cx - (left + right) / 2, cy - (top + bottom) / 2), text, font=badge_font, fill="white")
        elif kind == "text":
            text_strip(draw, font, (item["x"], item["y"]), str(item["text"]), color)
        elif kind in ("blur", "pixelate"):
            continue
        else:
            sys.exit("unknown item type %r" % kind)
    return canvas


def main():
    parser = argparse.ArgumentParser(description="Draw annotations from a JSON spec on a copy of a picture.", epilog=SPEC_HELP,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("input", help="picture to annotate (left unchanged)")
    parser.add_argument("spec", help="JSON spec file")
    parser.add_argument("output", help="new picture to write (png, jpg or webp by extension); must differ from the input")
    args = parser.parse_args()
    if Image is None:
        sys.exit("annotate.py needs Pillow, and importing PIL fails on this computer")
    if os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("output must be a different file from the input")
    with open(args.spec, encoding="utf-8") as handle:
        spec = json.load(handle)
    if not isinstance(spec.get("items"), list) or not spec["items"]:
        sys.exit("spec needs a non-empty items list")
    with Image.open(args.input) as source:
        source.load()
        result = render(source, spec)
    if args.output.lower().endswith((".jpg", ".jpeg")):
        result = result.convert("RGB")
    result.save(args.output)
    print("wrote %s (%dx%d, %d items)" % (args.output, result.width, result.height, len(spec["items"])))


if __name__ == "__main__":
    main()
