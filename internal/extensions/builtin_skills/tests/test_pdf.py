import json
import os
import subprocess
import tempfile
import unittest

from _support import need_modules, need_programs, run_script


def load_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def save_json(data, path):
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(data, handle)


def read_bytes(path):
    with open(path, "rb") as handle:
        return handle.read()


def read_text(path):
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def make_report(path):
    """Two pages: prose and a table on page 1, a second table on page 2."""
    from reportlab.lib.pagesizes import letter
    from reportlab.pdfgen import canvas
    pdf = canvas.Canvas(path, pagesize=letter)
    pdf.setFont("Helvetica", 12)
    pdf.drawString(72, 740, "Quarterly report")
    rows = [("Region", "Units", "Revenue"), ("North", "120", "4,500.50"), ("South", "95", "3,800.00"), ("West", "210", "9,100.25")]
    for index, row in enumerate(rows):
        y = 700 - index * 20
        pdf.drawString(72, y, row[0])
        pdf.drawRightString(300, y, row[1])
        pdf.drawRightString(450, y, row[2])
    pdf.showPage()
    pdf.setFont("Helvetica", 12)
    pdf.drawString(72, 740, "Second page")
    for index, row in enumerate([("Item", "Count"), ("Bolts", "40"), ("Nuts", "55")]):
        pdf.drawString(72, 700 - index * 34, row[0])
        pdf.drawRightString(260, 700 - index * 34, row[1])
    pdf.save()


def make_form(path):
    from reportlab.lib.pagesizes import letter
    from reportlab.pdfgen import canvas
    pdf = canvas.Canvas(path, pagesize=letter)
    pdf.drawString(72, 740, "Name")
    form = pdf.acroForm
    form.textfield(name="full_name", x=130, y=730, width=200, height=18, borderStyle="solid")
    pdf.drawString(72, 700, "Subscribe")
    form.checkbox(name="subscribe", x=130, y=696, size=14, buttonStyle="check")
    pdf.drawString(72, 660, "Plan")
    form.choice(name="plan", x=130, y=650, width=120, height=18, options=[("Basic", "basic"), ("Pro", "pro")], value="basic", fieldFlags="combo")
    pdf.save()


def make_flat_form(path):
    from reportlab.lib.pagesizes import letter
    from reportlab.pdfgen import canvas
    pdf = canvas.Canvas(path, pagesize=letter)
    pdf.setFont("Helvetica", 12)
    pdf.drawString(72, 700, "Last name")
    pdf.line(150, 698, 350, 698)
    pdf.save()


