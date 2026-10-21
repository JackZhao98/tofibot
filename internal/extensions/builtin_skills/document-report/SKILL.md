---
name: document-report
description: Write DOCX or PDF reports with real embedded charts; render and inspect the pages.
---

# Document and report

Scripts live in the skill folder named in the header of this text, under `scripts/`. Run each with `--help` first.

## Principles

- Every claim in the reply is true of the delivered file, and you have verified that by opening it. A chart, table, section or figure is mentioned only after you have seen it in the rendered pages.
- Charts are drawn from the actual data with matplotlib and embedded as images. A report that promises charts holds images, and the image count reported by the render script is the number you may claim.
- The words of the report come from the sources you read. Figures are copied from the data, and anything you could not read is listed in the report or the reply.
- Publish only the final file.

## Process

1. Collect the data and the points the report must make. Read the provided files completely; uploaded files are on the computer at the paths listed in the conversation.
2. Describe the report as a JSON spec (title, headings, paragraphs, bullets, tables, charts, images) and build it with `build_report.py` as DOCX or PDF. It draws each chart from the numbers in the spec, embeds the PNG, and stops with a message when chart data is missing, mismatched or not numeric. Use `--font` when the text needs a script the default fonts lack.
3. Run `render_preview.py` on the output. It converts Office files to PDF with `soffice --headless`, renders each page to PNG with `pdftoppm`, writes a contact sheet, and reports page count and image count. Pass `--expect-images` with the number of charts you intend to claim and `--expect-text` with key phrases or figures from the report.
4. Open the page PNGs in the computer browser through a file URL and look at them with a screenshot. Check clipped tables, overlapping text, unreadable axes, blank pages and charts that match the numbers in the text. Fix the spec and rebuild until the pages are right.
5. Write the reply from what the final render showed: page count, the charts and tables present, and the limits of the data.
6. Publish the final file with `publish_file`.
