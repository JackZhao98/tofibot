#!/usr/bin/env python3
"""Read, cut, join, rotate, render, table-extract and OCR PDF files.

Subcommands: info, text, tables, split, merge, rotate, render, ocr. Every
subcommand has --help. Page ranges are 1-based: "1-3,5,7-" (a trailing dash
runs to the last page). Output files are new files; a command refuses to
overwrite its input, exits non-zero when it cannot do the job, and prints
what it wrote. A password-protected input takes --password-file, a file whose
first line is the password; a password on the command line is never accepted.
Needs pypdf; tables also needs openpyxl (or --csv-dir); text and rendering use
Poppler (pdftotext, pdftoppm); ocr uses Tesseract.
"""
from __future__ import annotations

import argparse
import csv
import glob
import os
import re
import shutil
import subprocess
import sys
import tempfile


def fail(message, code=1):
    print("error: %s" % message, file=sys.stderr)
    sys.exit(code)


def need_program(name):
    if shutil.which(name) is None:
        fail("%s is not installed on this computer" % name, 2)


def run(cmd, timeout=600):
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        fail("%s failed (exit %d): %s" % (cmd[0], result.returncode, result.stderr.strip()))
    return result.stdout


def pypdf_module():
    try:
        import pypdf
    except ImportError:
        fail("the pypdf library is not installed", 2)
    return pypdf


def read_password(path):
    if not path:
        return None
    with open(path, encoding="utf-8") as handle:
        return handle.readline().rstrip("\r\n")


def open_reader(path, password_file=None):
    pypdf = pypdf_module()
    if not os.path.isfile(path):
        fail("%s is not a file" % path)
    try:
        reader = pypdf.PdfReader(path)
    except pypdf.errors.PyPdfError as exc:
        fail("%s cannot be read as a PDF: %s" % (path, exc))
    if reader.is_encrypted:
        password = read_password(password_file)
        if password is None:
            fail("%s is password protected; pass --password-file with a file holding the password" % path)
        if not reader.decrypt(password):
            fail("the password in %s does not open %s" % (password_file, path))
    return reader


def ranges(spec, total):
    """Turn '1-3,5,7-' into a list of 0-based page indexes in the order given."""
    if not spec:
        return list(range(total))
    pages = []
    for part in spec.split(","):
        part = part.strip()
        match = re.fullmatch(r"(\d*)(-?)(\d*)", part)
        if not part or not match:
            fail("bad page range %r" % part)
        first, dash, last = match.groups()
        start = int(first) if first else 1
        end = int(last) if last else (total if dash else start)
        if start < 1 or end > total or start > end:
            fail("page range %r is outside 1-%d" % (part, total))
        pages.extend(range(start - 1, end))
    return pages


def refuse_same(inputs, output):
    for item in inputs:
        if os.path.exists(output) and os.path.samefile(item, output):
            fail("the output %s is the input file; write to a new file" % output)


def write_pdf(writer, output):
    parent = os.path.dirname(os.path.abspath(output))
    os.makedirs(parent, exist_ok=True)
    with open(output, "wb") as handle:
        writer.write(handle)
    print("wrote %s (%d pages)" % (output, len(writer.pages)))


def plain_copy(path, password_file, folder):
    """A path the command line programs can read: a decrypted copy when the PDF is protected."""
    reader = open_reader(path, password_file)
    if not reader.is_encrypted:
        return path
    writer = pypdf_module().PdfWriter()
    for page in reader.pages:
        writer.add_page(page)
    copy = os.path.join(folder, "unlocked.pdf")
    with open(copy, "wb") as handle:
        writer.write(handle)
    return copy


# ---------------------------------------------------------------- info

