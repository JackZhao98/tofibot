#!/usr/bin/env python3
"""Reconcile figures extracted from source documents.

Input is a JSON file written by the extraction step:
  {"documents": [{
      "id": "INV-1", "source": "invoices/inv1.pdf", "currency": "USD",
      "lines": [{"description": "...", "quantity": 2, "unit_price": 3.5, "amount": 7.0}],
      "subtotal": 7.0, "tax": 0.7, "total": 7.7,
      "unreadable": ["due date"]}]}
Any of lines, subtotal, tax or total may be absent when the source did not show
it; list what could not be read in "unreadable".

Checks, all in exact decimal arithmetic (tolerance 0.01 unless --tolerance):
  * each line: quantity x unit_price equals amount
  * sum of line amounts equals the stated subtotal
  * subtotal plus tax equals the stated total
  * the total equals the figure in an independent reference (--reference CSV
    with a --key column and a --field column), when one is given
  * duplicate ids, documents without a total, reference rows with no document
  * files in --sources-dir that no document names (never processed)
The report ends with the recomputed grand total beside the stated one and a
list of everything unread. Exit status 1 with --strict when any issue exists.
Usage: reconcile.py extracted.json [--reference ref.csv --key id --field total]
                    [--sources-dir DIR] [--json out.json] [--strict]
"""
from __future__ import annotations

import argparse
import csv
import json
import os
import re
import sys
from decimal import Decimal, InvalidOperation


def money(value):
    """Decimal from a number or text such as '$1,234.50' or '(12.00)'; None when absent or unreadable."""
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float, Decimal)):
        return Decimal(str(value))
    text = str(value).strip()
    if not text:
        return None
    negative = text.startswith("(") and text.endswith(")")
    text = re.sub(r"[\s$€£¥,()]|[A-Za-z]{3}$", "", text)
    if text.startswith("-"):
        negative, text = True, text[1:]
    try:
        number = Decimal(text)
    except InvalidOperation:
        return None
    return -number if negative else number


def close(a, b, tolerance):
    return abs(a - b) <= tolerance


def check_document(doc, tolerance):
    issues = []
    label = doc.get("id") or doc.get("source") or "?"
    lines_sum = Decimal(0)
    lines_seen = 0
    for number, line in enumerate(doc.get("lines") or [], start=1):
        amount = money(line.get("amount"))
        quantity, price = money(line.get("quantity")), money(line.get("unit_price"))
        if amount is None:
            issues.append({"doc": label, "kind": "line_amount_unread", "detail": "line %d has no readable amount" % number})
            continue
        lines_sum += amount
        lines_seen += 1
        if quantity is not None and price is not None and not close(quantity * price, amount, tolerance):
            issues.append({"doc": label, "kind": "line_arithmetic",
                           "detail": "line %d: %s x %s = %s but amount is %s" % (number, quantity, price, quantity * price, amount)})
    subtotal, tax, total = money(doc.get("subtotal")), money(doc.get("tax")), money(doc.get("total"))
    if lines_seen and subtotal is not None and not close(lines_sum, subtotal, tolerance):
        issues.append({"doc": label, "kind": "subtotal", "detail": "lines add to %s but subtotal is %s" % (lines_sum, subtotal)})
    if subtotal is not None and tax is not None and total is not None and not close(subtotal + tax, total, tolerance):
        issues.append({"doc": label, "kind": "total", "detail": "subtotal %s + tax %s = %s but total is %s" % (subtotal, tax, subtotal + tax, total)})
    if total is None:
        issues.append({"doc": label, "kind": "total_unread", "detail": "no readable total"})
    for item in doc.get("unreadable") or []:
        issues.append({"doc": label, "kind": "unreadable", "detail": str(item)})
    recomputed = None
    if subtotal is not None and tax is not None:
        recomputed = subtotal + tax
    elif lines_seen and tax is None:
        recomputed = lines_sum
    return issues, total, recomputed


def load_reference(path, key, field):
    rows = {}
    with open(path, newline="", encoding="utf-8-sig") as handle:
        for row in csv.DictReader(handle):
            if key not in row or field not in row:
                raise SystemExit("reference needs columns %r and %r; found %s" % (key, field, list(row)))
            rows[str(row[key]).strip()] = money(row[field])
    return rows


