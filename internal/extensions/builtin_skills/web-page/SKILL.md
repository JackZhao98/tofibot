---
name: web-page
description: Make one self-contained HTML page for phone and desktop; verify it in the browser.
---

# Single-file web page

Scripts live in the skill folder named in the header of this text, under `scripts/`. Run each with `--help` first.

## Principles

- Every claim in the reply is true of the delivered file, and you have verified that by opening it in the browser. Sections, links, interactions and behaviours are claimed only after you saw them work.
- The deliverable is one HTML file: inline CSS and JavaScript, images as inline SVG or data URIs, system fonts. It opens from disk with no network.
- Mobile first: a viewport meta tag, fluid widths, a readable base size and touch-sized targets. The same file serves a 1280 px desktop and a 390 px phone.
- Real content from the request fills every section. Publish only the final file.

## Process

1. Plan the sections and the content from the request and any provided files, then write the page.
2. Run `check_page.py` with `--preview`. It lists structural errors and warnings, and writes a preview page that shows the file at 1280 px and at 390 px side by side. Fix every error.
3. Open the preview through its file URL in the computer browser and take a snapshot. Judge both widths: nothing clipped or overlapping, no horizontal scroll at the phone width, text readable, navigation and calls to action reachable. Use the browser to exercise every interaction the page offers.
4. Read the page text back with the browser and compare it with the request, so that every requested section and fact is present.
5. Write the reply from what you saw at both widths, then publish the final file with `publish_file`.
