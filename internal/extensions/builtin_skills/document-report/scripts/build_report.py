#!/usr/bin/env python3
"""Build a DOCX or PDF report with embedded matplotlib charts from a JSON spec.

Spec (JSON):
  {"title": "...", "subtitle": "...", "author": "...", "date": "...",
   "blocks": [
     {"type": "heading", "level": 1, "text": "..."},
     {"type": "paragraph", "text": "..."},
     {"type": "bullets", "items": ["...", "..."]},
     {"type": "table", "columns": ["A", "B"], "rows": [["x", 1], ["y", 2]], "caption": "..."},
     {"type": "chart", "kind": "bar|barh|line|pie", "title": "...",
      "categories": ["Q1", "Q2"], "series": [{"name": "Revenue", "values": [1, 2]}],
      "ylabel": "...", "caption": "..."},
     {"type": "image", "path": "figure.png", "caption": "..."},
     {"type": "pagebreak"}]}

Charts are drawn with matplotlib (Agg) as PNG and embedded in the file; chart
data must be numeric and match the category count, or the build stops.
--format docx uses python-docx, --format pdf uses reportlab.
--font PATH registers a TrueType font for body text and charts (needed for
scripts the default fonts lack).
Usage: build_report.py spec.json out.docx|out.pdf [--format docx|pdf]
       [--font font.ttf] [--charts-dir DIR]
The final line reports the number of charts and tables embedded.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile

PALETTE = ["#305496", "#C55A11", "#548235", "#7F6000", "#7030A0", "#2E75B6"]


class SpecError(Exception):
    pass


def numeric(values, where):
    out = []
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise SpecError("%s: %r is not a number" % (where, value))
        out.append(float(value))
    return out


def draw_chart(block, path, font=None):
    import matplotlib
    matplotlib.use("Agg")
    from matplotlib import font_manager, pyplot as plt

    if font:
        font_manager.fontManager.addfont(font)
        plt.rcParams["font.family"] = font_manager.FontProperties(fname=font).get_name()
    categories = [str(c) for c in block.get("categories") or []]
    series = block.get("series") or []
    kind = block.get("kind", "bar")
    if not categories or not series:
        raise SpecError("chart %r needs categories and series" % block.get("title"))
    data = []
    for item in series:
        values = numeric(item.get("values") or [], "chart %r series %r" % (block.get("title"), item.get("name")))
        if len(values) != len(categories):
            raise SpecError("chart %r series %r has %d values for %d categories" % (block.get("title"), item.get("name"), len(values), len(categories)))
        data.append((str(item.get("name", "")), values))
    fig, ax = plt.subplots(figsize=(7, 3.8), dpi=150)
    long_labels = max(len(c) for c in categories) > 8
    if kind == "pie":
        ax.pie(data[0][1], labels=categories, colors=PALETTE, autopct="%1.1f%%", startangle=90)
        ax.axis("equal")
    elif kind == "line":
        for i, (name, values) in enumerate(data):
            ax.plot(categories, values, marker="o", color=PALETTE[i % len(PALETTE)], label=name)
    elif kind in ("bar", "barh"):
        width = 0.8 / len(data)
        for i, (name, values) in enumerate(data):
            offsets = [x + i * width - 0.4 + width / 2 for x in range(len(categories))]
            (ax.barh if kind == "barh" else ax.bar)(offsets, values, width, color=PALETTE[i % len(PALETTE)], label=name)
        ticks = list(range(len(categories)))
        if kind == "barh":
            ax.set_yticks(ticks)
            ax.set_yticklabels(categories)
        else:
            ax.set_xticks(ticks)
            ax.set_xticklabels(categories, rotation=30 if long_labels else 0, ha="right" if long_labels else "center")
    else:
        raise SpecError("unknown chart kind %r" % kind)
    if kind != "pie":
        for side in ("top", "right"):
            ax.spines[side].set_visible(False)
        ax.grid(axis="x" if kind == "barh" else "y", alpha=0.3)
        if block.get("ylabel"):
            (ax.set_xlabel if kind == "barh" else ax.set_ylabel)(block["ylabel"])
        if len(data) > 1:
            ax.legend(frameon=False)
    if block.get("title"):
        ax.set_title(block["title"], fontsize=11, loc="left")
    fig.tight_layout()
    fig.savefig(path)
    plt.close(fig)


def prepare_charts(spec, folder, font):
    paths = {}
    count = 0
    for index, block in enumerate(spec.get("blocks") or []):
        if block.get("type") == "chart":
            count += 1
            paths[index] = os.path.join(folder, "chart-%02d.png" % count)
            draw_chart(block, paths[index], font)
    return paths


def build_docx(spec, out, charts, font):
    from docx import Document
    from docx.enum.text import WD_ALIGN_PARAGRAPH
    from docx.shared import Inches, Pt

    doc = Document()
    if font:
        doc.styles["Normal"].font.name = os.path.splitext(os.path.basename(font))[0]
    doc.styles["Normal"].font.size = Pt(11)
    doc.add_heading(spec.get("title", "Report"), 0)
    meta = " · ".join(str(spec[k]) for k in ("subtitle", "author", "date") if spec.get(k))
    if meta:
        doc.add_paragraph(meta)
    tables = 0
    for index, block in enumerate(spec.get("blocks") or []):
        kind = block.get("type")
        if kind == "heading":
            doc.add_heading(block["text"], int(block.get("level", 1)))
        elif kind == "paragraph":
            doc.add_paragraph(block["text"])
        elif kind == "bullets":
            for item in block.get("items") or []:
                doc.add_paragraph(str(item), style="List Bullet")
        elif kind == "table":
            tables += 1
            columns = block.get("columns") or []
            table = doc.add_table(rows=1, cols=len(columns))
            table.style = "Light Grid Accent 1"
            for i, name in enumerate(columns):
                table.rows[0].cells[i].text = str(name)
            for row in block.get("rows") or []:
                cells = table.add_row().cells
                for i, value in enumerate(row[:len(columns)]):
                    cells[i].text = "" if value is None else str(value)
            if block.get("caption"):
                doc.add_paragraph(block["caption"]).runs[0].italic = True
        elif kind in ("chart", "image"):
            path = charts[index] if kind == "chart" else block["path"]
            if not os.path.exists(path):
                raise SpecError("image file missing: %s" % path)
            doc.add_picture(path, width=Inches(6.0))
            doc.paragraphs[-1].alignment = WD_ALIGN_PARAGRAPH.CENTER
            if block.get("caption"):
                caption = doc.add_paragraph(block["caption"])
                caption.alignment = WD_ALIGN_PARAGRAPH.CENTER
        elif kind == "pagebreak":
            doc.add_page_break()
        else:
            raise SpecError("unknown block type %r" % kind)
    doc.save(out)
    return tables


def build_pdf(spec, out, charts, font):
    from xml.sax.saxutils import escape

    from reportlab.lib import colors
    from reportlab.lib.pagesizes import letter
    from reportlab.lib.styles import getSampleStyleSheet
    from reportlab.lib.units import inch
    from reportlab.pdfbase import pdfmetrics
    from reportlab.pdfbase.ttfonts import TTFont
    from reportlab.platypus import Image, ListFlowable, ListItem, PageBreak, Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle

    styles = getSampleStyleSheet()
    if font:
        pdfmetrics.registerFont(TTFont("ReportFont", font))
        for style in styles.byName.values():
            style.fontName = "ReportFont"
    story = [Paragraph(escape(str(spec.get("title", "Report"))), styles["Title"])]
    meta = " · ".join(str(spec[k]) for k in ("subtitle", "author", "date") if spec.get(k))
    if meta:
        story.append(Paragraph(escape(meta), styles["Normal"]))
    story.append(Spacer(1, 0.2 * inch))
    tables = 0
    for index, block in enumerate(spec.get("blocks") or []):
        kind = block.get("type")
        if kind == "heading":
            story.append(Paragraph(escape(block["text"]), styles["Heading%d" % min(max(int(block.get("level", 1)), 1), 3)]))
        elif kind == "paragraph":
            story += [Paragraph(escape(block["text"]), styles["BodyText"]), Spacer(1, 6)]
        elif kind == "bullets":
            items = [ListItem(Paragraph(escape(str(i)), styles["BodyText"])) for i in block.get("items") or []]
            story.append(ListFlowable(items, bulletType="bullet"))
        elif kind == "table":
            tables += 1
            columns = block.get("columns") or []
            data = [[Paragraph(escape(str(c)), styles["BodyText"]) for c in columns]]
            for row in block.get("rows") or []:
                data.append([Paragraph(escape("" if v is None else str(v)), styles["BodyText"]) for v in row[:len(columns)]])
            table = Table(data, repeatRows=1)
            table.setStyle(TableStyle([("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#D9E2F3")),
                                       ("GRID", (0, 0), (-1, -1), 0.4, colors.grey), ("VALIGN", (0, 0), (-1, -1), "TOP")]))
            story += [table, Spacer(1, 8)]
            if block.get("caption"):
                story.append(Paragraph(escape(block["caption"]), styles["Italic"]))
        elif kind in ("chart", "image"):
            path = charts[index] if kind == "chart" else block["path"]
            if not os.path.exists(path):
                raise SpecError("image file missing: %s" % path)
            from PIL import Image as PILImage
            with PILImage.open(path) as probe:
                w, h = probe.size
            width = 6.0 * inch
            story.append(Image(path, width=width, height=width * h / w))
            if block.get("caption"):
                story.append(Paragraph(escape(block["caption"]), styles["Italic"]))
            story.append(Spacer(1, 8))
        elif kind == "pagebreak":
            story.append(PageBreak())
        else:
            raise SpecError("unknown block type %r" % kind)
    SimpleDocTemplate(out, pagesize=letter, title=str(spec.get("title", "Report")), author=str(spec.get("author", ""))).build(story)
    return tables


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("spec")
    parser.add_argument("out")
    parser.add_argument("--format", choices=("docx", "pdf"))
    parser.add_argument("--font")
    parser.add_argument("--charts-dir")
    args = parser.parse_args(argv)
    fmt = args.format or ("pdf" if args.out.lower().endswith(".pdf") else "docx")
    with open(args.spec, encoding="utf-8") as handle:
        spec = json.load(handle)
    folder = args.charts_dir or tempfile.mkdtemp(prefix="report-charts-")
    os.makedirs(folder, exist_ok=True)
    try:
        charts = prepare_charts(spec, folder, args.font)
        tables = (build_pdf if fmt == "pdf" else build_docx)(spec, args.out, charts, args.font)
    except SpecError as exc:
        print("spec error: %s" % exc, file=sys.stderr)
        return 1
    except ImportError as exc:
        print("missing library: %s" % exc, file=sys.stderr)
        return 2
    images = sum(1 for b in spec.get("blocks") or [] if b.get("type") in ("chart", "image"))
    print("wrote %s (%s): %d chart(s) and image(s) embedded, %d table(s)" % (args.out, fmt, images, tables))
    return 0


if __name__ == "__main__":
    sys.exit(main())
