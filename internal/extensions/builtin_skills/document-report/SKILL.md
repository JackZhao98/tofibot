---
name: document-report
description: Write DOCX or PDF documents and reports; look at the rendered pages before delivering.
---

# Document and report

## What good looks like

- A clear structure: a title, headings that tell the reader where they are, short paragraphs, tables for comparisons and charts where a picture shows the data better than words.
- The content comes from the sources you read. Figures are copied from the data, charts are drawn from the real numbers and embedded as images, and the limits of the data are stated.
- The layout survives rendering: no clipped tables, overlapping text, unreadable axes or blank pages.

## Verifying

- Look at the pages the way the reader will: render them to images and view them. A rendered image reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. Check that each chart, table and figure the reply mentions is on a page and agrees with the numbers in the text.
- Every claim in the reply is true of the delivered file. Page counts, charts and tables are mentioned only after you saw them in the rendered pages.
- Publish only the final file.

## Tools

`python-docx`, `reportlab` and `matplotlib` are installed; write the code the task needs. `scripts/render_preview.py` converts a DOCX, PPTX, XLSX or PDF to a PDF with LibreOffice, renders each page to a PNG with a contact sheet, and reports the page count, embedded images and text length. `--help` describes it.
