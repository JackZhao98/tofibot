#!/usr/bin/env python3
"""Write a preview page that shows an HTML file at several viewport widths.

Writes <page>.preview.html next to the page: the page in one iframe per width,
side by side, each applying its own media queries. Open the preview through a
file:// URL in the computer's browser and look at it; the page itself is not
modified or judged.
Usage: preview_page.py page.html [--widths 1280,390] [--height 900]
"""
from __future__ import annotations

import argparse
import html
import os
import sys

PREVIEW = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preview at several widths</title>
<style>body{margin:0;background:#555;color:#fff;font:14px system-ui,sans-serif}
.row{display:flex;gap:24px;align-items:flex-start;padding:16px;width:max-content}
figure{margin:0}figcaption{padding:0 0 8px}iframe{border:0;background:#fff;height:%(height)dpx;display:block}</style></head>
<body><div class="row">
%(figures)s</div></body></html>
"""
FIGURE = '<figure><figcaption>%(width)d px</figcaption><iframe src="%(src)s" style="width:%(width)dpx" title="%(width)d px"></iframe></figure>\n'


def write_preview(path, widths, height=900):
    figures = "".join(FIGURE % {"width": w, "src": html.escape(os.path.basename(path), quote=True)} for w in widths)
    target = os.path.splitext(path)[0] + ".preview.html"
    with open(target, "w", encoding="utf-8") as handle:
        handle.write(PREVIEW % {"height": height, "figures": figures})
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("path")
    parser.add_argument("--widths", default="1280,390", help="comma-separated viewport widths in px")
    parser.add_argument("--height", type=int, default=900)
    args = parser.parse_args(argv)
    if not os.path.isfile(args.path):
        print("no such file: %s" % args.path, file=sys.stderr)
        return 2
    try:
        widths = [int(w) for w in args.widths.split(",") if w.strip()]
    except ValueError:
        parser.error("--widths takes integers separated by commas")
    if not widths:
        parser.error("give at least one width")
    print("preview: file://" + os.path.abspath(write_preview(args.path, widths, args.height)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
