import os
import tempfile
import unittest

from _support import need_modules, need_programs, run_script, script_path


def make_deck(path):
    from pptx import Presentation
    from pptx.util import Inches
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(13.333), Inches(7.5)
    slide = prs.slides.add_slide(prs.slide_layouts[5])
    slide.shapes.title.text = "Quarterly review"
    slide.shapes.add_shape(1, Inches(12.5), Inches(1), Inches(2), Inches(1))
    box = slide.shapes.add_textbox(Inches(1), Inches(2), Inches(4), Inches(1))
    box.text_frame.text = "Revenue rose"
    slide.notes_slide.notes_text_frame.text = "Speaker notes"
    prs.save(path)


@need_modules("pptx")
class Outline(unittest.TestCase):
    def test_dump_lists_shapes_text_notes_and_marks_off_slide_shapes(self):
        with tempfile.TemporaryDirectory() as folder:
            deck = os.path.join(folder, "deck.pptx")
            make_deck(deck)
            result = run_script("slides", "pptx_outline", deck)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Quarterly review", result.stdout)
            self.assertIn("Revenue rose", result.stdout)
            self.assertIn("OUTSIDE SLIDE", result.stdout)
            self.assertIn("notes: Speaker notes", result.stdout)

    def test_unreadable_file_exits_two(self):
        with tempfile.TemporaryDirectory() as folder:
            path = os.path.join(folder, "bad.pptx")
            with open(path, "w") as handle:
                handle.write("not a deck")
            self.assertEqual(run_script("slides", "pptx_outline", path).returncode, 2)


@need_modules("pptx", "pypdf", "PIL")
@need_programs("soffice", "pdftoppm")
class Render(unittest.TestCase):
    def test_every_slide_becomes_an_image(self):
        with tempfile.TemporaryDirectory() as folder:
            deck = os.path.join(folder, "deck.pptx")
            make_deck(deck)
            seen = run_script("slides", "render_preview", deck, "--outdir", os.path.join(folder, "p"))
            self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
            self.assertIn("pages: 1", seen.stdout)
            self.assertTrue(os.path.exists(os.path.join(folder, "p", "page-01.png")))


class SharedScript(unittest.TestCase):
    def test_both_skills_ship_the_same_render_script(self):
        with open(script_path("slides", "render_preview.py"), "rb") as a, open(script_path("document-report", "render_preview.py"), "rb") as b:
            self.assertEqual(a.read(), b.read())


def make_clean_deck(path):
    from pptx import Presentation
    from pptx.util import Inches, Pt
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(13.333), Inches(7.5)
    for title in ("First", "Second", "Third"):
        slide = prs.slides.add_slide(prs.slide_layouts[5])
        slide.shapes.title.text = title
        box = slide.shapes.add_textbox(Inches(1), Inches(2), Inches(6), Inches(1))
        box.text_frame.text = "Body of " + title
        box.text_frame.paragraphs[0].runs[0].font.size = Pt(18)
    table = prs.slides[1].shapes.add_table(2, 2, Inches(1), Inches(4), Inches(6), Inches(1)).table
    table.cell(0, 0).text, table.cell(0, 1).text = "Plan", "Price"
    table.cell(1, 0).text, table.cell(1, 1).text = "Basic", "10"
    prs.save(path)


@need_modules("pptx")
class Check(unittest.TestCase):
    def run_check(self, mutate=None):
        from pptx import Presentation
        from pptx.util import Inches
        with tempfile.TemporaryDirectory() as folder:
            deck = os.path.join(folder, "deck.pptx")
            make_clean_deck(deck)
            if mutate:
                prs = Presentation(deck)
                mutate(prs, Inches)
                prs.save(deck)
            return run_script("slides", "pptx_check", deck)

    def test_clean_deck_exits_zero(self):
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("FAIL", result.stdout)

    def test_overlap_fails(self):
        def mutate(prs, inches):
            box = prs.slides[0].shapes.add_textbox(inches(2), inches(2.2), inches(4), inches(1))
            box.text_frame.text = "Covers the body"
        result = self.run_check(mutate)
        self.assertEqual(result.returncode, 1)
        self.assertIn("overlaps", result.stdout)

    def test_off_slide_and_leftover_text_fail(self):
        def mutate(prs, inches):
            prs.slides[2].shapes.add_textbox(inches(12.5), inches(5), inches(3), inches(1)).text_frame.text = "Lorem ipsum"
        result = self.run_check(mutate)
        self.assertEqual(result.returncode, 1)
        self.assertIn("outside the slide", result.stdout)
        self.assertIn("leftover template text", result.stdout)

    def test_overflow_is_a_warning(self):
        def mutate(prs, inches):
            box = prs.slides[0].shapes.add_textbox(inches(1), inches(5), inches(2), inches(0.4))
            box.text_frame.text = "word " * 60
        result = self.run_check(mutate)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("probably overflows", result.stdout)

    def test_unreadable_file_exits_two(self):
        with tempfile.TemporaryDirectory() as folder:
            path = os.path.join(folder, "bad.pptx")
            with open(path, "w") as handle:
                handle.write("not a deck")
            self.assertEqual(run_script("slides", "pptx_check", path).returncode, 2)


@need_modules("pptx")
class SlideOps(unittest.TestCase):
    def titles(self, path):
        from pptx import Presentation
        return [s.shapes.title.text for s in Presentation(path).slides]

    def test_duplicate_delete_move_and_layouts(self):
        from pptx import Presentation
        with tempfile.TemporaryDirectory() as folder:
            deck, out = os.path.join(folder, "deck.pptx"), os.path.join(folder, "out.pptx")
            make_clean_deck(deck)
            result = run_script("slides", "pptx_slides", deck, "-o", out, "duplicate", 2)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.titles(out), ["First", "Second", "Second", "Third"])
            copy_table = [sh for sh in Presentation(out).slides[2].shapes if sh.has_table]
            self.assertEqual(copy_table[0].table.cell(1, 1).text, "10")
            out2 = os.path.join(folder, "out2.pptx")
            self.assertEqual(run_script("slides", "pptx_slides", out, "-o", out2, "delete", 1).returncode, 0)
            self.assertEqual(self.titles(out2), ["Second", "Second", "Third"])
            out3 = os.path.join(folder, "out3.pptx")
            self.assertEqual(run_script("slides", "pptx_slides", out2, "-o", out3, "move", 3, 1).returncode, 0)
            self.assertEqual(self.titles(out3), ["Third", "Second", "Second"])
            listed = run_script("slides", "pptx_slides", deck, "layouts")
            self.assertEqual(listed.returncode, 0)
            self.assertIn("Title Only", listed.stdout)

    def test_input_is_kept_and_bad_arguments_exit_two(self):
        with tempfile.TemporaryDirectory() as folder:
            deck = os.path.join(folder, "deck.pptx")
            make_clean_deck(deck)
            self.assertEqual(run_script("slides", "pptx_slides", deck, "-o", deck, "delete", 1).returncode, 2)
            self.assertEqual(run_script("slides", "pptx_slides", deck, "-o", os.path.join(folder, "o.pptx"), "delete", 9).returncode, 2)
            self.assertEqual(self.titles(deck), ["First", "Second", "Third"])


if __name__ == "__main__":
    unittest.main()