def cmd_info(args):
    reader = open_reader(args.pdf, args.password_file)
    print("file: %s" % args.pdf)
    print("pages: %d" % len(reader.pages))
    print("encrypted: %s" % ("yes (opened with the password file)" if reader.is_encrypted else "no"))
    meta = reader.metadata
    if meta:
        for key, label in (("/Title", "title"), ("/Author", "author"), ("/Producer", "producer")):
            if meta.get(key):
                print("%s: %s" % (label, meta.get(key)))
    fields = reader.get_fields() or {}
    print("form fields: %d" % len(fields))
    empty = []
    for index, page in enumerate(reader.pages, start=1):
        text = (page.extract_text() or "").strip()
        try:
            images = len(list(page.images))
        except (KeyError, ValueError, NotImplementedError, OSError):
            images = -1
        box = page.mediabox
        print("page %d: %.0fx%.0f pt, rotation %d, text characters %d, images %s" % (
            index, float(box.width), float(box.height), int(page.get("/Rotate", 0) or 0), len(text), images if images >= 0 else "unknown"))
        if not text:
            empty.append(index)
    if empty:
        print("pages without a text layer (scanned or drawn): %s" % ",".join(str(n) for n in empty))


# ---------------------------------------------------------------- text

def cmd_text(args):
    need_program("pdftotext")
    with tempfile.TemporaryDirectory() as folder:
        source = plain_copy(args.pdf, args.password_file, folder)
        cmd = ["pdftotext"]
        if not args.raw:
            cmd.append("-layout")
        if args.first:
            cmd += ["-f", str(args.first)]
        if args.last:
            cmd += ["-l", str(args.last)]
        cmd += [source, "-"]
        text = run(cmd)
    if not text.strip():
        print("warning: no text layer found; the pages may be scans (use the ocr subcommand)", file=sys.stderr)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as handle:
            handle.write(text)
        print("wrote %s (%d characters)" % (args.out, len(text)))
    else:
        sys.stdout.write(text)


# ---------------------------------------------------------------- tables

CELL = re.compile(r"\S+(?: \S+)*")
NUMBER = re.compile(r"-?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?")


def line_cells(line):
    """Cells of a layout line: runs of text split on two or more spaces, with their column spans."""
    return [(m.start(), m.end(), m.group()) for m in CELL.finditer(line)]


def table_blocks(page_text):
    """Groups of layout lines with two or more cells each; up to two blank lines may separate the rows of one table."""
    blocks, current, blanks = [], [], 0
    for line in page_text.split("\n"):
        if not line.strip():
            blanks += 1
            if blanks > 2 and current:
                if len(current) >= 2:
                    blocks.append(current)
                current = []
            continue
        cells = line_cells(line)
        if len(cells) >= 2:
            current.append(cells)
        else:
            if len(current) >= 2:
                blocks.append(current)
            current = []
        blanks = 0
    if len(current) >= 2:
        blocks.append(current)
    return blocks


def grid_from_block(block):
    spans = sorted((s, e) for row in block for s, e, _ in row)
    columns = []
    for start, end in spans:
        if columns and start < columns[-1][1]:
            columns[-1][1] = max(columns[-1][1], end)
        else:
            columns.append([start, end])
    grid = []
    for row in block:
        out = [""] * len(columns)
        for start, end, text in row:
            for index, (lo, hi) in enumerate(columns):
                if start < hi and end > lo:
                    out[index] = (out[index] + " " + text).strip()
                    break
        grid.append(out)
    keep = [i for i in range(len(columns)) if any(row[i] for row in grid)]
    return [[row[i] for i in keep] for row in grid]


def typed(cell):
    if NUMBER.fullmatch(cell) and not (len(cell) > 1 and cell.startswith("0") and "." not in cell and "," not in cell):
        value = float(cell.replace(",", ""))
        return int(value) if value.is_integer() and "." not in cell else value
    return cell


