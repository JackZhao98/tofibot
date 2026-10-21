---
name: spreadsheet
description: Build and reconcile XLSX workbooks with real numbers and formulas; verify the saved file.
---

# Spreadsheet work

Scripts live in the skill folder named in the header of this text, under `scripts/`. Run each with `--help` first; they are written for Python 3 with openpyxl on the computer.

## Principles

- Every claim in the reply is true of the delivered file, and you have verified that by opening the saved file.
- Numbers are stored as numbers. Totals and derived columns are formulas so the workbook stays correct when inputs change. Text that looks numeric is parsed once, at extraction, and anything unparseable is reported.
- Publish only the final workbook, after the checks below pass. Scratch files stay on the computer.

## Building

1. Gather the source data from the files the person provided; their paths are listed in the conversation. Read every source completely. Keep a list of what you could not read (pages, fields, files) and carry it into the reply.
2. Describe the workbook as a JSON spec and build it with `build_workbook.py`. It writes numeric cells, per-row formulas, SUM totals, header styling, filters and widths, and exits non-zero while listing every value it could not read.
3. Use `openpyxl` directly for anything the spec cannot express, keeping the same rules.

## Reconciling figures from documents

1. Extract each source document into the JSON shape described at the top of `reconcile.py`: the printed lines, subtotal, tax and total exactly as shown, plus an `unreadable` list.
2. Run `reconcile.py` with an independent reference whenever one exists (a statement, purchase order list or ledger), and with `--sources-dir` pointing at the folder you extracted from so unprocessed files surface.
3. The script recomputes line arithmetic, subtotals and totals in exact decimals and compares every document with the reference. Investigate every flagged document against its source before writing the summary; the count of discrepancies you report is the count the script lists, confirmed by reading those sources.
4. The reply states how many documents were checked, how many disagree and how, the grand total with the recomputed grand total beside it, and everything that could not be read.

## Verifying the saved file

1. Run `xlsx_summary.py` on the final workbook with `--recalc` so formula results are computed. Compare sheets, row counts and column sums with the source data using `--expect-sum` and `--expect-rows`.
2. Fix error cells and uncomputed formulas before delivery.
3. Quote figures in the reply from this summary output.
