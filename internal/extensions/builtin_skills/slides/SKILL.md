---
name: slides
description: Build PPTX decks; look at the rendered slides before delivering.
requires: [computer]
kind: deliverable
---

# Slide decks

## What good looks like

- One idea per slide, a title on every slide, short lines, and a chart or table where numbers are compared. Every item the request names has a place in the deck.
- Everything sits inside the slide with at least 0.5 in of margin, text is readable at presentation size, and nothing overlaps. Charts and tables are native and editable when the data allows.
- The numbers on the slides are the numbers in the sources.
- Type sizes for a new deck: titles 36 to 44 pt, section headers 20 to 24 pt, body 14 to 16 pt, captions 10 to 12 pt. When a body reaches its floor, cut words, enlarge the box or split the slide. In an existing deck, match its sizes.
- Fonts that render with the same widths here and in Office: Arial, Calibri, Cambria, Times New Roman, Courier New. A font outside that list gets about 10 percent slack in its boxes, because the preview approximates it.
- Titles keep one position, size and alignment across slides of the same type; chart labels look the same from chart to chart; body text is left-aligned; gaps between blocks are one of two values used consistently.
- Colors are chosen for the topic with one dominant color and one accent. Decorative accent lines under titles and colored edge stripes on cards or slides are left out. Text-only slides get a table, chart, picture or shape where one fits.

## Editing an existing deck

- Read it first: `scripts/pptx_outline.py` for every shape, position, text and note, and `scripts/pptx_slides.py deck.pptx layouts` for the layouts and their placeholders. Render it and view the slides to see the house style before changing anything.
- A new slide uses a layout of the deck itself, so fonts, colors and footers follow the template. Fill the layout's placeholders; add a table or chart with python-pptx at the position of the body placeholder, and remove a placeholder left empty.
- Do structure first and content second: `scripts/pptx_slides.py deck.pptx -o out.pptx duplicate N`, `delete N` or `move N M` copy, remove or reorder slides with all package bookkeeping and never edit the input. A duplicate shares chart parts with its source.
- Assigning `text_frame.text` collapses a paragraph to one unstyled run; set `run.text` to keep formatting. One paragraph per list item, bullets inherited from the layout, never a typed bullet character.
- When a template shows more slots than the content has items, delete the whole unused group (picture and text boxes), not only its text. Delete the animations of a deleted shape along with it.
- python-pptx cannot read SVG or EMF pictures; reuse such art by duplicating a slide or layout that holds it. A legacy `.ppt` is converted to `.pptx` with LibreOffice first.
- Leave every slide the request did not name as it was, and say which slides changed.

## Verifying

- Look at the deck the way the audience will: render each slide to an image and view it. A rendered image reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. The rendered picture decides whether a slide works.
- In each picture, check that every line of text ends inside its box and inside the slide, nothing covers anything else, no word or number is split across lines, contrast is strong, and gaps are even. Compare slides with each other as well as each one alone. Fix what you see, render again and view again until the slides are clean; the first render usually shows a few real faults.
- `scripts/pptx_check.py deck.pptx` exits 1 for shapes outside the slide, overlapping shapes, leftover template text and empty placeholders, and warns about likely text overflow, text under 10 pt, tight edges and missing titles. A clean run never replaces viewing the images.
- Read the file back for what a render hides: shape positions, hidden or off-slide content, speaker notes.
- Check the content against the request and the source: each figure, name and section they stress is on a slide of the final deck.
- Every claim in the reply is true of the delivered file. The slide count, figures and charts mentioned are the ones in the final deck, and omissions are stated with the reason.
- Publish only the final deck.

## Tools

`python-pptx` and `matplotlib` are installed; write the code the task needs. `scripts/render_preview.py` converts to PDF with LibreOffice and renders one PNG per slide plus a contact sheet, to view with files.view. `scripts/pptx_outline.py` prints every shape with its position, text and notes, and marks any shape outside the slide. `scripts/pptx_check.py` checks layout faults. `scripts/pptx_slides.py` lists layouts and duplicates, deletes or moves slides. `--help` describes each.
