import os
import tempfile
import unittest

from _support import load, run_script

check_page = load("web-page", "check_page")

GOOD = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Garden Cafe</title>
<style>body{font:16px system-ui;margin:0}main{max-width:40rem;margin:auto;padding:1rem}
.hero{background:url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'/>")}
@media (min-width:700px){main{padding:3rem;width:900px}}</style></head>
<body><main><h1>Garden Cafe</h1><img alt="Logo" src="data:image/png;base64,AAAA"><p>Open daily.</p>
<a href="https://example.com/menu">Menu</a></main></body></html>
"""


def write(folder, text, name="page.html"):
    path = os.path.join(folder, name)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)
    return path


class CheckPage(unittest.TestCase):
    def test_good_page_passes(self):
        with tempfile.TemporaryDirectory() as folder:
            report = check_page.check(write(folder, GOOD))
        self.assertEqual(report["errors"], [])
        self.assertEqual(report["warnings"], [])
        self.assertEqual(report["media_queries"], 1)

    def test_each_defect_is_reported(self):
        bad = """<html><head><title></title><link rel="stylesheet" href="https://cdn.example/x.css">
<script src="app.js"></script><style>@import url(https://fonts.example/f.css);.a{width:900px}</style></head>
<body><h1>A</h1><h1>B</h1><img src="photo.jpg"><p>Lorem ipsum {{name}}</p><div></body></html>"""
        with tempfile.TemporaryDirectory() as folder:
            report = check_page.check(write(folder, bad))
        text = "\n".join(report["errors"])
        for needle in ("doctype", "lang", "charset", "viewport", "title", "exactly one <h1>", "https://cdn.example/x.css", "app.js",
                       "@import", "without alt", "placeholder", "unbalanced"):
            self.assertIn(needle, text)
        self.assertTrue(any("900px" in w for w in report["warnings"]))

    def test_cli_exit_codes_and_preview(self):
        with tempfile.TemporaryDirectory() as folder:
            path = write(folder, GOOD)
            result = run_script("web-page", "check_page", path, "--preview")
            self.assertEqual(result.returncode, 0, result.stdout)
            with open(os.path.join(folder, "page.preview.html"), encoding="utf-8") as handle:
                preview = handle.read()
            self.assertIn('style="width:390px"', preview)
            self.assertIn('style="width:1280px"', preview)
            self.assertIn('src="page.html"', preview)
            broken = write(folder, "<p>hi</p>", "broken.html")
            self.assertEqual(run_script("web-page", "check_page", broken).returncode, 1)


if __name__ == "__main__":
    unittest.main()
