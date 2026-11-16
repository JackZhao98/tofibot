#!/usr/bin/env python3
"""Check a PPTX deck for layout faults a render may hide or a reader may miss.

Per slide it reports:
  FAIL  a shape outside the slide
  FAIL  two text, table, chart or picture shapes that overlap
  FAIL  leftover template text (lorem ipsum, xxx, TODO, bracketed insert notes,
        "Click to add")
  FAIL  an empty placeholder
  WARN  text that probably does not fit its box (an estimate from font size and
        box width; the render decides)
  WARN  text under 10 pt, a shape closer than 0.3 in to a slide edge, no title
  WARN  a table whose rows run past the slide bottom

Exit status: 0 no FAIL; 1 at least one FAIL; 2 unreadable deck. Warnings do not
change the status. A clean run does not replace looking at the rendered slides.
Usage: pptx_check.py deck.pptx
"""
from __future__ import annotations

import argparse
import math
import re
import sys

EMU = 914400.0
LEFTOVER = re.compile(r"\blorem\b|\bipsum\b|\bx{3,}\b|\bTODO\b|\[insert|click to (?:add|edit)", re.I)


def walk(shapes):
    for shape in shapes:
        if shape.shape_type == 6:
            yield from walk(shape.shapes)
        else:
            yield shape


def has_content(shape):
    if getattr(shape, "has_chart", False) and shape.has_chart:
        return True
    if getattr(shape, "has_table", False) and shape.has_table:
        return True
    if shape.shape_type == 13:
        return True
    return shape.has_text_frame and bool(shape.text_frame.text.strip())


def is_wide(ch):
    return ord(ch) >= 0x2E80


def estimated_height_in(shape, default_pt):
    frame = shape.text_frame
    width = shape.width / EMU - 0.2
    if width <= 0.1:
        return 0.0, None
    total, smallest = 0.0, None
    for paragraph in frame.paragraphs:
        size = default_pt
        for run in paragraph.runs:
            if run.font.size:
                size = run.font.size.pt
                smallest = size if smallest is None else min(smallest, size)
        text = "".join(run.text for run in paragraph.runs) or paragraph.text
        em = sum(1.0 if is_wide(c) else 0.5 for c in text)
        lines = max(1, math.ceil(em * size / 72.0 / width)) if text else 1
        total += lines * size * 1.2 / 72.0
    return total, smallest


def is_placeholder(shape):
    return getattr(shape, "is_placeholder", False)


def check(path):
    from pptx import Presentation
    prs = Presentation(path)
    width, height = prs.slide_width / EMU, prs.slide_height / EMU
    findings = []

    def add(level, slide, text):
        findings.append((level, "slide %d: %s" % (slide, text)))

    for number, slide in enumerate(prs.slides, start=1):
        boxes = []
        title = slide.shapes.title
        if title is None or not title.text_frame.text.strip():
            add("WARN", number, "no title text")
        for shape in walk(slide.shapes):
            if None in (shape.left, shape.top, shape.width, shape.height):
                continue
            left, top, w, h = shape.left / EMU, shape.top / EMU, shape.width / EMU, shape.height / EMU
            if left < -0.02 or top < -0.02 or left + w > width + 0.02 or top + h > height + 0.02:
                add("FAIL", number, "%r lies outside the slide (box %.2f,%.2f %.2fx%.2f in)" % (shape.name, left, top, w, h))
            elif min(left, top, width - left - w, height - top - h) < 0.3 and has_content(shape):
                add("WARN", number, "%r is closer than 0.3 in to a slide edge" % shape.name)
            if is_placeholder(shape) and shape.has_text_frame and not shape.text_frame.text.strip():
                add("FAIL", number, "empty placeholder %r" % shape.name)
            if shape.has_text_frame and shape.text_frame.text.strip():
                if LEFTOVER.search(shape.text_frame.text):
                    add("FAIL", number, "%r holds leftover template text" % shape.name)
                default = 32 if is_placeholder(shape) and shape == title else 18
                need, smallest = estimated_height_in(shape, default)
                if smallest is not None and smallest < 10:
                    add("WARN", number, "%r uses %.0f pt text" % (shape.name, smallest))
                if need > h * 1.1 + 0.1:
                    add("WARN", number, "%r probably overflows: text needs about %.1f in, box is %.1f in" % (shape.name, need, h))
            if getattr(shape, "has_table", False) and shape.has_table:
                rows = sum(r.height for r in shape.table.rows) / EMU
                if top + rows > height + 0.02:
                    add("WARN", number, "table %r rows end at %.2f in, slide is %.2f in high" % (shape.name, top + rows, height))
                for row in shape.table.rows:
                    for cell in row.cells:
                        if LEFTOVER.search(cell.text):
                            add("FAIL", number, "table %r holds leftover template text" % shape.name)
                            break
            if has_content(shape):
                boxes.append((shape.name, left, top, left + w, top + h))
        for i, a in enumerate(boxes):
            for b in boxes[i + 1:]:
                ix = min(a[3], b[3]) - max(a[1], b[1])
                iy = min(a[4], b[4]) - max(a[2], b[2])
                if ix > 0.05 and iy > 0.05:
                    smaller = min((a[3] - a[1]) * (a[4] - a[2]), (b[3] - b[1]) * (b[4] - b[2]))
                    if smaller > 0 and ix * iy / smaller > 0.04:
                        add("FAIL", number, "%r overlaps %r" % (a[0], b[0]))
    return len(prs.slides), findings


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("deck")
    args = parser.parse_args(argv)
    try:
        count, findings = check(args.deck)
    except ImportError:
        print("python-pptx is not installed in this Python", file=sys.stderr)
        return 2
    except Exception as exc:  # python-pptx raises several package errors for a damaged file
        print("cannot read the deck: %s" % exc, file=sys.stderr)
        return 2
    print("%d slides checked" % count)
    for level, text in findings:
        print("%s: %s" % (level, text))
    return 1 if any(level == "FAIL" for level, _ in findings) else 0


if __name__ == "__main__":
    sys.exit(main())
