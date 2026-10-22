import os
import tempfile
import unittest

from _support import run_script


class PreviewPage(unittest.TestCase):
    def write(self, folder):
        path = os.path.join(folder, "page.html")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write("<!doctype html><title>x</title><p>hi</p>")
        return path

    def test_default_widths_are_desktop_and_phone(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("web-page", "preview_page", self.write(folder))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("page.preview.html", result.stdout)
            with open(os.path.join(folder, "page.preview.html"), encoding="utf-8") as handle:
                preview = handle.read()
            self.assertIn('style="width:1280px"', preview)
            self.assertIn('style="width:390px"', preview)
            self.assertIn('src="page.html"', preview)

    def test_widths_are_chosen_by_the_caller(self):
        with tempfile.TemporaryDirectory() as folder:
            self.assertEqual(run_script("web-page", "preview_page", self.write(folder), "--widths", "768").returncode, 0)
            with open(os.path.join(folder, "page.preview.html"), encoding="utf-8") as handle:
                preview = handle.read()
            self.assertIn('style="width:768px"', preview)
            self.assertNotIn("1280", preview)

    def test_missing_file_exits_two(self):
        self.assertEqual(run_script("web-page", "preview_page", "/nonexistent/page.html").returncode, 2)


if __name__ == "__main__":
    unittest.main()
