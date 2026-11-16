---
name: pdf
description: Read, split, merge, rotate PDFs; extract tables and text, fill forms, OCR scans; view pages.
requires: [computer]
kind: deliverable
---

# PDF files

Most jobs here end in a new file (a spreadsheet of tables, a split or merged PDF, a filled form, a searchable scan), so the Skill is a deliverable one. A job that only reads a PDF ends in the reply and delivers no file.

## What good looks like

- The result comes from the pages of the file. Text, table cells and field values are copied from what the PDF holds, numbers keep their digits, and a value that could not be read is reported as unreadable.
- The page picture decides. After any extraction, merge, split, rotation or form fill, the pages involved are rendered and viewed, and the result agrees with what the page shows.
- Page counts, page order and orientation in the result are the ones the request asked for.
- The original file is left as it was; every result is a new file.

## Probing

- `python3 scripts/pdf_tools.py info FILE.pdf` reports pages, sizes, rotation, text characters per page, images, form fields and encryption. A page with no text characters is a scan or a drawing and needs OCR. A file with form fields is a fillable form.
- A protected PDF opens with `--password-file`, a file whose first line holds the password the person gave. A password is never typed on a command line or repeated in the reply, and the file is deleted when the job is done. A file whose encryption pypdf cannot open reports the missing library; user-space installs follow the install-software Skill.

## Reading

- `pdf_tools.py text FILE.pdf` keeps the column layout through pdftotext; `--raw` gives reading order, `--first` and `--last` select pages, `--out` writes a file.
- Text in a scan is found with the OCR job below. A PDF whose text comes out as broken glyphs is read from its rendered pages instead.
- Embedded pictures are listed with `pdfimages -list FILE.pdf` and written out with `pdfimages -all FILE.pdf prefix`.
- Metadata and links come from pypdf: `PdfReader(path).metadata` and the `/Annots` entry of a page.

## Tables to a spreadsheet

- `pdf_tools.py tables FILE.pdf --out tables.xlsx` finds runs of lines with aligned columns in the text layer and writes one sheet per table, named by page and table number, with plain numbers stored as numbers. `--csv-dir DIR` writes csv files, `--pages` limits the pages.
- Layout detection reads the text positions. A cell that wraps onto two lines, a merged header or a table without aligned columns comes out shifted. Render the page, compare every row and column with the picture, and repair the sheet with openpyxl where it differs. A table that continues over pages is joined by the request, not by the script.
- Totals and formulas in the source are values in the sheet. When the request wants live formulas, build them afterwards with the spreadsheet Skill.

## Split, merge, rotate

- `pdf_tools.py split FILE.pdf --outdir DIR` writes one file per page; `--every N` writes N pages per file and `--ranges '1-3;4-6'` writes one file per group.
- `pdf_tools.py merge A.pdf B.pdf::2-4 -o out.pdf` joins files in the order given; `FILE::RANGE` takes only those pages.
- `pdf_tools.py rotate FILE.pdf -o out.pdf --degrees 90 --pages 2` turns pages clockwise by a multiple of 90. Orientation is judged from the rendered page, since the stored rotation and the visible direction of a scan can differ.

## Scans

- `pdf_tools.py ocr FILE.pdf --outdir DIR` renders the pages at 300 dpi and writes `DIR/ocr.txt` with a header per page; `--searchable out.pdf` writes a PDF whose pages carry a text layer. The default language is English. Other languages need their Tesseract data in a user directory, set through TESSDATA_PREFIX; fetching it follows the install-software Skill, and `--lang` names the code.
- OCR output is approximate. Digits, names and amounts are compared with the page image before they are used, and a line that is unclear is reported as unclear.

## Forms

- A form with fillable fields and a flat form are filled differently, and the long procedure is in `references/forms.md`, read with read_skill_file. `scripts/pdf_form.py fields` lists the fillable fields, `fill` writes values into them, `labels` lists the words of a flat form with their boxes, and `overlay` places text on a flat form.
- Values the person has not given are left empty and named in the reply. A form value is never invented.

## Looking at the result

- `pdf_tools.py render FILE.pdf --outdir DIR --dpi 80` writes one PNG per page. A rendered image reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. Use a higher `--dpi` or crop a page region with ImageMagick to read small print.
- For a filled form, check that each value sits inside its box and is readable. For a merge, check page order and that no page is blank. For a split, view the first and last page of each part.

## Other jobs

- More command line and library recipes (crop, watermark, metadata, images, repair, creating a PDF with reportlab) are in `references/reference.md`, read with read_skill_file.
- A new document written from scratch belongs to the document-report Skill.

## Delivering

- Send the final file with send_message attachments, giving its path.
- Every claim in the reply is true of the delivered file. Page counts, table counts, field names and values are the ones in the final file, and the reply says what could not be read or filled and why.
- Publish only the final file; page images, logs and scratch copies stay on the Bot computer, and the password file is removed.
