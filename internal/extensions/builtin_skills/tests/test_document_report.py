import json
import os
import tempfile
import unittest

from _support import need_modules, need_programs, run_script

SPEC = {"title": "Quarterly report", "author": "Bot", "blocks": [
    {"type": "heading", "level": 1, "text": "Summary"},
    {"type": "paragraph", "text": "Revenue rose in every quarter."},
    {"type": "bullets", "items": ["one", "two"]},
    {"type": "chart", "kind": "bar", "title": "Revenue", "categories": ["Q1", "Q2", "Q3"],
     "series": [{"name": "Revenue", "values": [10, 12, 15]}], "caption": "Figure 1"},
    {"type": "chart", "kind": "pie", "title": "Mix", "categories": ["A", "B"], "series": [{"name": "s", "values": [1, 3]}]},
    {"type": "table", "columns": ["Quarter", "Revenue"], "rows": [["Q1", 10], ["Q2", 12]]}]}


def write_spec(folder, spec):
    path = os.path.join(folder, "spec.json")
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(spec, handle)
    return path


@need_modules("matplotlib", "reportlab", "pypdf", "PIL")
@need_programs("pdftoppm")
class PdfReport(unittest.TestCase):
    def test_pdf_holds_the_claimed_charts(self):
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "report.pdf")
            built = run_script("document-report", "build_report", write_spec(folder, SPEC), out)
            self.assertEqual(built.returncode, 0, built.stderr)
            self.assertIn("2 chart(s) and image(s) embedded", built.stdout)
            preview = os.path.join(folder, "preview")
            seen = run_script("document-report", "render_preview", out, "--outdir", preview, "--expect-images", 2, "--expect-text", "Revenue rose")
            self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
            self.assertIn("images in pdf: 2", seen.stdout)
            self.assertTrue(os.path.exists(os.path.join(preview, "page-01.png")))
            self.assertTrue(os.path.exists(os.path.join(preview, "contact.png")))

    def test_a_report_without_charts_fails_the_image_claim(self):
        spec = {"title": "No charts", "blocks": [{"type": "paragraph", "text": "Only words."}]}
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "report.pdf")
            self.assertEqual(run_script("document-report", "build_report", write_spec(folder, spec), out).returncode, 0)
            seen = run_script("document-report", "render_preview", out, "--outdir", os.path.join(folder, "p"), "--expect-images", 1)
            self.assertEqual(seen.returncode, 1)
            self.assertIn("expected at least 1 images, found 0", seen.stdout)

    def test_mismatched_chart_data_stops_the_build(self):
        spec = {"title": "x", "blocks": [{"type": "chart", "kind": "bar", "categories": ["a", "b"], "series": [{"name": "s", "values": [1]}]}]}
        with tempfile.TemporaryDirectory() as folder:
            built = run_script("document-report", "build_report", write_spec(folder, spec), os.path.join(folder, "r.pdf"))
            self.assertEqual(built.returncode, 1)
            self.assertIn("1 values for 2 categories", built.stderr)


@need_modules("matplotlib", "docx", "PIL")
class DocxReport(unittest.TestCase):
    def test_docx_embeds_chart_images(self):
        import docx
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "report.docx")
            built = run_script("document-report", "build_report", write_spec(folder, SPEC), out)
            self.assertEqual(built.returncode, 0, built.stderr)
            document = docx.Document(out)
            self.assertEqual(len(document.inline_shapes), 2)
            self.assertEqual(len(document.tables), 1)

    @need_modules("pypdf")
    @need_programs("soffice", "pdftoppm")
    def test_docx_converts_and_renders(self):
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "report.docx")
            run_script("document-report", "build_report", write_spec(folder, SPEC), out)
            seen = run_script("document-report", "render_preview", out, "--outdir", os.path.join(folder, "p"), "--expect-images", 2)
            self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)


if __name__ == "__main__":
    unittest.main()
