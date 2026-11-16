---
name: spreadsheet
description: Read, build and check XLSX workbooks with real numbers and formulas.
requires: [computer]
kind: deliverable
---

# Spreadsheet work

## What good looks like

- Numbers are stored as numbers. Totals and derived columns are formulas, so the workbook stays correct when its inputs change. Headers, widths, number formats and filters make the sheet readable without explanation.
- Every figure traces to a source the person provided. Read each source completely, and say plainly what could not be read.
- Arithmetic that matters is checked independently of the workbook, from the source documents, before it is reported.
- The request is followed literally: the sheet names, column headers and formulas the person spelled out are the ones in the file.
- Each assumption and hardcoded input sits in a labeled cell or carries a note naming its source; figures the person typed are said to come from the person.
- Editing an existing file follows its conventions, which override this list. Input cells are found first (a distinct font color, fill or shading marks them), only they are written, and existing formulas stay untouched.
- A workbook made for someone else to fill in has a short legend naming the cells to edit.

## Building

- Write each formula into its cell as a string that starts with an equals sign. Never store a number computed in Python where a formula belongs.
- A formula cell needs its own `number_format`: nothing infers a date, percentage or currency format, so a date shows as a serial number and 30% as 0.3. Percentages are stored as fractions.
- Choose functions that evaluate on the Bot computer. Prefer `SUMIFS`, `INDEX`, `MATCH`, `IFERROR` and `SUMPRODUCT`. `TEXTJOIN`, `CONCAT`, `IFS`, `SWITCH`, `MAXIFS` and `MINIFS` evaluate only when written with the `_xlfn.` prefix. `XLOOKUP`, `XMATCH`, `SORT`, `FILTER`, `UNIQUE`, `SEQUENCE` and `LET` do not evaluate here; use `INDEX`/`MATCH` and sort, filter or de-duplicate in Python before writing cells.
- Quote a sheet name that contains a space in cross-sheet references. Write only the top-left cell of a merged range.
- Guard a divisor that can be zero, and keep one formula pattern across a row or column of periods; a lone edited cell in a series is the commonest silent error.
- Financial models, unless the person or the file says otherwise: blue text for hardcoded inputs, black for formulas, green for links to another sheet, yellow fill for key assumptions; the unit named in the header; negatives in parentheses; zeros shown as a dash; years stored as text; every assumption in its own cell and referenced by the formulas that use it.
- A workbook loaded with `data_only=True` has no formulas left, and saving it replaces every formula with its last value for good. Open a second copy with `data_only=True` to read values and never save it. `.xlsm` files need `keep_vba=True` to keep their macros. Partly bold cell text needs `rich_text=True` to survive a save. Saving drops text boxes, shapes and slicers from an uploaded file, so search `xl/drawings/` and `xl/slicers/` first and tell the person when they exist.

## Verifying

- `scripts/xlsx_recalc.py in.xlsx out.xlsx` has LibreOffice calculate a copy, keeps every formula, stores the calculated values (openpyxl writes none, so previewers and pandas would otherwise show empty cells) and exits non-zero when any cell holds `#REF!`, `#DIV/0!`, `#VALUE!`, `#NAME?` or `#N/A`, when a formula lost its text, or when a formula has no value. Fix what it lists and run it again until it exits 0. Deliver `out.xlsx`. It never edits its input. `--check book.xlsx` reads a file as saved without LibreOffice.
- A clean exit shows the formulas evaluate. It does not show they are right: an off-by-one range gives a clean file with wrong numbers. Compare two or three totals with arithmetic done from the source figures.
- Open the saved file the way the recipient will: formulas calculated, no error cells, the sheets and rows the reply describes, the totals the reply quotes.
- Every claim in the reply is true of the delivered file. Figures in the reply are read from the saved file, and the reply lists any discrepancy found in the sources and anything left out.
- Publish only the final workbook; scratch files stay on the Bot computer.

## Tools

`openpyxl` and the other Python libraries are installed; write the code the task needs. `scripts/xlsx_recalc.py` produces the calculated deliverable and fails on errors. `scripts/xlsx_summary.py` prints each sheet's structure, formula count, error cells and numeric column sums, with `--recalc` to calculate first. `--help` describes each.
