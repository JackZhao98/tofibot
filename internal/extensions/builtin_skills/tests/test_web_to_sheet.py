import os
import tempfile
import unittest

from _support import need_modules, run_script

PAGE = """<html><body><table><tr><td>layout box</td></tr></table>
<table><caption>Sales</caption>
<thead><tr><th>Item</th><th>Price</th><th>Share</th><th>Date</th></tr></thead>
<tbody>
<tr><td>Alpha[1]</td><td>$1,200.50</td><td>12.5%</td><td>2026-01-05</td></tr>
<tr><td>Beta</td><td>980</td><td>7%</td><td>2026-02-10</td></tr>
<tr><td>Gamma</td><td>&mdash;</td><td>80.5%</td><td>2026-03-15</td></tr>
</tbody></table></body></html>"""
URL = "https://example.test/sales"


@need_modules("pandas", "openpyxl", "lxml")
class HtmlTablesToXlsx(unittest.TestCase):
    def page(self, folder):
        path = os.path.join(folder, "page.html")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(PAGE)
        return path

    def test_list_tables(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("web-to-sheet", "html_tables_to_xlsx", self.page(folder), "--list")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("table 2: 3 rows x 4 columns; Item | Price | Share | Date", result.stdout)

    def test_writes_typed_data_and_source_sheet(self):
        import datetime
        import openpyxl
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "o.xlsx")
            result = run_script("web-to-sheet", "html_tables_to_xlsx", self.page(folder), "--url", URL, "--table", 2, "--out", out)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("rows counted in the page markup: 3; rows in the workbook: 3", result.stdout)
            book = openpyxl.load_workbook(out)
            rows = list(book["Data"].iter_rows(values_only=True))
            self.assertEqual(rows[0], ("Item", "Price", "Share", "Date"))
            self.assertEqual(rows[1][0], "Alpha")
            self.assertEqual(rows[1][1], 1200.5)
            self.assertIsNone(rows[3][1])
            self.assertAlmostEqual(rows[1][2], 0.125)
            self.assertEqual(book["Data"]["C2"].number_format, "0.0%")
            self.assertIsInstance(rows[1][3], datetime.datetime)
            source = {row[0]: row[1] for row in book["Source"].iter_rows(values_only=True)}
            self.assertEqual(source["Page URL"], URL)
            self.assertRegex(source["Read at (UTC)"], r"^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$")
            self.assertEqual(source["Data rows"], 3)

    def test_file_source_needs_url_and_table(self):
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "o.xlsx")
            result = run_script("web-to-sheet", "html_tables_to_xlsx", self.page(folder), "--table", 2, "--out", out)
            self.assertEqual(result.returncode, 1)
            self.assertIn("--url", result.stderr)
            result = run_script("web-to-sheet", "html_tables_to_xlsx", self.page(folder), "--url", URL, "--out", out)
            self.assertEqual(result.returncode, 1)
            self.assertIn("--table", result.stderr)
            self.assertFalse(os.path.exists(out))

    def test_no_table_and_bad_table_number(self):
        with tempfile.TemporaryDirectory() as folder:
            empty = os.path.join(folder, "e.html")
            with open(empty, "w", encoding="utf-8") as handle:
                handle.write("<html><body><p>none</p></body></html>")
            result = run_script("web-to-sheet", "html_tables_to_xlsx", empty, "--url", URL, "--table", 1, "--out", os.path.join(folder, "o.xlsx"))
            self.assertEqual(result.returncode, 1)
            result = run_script("web-to-sheet", "html_tables_to_xlsx", self.page(folder), "--url", URL, "--table", 9, "--out", os.path.join(folder, "o.xlsx"))
            self.assertEqual(result.returncode, 1)
            self.assertIn("does not exist", result.stderr)


if __name__ == "__main__":
    unittest.main()
