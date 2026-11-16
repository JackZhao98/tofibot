---
name: web-to-sheet
description: Turn a web page's table or list into an XLSX spreadsheet with typed columns and the source link.
requires: [computer]
kind: deliverable
---

# Web page to spreadsheet

## What good looks like

- The rows in the workbook are the rows of the table or list on the page: the count matches, no row is lost to a footnote or a merged cell, and none is invented.
- The first row is a header row. Numbers are stored as numbers, dates as dates, and currency and percent as numbers with a number format; bracketed footnote numbers are removed from the values.
- The workbook names its source: the page URL and the time it was read sit in a Source sheet or a labelled cell, so the data can be traced and refreshed.
- The reply says which table or list was taken, how many rows it holds, and what was cleaned or left out.

## Reading the page

- Read the page with web_fetch first. When the page shows an empty shell, needs a script to build its table, or sits behind a sign-in the person has given the Bot computer, open it in the Bot computer browser (computer_browser) and read the rendered table from there.
- A page can hold several tables. List them with their sizes and headers before choosing, and take the one the request names. When two tables fit, take both into separate sheets or ask which one is meant.
- A long table can be paged, sorted by script or limited to the first rows by the site. Look for a next-page link or a show-all control and read every page; say so when only part of the table could be read.

## Extracting

- HTML tables: `pandas.read_html` on the page text (`io.StringIO(html)`) returns one frame per table; header rows with merged cells arrive as multi-level columns that are joined into one header row. Save the page HTML to a file on the Bot computer with curl or the browser when web_fetch returned only text.
- Lists and cards without table markup: parse the repeating element into rows with the standard library or lxml, one column per field, and check that every row has every field.
- Keep the page's own column names. Add a column only when it carries information from outside the cell, like the section heading a row sits under.

## Cleaning

- Strip footnote marks, thousands separators, currency signs and non-breaking spaces; convert with `pd.to_numeric` and `pd.to_datetime`. A value that does not convert stays as text and is reported.
- Empty cells stay empty. Dashes and not-available marks become empty cells, and the reply says so.
- Percent values keep their meaning: 12.5% is stored as 0.125 with a percent format.

## The workbook

- Follow the spreadsheet Skill for the workbook: read internal/extensions/builtin_skills/spreadsheet/SKILL.md with read_skill when it has not been read in this run. Totals and derived columns the person asked for are formulas.
- `scripts/html_tables_to_xlsx.py` lists the tables of a page, writes one as a Data sheet with a bold header row, frozen header, filter, widths and typed columns plus a Source sheet with the URL (as a link), the read time in UTC and the table number, and compares the written row count with the rows counted in the page markup. `--help` describes it. Write openpyxl code directly when the page needs lists, several tables or extra sheets.

## Verifying

- Count the rows on the page and in the workbook and compare them; a table that the site shows paged is counted over all pages.
- Open the saved file the way the recipient will: header row, a sample of rows from the top, the middle and the bottom compared with the page, number columns that sum and sort as numbers, the Source sheet with a working URL.
- Every claim in the reply is true of the delivered file. The row count, columns and cleaning described are those of the final workbook, and rows or columns left out are listed with the reason.
- Publish only the final workbook; scratch HTML and intermediate files stay on the Bot computer.

## Delivery

- Send the XLSX as a send_message attachment (path) with the page URL, the read time, the row count and the cleaning notes in the message text.

## When something cannot be done

- A page that cannot be read, a table that loads only after a sign-in the Bot does not have, or a site that refuses automated reading is reported with what was tried. Rows copied from memory are never delivered as page data.