def cmd_tables(args):
    need_program("pdftotext")
    with tempfile.TemporaryDirectory() as folder:
        source = plain_copy(args.pdf, args.password_file, folder)
        total = len(open_reader(source).pages)
        pages = [i + 1 for i in ranges(args.pages, total)]
        found = []
        for number in pages:
            text = run(["pdftotext", "-layout", "-f", str(number), "-l", str(number), source, "-"])
            for index, block in enumerate(table_blocks(text), start=1):
                found.append(("p%d_t%d" % (number, index), grid_from_block(block)))
    if not found:
        fail("no table found: no run of two or more lines with aligned columns (a scanned PDF needs the ocr subcommand first)")
    if args.csv_dir:
        os.makedirs(args.csv_dir, exist_ok=True)
        for name, grid in found:
            path = os.path.join(args.csv_dir, name + ".csv")
            with open(path, "w", newline="", encoding="utf-8-sig") as handle:
                csv.writer(handle).writerows(grid)
            print("wrote %s" % path)
    else:
        try:
            import openpyxl
        except ImportError:
            fail("the openpyxl library is not installed; use --csv-dir instead", 2)
        book = openpyxl.Workbook()
        book.remove(book.active)
        for name, grid in found:
            sheet = book.create_sheet(name)
            for row in grid:
                sheet.append([typed(cell) for cell in row])
            for index in range(len(grid[0])):
                width = max(len(str(row[index])) for row in grid if index < len(row))
                sheet.column_dimensions[openpyxl.utils.get_column_letter(index + 1)].width = min(60, width + 3)
        parent = os.path.dirname(os.path.abspath(args.out))
        os.makedirs(parent, exist_ok=True)
        book.save(args.out)
        print("wrote %s" % args.out)
    for name, grid in found:
        print("table %s: %d rows x %d columns, first row: %s" % (name, len(grid), len(grid[0]), " | ".join(grid[0])))
    print("check each table against the rendered page before relying on it: merged header cells and wrapped cells can shift columns")


# ---------------------------------------------------------------- split, merge, rotate

def cmd_split(args):
    pypdf = pypdf_module()
    reader = open_reader(args.pdf, args.password_file)
    total = len(reader.pages)
    os.makedirs(args.outdir, exist_ok=True)
    stem = os.path.splitext(os.path.basename(args.pdf))[0]
    if args.ranges:
        groups = [ranges(part, total) for part in args.ranges.split(";")]
    elif args.every:
        groups = [list(range(i, min(i + args.every, total))) for i in range(0, total, args.every)]
    else:
        groups = [[i] for i in range(total)]
    width = len(str(len(groups)))
    for number, group in enumerate(groups, start=1):
        writer = pypdf.PdfWriter()
        for index in group:
            writer.add_page(reader.pages[index])
        write_pdf(writer, os.path.join(args.outdir, "%s-part%0*d.pdf" % (stem, width, number)))


def cmd_merge(args):
    pypdf = pypdf_module()
    refuse_same([item.split("::")[0] for item in args.inputs], args.output)
    writer = pypdf.PdfWriter()
    for item in args.inputs:
        path, _, spec = item.partition("::")
        reader = open_reader(path, None)
        for index in ranges(spec, len(reader.pages)):
            writer.add_page(reader.pages[index])
    write_pdf(writer, args.output)


def cmd_rotate(args):
    pypdf = pypdf_module()
    refuse_same([args.pdf], args.output)
    if args.degrees % 90 != 0:
        fail("--degrees must be a multiple of 90")
    reader = open_reader(args.pdf, args.password_file)
    chosen = set(ranges(args.pages, len(reader.pages)))
    writer = pypdf.PdfWriter()
    for index, page in enumerate(reader.pages):
        if index in chosen:
            page.rotate(args.degrees)
        writer.add_page(page)
    write_pdf(writer, args.output)
    print("rotated %d page(s) by %d degrees clockwise" % (len(chosen), args.degrees))


# ---------------------------------------------------------------- render

def render_pages(pdf, outdir, dpi, first=None, last=None, password_file=None):
    need_program("pdftoppm")
    os.makedirs(outdir, exist_ok=True)
    for old in glob.glob(os.path.join(outdir, "page-*.png")):
        os.remove(old)
    with tempfile.TemporaryDirectory() as folder:
        source = plain_copy(pdf, password_file, folder)
        cmd = ["pdftoppm", "-png", "-r", str(dpi)]
        if first:
            cmd += ["-f", str(first)]
        if last:
            cmd += ["-l", str(last)]
        run(cmd + [source, os.path.join(outdir, "page")])
    return sorted(glob.glob(os.path.join(outdir, "page-*.png")))


def cmd_render(args):
    pages = render_pages(args.pdf, args.outdir, args.dpi, args.first, args.last, args.password_file)
    if not pages:
        fail("no page was rendered")
    for path in pages:
        print("wrote %s (%d bytes)" % (path, os.path.getsize(path)))
    print("view each image with the computer_files files.view action before saying anything about the pages")


# ---------------------------------------------------------------- ocr

