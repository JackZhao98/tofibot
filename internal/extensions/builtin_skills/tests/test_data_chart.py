import os
import tempfile
import unittest

from _support import need_modules, run_script

CSV = "month,region,sales\n" + "\n".join("2026-%02d,%s,%d" % (m, r, v + m * k) for m in (1, 2, 3)
                                          for r, v, k in (("North", 100, 2), ("South", 50, 9))) + "\n"


def write(folder, name, text):
    path = os.path.join(folder, name)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)
    return path


@need_modules("pandas", "matplotlib")
class ChartFromTable(unittest.TestCase):
    def test_long_table_draws_a_png_and_prints_numbers(self):
        with tempfile.TemporaryDirectory() as folder:
            data = write(folder, "s.csv", CSV)
            out = os.path.join(folder, "c.png")
            result = run_script("data-chart", "chart_from_table", data, "--x", "month", "--y", "sales", "--group", "region",
                                "--title", "South grows fastest", "--ylabel", "Sales (k USD)", "--out", out)
            self.assertEqual(result.returncode, 0, result.stderr)
            with open(out, "rb") as handle:
                self.assertEqual(handle.read(8), b"\x89PNG\r\n\x1a\n")
            self.assertIn("wrote " + out, result.stdout)
            self.assertIn("2026-03", result.stdout)
            self.assertIn("South", result.stdout)

    def test_refuses_non_numeric_series(self):
        with tempfile.TemporaryDirectory() as folder:
            data = write(folder, "s.csv", "k,v\na,x\nb,y\n")
            result = run_script("data-chart", "chart_from_table", data, "--x", "k", "--y", "v", "--title", "t", "--out", os.path.join(folder, "c.png"))
            self.assertEqual(result.returncode, 1)
            self.assertIn("no numeric values", result.stderr)
            self.assertFalse(os.path.exists(os.path.join(folder, "c.png")))

    def test_refuses_partly_bad_values_empty_table_and_missing_column(self):
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "c.png")
            bad = write(folder, "b.csv", "k,v\na,1\nb,n/a-ish\n")
            result = run_script("data-chart", "chart_from_table", bad, "--x", "k", "--y", "v", "--title", "t", "--out", out)
            self.assertEqual(result.returncode, 1)
            self.assertIn("not numbers", result.stderr)
            empty = write(folder, "e.csv", "k,v\n")
            result = run_script("data-chart", "chart_from_table", empty, "--x", "k", "--y", "v", "--title", "t", "--out", out)
            self.assertEqual(result.returncode, 1)
            good = write(folder, "g.csv", "k,v\na,1\n")
            result = run_script("data-chart", "chart_from_table", good, "--x", "k", "--y", "nope", "--title", "t", "--out", out)
            self.assertEqual(result.returncode, 1)
            self.assertIn("not in the table", result.stderr)

    def test_bar_chart_with_table_out_and_cjk_labels(self):
        from matplotlib import font_manager
        has_cjk = any("CJK" in f.name or f.name in ("PingFang SC", "Heiti SC", "Hiragino Sans GB", "WenQuanYi Zen Hei", "Microsoft YaHei")
                      for f in font_manager.fontManager.ttflist)
        with tempfile.TemporaryDirectory() as folder:
            data = write(folder, "s.csv", "地区,销售额\n华东,10\n华北,8\n")
            out = os.path.join(folder, "c.png")
            numbers = os.path.join(folder, "n.csv")
            result = run_script("data-chart", "chart_from_table", data, "--x", "地区", "--y", "销售额", "--kind", "bar",
                                "--title", "华东销售额最高", "--table-out", numbers, "--out", out)
            if has_cjk:
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertTrue(os.path.getsize(numbers) > 0)
            else:
                self.assertEqual(result.returncode, 1)
                self.assertIn("no CJK font", result.stderr)


if __name__ == "__main__":
    unittest.main()
