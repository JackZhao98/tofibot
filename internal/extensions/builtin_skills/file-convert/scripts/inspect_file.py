#!/usr/bin/env python3
"""Print the facts that show whether a converted file kept its content: counts for docx, pptx, xlsx, pdf, html, md, text, zip and tar.

Read-only; uses the standard library plus Poppler's pdfinfo and pdftotext when present.
"""
from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile


def count(pattern, text):
    return len(re.findall(pattern, text))


def office(path):
    with zipfile.ZipFile(path) as z:
        names = z.namelist()
        media = [n for n in names if "/media/" in n]
        print("media files: %d" % len(media))
        if "word/document.xml" in names:
            xml = z.read("word/document.xml").decode("utf-8", "replace")
            text = " ".join(re.findall(r"<w:t[^>]*>([^<]*)</w:t>", xml))
            print("kind: docx; paragraphs: %d; tables: %d; table rows: %d; headings: %d; drawings: %d" % (
                count(r"<w:p[ >]", xml), count(r"<w:tbl>", xml), count(r"<w:tr[ >]", xml), count(r'<w:pStyle w:val="(?:Heading|heading|标题)', xml), count(r"<w:drawing>", xml)))
            print("characters of text: %d" % len(text))
            print("first text: %s" % text[:200])
        elif any(n.startswith("ppt/slides/slide") for n in names):
            print("kind: pptx; slides: %d" % len([n for n in names if re.match(r"ppt/slides/slide\d+\.xml$", n)]))
        elif "xl/workbook.xml" in names:
            print("kind: xlsx; sheets: %d" % count(r"<sheet ", z.read("xl/workbook.xml").decode("utf-8", "replace")))
        else:
            print("kind: zip; entries: %d" % len(names))


def pdf(path):
    if shutil.which("pdfinfo"):
        print(subprocess.run(["pdfinfo", path], capture_output=True, text=True).stdout.strip())
    if shutil.which("pdftotext"):
        text = subprocess.run(["pdftotext", "-layout", path, "-"], capture_output=True, text=True).stdout
        print("characters of text: %d (0 means a scan with no text layer)" % len(text.strip()))
        print("first text: %s" % " ".join(text.split())[:200])


def textual(path):
    with open(path, encoding="utf-8", errors="replace") as handle:
        text = handle.read()
    print("characters: %d; lines: %d" % (len(text), text.count("\n") + 1))
    if re.search(r"<html|<body|<table", text, re.I):
        print("kind: html; tables: %d; images: %d; headings: %d; links: %d" % (
            count(r"<table", text), count(r"<img", text), count(r"<h[1-6][ >]", text), count(r"<a ", text)))
    else:
        print("markdown-like: headings %d; table rows %d; images %d; links %d" % (
            count(r"(?m)^#{1,6} ", text), count(r"(?m)^\s*\|.*\|\s*$", text), count(r"!\[", text), count(r"\]\(", text)))
    print("first text: %s" % " ".join(text.split())[:200])


def archive(path):
    if zipfile.is_zipfile(path):
        with zipfile.ZipFile(path) as z:
            names = z.namelist()
    elif tarfile.is_tarfile(path):
        with tarfile.open(path) as t:
            names = t.getnames()
    else:
        sys.exit("not an archive: %s" % path)
    print("kind: archive; entries: %d" % len(names))
    for name in names[:20]:
        print("  " + name)


def main():
    parser = argparse.ArgumentParser(description="Print content facts of a file to compare an original with its conversion.")
    parser.add_argument("path", help="file to inspect")
    args = parser.parse_args()
    if not os.path.isfile(args.path):
        sys.exit("file not found: %s" % args.path)
    print("file: %s; bytes: %d" % (args.path, os.path.getsize(args.path)))
    ext = os.path.splitext(args.path)[1].lower()
    if ext in (".docx", ".pptx", ".xlsx", ".odt") and zipfile.is_zipfile(args.path):
        office(args.path)
    elif ext == ".pdf":
        pdf(args.path)
    elif ext in (".md", ".markdown", ".html", ".htm", ".txt", ".csv", ".rst", ".tex"):
        textual(args.path)
    elif ext in (".zip", ".tar", ".gz", ".tgz", ".xz", ".bz2"):
        archive(args.path)
    else:
        print(subprocess.run(["file", args.path], capture_output=True, text=True).stdout.strip())


if __name__ == "__main__":
    main()
