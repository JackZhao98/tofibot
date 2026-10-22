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

- Look at the page as it renders at desktop and phone widths. A rendered image reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. At phone width check that no element overlaps or hides another, that text stays inside its container and that sections keep their side margins. Fix what you see, render again and view again until both widths are clean.
- Use every interaction the page offers. Read the rendered text back against the request.
- Every claim in the reply is true of the delivered file. Sections, links and behaviours are claimed only after you saw them work.
- Publish only the final file.

## Tools

`scripts/preview_page.py` renders the file at chosen viewport widths in headless Chrome and writes PNG sheets of its screens next to it, to view with files.view; the browser tool opens only http and https pages. `--help` describes it.
