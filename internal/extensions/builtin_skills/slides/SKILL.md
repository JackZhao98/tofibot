---
name: slides
description: Build PPTX decks, one idea per slide; check bounds and text fit and inspect rendered slides.
---

# Slide decks

Scripts live in the skill folder named in the header of this text, under `scripts/`. Run each with `--help` first.

## Principles

- Every claim in the reply is true of the delivered file, and you have verified that by opening it. The slide count, the figures and the charts you mention are the ones in the final rendered deck.
- One idea per slide, a title on every slide, short lines, and a visual or a table wherever numbers are compared. Every figure the request names appears on a slide; the plan lists them before building and the check afterwards.
- Everything sits inside the slide bounds and every text box holds its text at a readable size.
- Publish only the final deck.

## Process

1. Write the outline: for each slide, its title, the single point, and the data or figure it carries. Cross-check the outline against the request so that every requested item has a slide.
2. Describe the deck as a JSON spec and build it with `build_deck.py`. It lays out a 16:9 deck from a grid, chooses font sizes that fit the estimated text height, uses native editable charts and tables, and keeps speaker notes.
3. Run `check_deck.py`. It reports shapes outside the slide, estimated text overflow, slides without titles, tiny fonts, overlaps, and counts of slides, charts, pictures and tables. Pass `--expect-slides` and `--expect-charts` with the numbers you intend to claim. Fix the spec until it exits cleanly.
4. Run `render_preview.py`, which converts through `soffice --headless` and `pdftoppm` and writes one PNG per slide plus a contact sheet. Open the PNGs in the computer browser through a file URL and look at every slide with a screenshot; the estimates in the check are approximations and the picture decides.
5. Write the reply from the final render: slide count, what each requested item became, and anything omitted with the reason.
6. Publish the final file with `publish_file`.
