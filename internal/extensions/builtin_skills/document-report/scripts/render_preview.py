#!/usr/bin/env python3
"""Convert a document to PDF, render every page to PNG and report what it holds.

Accepts DOCX, PPTX, XLSX or PDF. Office files are converted with
`soffice --headless`; pages are rendered with `pdftoppm`. Writes page-NN.png and
a contact sheet (contact.png) into --outdir and prints the page count, the
number of embedded images per page and in total (from the PDF itself), and the
files to open. Open the PNGs to look at the pages the way a reader will.
Usage: render_preview.py file [--outdir DIR] [--dpi 70]
"""
from __future__ import annotations

import argparse
import glob
import os
import shutil
import subprocess
import sys
import tempfile


def to_pdf(path, outdir):
    if path.lower().endswith(".pdf"):
        return path
    soffice = shutil.which("soffice") or shutil.which("libreoffice")
    if not soffice:
        raise RuntimeError("soffice is not installed; cannot convert %s" % os.path.basename(path))
    profile = "file://" + tempfile.mkdtemp(prefix="soffice-profile-")
    subprocess.run([soffice, "-env:UserInstallation=" + profile, "--headless", "--convert-to", "pdf", "--outdir", outdir, path],
                   check=True, capture_output=True, timeout=240)
    pdf = os.path.join(outdir, os.path.splitext(os.path.basename(path))[0] + ".pdf")
    if not os.path.exists(pdf):
        raise RuntimeError("conversion produced no PDF")
    return pdf


def pdf_facts(pdf):
    from pypdf import PdfReader
    reader = PdfReader(pdf)
    per_page, texts = [], []
    for page in reader.pages:
        try:
            per_page.append(len(list(page.images)))
        except Exception:
            per_page.append(0)
        texts.append(page.extract_text() or "")
    return per_page, "\n".join(texts)


def render_pages(pdf, outdir, dpi):
    if not shutil.which("pdftoppm"):
        raise RuntimeError("pdftoppm is not installed")
    for old in glob.glob(os.path.join(outdir, "page-*.png")):
        os.remove(old)
    subprocess.run(["pdftoppm", "-png", "-r", str(dpi), pdf, os.path.join(outdir, "page")], check=True, timeout=240)
    pages = sorted(glob.glob(os.path.join(outdir, "page-*.png")))
    renamed = []
    for index, name in enumerate(pages, start=1):
        target = os.path.join(outdir, "page-%02d.png" % index)
        if name != target:
            os.replace(name, target)
        renamed.append(target)
    return renamed


def contact_sheet(pages, out, columns=3, cell_width=420):
    from PIL import Image
    if not pages:
        return None
    thumbs = []
    for name in pages:
        with Image.open(name) as image:
            image = image.convert("RGB")
            height = int(image.height * cell_width / image.width)
            thumbs.append(image.resize((cell_width, height)))
    rows = (len(thumbs) + columns - 1) // columns
    cell_height = max(t.height for t in thumbs)
    sheet = Image.new("RGB", (columns * (cell_width + 8) + 8, rows * (cell_height + 8) + 8), "#888888")
    for i, thumb in enumerate(thumbs):
        sheet.paste(thumb, (8 + (i % columns) * (cell_width + 8), 8 + (i // columns) * (cell_height + 8)))
    sheet.save(out)
    return out


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("path")
    parser.add_argument("--outdir")
    parser.add_argument("--dpi", type=int, default=70)
    args = parser.parse_args(argv)
    outdir = os.path.abspath(args.outdir or os.path.splitext(args.path)[0] + "-preview")
    os.makedirs(outdir, exist_ok=True)
    try:
        pdf = to_pdf(os.path.abspath(args.path), outdir)
        per_page, text = pdf_facts(pdf)
        pages = render_pages(pdf, outdir, args.dpi)
        sheet = contact_sheet(pages, os.path.join(outdir, "contact.png"))
    except ImportError as exc:
        print("missing library: %s" % exc, file=sys.stderr)
        return 2
    except (RuntimeError, subprocess.SubprocessError) as exc:
        print("cannot render: %s" % exc, file=sys.stderr)
        return 2
    total_images = sum(per_page)
    print("file: %s" % os.path.basename(args.path))
    print("pdf: %s" % pdf)
    print("pages: %d" % len(per_page))
    print("images in pdf: %d (per page: %s)" % (total_images, ", ".join(str(n) for n in per_page)))
    print("text characters: %d" % len(text.strip()))
    print("page images: %s" % ", ".join(pages))
    if sheet:
        print("contact sheet: %s" % sheet)
    return 0

if __name__ == "__main__":
    sys.exit(main())
