---
name: web-page
description: Make one self-contained HTML page for phone and desktop; view it in a browser.
---

# Single-file web page

## What good looks like

- One HTML file with inline CSS and JavaScript, images as inline SVG or data URIs, and system fonts. It opens from disk with no network.
- Built for the phone first: a viewport meta tag, fluid widths, a readable base size, touch-sized targets. The same file serves a wide desktop and a narrow phone, with no horizontal scroll.
- Real content from the request fills every section; no placeholder text. Semantic structure, alt text, a title and a language make it usable by everyone.

## Verifying

- Open the file in a browser and view it at desktop and phone widths. Use every interaction the page offers. Read the rendered text back against the request.
- Every claim in the reply is true of the delivered file. Sections, links and behaviours are claimed only after you saw them work.
- Publish only the final file.

## Tools

`scripts/preview_page.py` writes a preview page that shows the file in side by side frames at chosen viewport widths, to open through a file URL in the computer's browser. `--help` describes it.
