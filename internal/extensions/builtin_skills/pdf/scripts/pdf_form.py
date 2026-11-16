#!/usr/bin/env python3
"""Fill PDF forms: list fillable fields, fill them, or place text on a flat form.

Subcommands: fields, fill, labels, overlay. Every subcommand has --help. The
input PDF is never changed; each command writes a new file and exits non-zero
when a value or a box is invalid, naming the reason. Needs pypdf; overlay also
needs reportlab; labels uses Poppler's pdftotext.

fields   list the fillable fields (id, page, type, allowed values) as JSON.
fill     write field values (a JSON list of {field_id, page, value}) into the form.
labels   list every word of a flat form with its box, in points from the top left.
overlay  place text in boxes on a flat form (fields.json: see references/forms.md).
"""
from __future__ import annotations

import argparse
import io
import json
import os
import re
import shutil
import subprocess
import sys
import xml.etree.ElementTree as ET


def fail(message, code=1):
    print("error: %s" % message, file=sys.stderr)
    sys.exit(code)


def pypdf_module():
    try:
        import pypdf
    except ImportError:
        fail("the pypdf library is not installed", 2)
    return pypdf


def open_reader(path):
    pypdf = pypdf_module()
    if not os.path.isfile(path):
        fail("%s is not a file" % path)
    try:
        reader = pypdf.PdfReader(path)
    except pypdf.errors.PyPdfError as exc:
        fail("%s cannot be read as a PDF: %s" % (path, exc))
    if reader.is_encrypted:
        fail("%s is password protected; make an unlocked copy first with pdf_tools.py (any subcommand reads --password-file)" % path)
    return reader


def refuse_same(source, output):
    if os.path.exists(output) and os.path.samefile(source, output):
        fail("the output %s is the input file; write to a new file" % output)


# ---------------------------------------------------------------- fillable fields

def full_field_id(annotation):
    parts = []
    while annotation:
        name = annotation.get("/T")
        if name:
            parts.append(name)
        annotation = annotation.get("/Parent")
    return ".".join(reversed(parts)) if parts else None


def field_dict(field, field_id):
    info = {"field_id": field_id}
    kind = field.get("/FT")
    if kind == "/Tx":
        info["type"] = "text"
    elif kind == "/Btn":
        info["type"] = "checkbox"
        states = field.get("/_States_", [])
        if len(states) == 2:
            if "/Off" in states:
                info["checked_value"] = states[0] if states[0] != "/Off" else states[1]
                info["unchecked_value"] = "/Off"
            else:
                info["checked_value"] = states[0]
                info["unchecked_value"] = states[1]
                info["note"] = "unusual checkbox states; check the result on the rendered page"
    elif kind == "/Ch":
        info["type"] = "choice"
        options = []
        for state in field.get("/_States_", []):
            if isinstance(state, (list, tuple)) and len(state) == 2:
                options.append({"value": str(state[0]), "text": str(state[1])})
            else:
                options.append({"value": str(state), "text": str(state)})
        info["choice_options"] = options
    else:
        info["type"] = "unknown (%s)" % kind
    value = field.get("/V")
    if value not in (None, ""):
        info["current_value"] = str(value)
    return info


def get_field_info(reader):
    fields = reader.get_fields() or {}
    by_id, radio_names = {}, set()
    for field_id, field in fields.items():
        if field.get("/Kids"):
            if field.get("/FT") == "/Btn":
                radio_names.add(field_id)
            continue
        by_id[field_id] = field_dict(field, field_id)
    radios = {}
    for page_index, page in enumerate(reader.pages):
        for annotation in page.get("/Annots", []) or []:
            annotation = annotation.get_object()
            field_id = full_field_id(annotation)
            rect = [round(float(v), 1) for v in annotation.get("/Rect", [0, 0, 0, 0])]
            if field_id in by_id:
                by_id[field_id]["page"] = page_index + 1
                by_id[field_id]["rect"] = rect
            elif field_id in radio_names:
                try:
                    on_values = [v for v in annotation["/AP"]["/N"] if v != "/Off"]
                except KeyError:
                    continue
                if len(on_values) == 1:
                    group = radios.setdefault(field_id, {"field_id": field_id, "type": "radio_group", "page": page_index + 1, "radio_options": []})
                    group["radio_options"].append({"value": str(on_values[0]), "rect": rect})
    located = [info for info in by_id.values() if "page" in info]
    for info in by_id.values():
        if "page" not in info:
            print("note: field %s has no place on any page and is left out" % info["field_id"], file=sys.stderr)

    def order(info):
        rect = info["radio_options"][0]["rect"] if "radio_options" in info else info.get("rect", [0, 0, 0, 0])
        return (info["page"], -rect[1], rect[0])

    return sorted(located + list(radios.values()), key=order)


