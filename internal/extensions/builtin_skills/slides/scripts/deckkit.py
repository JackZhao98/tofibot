"""Shared text-fit estimation for build_deck.py and check_deck.py.

The estimate is deliberately simple: an average glyph is 0.52 em wide (a CJK or
other full-width glyph is 1.0 em), lines are 1.2 em tall and paragraphs add
0.35 em. It is a screening tool; the rendered slide has the last word.
"""
from __future__ import annotations

import math
import unicodedata

EM_LATIN = 0.52
EM_WIDE = 1.0
LINE = 1.2
PARA_GAP = 0.35


def glyph_em(ch):
    if unicodedata.east_asian_width(ch) in ("W", "F"):
        return EM_WIDE
    return EM_LATIN


def text_width_em(text):
    return sum(glyph_em(c) for c in text)


def wrapped_lines(text, width_in, size_pt):
    """Number of lines `text` takes in a box `width_in` wide at `size_pt`."""
    usable = max(width_in * 72.0 / size_pt, 1.0)  # width in em
    total = 0
    for paragraph in text.split("\n") or [""]:
        words = paragraph.split(" ")
        current, lines = 0.0, 1
        for word in words:
            w = text_width_em(word)
            space = EM_LATIN if current else 0.0
            if current + space + w <= usable:
                current += space + w
            elif w > usable:  # a long unbroken word wraps by character
                lines += int(math.ceil((current + space + w) / usable)) - 1 if current else int(math.ceil(w / usable)) - 1
                current = (current + space + w) % usable
            else:
                lines += 1
                current = w
        total += lines
    return total


def text_height_in(paragraphs, width_in, size_pt, margin_in=0.1):
    """Estimated height in inches of paragraphs (list of str) at one size."""
    inner = width_in - 2 * 0.1
    lines = sum(wrapped_lines(p, inner, size_pt) for p in paragraphs)
    em = lines * LINE + max(len(paragraphs) - 1, 0) * PARA_GAP
    return em * size_pt / 72.0 + margin_in


def fit_size(paragraphs, width_in, height_in, start_pt, min_pt):
    """Largest whole-point size in [min_pt, start_pt] whose estimate fits; min_pt when none does."""
    size = int(start_pt)
    while size > min_pt:
        if text_height_in(paragraphs, width_in, size) <= height_in:
            return size
        size -= 1
    return int(min_pt)
