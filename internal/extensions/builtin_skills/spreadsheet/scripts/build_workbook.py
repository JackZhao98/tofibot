#!/usr/bin/env python3
"""Build an XLSX workbook from a JSON spec with openpyxl.

Numbers are written as numbers, formulas as live formulas, and anything that
cannot be read as the declared type is reported instead of written silently.

Spec shape (JSON):
  {"sheets": [{
      "name": "Invoices",
      "columns": [{"key": "id", "title": "Invoice"},
                  {"key": "qty", "title": "Qty", "type": "number"},
                  {"key": "price", "title": "Unit price", "type": "currency"},
                  {"key": "amount", "title": "Amount", "type": "currency",
                   "formula": "={qty}*{price}"}],
      "rows": [{"id": "A-1", "qty": "2", "price": "$3.50"}],
      "totals": ["amount"]}]}

Column types: text (default), number, integer, currency, percent, date.
"formula" is a template evaluated per row; {key} becomes that row's cell.
"totals" adds a final row with SUM formulas for the listed column keys.
Text such as "1,234.50", "$12" or "(45.00)" is parsed for numeric types.
Usage: build_workbook.py spec.json out.xlsx
Exit status is 1 when any value could not be read (the file is still written).
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import date, datetime
from decimal import Decimal, InvalidOperation

FORMATS = {
    "number": "#,##0.00",
    "integer": "#,##0",
    "currency": "#,##0.00",
    "percent": "0.0%",
    "date": "yyyy-mm-dd",
}


def parse_number(value):
    """Return a Decimal for a number or numeric text, else None."""
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float, Decimal)):
        return Decimal(str(value))
    if not isinstance(value, str):
        return None
    text = value.strip()
    if not text:
        return None
    negative = text.startswith("(") and text.endswith(")")
    text = re.sub(r"[\s$€£¥,()]", "", text)
    percent = text.endswith("%")
    text = text.rstrip("%")
    if text.startswith("-"):
        negative, text = True, text[1:]
    try:
        number = Decimal(text)
    except InvalidOperation:
        return None
    if percent:
        number /= 100
    return -number if negative else number


def parse_date(value):
    if isinstance(value, (date, datetime)):
        return value
    if not isinstance(value, str):
        return None
    for fmt in ("%Y-%m-%d", "%Y/%m/%d", "%m/%d/%Y", "%d %b %Y", "%b %d, %Y"):
        try:
            return datetime.strptime(value.strip(), fmt)
        except ValueError:
            continue
    return None


def column_letter(index):
    from openpyxl.utils import get_column_letter
    return get_column_letter(index)


def build(spec, out_path):
    from openpyxl import Workbook
    from openpyxl.styles import Alignment, Font, PatternFill

    problems = []
    workbook = Workbook()
    workbook.remove(workbook.active)
    for sheet_spec in spec.get("sheets", []):
        name = str(sheet_spec.get("name") or "Sheet")[:31]
        columns = sheet_spec.get("columns") or []
        rows = sheet_spec.get("rows") or []
        if not columns:
            problems.append("sheet %s has no columns" % name)
            continue
        sheet = workbook.create_sheet(name)
        letters = {col["key"]: column_letter(i + 1) for i, col in enumerate(columns)}
        for i, col in enumerate(columns, start=1):
            cell = sheet.cell(row=1, column=i, value=col.get("title", col["key"]))
            cell.font = Font(bold=True, color="FFFFFF")
            cell.fill = PatternFill("solid", fgColor="305496")
            cell.alignment = Alignment(horizontal="center", vertical="center", wrap_text=True)
        for r, row in enumerate(rows, start=2):
            for i, col in enumerate(columns, start=1):
                kind = col.get("type", "text")
                key = col["key"]
                cell = sheet.cell(row=r, column=i)
                template = col.get("formula")
                if template and row.get(key) in (None, ""):
                    refs = {k: "%s%d" % (letter, r) for k, letter in letters.items()}
                    try:
                        cell.value = template.format(**refs)
                    except (KeyError, IndexError) as exc:
                        problems.append("%s row %d: formula refers to unknown column %s" % (name, r, exc))
                        continue
                else:
                    raw = row.get(key)
                    if raw is None or raw == "":
                        continue
                    if kind in ("number", "integer", "currency", "percent"):
                        number = parse_number(raw)
                        if number is None:
                            problems.append("%s row %d column %s: %r is not a number" % (name, r, key, raw))
                            cell.value = None
                            continue
                        cell.value = int(number) if kind == "integer" and number == number.to_integral() else float(number)
                    elif kind == "date":
                        parsed = parse_date(raw)
                        if parsed is None:
                            problems.append("%s row %d column %s: %r is not a date" % (name, r, key, raw))
                            continue
                        cell.value = parsed
                    else:
                        cell.value = str(raw)
                if kind in FORMATS:
                    cell.number_format = col.get("format", FORMATS[kind])
        last = len(rows) + 1
        totals = sheet_spec.get("totals") or []
        if totals and rows:
            total_row = last + 1
            sheet.cell(row=total_row, column=1, value="Total").font = Font(bold=True)
            for key in totals:
                if key not in letters:
                    problems.append("%s: totals names unknown column %s" % (name, key))
                    continue
                letter = letters[key]
                cell = sheet["%s%d" % (letter, total_row)]
                cell.value = "=SUM(%s2:%s%d)" % (letter, letter, last)
                cell.font = Font(bold=True)
                kind = next(c.get("type", "text") for c in columns if c["key"] == key)
                if kind in FORMATS:
                    cell.number_format = FORMATS[kind]
        sheet.freeze_panes = "A2"
        if rows:
            sheet.auto_filter.ref = "A1:%s%d" % (column_letter(len(columns)), last)
        for i, col in enumerate(columns, start=1):
            width = max([len(str(col.get("title", col["key"])))] + [len(str(r.get(col["key"], ""))) for r in rows])
            sheet.column_dimensions[column_letter(i)].width = min(max(width + 2, 10), 60)
    if not workbook.sheetnames:
        workbook.create_sheet("Sheet1")
    workbook.save(out_path)
    return problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("spec")
    parser.add_argument("out")
    args = parser.parse_args(argv)
    try:
        import openpyxl  # noqa: F401
    except ImportError:
        print("openpyxl is not installed in this Python", file=sys.stderr)
        return 2
    with open(args.spec, encoding="utf-8") as handle:
        spec = json.load(handle)
    problems = build(spec, args.out)
    print("wrote %s with %d sheet(s)" % (args.out, len(spec.get("sheets", []))))
    if problems:
        print("COULD NOT READ %d value(s):" % len(problems))
        for item in problems:
            print("  - " + item)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
