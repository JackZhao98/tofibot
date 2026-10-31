---
name: spreadsheet
description: Read, build and check XLSX workbooks with real numbers and formulas.
---

# Spreadsheet work

## What good looks like

- Numbers are stored as numbers. Totals and derived columns are formulas, so the workbook stays correct when its inputs change. Headers, widths, number formats and filters make the sheet readable without explanation.
- Every figure traces to a source the person provided. Read each source completely, and say plainly what could not be read.
- Arithmetic that matters is checked independently of the workbook, from the source documents, before it is reported.

## Verifying

- Open the saved file the way the recipient will: formulas calculated, no error cells, the sheets and rows the reply describes, the totals the reply quotes.
- Every claim in the reply is true of the delivered file. Figures in the reply are read from the saved file, and the reply lists any discrepancy found in the sources and anything left out.
- Publish only the final workbook; scratch files stay on the Bot computer.

## Tools

`openpyxl` and the other Python libraries are installed; write the code the task needs. `scripts/xlsx_summary.py` recalculates formulas with LibreOffice and prints each sheet's structure, formula count, error cells and numeric column sums. `--help` describes it.