def cmd_ocr(args):
    need_program("tesseract")
    pypdf = pypdf_module()
    os.makedirs(args.outdir, exist_ok=True)
    images = render_pages(args.pdf, os.path.join(args.outdir, "scan"), args.dpi, args.first, args.last, args.password_file)
    if not images:
        fail("no page was rendered")
    texts, searchable = [], pypdf.PdfWriter()
    for image in images:
        base = os.path.splitext(image)[0]
        cmd = ["tesseract", image, base, "-l", args.lang]
        if args.searchable:
            cmd.append("pdf")
        run(cmd)
        if args.searchable:
            for page in pypdf.PdfReader(base + ".pdf").pages:
                searchable.add_page(page)
        else:
            with open(base + ".txt", encoding="utf-8") as handle:
                texts.append(handle.read())
    if args.searchable:
        refuse_same([args.pdf], args.searchable)
        write_pdf(searchable, args.searchable)
        if shutil.which("pdftotext"):
            print("text characters in the searchable PDF: %d" % len(run(["pdftotext", "-layout", args.searchable, "-"]).strip()))
        return
    out = os.path.join(args.outdir, "ocr.txt")
    with open(out, "w", encoding="utf-8") as handle:
        for number, text in enumerate(texts, start=1):
            handle.write("=== page %d ===\n%s\n" % (number, text.strip()))
    print("wrote %s (%d pages, %d characters)" % (out, len(texts), sum(len(t.strip()) for t in texts)))
    print("OCR is approximate: compare figures and names against the rendered page image")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)

    def add(name, func, help_text, password=True):
        p = sub.add_parser(name, help=help_text, description=help_text)
        if password:
            p.add_argument("--password-file", help="file whose first line is the PDF password")
        p.set_defaults(func=func)
        return p

    p = add("info", cmd_info, "page count, sizes, text layer per page, form fields, encryption")
    p.add_argument("pdf")
    p = add("text", cmd_text, "extract text with pdftotext (layout kept unless --raw)")
    p.add_argument("pdf")
    p.add_argument("--first", type=int)
    p.add_argument("--last", type=int)
    p.add_argument("--raw", action="store_true", help="reading order without column layout")
    p.add_argument("--out", help="write to this file instead of standard output")
    p = add("tables", cmd_tables, "find aligned-column tables in the text layer and write an xlsx (one sheet per table) or csv files")
    p.add_argument("pdf")
    p.add_argument("--pages", help="page ranges, default all")
    p.add_argument("--out", default="tables.xlsx", help="xlsx to write")
    p.add_argument("--csv-dir", help="write one csv per table here instead of an xlsx")
    p = add("split", cmd_split, "one file per page, per --every N pages, or per --ranges group")
    p.add_argument("pdf")
    p.add_argument("--outdir", required=True)
    p.add_argument("--every", type=int, help="pages per file")
    p.add_argument("--ranges", help="groups separated by semicolons, for example 1-3;4-6")
    p = add("merge", cmd_merge, "join PDFs in the order given; FILE::RANGE takes only those pages of FILE", password=False)
    p.add_argument("inputs", nargs="+")
    p.add_argument("-o", "--output", required=True)
    p = add("rotate", cmd_rotate, "rotate pages clockwise by a multiple of 90 degrees")
    p.add_argument("pdf")
    p.add_argument("-o", "--output", required=True)
    p.add_argument("--degrees", type=int, default=90)
    p.add_argument("--pages", help="page ranges, default all")
    p = add("render", cmd_render, "render pages to PNG files with pdftoppm, to view with files.view")
    p.add_argument("pdf")
    p.add_argument("--outdir", required=True)
    p.add_argument("--dpi", type=int, default=80)
    p.add_argument("--first", type=int)
    p.add_argument("--last", type=int)
    p = add("ocr", cmd_ocr, "OCR scanned pages with Tesseract: text file, or a searchable PDF with --searchable")
    p.add_argument("pdf")
    p.add_argument("--outdir", required=True, help="working folder for page images and the text result")
    p.add_argument("--lang", default="eng", help="Tesseract language code; others need their tessdata installed")
    p.add_argument("--dpi", type=int, default=300)
    p.add_argument("--first", type=int)
    p.add_argument("--last", type=int)
    p.add_argument("--searchable", help="write a searchable PDF to this path instead of a text file")

    args = parser.parse_args(argv)
    args.func(args)
    return 0


if __name__ == "__main__":
    sys.exit(main())
