#!/usr/bin/env python3
"""Render an HTML file at several viewport widths into PNG images you can view.

For every width the page is laid out in headless Chrome at that width, cut into
screens, and the screens are laid side by side into sheet images
(<page>.<width>px-<n>.png) next to the page. The page itself is not modified or
judged. View the sheets with the computer_files tool's files.view action; the
browser tool only opens http(s) pages, so a file:// URL is not an option.
A side by side page of frames (<page>.preview.html) is written as well.
Usage: preview_page.py page.html [--widths 1280,390] [--height 900] [--max-sheets 4]
"""
from __future__ import annotations

import argparse
import html
import os
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import quote

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

CHROMES = ["google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome",
           "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
SHEET_WIDTH = 1500  # sheets stay near the size a viewer shows without scaling
GAP = 8
MAX_PAGE_HEIGHT = 20000


def find_chrome():
    for name in CHROMES:
        found = shutil.which(name) or (name if os.path.isfile(name) else None)
        if found:
            return found
    return None


def write_preview(path, widths, height=900):
    figures = "".join(FIGURE % {"width": w, "src": html.escape(os.path.basename(path), quote=True)} for w in widths)
    target = os.path.splitext(path)[0] + ".preview.html"
    with open(target, "w", encoding="utf-8") as handle:
        handle.write(PREVIEW % {"height": height, "figures": figures})
    return target


def file_url(path):
    return "file://" + quote(os.path.abspath(path))


def chrome_run(chrome, profile, args, timeout=90):
    cmd = [chrome, "--headless", "--no-sandbox", "--disable-gpu", "--hide-scrollbars", "--allow-file-access-from-files",
           "--user-data-dir=" + profile, "--virtual-time-budget=4000"] + args
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)


def frame(src, width, height, top_screen):
    return ('<div class="s" style="width:%dpx;height:%dpx"><iframe data-top="%d" src="%s" style="width:%dpx;height:%dpx"></iframe></div>'
            % (width, height, top_screen, html.escape(src, quote=True), width, height))


def wrapper(frames, script="", band=0, caption=""):
    # A headless window can hide some of its height from the page and repeat the
    # top of the document in its place, so the top band is a caption and the
    # repeat is harmless.
    return ("<!doctype html><meta charset=utf-8><style>html,body{margin:0;background:#666}"
            "#cap{height:%dpx;overflow:hidden;color:#fff;font:12px/%dpx sans-serif;padding-left:6px}"
            "#row{display:flex;gap:%dpx}.s{overflow:hidden;background:#fff}iframe{border:0;display:block}</style>"
            "<div id=cap>%s</div><div id=row>%s</div><script>%s</script>"
            % (band, max(band, 1), GAP, html.escape(caption), "".join(frames), script))


def page_height(chrome, profile, src, width, height, folder):
    probe = os.path.join(folder, "probe.html")
    script = ("var f=document.querySelector('iframe');function done(){var e=document.documentElement;e.setAttribute('data-vh',"
              "window.innerHeight);e.setAttribute('data-h',f.contentDocument.documentElement.scrollHeight)}"
              "f.addEventListener('load',function(){setTimeout(done,300)});")
    with open(probe, "w", encoding="utf-8") as handle:
        handle.write(wrapper([frame(src, width, height, 0)], script))
    out = chrome_run(chrome, profile, ["--dump-dom", "--window-size=%d,%d" % (width, height), file_url(probe)])
    found = re.search(r'data-h="(\d+)"', out.stdout)
    visible = re.search(r'data-vh="(\d+)"', out.stdout)
    # Headless Chrome may reserve part of the window height; the screenshot has
    # to be taller by that much for the page area to be exactly one screen.
    extra = max(0, height - int(visible.group(1))) if visible else 0
    return (int(found.group(1)) if found else None), extra


def render_sheets(path, widths, height, max_sheets):
    chrome = find_chrome()
    if not chrome:
        return [], "no Chrome or Chromium on this computer; only the frame page was written"
    src = file_url(path)
    stem = os.path.splitext(os.path.abspath(path))[0]
    written, notes = [], []
    with tempfile.TemporaryDirectory(prefix="preview-") as folder:
        profile = os.path.join(folder, "profile")
        for width in widths:
            screen = height
            total, extra = page_height(chrome, profile, src, width, screen, folder)
            if total is None:
                notes.append("%d px: could not measure the page" % width)
                total = screen
            total = min(total, MAX_PAGE_HEIGHT)
            screens = max(1, -(-total // screen))
            per_sheet = max(1, SHEET_WIDTH // (width + GAP))
            sheets = max(1, -(-screens // per_sheet))
            shown = min(sheets, max_sheets)
            for n in range(shown):
                first = n * per_sheet
                count = min(per_sheet, screens - first)
                frames = [frame(src, width, screen, first + i) for i in range(count)]
                script = ("document.querySelectorAll('iframe').forEach(function(f){f.addEventListener('load',function(){"
                          "setTimeout(function(){f.contentWindow.scrollTo(0,%d*Number(f.dataset.top))},200)})});" % screen)
                doc = os.path.join(folder, "sheet.html")
                with open(doc, "w", encoding="utf-8") as handle:
                    handle.write(wrapper(frames, script, extra, "%d px wide, screens %d-%d of %d (%d px each)"
                                         % (width, first + 1, first + count, screens, screen)))
                target = "%s.%dpx-%d.png" % (stem, width, n + 1)
                size = "--window-size=%d,%d" % (count * (width + GAP) - GAP, screen + 2 * extra)
                chrome_run(chrome, profile, ["--screenshot=" + target, size, file_url(doc)])
                if os.path.isfile(target):
                    written.append(target)
                else:
                    notes.append("%d px: sheet %d was not written" % (width, n + 1))
            notes.append("%d px: page is %d px tall, %d screen(s) of %d px, %d sheet(s) written of %d"
                         % (width, total, screens, screen, shown, sheets))
    return written, "\n".join(notes)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("path")
    parser.add_argument("--widths", default="1280,390", help="comma-separated viewport widths in px")
    parser.add_argument("--height", type=int, default=900, help="height of one screen in px")
    parser.add_argument("--max-sheets", type=int, default=4, help="most sheet images written per width")
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
    frames_page = write_preview(args.path, widths, args.height)
    try:
        sheets, notes = render_sheets(args.path, widths, args.height, max(1, args.max_sheets))
    except (OSError, subprocess.SubprocessError) as exc:
        sheets, notes = [], "rendering failed: %s" % exc
    for sheet in sheets:
        print("sheet: " + sheet)
    if notes:
        print(notes)
    print("preview: file://" + os.path.abspath(frames_page))
    return 0


if __name__ == "__main__":
    sys.exit(main())
