import json
import os
import tempfile
import unittest

from _support import need_modules, need_programs, run_script


@need_modules("openpyxl")
class XlsxSummary(unittest.TestCase):
    def workbook(self, folder, bad=False):
        import openpyxl
        wb = openpyxl.Workbook()
        ws = wb.active
        ws.title = "Data"
        ws.append(["Item", "Qty", "Price"])
        ws.append(["A", 2, 10.5])
        ws.append(["B", 3, 1000.5])
        ws["D1"] = "Total"
        ws["D2"] = "=B2*C2"
        if bad:
            ws["E2"] = "#DIV/0!"
        path = os.path.join(folder, "book.xlsx")
        wb.save(path)
        return path

    def test_structure_dump_and_uncomputed_formulas(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("spreadsheet", "xlsx_summary", self.workbook(folder))
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("header: Item | Qty | Price | Total", result.stdout)
            self.assertIn("Price (C): count=2 sum=1011.0", result.stdout)
            self.assertIn("UNCOMPUTED formulas: 1", result.stdout)

    def test_error_cells_fail(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("spreadsheet", "xlsx_summary", self.workbook(folder, bad=True))
            self.assertEqual(result.returncode, 1)
            self.assertIn("E2=#DIV/0!", result.stdout)

    def test_json_output(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("spreadsheet", "xlsx_summary", self.workbook(folder), "--json")
            self.assertEqual(json.loads(result.stdout)["sheets"][0]["rows"], 3)


def budget(folder, formula_cell=None):
    import openpyxl
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = "Budget"
    ws.append(["Item", "Amount"])
    ws.append(["Rent", 1200])
    ws.append(["Food", 450.5])
    ws["A4"] = "Total"
    ws["B4"] = "=SUM(B2:B3)"
    if formula_cell:
        ws["C2"] = formula_cell
    path = os.path.join(folder, "budget.xlsx")
    wb.save(path)
    return path


@need_modules("openpyxl")
class Recalc(unittest.TestCase):
    def test_check_fails_on_formula_without_calculated_value(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("spreadsheet", "xlsx_recalc", "--check", budget(folder))
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertIn("no calculated value", result.stdout)

    def test_check_fails_on_error_value(self):
        with tempfile.TemporaryDirectory() as folder:
            import openpyxl
            path = budget(folder)
            wb = openpyxl.load_workbook(path)
            wb.active["C2"] = "#DIV/0!"
            wb.save(path)
            result = run_script("spreadsheet", "xlsx_recalc", "--check", path)
            self.assertEqual(result.returncode, 1)
            self.assertIn("#DIV/0!", result.stdout)

    def test_check_warns_about_functions_that_do_not_calculate(self):
        with tempfile.TemporaryDirectory() as folder:
            result = run_script("spreadsheet", "xlsx_recalc", "--check", budget(folder, '=XLOOKUP("Rent",A2:A3,B2:B3)'))
            self.assertIn("XLOOKUP may not be calculable", result.stdout)

    def test_refuses_to_overwrite_input(self):
        with tempfile.TemporaryDirectory() as folder:
            path = budget(folder)
            self.assertEqual(run_script("spreadsheet", "xlsx_recalc", path, path).returncode, 2)

    def test_unreadable_file_exits_two(self):
        with tempfile.TemporaryDirectory() as folder:
            path = os.path.join(folder, "bad.xlsx")
            with open(path, "w") as handle:
                handle.write("not a workbook")
            self.assertEqual(run_script("spreadsheet", "xlsx_recalc", "--check", path).returncode, 2)

    @need_programs("soffice")
    def test_recalc_keeps_formulas_and_adds_values(self):
        import openpyxl
        with tempfile.TemporaryDirectory() as folder:
            source, target = budget(folder), os.path.join(folder, "out.xlsx")
            result = run_script("spreadsheet", "xlsx_recalc", source, target)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(openpyxl.load_workbook(target)["Budget"]["B4"].value, "=SUM(B2:B3)")
            self.assertEqual(openpyxl.load_workbook(target, data_only=True)["Budget"]["B4"].value, 1650.5)
            self.assertIsNone(openpyxl.load_workbook(source, data_only=True)["Budget"]["B4"].value)

    @need_programs("soffice")
    def test_recalc_exits_nonzero_when_a_cell_holds_an_error(self):
        with tempfile.TemporaryDirectory() as folder:
            source = budget(folder, "=B2/0")
            result = run_script("spreadsheet", "xlsx_recalc", source, os.path.join(folder, "out.xlsx"))
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertIn("#DIV/0!", result.stdout)


if __name__ == "__main__":
    unittest.main()
