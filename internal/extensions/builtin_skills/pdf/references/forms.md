# Filling a PDF form

Adapted from Anthropic's pdf Skill (Apache-2.0, see NOTICE). Every command below is `scripts/pdf_form.py` or `scripts/pdf_tools.py` of this Skill.

A form is either fillable (it has AcroForm fields) or flat (a page of text and lines). `pdf_tools.py info FILE.pdf` prints the number of form fields; zero means flat.

## Fillable form

1. List the fields: `pdf_form.py fields FILE.pdf --out fields.json`. Each entry has `field_id`, `page` (1-based), `type` (text, checkbox, radio_group, choice), `rect` as [left, bottom, right, top] in PDF points with y=0 at the bottom, and the allowed values: `checked_value` and `unchecked_value` for a checkbox, `radio_options` for a radio group, `choice_options` for a choice. `current_value` shows what is already filled.
2. Learn what each field is for. Field ids are often unreadable, so render the pages (`pdf_tools.py render FILE.pdf --outdir pages`), view them, and match each field to its label by position. The page image is in pixels; a point of the PDF is `dpi / 72` pixels, and the PDF y axis points up.
3. Write `values.json`, a list with one entry per field to fill:

```
[
  {"field_id": "last_name", "page": 1, "value": "Simpson"},
  {"field_id": "over_18", "page": 1, "value": "/Yes"},
  {"field_id": "plan", "page": 1, "value": "pro"}
]
```

A checkbox takes its `checked_value` to be ticked; a radio group takes the `value` of one option; a choice takes the `value` of one option.
4. Fill: `pdf_form.py fill FILE.pdf values.json filled.pdf`. The command checks every id, page and value before it writes anything. It exits non-zero and names each problem, and nothing is written until all values are valid.
5. Render `filled.pdf` and view it. A value that is cut off, outside its box or in the wrong field is corrected in `values.json` and the form is filled again from the original file. Some viewers show values only after regenerating the appearance; the command sets the flag that asks them to.

Fields left out of `values.json` stay as they were.

## Flat form

Text is placed on the page at chosen boxes. Latin text uses Helvetica by default; Times-Roman and Courier are available, and `STSong-Light` draws Chinese. A page with a /Rotate value is rotated upright first with `pdf_tools.py rotate`.

1. Find where the labels are. With a text layer, `pdf_form.py labels FILE.pdf --out labels.json` lists every word with `x0`, `top`, `x1`, `bottom` in points measured from the top left, and the page sizes. Group neighbouring words into labels, and read rows from labels with the same `top`. Without a text layer the words are read from rendered pages and located in pixels.
2. Find the entry areas next to or below each label: the blank run after a label, the line under it, or a box. A checkbox is a small square; its box is the place for an X.
3. For pixel positions read from images, refine them by cropping a region around each field with ImageMagick (`magick page.png -crop 300x80+50+120 +repage crop.png`, or `convert` when `magick` is missing), viewing the crop, and adding the crop offset back to the position found inside it.
4. Write `fields.json` in one coordinate system. With positions in points use `pdf_width` and `pdf_height` in the page record; with pixels use `image_width` and `image_height`, and the command converts them.

```
{
  "pages": [{"page_number": 1, "pdf_width": 612, "pdf_height": 792}],
  "form_fields": [
    {
      "page_number": 1,
      "description": "Last name entry field",
      "field_label": "Last Name",
      "label_bounding_box": [43, 63, 87, 73],
      "entry_bounding_box": [92, 63, 260, 79],
      "entry_text": {"text": "Smith", "font_size": 10}
    }
  ]
}
```

Boxes are [x0, top, x1, bottom]. The entry box starts a few points after its label and ends before the next label or at the row line.
5. Place the text: `pdf_form.py overlay FILE.pdf fields.json filled.pdf`. The command refuses to write when two boxes overlap, an entry box is shorter than its font size, or the text is wider than its box, and it names each problem.
6. Render `filled.pdf`, view it, and move any text that sits off its line. Correct `fields.json` and place the text again from the original file.

## Limits

- Text on a flat form is drawn onto the page; it is not a form field and cannot be edited later in a viewer.
- Signatures, dates the person did not give and identity numbers are left empty unless the person supplied them in the request.
- A password-protected form needs an unlocked copy first: `pdf_tools.py rotate FILE.pdf -o unlocked.pdf --degrees 0 --password-file pw.txt` writes one.
