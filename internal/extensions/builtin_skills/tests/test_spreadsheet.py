import csv
import json
import os
import tempfile
import unittest
from decimal import Decimal

from _support import load, need_modules, run_script

reconcile = load("spreadsheet", "reconcile")
build_workbook = load("spreadsheet", "build_workbook")


def write(path, text):
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)


class ParseNumbers(unittest.TestCase):
    def test_money_forms(self):
        self.assertEqual(reconcile.money("$1,234.50"), Decimal("1234.50"))
        self.assertEqual(reconcile.money("(45.00)"), Decimal("-45.00"))
        self.assertEqual(reconcile.money("-3"), Decimal("-3"))
        self.assertIsNone(reconcile.money("n/a"))
        self.assertIsNone(reconcile.money(None))
        self.assertIsNone(reconcile.money(True))

    def test_workbook_number_parser(self):
        self.assertEqual(build_workbook.parse_number("12%"), Decimal("0.12"))
        self.assertIsNone(build_workbook.parse_number("twelve"))


class Reconcile(unittest.TestCase):
    def docs(self):
        return {"documents": [
            {"id": "A", "source": "a.pdf", "lines": [{"quantity": 2, "unit_price": "10.00", "amount": "20.00"}],
             "subtotal": "20.00", "tax": "2.00", "total": "22.00"},
            {"id": "B", "source": "b.pdf", "lines": [{"quantity": 3, "unit_price": "5", "amount": "16"}],
             "subtotal": "16", "tax": "1.60", "total": "17.60"},
            {"id": "C", "source": "c.pdf", "lines": [{"quantity": 1, "unit_price": "100", "amount": "100"}],
             "subtotal": "90", "tax": "9", "total": "99"},
            {"id": "D", "source": "d.pdf", "subtotal": "50", "tax": "5", "total": "60"},
            {"id": "E", "source": "e.pdf", "unreadable": ["total"]},
        ]}

    def kinds(self, report):
        return sorted((i["doc"], i["kind"]) for i in report["issues"])

    def test_finds_every_internal_discrepancy(self):
        report = reconcile.reconcile(self.docs())
        found = self.kinds(report)
        self.assertIn(("B", "line_arithmetic"), found)
        self.assertIn(("C", "subtotal"), found)
        self.assertIn(("D", "total"), found)
        self.assertIn(("E", "total_unread"), found)
        self.assertIn(("E", "unreadable"), found)
        self.assertNotIn("A", [d for d, _ in found])

    def test_reference_mismatch_and_missing_rows(self):
        with tempfile.TemporaryDirectory() as folder:
            ref = os.path.join(folder, "ref.csv")
            with open(ref, "w", newline="") as handle:
                writer = csv.writer(handle)
                writer.writerow(["id", "total"])
                writer.writerows([["A", "22.00"], ["B", "18.00"], ["C", "99"], ["Z", "5"]])
            report = reconcile.reconcile(self.docs(), reference=reconcile.load_reference(ref, "id", "total"))
        found = self.kinds(report)
        self.assertIn(("B", "reference_mismatch"), found)
        self.assertIn(("D", "not_in_reference"), found)
        self.assertIn(("Z", "reference_without_document"), found)
        self.assertNotIn(("A", "reference_mismatch"), found)
        self.assertNotIn(("C", "reference_mismatch"), found)

    def test_unprocessed_sources_and_duplicates(self):
        with tempfile.TemporaryDirectory() as folder:
            for name in ("a.pdf", "b.pdf", "extra.pdf"):
                write(os.path.join(folder, name), "x")
            data = {"documents": [{"id": "A", "source": "a.pdf", "total": "1"}, {"id": "A", "source": "b.pdf", "total": "1"}]}
            report = reconcile.reconcile(data, sources_dir=folder)
        found = self.kinds(report)
        self.assertIn(("extra.pdf", "source_not_processed"), found)
        self.assertIn(("A", "duplicate_id"), found)

    def test_grand_totals_are_exact_decimals(self):
        data = {"documents": [{"id": str(i), "total": "0.10", "subtotal": "0.10", "tax": "0"} for i in range(3)]}
        report = reconcile.reconcile(data)
        self.assertEqual(report["stated_total_sum"], "0.30")
        self.assertEqual(report["issues"], [])

    def test_cli_strict_exit_code(self):
        with tempfile.TemporaryDirectory() as folder:
            path = os.path.join(folder, "x.json")
            write(path, json.dumps(self.docs()))
            self.assertEqual(run_script("spreadsheet", "reconcile", path).returncode, 0)
            result = run_script("spreadsheet", "reconcile", path, "--strict")
            self.assertEqual(result.returncode, 1)
            self.assertIn("documents with a discrepancy", result.stdout)


@need_modules("openpyxl")
class WorkbookRoundTrip(unittest.TestCase):
    SPEC = {"sheets": [{"name": "Invoices", "columns": [
        {"key": "id", "title": "Invoice"},
        {"key": "qty", "title": "Qty", "type": "number"},
        {"key": "price", "title": "Price", "type": "currency"},
        {"key": "amount", "title": "Amount", "type": "currency", "formula": "={qty}*{price}"}],
        "rows": [{"id": "A", "qty": "2", "price": "$10.00"}, {"id": "B", "qty": 3, "price": "1,000.50"}],
        "totals": ["amount"]}]}

    def build(self, spec, folder):
        spec_path, out = os.path.join(folder, "spec.json"), os.path.join(folder, "out.xlsx")
        write(spec_path, json.dumps(spec))
        return run_script("spreadsheet", "build_workbook", spec_path, out), out

    def test_numbers_formulas_and_summary(self):
        import openpyxl
        with tempfile.TemporaryDirectory() as folder:
            result, out = self.build(self.SPEC, folder)
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
            sheet = openpyxl.load_workbook(out)["Invoices"]
            self.assertEqual(sheet["C3"].value, 1000.5)
            self.assertEqual(sheet["B2"].value, 2)
            self.assertEqual(sheet["D2"].value, "=B2*C2")
            self.assertEqual(sheet["D4"].value, "=SUM(D2:D3)")
            summary = run_script("spreadsheet", "xlsx_summary", out)
            self.assertEqual(summary.returncode, 0, summary.stdout + summary.stderr)
            self.assertIn("Price (C): count=2 sum=1010.5", summary.stdout)
            self.assertIn("UNCOMPUTED formulas: 3", summary.stdout)
            bad = run_script("spreadsheet", "xlsx_summary", out, "--expect-sum", "Invoices.Price=1.0")
            self.assertEqual(bad.returncode, 1)
            good = run_script("spreadsheet", "xlsx_summary", out, "--expect-sum", "Invoices.Price=1010.5", "--expect-rows", "Invoices=4")
            self.assertEqual(good.returncode, 0, good.stdout)

    def test_unreadable_value_is_reported_not_written(self):
        import openpyxl
        spec = json.loads(json.dumps(self.SPEC))
        spec["sheets"][0]["rows"][0]["price"] = "ten dollars"
        with tempfile.TemporaryDirectory() as folder:
            result, out = self.build(spec, folder)
            self.assertEqual(result.returncode, 1)
            self.assertIn("COULD NOT READ 1 value", result.stdout)
            self.assertIn("ten dollars", result.stdout)
            self.assertIsNone(openpyxl.load_workbook(out)["Invoices"]["C2"].value)


if __name__ == "__main__":
    unittest.main()
