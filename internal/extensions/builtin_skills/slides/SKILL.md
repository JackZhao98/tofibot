---
name: slides
description: Build PPTX decks; look at the rendered slides before delivering.
---

# Slide decks

## What good looks like

- One idea per slide, a title on every slide, short lines, and a chart or table where numbers are compared. Every item the request names has a place in the deck.
- Everything sits inside the slide, text is readable at presentation size, and nothing overlaps. Charts and tables are native and editable when the data allows.
- The numbers on the slides are the numbers in the sources.

## Verifying

- Look at the deck the way the audience will: render each slide to an image and view it. The rendered picture decides whether a slide works.
- Read the file back for what a render hides: shape positions, hidden or off-slide content, speaker notes.
- Every claim in the reply is true of the delivered file. The slide count, figures and charts mentioned are the ones in the final deck, and omissions are stated with the reason.
- Publish only the final deck.

## Tools

`python-pptx` and `matplotlib` are installed; write the code the task needs. `scripts/render_preview.py` converts to PDF with LibreOffice and renders one PNG per slide plus a contact sheet. `scripts/pptx_outline.py` prints every shape with its position, text and notes, and marks any shape outside the slide. `--help` describes each.