def reconcile(data, tolerance=Decimal("0.01"), reference=None, sources_dir=None):
    documents = data.get("documents") or []
    issues = []
    seen = {}
    stated_sum = Decimal(0)
    recomputed_sum = Decimal(0)
    per_doc = []
    for doc in documents:
        found, total, recomputed = check_document(doc, tolerance)
        issues.extend(found)
        label = doc.get("id") or doc.get("source") or "?"
        if label in seen:
            issues.append({"doc": label, "kind": "duplicate_id", "detail": "also appears in %s" % seen[label]})
        seen[label] = doc.get("source", "?")
        if total is not None:
            stated_sum += total
        if recomputed is not None:
            recomputed_sum += recomputed
        elif total is not None:
            recomputed_sum += total
        if reference is not None and doc.get("id") is not None:
            expected = reference.get(str(doc["id"]).strip())
            if str(doc["id"]).strip() not in reference:
                issues.append({"doc": label, "kind": "not_in_reference", "detail": "no row in the reference"})
            elif expected is None or total is None or not close(expected, total, tolerance):
                issues.append({"doc": label, "kind": "reference_mismatch", "detail": "total %s but reference says %s" % (total, expected)})
        per_doc.append({"id": label, "source": doc.get("source"), "total": None if total is None else str(total),
                        "issues": len([i for i in found if i["doc"] == label])})
    if reference is not None:
        known = {str(d.get("id")).strip() for d in documents if d.get("id") is not None}
        for ref_id in sorted(set(reference) - known):
            issues.append({"doc": ref_id, "kind": "reference_without_document", "detail": "reference row has no extracted document"})
    unprocessed = []
    if sources_dir:
        named = {os.path.normpath(str(d.get("source", ""))) for d in documents}
        named_base = {os.path.basename(n) for n in named}
        for folder, _, files in os.walk(sources_dir):
            for name in sorted(files):
                full = os.path.normpath(os.path.join(folder, name))
                if full not in named and name not in named_base and not name.startswith("."):
                    unprocessed.append(os.path.relpath(full, sources_dir))
        for item in unprocessed:
            issues.append({"doc": item, "kind": "source_not_processed", "detail": "file in sources-dir named by no document"})
    return {"documents": len(documents), "stated_total_sum": str(stated_sum), "recomputed_total_sum": str(recomputed_sum),
            "issues": issues, "per_document": per_doc}


def render(report):
    lines = ["documents checked: %d" % report["documents"]]
    by_kind = {}
    for issue in report["issues"]:
        by_kind.setdefault(issue["kind"], []).append(issue)
    flagged = sorted({i["doc"] for i in report["issues"] if i["kind"] not in ("unreadable", "source_not_processed", "reference_without_document")})
    lines.append("documents with a discrepancy: %d%s" % (len(flagged), " (" + ", ".join(flagged) + ")" if flagged else ""))
    lines.append("sum of stated totals:     %s" % report["stated_total_sum"])
    lines.append("sum of recomputed totals: %s" % report["recomputed_total_sum"])
    if not report["issues"]:
        lines.append("no issues found")
    for kind in sorted(by_kind):
        lines.append("")
        lines.append("%s (%d)" % (kind, len(by_kind[kind])))
        for issue in by_kind[kind]:
            lines.append("  - %s: %s" % (issue["doc"], issue["detail"]))
    return "\n".join(lines)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("extracted")
    parser.add_argument("--reference")
    parser.add_argument("--key", default="id")
    parser.add_argument("--field", default="total")
    parser.add_argument("--sources-dir")
    parser.add_argument("--tolerance", default="0.01")
    parser.add_argument("--json", dest="json_out")
    parser.add_argument("--strict", action="store_true")
    args = parser.parse_args(argv)
    with open(args.extracted, encoding="utf-8") as handle:
        data = json.load(handle)
    reference = load_reference(args.reference, args.key, args.field) if args.reference else None
    report = reconcile(data, Decimal(args.tolerance), reference, args.sources_dir)
    print(render(report))
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2)
    return 1 if args.strict and report["issues"] else 0


if __name__ == "__main__":
    sys.exit(main())