@need_modules("pypdf", "reportlab", "openpyxl")
@need_programs("pdftotext", "pdftoppm")
class PdfTools(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = self.tmp.name
        self.report = os.path.join(self.dir, "report.pdf")
        make_report(self.report)

    def tearDown(self):
        self.tmp.cleanup()

    def tools(self, *args, **kw):
        return run_script("pdf", "pdf_tools", *args, **kw)

    def test_info_counts_pages_and_text(self):
        seen = self.tools("info", self.report)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("pages: 2", seen.stdout)
        self.assertNotIn("without a text layer", seen.stdout)

    def test_text_keeps_the_table_columns(self):
        seen = self.tools("text", self.report, "--first", "1", "--last", "1")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("West", seen.stdout)
        self.assertNotIn("Second page", seen.stdout)

    def test_tables_become_an_xlsx_with_numbers(self):
        import openpyxl
        out = os.path.join(self.dir, "t.xlsx")
        seen = self.tools("tables", self.report, "--out", out)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
        book = openpyxl.load_workbook(out)
        self.assertEqual(book.sheetnames, ["p1_t1", "p2_t1"])
        rows = [[c.value for c in row] for row in book["p1_t1"].iter_rows()]
        self.assertEqual(rows[0], ["Region", "Units", "Revenue"])
        self.assertEqual(rows[1], ["North", 120, 4500.5])
        self.assertEqual(len(rows), 4)

    def test_tables_in_a_pdf_without_one_fail(self):
        from reportlab.pdfgen import canvas
        path = os.path.join(self.dir, "prose.pdf")
        pdf = canvas.Canvas(path)
        pdf.drawString(72, 700, "Only one line of prose here.")
        pdf.save()
        seen = self.tools("tables", path, "--out", os.path.join(self.dir, "x.xlsx"))
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("no table found", seen.stderr)

    def test_split_merge_and_rotate(self):
        import pypdf
        parts = os.path.join(self.dir, "parts")
        seen = self.tools("split", self.report, "--outdir", parts)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertEqual(sorted(os.listdir(parts)), ["report-part1.pdf", "report-part2.pdf"])
        merged = os.path.join(self.dir, "m.pdf")
        seen = self.tools("merge", os.path.join(parts, "report-part2.pdf"), self.report + "::1", "-o", merged)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertEqual(len(pypdf.PdfReader(merged).pages), 2)
        rotated = os.path.join(self.dir, "r.pdf")
        seen = self.tools("rotate", self.report, "-o", rotated, "--degrees", "90", "--pages", "2")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        pages = pypdf.PdfReader(rotated).pages
        self.assertEqual((pages[0].get("/Rotate", 0), pages[1].get("/Rotate", 0)), (0, 90))

    def test_the_input_is_never_overwritten(self):
        before = read_bytes(self.report)
        seen = self.tools("rotate", self.report, "-o", self.report)
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("is the input file", seen.stderr)
        self.assertEqual(read_bytes(self.report), before)

    def test_bad_page_range_fails(self):
        seen = self.tools("rotate", self.report, "-o", os.path.join(self.dir, "o.pdf"), "--pages", "5")
        self.assertNotEqual(seen.returncode, 0)

    def test_render_writes_one_png_per_page(self):
        out = os.path.join(self.dir, "img")
        seen = self.tools("render", self.report, "--outdir", out, "--dpi", "50")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertEqual(sorted(os.listdir(out)), ["page-1.png", "page-2.png"])

    def test_protected_pdf_needs_a_password_file(self):
        import pypdf
        writer = pypdf.PdfWriter(clone_from=self.report)
        try:
            writer.encrypt("pw-for-test", algorithm="RC4-128")
        except (ImportError, pypdf.errors.DependencyError):
            self.skipTest("encryption library missing")
        locked = os.path.join(self.dir, "locked.pdf")
        with open(locked, "wb") as handle:
            writer.write(handle)
        seen = self.tools("info", locked)
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("--password-file", seen.stderr)
        secret = os.path.join(self.dir, "pw.txt")
        with open(secret, "w") as handle:
            handle.write("pw-for-test\n")
        seen = self.tools("text", locked, "--password-file", secret)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("Quarterly report", seen.stdout)
        unlocked = os.path.join(self.dir, "unlocked.pdf")
        seen = self.tools("rotate", locked, "-o", unlocked, "--degrees", "0", "--password-file", secret)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertFalse(pypdf.PdfReader(unlocked).is_encrypted)
        seen = self.tools("info", locked, "--password-file", secret)
        self.assertNotIn("pw-for-test", seen.stdout + seen.stderr)
        seen = self.tools("info", locked, "--password", "pw-for-test")
        self.assertNotEqual(seen.returncode, 0)

    @need_programs("tesseract")
    @need_modules("PIL")
    def test_ocr_reads_a_scanned_page(self):
        from PIL import Image, ImageDraw, ImageFont
        langs = subprocess.run(["tesseract", "--list-langs"], capture_output=True, text=True).stdout
        if "eng" not in langs:
            self.skipTest("tesseract has no eng data")
        image = Image.new("RGB", (1700, 400), "white")
        draw = ImageDraw.Draw(image)
        font = None
        for name in ("/System/Library/Fonts/Supplemental/Arial.ttf", "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf"):
            if os.path.exists(name):
                font = ImageFont.truetype(name, 90)
        if font is None:
            self.skipTest("no TrueType font to draw the scan")
        draw.text((60, 120), "Invoice total 4821", fill="black", font=font)
        scan = os.path.join(self.dir, "scan.pdf")
        image.save(scan, "PDF", resolution=200)
        seen = self.tools("info", scan)
        self.assertIn("without a text layer", seen.stdout)
        out = os.path.join(self.dir, "ocr")
        seen = self.tools("ocr", scan, "--outdir", out)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
        text = read_text(os.path.join(out, "ocr.txt"))
        self.assertIn("4821", text)
        searchable = os.path.join(self.dir, "s.pdf")
        seen = self.tools("ocr", scan, "--outdir", out, "--searchable", searchable)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
        found = subprocess.run(["pdftotext", searchable, "-"], capture_output=True, text=True).stdout
        self.assertIn("4821", found)


@need_modules("pypdf", "reportlab")
@need_programs("pdftotext", "pdftoppm")
class PdfForms(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def form(self, *args):
        return run_script("pdf", "pdf_form", *args)

    def test_fillable_fields_are_listed_and_filled(self):
        import pypdf
        src = os.path.join(self.dir, "form.pdf")
        make_form(src)
        listing = os.path.join(self.dir, "fields.json")
        seen = self.form("fields", src, "--out", listing)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        fields = {f["field_id"]: f for f in load_json(listing)}
        self.assertEqual(set(fields), {"full_name", "subscribe", "plan"})
        self.assertEqual(fields["subscribe"]["checked_value"], "/Yes")
        self.assertEqual([o["value"] for o in fields["plan"]["choice_options"]], ["basic", "pro"])
        values = os.path.join(self.dir, "values.json")
        save_json([{"field_id": "full_name", "page": 1, "value": "Ada Lovelace"},
                   {"field_id": "subscribe", "page": 1, "value": "/Yes"},
                   {"field_id": "plan", "page": 1, "value": "pro"}], values)
        out = os.path.join(self.dir, "filled.pdf")
        seen = self.form("fill", src, values, out)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        got = pypdf.PdfReader(out).get_fields()
        self.assertEqual(got["full_name"].get("/V"), "Ada Lovelace")
        self.assertEqual(got["plan"].get("/V"), "pro")
        self.assertEqual(got["subscribe"].get("/V"), "/Yes")
        shown = os.path.join(self.dir, "p")
        subprocess.run(["pdftoppm", "-png", "-r", "60", out, shown], check=True)
        self.assertTrue(any(n.startswith("p") for n in os.listdir(self.dir)))

    def test_wrong_values_fail_and_name_the_reason(self):
        src = os.path.join(self.dir, "form.pdf")
        make_form(src)
        values = os.path.join(self.dir, "values.json")
        save_json([{"field_id": "plan", "page": 1, "value": "gold"}, {"field_id": "nope", "page": 1, "value": "x"}], values)
        seen = self.form("fill", src, values, os.path.join(self.dir, "o.pdf"))
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("invalid value 'gold'", seen.stderr)
        self.assertIn("'nope' is not a field", seen.stderr)
        self.assertFalse(os.path.exists(os.path.join(self.dir, "o.pdf")))

    def test_flat_form_labels_and_overlay(self):
        src = os.path.join(self.dir, "flat.pdf")
        make_flat_form(src)
        seen = self.form("fields", src)
        self.assertIn("flat", seen.stderr)
        labels = os.path.join(self.dir, "labels.json")
        seen = self.form("labels", src, "--out", labels)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        data = load_json(labels)
        word = next(w for w in data["words"] if w["text"] == "name")
        self.assertEqual(data["pages"][0]["pdf_height"], 792.0)
        top = word["top"]
        fields = {"pages": [{"page_number": 1, "pdf_width": 612, "pdf_height": 792}],
                  "form_fields": [{"page_number": 1, "description": "Last name", "field_label": "Last name",
                                   "label_bounding_box": [word["x0"] - 50, top, word["x1"], word["bottom"]],
                                   "entry_bounding_box": [152, top - 2, 350, top + 14],
                                   "entry_text": {"text": "Lovelace", "font_size": 11}}]}
        path = os.path.join(self.dir, "fields.json")
        save_json(fields, path)
        out = os.path.join(self.dir, "out.pdf")
        seen = self.form("overlay", src, path, out)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
        text = subprocess.run(["pdftotext", "-layout", out, "-"], capture_output=True, text=True).stdout
        self.assertIn("Lovelace", text)
        self.assertIn("Last name", text)
        # text lands on the line: the entry word sits right of the label on the same text row
        row = next(line for line in text.splitlines() if "Lovelace" in line)
        self.assertIn("Last name", row)

    def test_overlay_rejects_overlapping_or_overflowing_boxes(self):
        src = os.path.join(self.dir, "flat.pdf")
        make_flat_form(src)
        fields = {"pages": [{"page_number": 1, "pdf_width": 612, "pdf_height": 792}],
                  "form_fields": [{"page_number": 1, "description": "Last name", "label_bounding_box": [72, 80, 140, 94],
                                   "entry_bounding_box": [100, 80, 120, 94], "entry_text": {"text": "A very long surname indeed", "font_size": 11}}]}
        path = os.path.join(self.dir, "fields.json")
        save_json(fields, path)
        seen = self.form("overlay", src, path, os.path.join(self.dir, "o.pdf"))
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("overlaps", seen.stderr)
        self.assertIn("wider than its entry box", seen.stderr)


if __name__ == "__main__":
    unittest.main()
