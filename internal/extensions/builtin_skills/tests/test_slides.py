import json
import os
import tempfile
import unittest

from _support import load, need_modules, need_programs, run_script

deckkit = load("slides", "deckkit")

SPEC = {"slides": [
    {"layout": "title", "title": "Quarterly review", "subtitle": "Revenue and costs"},
    {"layout": "bullets", "title": "Highlights", "bullets": ["Revenue up", "Costs flat", "Two launches"], "notes": "Speaker notes"},
    {"layout": "chart", "title": "Revenue by quarter", "chart": {"kind": "bar", "categories": ["Q1", "Q2", "Q3"], "series": [{"name": "Revenue", "values": [1, 2, 3]}]}, "bullets": ["Growth each quarter"]},
    {"layout": "table", "title": "Regions", "columns": ["Region", "Sales"], "rows": [["East", 1], ["West", 2]]},
    {"layout": "two_column", "title": "Before and after", "left": {"heading": "Before", "bullets": ["a", "b"]}, "right": {"heading": "After", "bullets": ["c"]}}]}


class Estimator(unittest.TestCase):
    def test_longer_text_needs_more_height_and_smaller_size_fits(self):
        short = deckkit.text_height_in(["hello"], 6, 20)
        long = deckkit.text_height_in(["hello world " * 40], 6, 20)
        self.assertGreater(long, short * 3)
        text = ["word " * 120]
        size = deckkit.fit_size(text, 6, 3, 28, 10)
        self.assertLess(size, 28)
        self.assertLessEqual(deckkit.text_height_in(text, 6, size), 3)

    def test_wide_glyphs_count_double(self):
        self.assertGreater(deckkit.wrapped_lines("字" * 40, 3, 20), deckkit.wrapped_lines("a" * 40, 3, 20))


@need_modules("pptx", "PIL")
class DeckBuildAndCheck(unittest.TestCase):
    def build(self, folder, spec=None):
        path, out = os.path.join(folder, "spec.json"), os.path.join(folder, "deck.pptx")
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(spec or SPEC, handle)
        return run_script("slides", "build_deck", path, out), out

    def test_built_deck_passes_its_own_check(self):
        with tempfile.TemporaryDirectory() as folder:
            built, out = self.build(folder)
            self.assertEqual(built.returncode, 0, built.stderr)
            checked = run_script("slides", "check_deck", out, "--expect-slides", 5, "--expect-charts", 1, "--expect-tables", 1)
            self.assertEqual(checked.returncode, 0, checked.stdout)
            self.assertIn("5 slides", checked.stdout)
            from pptx import Presentation
            self.assertEqual(Presentation(out).slides[1].notes_slide.notes_text_frame.text, "Speaker notes")

    def test_wrong_expectation_fails(self):
        with tempfile.TemporaryDirectory() as folder:
            _, out = self.build(folder)
            checked = run_script("slides", "check_deck", out, "--expect-charts", 3)
            self.assertEqual(checked.returncode, 1)
            self.assertIn("expected 3 charts, found 1", checked.stdout)

    def test_shape_outside_the_slide_and_overflow_are_errors(self):
        from pptx.util import Inches
        from pptx import Presentation
        with tempfile.TemporaryDirectory() as folder:
            _, out = self.build(folder)
            prs = Presentation(out)
            slide = prs.slides[1]
            slide.shapes.add_shape(1, Inches(12.5), Inches(1), Inches(2), Inches(1))
            box = slide.shapes.add_textbox(Inches(1), Inches(6), Inches(2), Inches(0.5))
            box.text_frame.word_wrap = True
            box.text_frame.text = "This is a long sentence that cannot possibly fit in a tiny box at this size. " * 3
            box.text_frame.paragraphs[0].runs[0].font.size = Inches(0.3)
            prs.save(out)
            checked = run_script("slides", "check_deck", out)
            self.assertEqual(checked.returncode, 1)
            self.assertIn("extends beyond the slide", checked.stdout)
            self.assertIn("likely overflows its box", checked.stdout)

    def test_untitled_slide_is_an_error(self):
        from pptx import Presentation
        with tempfile.TemporaryDirectory() as folder:
            _, out = self.build(folder)
            prs = Presentation(out)
            prs.slides.add_slide(prs.slide_layouts[6])
            prs.save(out)
            checked = run_script("slides", "check_deck", out)
            self.assertEqual(checked.returncode, 1)
            self.assertIn("slide 6: no title", checked.stdout)

    def test_text_that_cannot_fit_stops_the_build(self):
        spec = {"slides": [{"layout": "bullets", "title": "Dense", "bullets": ["line of text that goes on and on " * 6] * 40}]}
        with tempfile.TemporaryDirectory() as folder:
            built, _ = self.build(folder, spec)
            self.assertEqual(built.returncode, 1)
            self.assertIn("split it across slides", built.stderr)

    def test_slide_without_title_in_spec_is_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            built, _ = self.build(folder, {"slides": [{"layout": "bullets", "bullets": ["x"]}]})
            self.assertEqual(built.returncode, 1)
            self.assertIn("slide 1 has no title", built.stderr)

    @need_modules("pypdf")
    @need_programs("soffice", "pdftoppm")
    def test_render_preview_of_the_deck(self):
        with tempfile.TemporaryDirectory() as folder:
            _, out = self.build(folder)
            seen = run_script("slides", "render_preview", out, "--outdir", os.path.join(folder, "p"), "--expect-pages", 5)
            self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)


class SharedScript(unittest.TestCase):
    def test_both_skills_ship_the_same_render_script(self):
        from _support import script_path
        with open(script_path("slides", "render_preview.py"), "rb") as a, open(script_path("document-report", "render_preview.py"), "rb") as b:
            self.assertEqual(a.read(), b.read())


if __name__ == "__main__":
    unittest.main()
