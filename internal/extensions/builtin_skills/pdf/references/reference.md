# PDF recipes

Adapted from Anthropic's pdf Skill (Apache-2.0, see NOTICE). The Bot computer has pypdf, reportlab, Pillow, pandas, openpyxl, Poppler (pdftotext, pdftoppm, pdfimages, pdfinfo) and Tesseract. It has no qpdf, pdfplumber, pdftk or pypdfium2; user-space installs follow the install-software Skill.

## Crop a page

```python
from pypdf import PdfReader, PdfWriter
reader = PdfReader('in.pdf')
page = reader.pages[0]
page.mediabox.left, page.mediabox.bottom, page.mediabox.right, page.mediabox.top = 50, 50, 550, 750
writer = PdfWriter()
writer.add_page(page)
with open('cropped.pdf', 'wb') as handle:
    writer.write(handle)
```

Values are points from the bottom left. Many viewers also read the crop box, so set `page.cropbox` the same way when the page must look cropped everywhere.

## Watermark or stamp

```python
from pypdf import PdfReader, PdfWriter
mark = PdfReader('mark.pdf').pages[0]
reader = PdfReader('in.pdf')
writer = PdfWriter()
for page in reader.pages:
    page.merge_page(mark)
    writer.add_page(page)
with open('marked.pdf', 'wb') as handle:
    writer.write(handle)
```

Draw the mark page with reportlab; render a page afterwards to check it covers nothing that matters.

## Metadata

`PdfReader(path).metadata` gives title, author, subject and creator. `writer.add_metadata({'/Title': 'New title'})` sets them on a new file.

## Text with positions

`pdftotext -bbox-layout FILE.pdf out.html` writes every word, line and block with its box. `pdftotext -layout -f 2 -l 4 FILE.pdf -` prints pages 2 to 4 with columns kept.

## Pictures

`pdfimages -list FILE.pdf` lists the embedded images; `pdfimages -all FILE.pdf out/img` writes them in their original format. `pdftoppm -png -r 200 -f 3 -l 3 FILE.pdf page` renders page 3 at 200 dpi, and `-x -y -W -H` crops the render to a pixel region.

## Scans

A scanned page without a text layer is rendered with `pdftoppm -r 300` and read with `tesseract page.png out -l eng`; `tesseract page.png out pdf` writes a one-page searchable PDF. `pdf_tools.py ocr` does this for every page.

## Repair

A damaged file sometimes opens with `PdfReader(path, strict=False)`; writing its pages to a new PdfWriter produces a clean copy. Report a file that still fails to open, with the error text.

## Create a PDF with reportlab

```python
from reportlab.lib.pagesizes import letter
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, Table, PageBreak
styles = getSampleStyleSheet()
doc = SimpleDocTemplate('out.pdf', pagesize=letter)
doc.build([Paragraph('Title', styles['Title']), Spacer(1, 12), Paragraph('Body text', styles['Normal']), Table([['A', 'B'], ['1', '2']])])
```

- The built-in fonts have no glyphs for Unicode sub- and superscript digits and draw black boxes. Use the markup tags `<sub>` and `<super>` inside a Paragraph, or adjust size and position when drawing on a canvas.
- Chinese text needs a CID font: `pdfmetrics.registerFont(UnicodeCIDFont('STSong-Light'))`, then use that font name in the paragraph style.
- Long documents with headings, charts and tables are the document-report Skill's job.

## Large files

Process page ranges instead of loading everything at once, and prefer `pdftotext` for plain text over `extract_text()` on very large files. Splitting into parts of ten pages first keeps memory small.