def cmd_fields(args):
    reader = open_reader(args.pdf)
    info = get_field_info(reader)
    text = json.dumps(info, indent=2, ensure_ascii=False)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as handle:
            handle.write(text + "\n")
        print("wrote %s (%d fields)" % (args.out, len(info)))
    else:
        print(text)
    if not info:
        print("no fillable fields: this form is flat; use labels and overlay", file=sys.stderr)


def value_error(info, value):
    field_id = info["field_id"]
    kind = info["type"]
    if kind == "checkbox":
        allowed = [info.get("checked_value"), info.get("unchecked_value")]
        if value not in allowed:
            return "invalid value %r for checkbox %s; allowed: %s" % (value, field_id, allowed)
    elif kind == "radio_group":
        allowed = [option["value"] for option in info["radio_options"]]
        if value not in allowed:
            return "invalid value %r for radio group %s; allowed: %s" % (value, field_id, allowed)
    elif kind == "choice":
        allowed = [option["value"] for option in info["choice_options"]]
        if value not in allowed:
            return "invalid value %r for choice %s; allowed: %s" % (value, field_id, allowed)
    return None


def cmd_fill(args):
    pypdf = pypdf_module()
    refuse_same(args.pdf, args.output)
    with open(args.values, encoding="utf-8") as handle:
        requested = json.load(handle)
    if not isinstance(requested, list):
        fail("%s must hold a JSON list of {field_id, page, value}" % args.values)
    reader = open_reader(args.pdf)
    known = {info["field_id"]: info for info in get_field_info(reader)}
    problems, by_page = [], {}
    for item in requested:
        if "value" not in item:
            continue
        info = known.get(item.get("field_id"))
        if info is None:
            problems.append("%r is not a field of this PDF (run the fields subcommand)" % item.get("field_id"))
        elif item.get("page") != info["page"]:
            problems.append("field %s is on page %s, not %s" % (item["field_id"], info["page"], item.get("page")))
        else:
            message = value_error(info, item["value"])
            if message:
                problems.append(message)
            else:
                by_page.setdefault(info["page"], {})[item["field_id"]] = item["value"]
    if problems:
        for line in problems:
            print("error: " + line, file=sys.stderr)
        sys.exit(1)
    writer = pypdf.PdfWriter(clone_from=reader)
    for page, values in by_page.items():
        writer.update_page_form_field_values(writer.pages[page - 1], values, auto_regenerate=False)
    writer.set_need_appearances_writer(True)
    os.makedirs(os.path.dirname(os.path.abspath(args.output)), exist_ok=True)
    with open(args.output, "wb") as handle:
        writer.write(handle)
    print("wrote %s with %d field values" % (args.output, sum(len(v) for v in by_page.values())))
    print("render the result and view it: values must sit inside their boxes")


# ---------------------------------------------------------------- flat forms

def cmd_labels(args):
    if shutil.which("pdftotext") is None:
        fail("pdftotext is not installed on this computer", 2)
    reader = open_reader(args.pdf)
    result = subprocess.run(["pdftotext", "-bbox", args.pdf, "-"], capture_output=True, text=True, timeout=300)
    if result.returncode != 0:
        fail("pdftotext failed: %s" % result.stderr.strip())
    root = ET.fromstring(re.sub(r"<!DOCTYPE[^>]*>", "", result.stdout, count=1))
    ns = {"x": "http://www.w3.org/1999/xhtml"}
    pages, words = [], []
    for number, page in enumerate(root.iter("{http://www.w3.org/1999/xhtml}page"), start=1):
        pages.append({"page_number": number, "pdf_width": float(page.get("width")), "pdf_height": float(page.get("height"))})
        for word in page.iterfind("x:word", ns):
            words.append({"page": number, "text": word.text or "", "x0": round(float(word.get("xMin")), 1), "top": round(float(word.get("yMin")), 1),
                          "x1": round(float(word.get("xMax")), 1), "bottom": round(float(word.get("yMax")), 1)})
    text = json.dumps({"pages": pages, "words": words}, indent=1, ensure_ascii=False)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as handle:
            handle.write(text + "\n")
        print("wrote %s (%d pages, %d words)" % (args.out, len(pages), len(words)))
    else:
        print(text)
    if len(reader.pages) != len(pages):
        fail("page count differs between pypdf and pdftotext")
    if not words:
        print("no text layer: read the labels from rendered page images instead", file=sys.stderr)


def intersect(a, b):
    return not (a[0] >= b[2] or a[2] <= b[0] or a[1] >= b[3] or a[3] <= b[1])


def to_points(box, page, pdf_width, pdf_height):
    """A box in the units of the page record, as [x0, top, x1, bottom] in points from the top left."""
    if "pdf_width" in page:
        return list(box)
    sx, sy = pdf_width / page["image_width"], pdf_height / page["image_height"]
    return [box[0] * sx, box[1] * sy, box[2] * sx, box[3] * sy]


