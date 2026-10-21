#!/usr/bin/env python3
"""Static checks for a single self-contained HTML page, plus a two-width preview.

Errors (exit status 1): missing doctype, lang, charset, viewport meta
(width=device-width) or title; no h1 or several; external scripts, styles,
images, fonts, frames or @import (only data: URIs, fragments and ordinary
outbound <a> links are allowed); images without alt; placeholder text left in;
unbalanced tags.
Warnings: fixed pixel widths above 390 outside media queries, no media query
and no fluid layout hint, very large file.

--preview writes <page>.preview.html next to the page: the page twice, in
iframes 1280 px and 390 px wide, side by side. Open that file in the
computer's browser through a file:// URL and take a snapshot to judge the
desktop and the phone layout; each iframe applies its own media queries.
Usage: check_page.py page.html [--preview] [--json]
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
from html.parser import HTMLParser

VOID = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"}
PLACEHOLDER = re.compile(r"lorem ipsum|\{\{[^}]*\}\}|\bTODO\b|\bFIXME\b|your text here|placeholder text", re.I)


class Scan(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.doctype = False
        self.lang = ""
        self.charset = False
        self.viewport = ""
        self.title = ""
        self.h1 = 0
        self.external = []
        self.images_without_alt = 0
        self.stack = []
        self.unbalanced = []
        self.styles = []
        self.text = []
        self._in = None

    def handle_decl(self, decl):
        if decl.strip().lower().startswith("doctype html"):
            self.doctype = True

    def handle_starttag(self, tag, attrs):
        a = {k: (v or "") for k, v in attrs}
        if tag == "html":
            self.lang = a.get("lang", "")
        if tag == "meta":
            if "charset" in a or "charset=" in a.get("content", "").lower():
                self.charset = True
            if a.get("name", "").lower() == "viewport":
                self.viewport = a.get("content", "")
        if tag == "h1":
            self.h1 += 1
        if tag == "img":
            if "alt" not in a:
                self.images_without_alt += 1
        rel = a.get("rel", "").lower().split()
        loads_link = tag == "link" and any(r in ("stylesheet", "icon", "preload", "modulepreload", "manifest", "apple-touch-icon") for r in rel)
        loads_tag = tag in ("script", "img", "iframe", "source", "video", "audio", "embed", "object", "track")
        if loads_link or loads_tag:
            for attr in ("src", "href", "poster", "data"):
                value = a.get(attr, "")
                if value and not value.startswith(("data:", "#", "about:blank")):
                    self.external.append("<%s %s=%s>" % (tag, attr, value[:80]))
        if "style" in a:
            self.styles.append(a["style"])
        if tag in ("title", "style", "script"):
            self._in = tag
        if tag not in VOID:
            self.stack.append(tag)

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if tag not in VOID and self.stack and self.stack[-1] == tag:
            self.stack.pop()

    def handle_endtag(self, tag):
        if tag in VOID:
            return
        if tag in self.stack:
            while self.stack and self.stack[-1] != tag:
                self.unbalanced.append(self.stack.pop())
            self.stack.pop()
        else:
            self.unbalanced.append("/" + tag)
        if tag == self._in:
            self._in = None

    def handle_data(self, data):
        if self._in == "title":
            self.title += data
        elif self._in == "style":
            self.styles.append(data)
        elif self._in != "script":
            self.text.append(data)


def split_media(css):
    """Return (css outside @media blocks, number of media queries)."""
    out, depth, i, media = [], 0, 0, 0
    while i < len(css):
        if css.startswith("@media", i):
            media += 1
            j = css.find("{", i)
            if j < 0:
                break
            depth, i = 1, j + 1
            while i < len(css) and depth:
                depth += (css[i] == "{") - (css[i] == "}")
                i += 1
            continue
        out.append(css[i])
        i += 1
    return "".join(out), media


def check(path):
    with open(path, encoding="utf-8") as handle:
        source = handle.read()
    scan = Scan()
    scan.feed(source)
    errors, warnings = [], []
    if not scan.doctype:
        errors.append("missing <!doctype html>")
    if not scan.lang:
        errors.append("<html> has no lang attribute")
    if not scan.charset:
        errors.append("missing charset meta")
    if "width=device-width" not in scan.viewport.replace(" ", ""):
        errors.append("missing viewport meta with width=device-width")
    if not scan.title.strip():
        errors.append("missing or empty <title>")
    if scan.h1 != 1:
        errors.append("expected exactly one <h1>, found %d" % scan.h1)
    for item in scan.external:
        errors.append("external resource: " + item)
    css = "\n".join(scan.styles)
    for match in re.finditer(r"@import[^;]+;", css):
        errors.append("external stylesheet import: " + match.group(0)[:80])
    for match in re.finditer(r"url\(\s*['\"]?(https?:)?//[^)]+\)", css):
        errors.append("external url in CSS: " + match.group(0)[:80])
    if scan.images_without_alt:
        errors.append("%d image(s) without alt" % scan.images_without_alt)
    visible = " ".join(scan.text)
    for match in PLACEHOLDER.finditer(visible):
        errors.append("placeholder text left in the page: %r" % match.group(0))
    if scan.unbalanced:
        errors.append("unbalanced tags: " + ", ".join(sorted(set(scan.unbalanced))[:6]))
    outside, media = split_media(css)
    wide = sorted({int(m.group(1)) for m in re.finditer(r"(?<![-\w])(?:min-)?width\s*:\s*(\d{3,4})px", outside) if int(m.group(1)) > 390})
    if wide:
        warnings.append("fixed pixel widths above 390 outside media queries: %s" % ", ".join("%dpx" % w for w in wide[:6]))
    fluid = re.search(r"max-width|clamp\(|minmax\(|flex-wrap|auto-fit|auto-fill|\d\s*(vw|%|fr)\b", css)
    if not media and not fluid:
        warnings.append("no media query and no fluid layout rule found")
    size = len(source.encode("utf-8"))
    if size > 2_000_000:
        warnings.append("file is %.1f MB" % (size / 1e6))
    return {"file": os.path.basename(path), "bytes": size, "title": scan.title.strip(), "media_queries": media,
            "errors": errors, "warnings": warnings}


PREVIEW = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preview at two widths</title>
<style>body{margin:0;background:#555;color:#fff;font:14px system-ui,sans-serif}
.row{display:flex;gap:24px;align-items:flex-start;padding:16px;width:max-content}
figure{margin:0}figcaption{padding:0 0 8px}iframe{border:0;background:#fff;height:%(height)dpx;display:block}</style></head>
<body><div class="row">
<figure><figcaption>Desktop, 1280 px</figcaption><iframe src="%(src)s" style="width:1280px" title="desktop"></iframe></figure>
<figure><figcaption>Phone, 390 px</figcaption><iframe src="%(src)s" style="width:390px" title="phone"></iframe></figure>
</div></body></html>
"""


def write_preview(path, height=900):
    target = os.path.splitext(path)[0] + ".preview.html"
    with open(target, "w", encoding="utf-8") as handle:
        handle.write(PREVIEW % {"src": os.path.basename(path), "height": height})
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("path")
    parser.add_argument("--preview", action="store_true")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)
    report = check(args.path)
    if args.preview:
        report["preview"] = os.path.abspath(write_preview(args.path))
    if args.json:
        print(json.dumps(report, indent=2))
    else:
        print("%s: %d bytes, title %r, %d media query block(s)" % (report["file"], report["bytes"], report["title"], report["media_queries"]))
        for text in report["warnings"]:
            print("WARNING: " + text)
        for text in report["errors"]:
            print("ERROR: " + text)
        if report.get("preview"):
            print("preview: file://" + report["preview"])
        if not report["errors"]:
            print("no errors")
    return 1 if report["errors"] else 0


if __name__ == "__main__":
    sys.exit(main())
