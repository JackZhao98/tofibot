---
name: file-convert
description: Change a file's format between Word, PDF, Markdown, HTML, text, images, zip and tar; check it.
requires: [computer]
kind: deliverable
---

# Converting a file to another format

This Skill changes the format of an existing file. Writing a new report belongs to document-report, reading or editing PDF pages to pdf, and workbook formulas to spreadsheet.

## What good looks like

- The converted file opens in the target program and holds the content of the original: every heading, paragraph, table, list and picture the original had, in the same order.
- The reply says which parts of the layout the conversion cannot keep.
- The original file is kept. The new file has the same base name with the new extension, in a different folder or with a different extension.
- Text in Chinese, Japanese and Korean arrives as the same characters, not as boxes or question marks.

## Which tool for which pair

- Markdown or HTML to DOCX: `pandoc in.md -o out.docx`. Tables, headings, lists, links, code blocks and pictures come across as Word styles. A reference document sets fonts and page size: `--reference-doc=template.docx`. For a Chinese document set a CJK font in the template or after conversion.
- DOCX to Markdown: `pandoc in.docx -t gfm --wrap=none --extract-media=media -o out.md` writes the pictures into the media folder. Merged cells, text boxes, comments and tracked changes do not survive; tracked changes are accepted with `--track-changes=accept`.
- DOCX, ODT, PPTX, XLSX to PDF, and between office formats: `scripts/soffice_convert.py in.docx pdf --outdir out/`, or `soffice --headless --convert-to pdf --outdir out in.docx`. The script uses a private profile and a timeout. LibreOffice lays the pages out with its own fonts, so line breaks and page breaks can differ from Word.
- Legacy DOC, XLS, PPT to the modern formats: the same command with docx, xlsx or pptx as the target.
- Markdown or HTML to PDF: convert to DOCX or HTML and then to PDF with LibreOffice: `pandoc in.md -s -o page.html` followed by `soffice --headless --convert-to pdf page.html`. Check CJK fonts in the render.
- PDF to text: `pdftotext -layout in.pdf out.txt` keeps columns; without `-layout` the reading order is kept. A scanned PDF has no text layer and needs OCR with the pdf Skill.
- PDF to pictures: `pdftoppm -r 150 -png in.pdf page` writes one PNG per page; `-jpeg` for smaller files. Pictures to PDF: `convert a.png b.png out.pdf` or `img2pdf` when installed.
- PDF to DOCX or Markdown is a reconstruction. Text-heavy PDFs come out usable through `pdftotext` followed by `pandoc`; designed layouts, columns and tables do not. LibreOffice opens a PDF in Draw: `soffice --headless --infilter='writer_pdf_import' --convert-to docx in.pdf` gives an editable but loosely laid out file. Say this in the reply.
- Images between formats: `convert in.png out.jpg`; HEIC, WebP, TIFF and GIF are read by ImageMagick. A multi-page TIFF or GIF writes one file per frame with `out-%d.png`.
- Spreadsheets to CSV: `soffice --headless --convert-to csv in.xlsx` writes the first sheet only; list the sheets and convert each with openpyxl when more than one matters. Formulas become their values; formatting is lost.
- HTML to text or Markdown: `pandoc in.html -t gfm --wrap=none -o out.md`.
- Archives: `python3 -m zipfile -c out.zip folder/` (the Bot computer has unzip and no zip command), `unzip -l in.zip` lists, `unzip in.zip -d out/` extracts, `tar -czf out.tar.gz folder/`, `tar -tzf in.tar.gz` lists, `tar -xzf in.tar.gz -C out/` extracts. Converting a zip to a tar means extracting into a new folder and packing that folder again. Names with Chinese characters stay readable in zip made on this computer.
- A format no tool here reads is reported as unsupported with the nearest option.

## What conversion loses

- Markdown to Word: no page layout beyond the template; raw HTML in the Markdown is dropped.
- Word to Markdown: styles, fonts, colors, text boxes, columns, headers and footers, comments, merged table cells.
- Word to PDF through LibreOffice: fonts replaced when missing, which shifts line and page breaks; a table of contents field shows its stored text.
- PDF to anything editable: layout, columns, tables, fonts, and often reading order.
- Anything to plain text: all formatting, pictures and tables beyond a text approximation.
- Image to JPEG: transparency and exact pixels; the quality setting trades size for detail.
- Spreadsheet to CSV: formulas, formatting, other sheets, charts.

## Checking the result

- `scripts/inspect_file.py out.docx` prints counts of paragraphs, tables, rows, headings, pictures and the first text of a DOCX, PPTX, XLSX, PDF, HTML, Markdown or text file, and the entries of an archive; use it on the original and on the result and compare the numbers.
- Render and view the result when layout matters. For Word, PowerPoint and Excel files convert to PDF with LibreOffice and then `pdftoppm -r 80 -png out.pdf check`; for a PDF render it directly. A picture reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing.
- Check that tables kept their rows and columns, that Chinese text shows characters, that pictures appear, and that the page count is sensible.
- Text formats are read with `head` and compared with the original around tables and lists.
- Fix what the render shows, convert again and look again.

## Delivery

- Send the result with send_message attachments (path). Convert each file of a batch the same way and send them as one archive when there are more than a few.
- Every claim in the reply is true of the delivered file. Name the tool used, the format of the result, and each kind of formatting that the conversion lost.
- Publish only the final file. Intermediate pages and check renders stay in the working folder.

## When something cannot be done

- A password-protected file, a corrupt file, or a missing converter is reported with the tool's message; user-space installs are covered by the install-software Skill.
- When a clean conversion is impossible, deliver the closest result, say what differs, and offer an alternative route.