def check_boxes(data, sizes, font_width):
    problems, boxes = [], []
    for field in data["form_fields"]:
        number = field["page_number"]
        page = next((p for p in data["pages"] if p["page_number"] == number), None)
        if page is None or number not in sizes:
            problems.append("page %s of field %r is not in this PDF or in the pages list" % (number, field.get("description")))
            continue
        width, height = sizes[number]
        label = to_points(field["label_bounding_box"], page, width, height)
        entry = to_points(field["entry_bounding_box"], page, width, height)
        boxes.append((number, field["description"], "label", label))
        boxes.append((number, field["description"], "entry", entry))
        text = field.get("entry_text", {})
        size = text.get("font_size", 12)
        if text.get("text") and entry[3] - entry[1] < size:
            problems.append("entry box of %r is %.1f pt high, shorter than its font size %s" % (field["description"], entry[3] - entry[1], size))
        if text.get("text") and font_width(text["text"], text.get("font", "Helvetica"), size) > entry[2] - entry[0] + 0.5:
            problems.append("text of %r is wider than its entry box; shorten it, lower the font size or widen the box" % field["description"])
    for i, first in enumerate(boxes):
        for second in boxes[i + 1:]:
            if first[0] == second[0] and intersect(first[3], second[3]):
                problems.append("%s box of %r overlaps %s box of %r on page %d" % (first[2], first[1], second[2], second[1], first[0]))
    return problems


def cmd_overlay(args):
    pypdf = pypdf_module()
    refuse_same(args.pdf, args.output)
    try:
        from reportlab.pdfbase import pdfmetrics
        from reportlab.pdfbase.cidfonts import UnicodeCIDFont
        from reportlab.pdfgen import canvas
    except ImportError:
        fail("the reportlab library is not installed", 2)
    with open(args.fields, encoding="utf-8") as handle:
        data = json.load(handle)
    reader = open_reader(args.pdf)
    sizes = {i + 1: (float(p.mediabox.width), float(p.mediabox.height)) for i, p in enumerate(reader.pages)}
    for index, page in enumerate(reader.pages, start=1):
        if int(page.get("/Rotate", 0) or 0) % 360:
            fail("page %d is rotated; rotate it upright first with pdf_tools.py rotate" % index)
    cjk = {"STSong-Light", "STSong"}

    def font_width(text, font, size):
        if font in cjk:
            pdfmetrics.registerFont(UnicodeCIDFont("STSong-Light"))
            font = "STSong-Light"
        try:
            return pdfmetrics.stringWidth(text, font, size)
        except KeyError:
            fail("font %r is not available; use Helvetica, Times-Roman, Courier or STSong-Light" % font)

    problems = check_boxes(data, sizes, font_width)
    if problems:
        for line in problems:
            print("error: " + line, file=sys.stderr)
        sys.exit(1)
    per_page = {}
    for field in data["form_fields"]:
        if field.get("entry_text", {}).get("text"):
            per_page.setdefault(field["page_number"], []).append(field)
    writer = pypdf.PdfWriter(clone_from=reader)
    placed = 0
    for number, items in per_page.items():
        width, height = sizes[number]
        page_record = next(p for p in data["pages"] if p["page_number"] == number)
        buffer = io.BytesIO()
        pdf = canvas.Canvas(buffer, pagesize=(width, height))
        for field in items:
            entry = to_points(field["entry_bounding_box"], page_record, width, height)
            text = field["entry_text"]
            font = text.get("font", "Helvetica")
            if font in cjk:
                font = "STSong-Light"
            size = text.get("font_size", 12)
            pdf.setFont(font, size)
            baseline = height - entry[3] + (entry[3] - entry[1] - size) / 2 + size * 0.2
            pdf.drawString(entry[0] + 1, baseline, text["text"])
            placed += 1
        pdf.save()
        buffer.seek(0)
        writer.pages[number - 1].merge_page(pypdf.PdfReader(buffer).pages[0])
    os.makedirs(os.path.dirname(os.path.abspath(args.output)), exist_ok=True)
    with open(args.output, "wb") as handle:
        writer.write(handle)
    print("wrote %s with %d text entries" % (args.output, placed))
    print("render the result and view it: each text must sit on its line or in its box")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("fields", help="list fillable fields as JSON")
    p.add_argument("pdf")
    p.add_argument("--out")
    p.set_defaults(func=cmd_fields)
    p = sub.add_parser("fill", help="fill fillable fields from a JSON list")
    p.add_argument("pdf")
    p.add_argument("values", help="JSON list of {field_id, page, value}")
    p.add_argument("output")
    p.set_defaults(func=cmd_fill)
    p = sub.add_parser("labels", help="words of a flat form with their boxes")
    p.add_argument("pdf")
    p.add_argument("--out")
    p.set_defaults(func=cmd_labels)
    p = sub.add_parser("overlay", help="place text in boxes on a flat form")
    p.add_argument("pdf")
    p.add_argument("fields", help="fields.json")
    p.add_argument("output")
    p.set_defaults(func=cmd_overlay)
    args = parser.parse_args(argv)
    args.func(args)
    return 0


if __name__ == "__main__":
    sys.exit(main())
