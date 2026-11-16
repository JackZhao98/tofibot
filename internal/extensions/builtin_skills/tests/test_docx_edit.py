import json
import os
import re
import subprocess
import tempfile
import unittest
import zipfile

from _support import need_modules, need_programs, run_script


def make_docx(path):
    """A letter whose key sentence is split over runs of different formatting, plus a Chinese line and a table."""
    import docx
    document = docx.Document()
    document.add_heading("Service agreement", 1)
    p = document.add_paragraph()
    p.add_run("Payment is due ")
    bold = p.add_run("within 30 days")
    bold.bold = True
    p.add_run(" of the invoice date. Late fees apply.")
    document.add_paragraph("The supplier will deliver the goods to the buyer.")
    document.add_paragraph("合同期限为三年，自签字之日起生效。")
    document.add_paragraph("Contact: {{name}} at {{email}}")
    table = document.add_table(rows=2, cols=2)
    table.cell(0, 0).text = "Item"
    table.cell(0, 1).text = "Price"
    table.cell(1, 0).text = "Widget"
    table.cell(1, 1).text = "10"
    document.save(path)


def part(path, name="word/document.xml"):
    with zipfile.ZipFile(path) as archive:
        return archive.read(name).decode("utf-8")


def rewrite(source_path, target_path, name, change):
    """Copy a docx, passing one part through change(text)."""
    with zipfile.ZipFile(source_path) as source, zipfile.ZipFile(target_path, "w") as target:
        for item in source.infolist():
            data = source.read(item.filename)
            if item.filename == name:
                data = change(data.decode("utf-8")).encode("utf-8")
            target.writestr(item, data)


@need_modules("docx", "lxml")
class DocxEdit(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = self.tmp.name
        self.src = os.path.join(self.dir, "in.docx")
        make_docx(self.src)

    def tearDown(self):
        self.tmp.cleanup()

    def tool(self, *args):
        return run_script("docx-edit", "docx_edit", *args)

    def out(self, name="out.docx"):
        return os.path.join(self.dir, name)

    def read(self, path):
        seen = self.tool("read", path)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        return seen.stdout

    def test_read_shows_structure(self):
        text = self.read(self.src)
        self.assertIn("Payment is due within 30 days of the invoice date.", text)
        self.assertIn("table 1:", text)
        self.assertIn("r2: Widget | 10", text)
        self.assertIn("tracked changes: 0, comments: 0", text)

    def test_tracked_replace_across_runs_marks_only_the_changed_words(self):
        seen = self.tool("replace", self.src, "-o", self.out(), "--tracked", "--author", "Tester",
                         "--find", "within 30 days", "--replace", "within 45 days")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        text = self.read(self.out())
        self.assertIn("within [-30-]{+45+} days of the invoice date", text)
        self.assertIn("tracked del by Tester: 1", text)
        xml = part(self.out())
        self.assertIn("<w:delText", xml)
        self.assertIn('w:author="Tester"', xml)
        seen = self.tool("check", self.out(), "--original", self.src)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)

    def test_tracked_replace_keeps_the_formatting_of_the_old_words(self):
        self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "within 30 days", "--replace", "within 45 days")
        xml = part(self.out())
        start = xml.index("<w:ins ")
        self.assertIn("<w:b/>", xml[start:start + 400])

    def test_reject_restores_the_original_and_accept_applies_the_change(self):
        self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "within 30 days", "--replace", "within 45 days")
        seen = self.tool("accept", self.out(), "-o", self.out("a.docx"))
        self.assertEqual(seen.returncode, 0, seen.stderr)
        accepted = self.read(self.out("a.docx"))
        self.assertIn("Payment is due within 45 days of the invoice date.", accepted)
        self.assertIn("tracked changes: 0", accepted)
        seen = self.tool("reject", self.out(), "-o", self.out("r.docx"))
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("Payment is due within 30 days of the invoice date.", self.read(self.out("r.docx")))
        self.assertEqual(self.tool("check", self.out("r.docx"), "--original", self.src).returncode, 0)

    def test_chinese_sentence_is_changed_with_tracking(self):
        seen = self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "合同期限为三年，自签字之日起生效。",
                         "--replace", "合同期限为五年，自签字之日起生效。")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("合同期限为[-三-]{+五+}年，自签字之日起生效。", self.read(self.out()))

    def test_whole_marks_the_entire_found_text(self):
        self.tool("replace", self.src, "-o", self.out(), "--tracked", "--whole", "--find", "within 30 days", "--replace", "within 45 days")
        self.assertIn("[-within 30 days-]{+within 45 days+}", self.read(self.out()))

    def test_insertion_and_deletion_only(self):
        self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "Late fees apply.", "--replace", "Late fees of 2% apply.")
        self.assertIn("Late fees {+of 2% +}apply.", self.read(self.out()))
        self.tool("replace", self.src, "-o", self.out("d.docx"), "--tracked", "--find", " Late fees apply.", "--replace", "")
        self.assertIn("[- Late fees apply.-]", self.read(self.out("d.docx")))
        self.assertEqual(self.tool("check", self.out("d.docx"), "--original", self.src).returncode, 0)

    def test_untracked_edit_is_caught_by_check(self):
        seen = self.tool("replace", self.src, "-o", self.out(), "--find", "within 30 days", "--replace", "within 45 days")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("within 45 days of the invoice date", self.read(self.out()))
        seen = self.tool("check", self.out(), "--original", self.src)
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("text changed without tracking", seen.stderr)

    def test_missing_and_ambiguous_text_fail_without_writing(self):
        seen = self.tool("replace", self.src, "-o", self.out(), "--find", "Within 30 Days", "--replace", "x")
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("different capitals", seen.stderr)
        self.assertFalse(os.path.exists(self.out()))
        seen = self.tool("replace", self.src, "-o", self.out(), "--find", "the", "--replace", "THE")
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("matches of 'the'", seen.stderr)
        self.assertFalse(os.path.exists(self.out()))
        seen = self.tool("replace", self.src, "-o", self.out(), "--find", "the", "--replace", "THE", "--all")
        self.assertEqual(seen.returncode, 0, seen.stderr)

    def test_the_input_is_never_overwritten(self):
        with open(self.src, "rb") as handle:
            before = handle.read()
        seen = self.tool("replace", self.src, "-o", self.src, "--find", "Late", "--replace", "Early")
        self.assertNotEqual(seen.returncode, 0)
        self.assertIn("is the input file", seen.stderr)
        with open(self.src, "rb") as handle:
            self.assertEqual(handle.read(), before)

    def test_map_fills_placeholders_in_place(self):
        mapping = os.path.join(self.dir, "m.json")
        with open(mapping, "w", encoding="utf-8") as handle:
            json.dump({"{{name}}": "Ada Lovelace", "{{email}}": "ada@example.com"}, handle)
        seen = self.tool("replace", self.src, "-o", self.out(), "--map", mapping)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("Contact: Ada Lovelace at ada@example.com", self.read(self.out()))

    def test_text_in_a_table_cell_is_changed(self):
        seen = self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "Widget", "--replace", "Gadget")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("r2: [-Widget-]{+Gadget+} | 10", self.read(self.out()))

    def test_comment_is_anchored_and_the_file_stays_valid(self):
        seen = self.tool("comment", self.src, "-o", self.out(), "--find", "within 30 days", "--text", "Check this term with legal.", "--author", "Ada Lovelace")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        text = self.read(self.out())
        self.assertIn("comment 1 by Ada Lovelace", text)
        self.assertIn("'within 30 days'", text)
        self.assertIn("Check this term with legal.", text)
        seen = self.tool("check", self.out(), "--original", self.src)
        self.assertEqual(seen.returncode, 0, seen.stdout + seen.stderr)
        second = self.tool("comment", self.out(), "-o", self.out("c2.docx"), "--find", "Widget", "--text", "Price?")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("comments: 2", self.read(self.out("c2.docx")))

    def test_a_run_with_a_tab_inside_is_searched(self):
        path = self.out("tab.docx")
        rewrite(self.src, path, "word/document.xml",
                lambda xml: xml.replace("<w:t>Contact: {{name}} at {{email}}</w:t>", "<w:t>Phone</w:t><w:tab/><w:t>555-0100</w:t>"))
        self.assertIn("<w:tab/>", part(path))
        seen = self.tool("replace", path, "-o", self.out("t2.docx"), "--tracked", "--whole", "--find", "555-0100", "--replace", "555-0199")
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertIn("Phone\t[-555-0100-]{+555-0199+}", self.read(self.out("t2.docx")))
        self.assertEqual(self.tool("check", self.out("t2.docx"), "--original", path).returncode, 0)

    def test_paragraph_deletion_joins_on_accept_and_returns_on_reject(self):
        path = self.out("para.docx")
        old = "<w:p><w:r><w:t>The supplier will deliver the goods to the buyer.</w:t></w:r></w:p>"
        new = ('<w:p><w:pPr><w:rPr><w:del w:id="900" w:author="X" w:date="2026-01-01T00:00:00Z"/></w:rPr></w:pPr>'
               '<w:del w:id="901" w:author="X" w:date="2026-01-01T00:00:00Z"><w:r><w:delText>The supplier will deliver the goods to the buyer.</w:delText></w:r></w:del></w:p>')
        self.assertIn(old, part(self.src))
        rewrite(self.src, path, "word/document.xml", lambda xml: xml.replace(old, new))
        seen = self.tool("accept", path, "-o", self.out("acc.docx"))
        self.assertEqual(seen.returncode, 0, seen.stderr)
        accepted = self.read(self.out("acc.docx"))
        self.assertNotIn("supplier", accepted)
        self.assertEqual(len(re.findall(r"^p\d+", accepted, re.M)), 4)
        seen = self.tool("reject", path, "-o", self.out("rej.docx"))
        self.assertEqual(seen.returncode, 0, seen.stderr)
        rejected = self.read(self.out("rej.docx"))
        self.assertIn("The supplier will deliver the goods to the buyer.", rejected)
        self.assertNotIn("paragraph mark deleted", rejected)

    def test_unpack_and_pack_round_trip(self):
        import docx
        folder = os.path.join(self.dir, "unpacked")
        seen = self.tool("unpack", self.src, folder)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        self.assertTrue(os.path.exists(os.path.join(folder, "word", "document.xml")))
        packed = self.out("packed.docx")
        seen = self.tool("pack", folder, "-o", packed)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        with zipfile.ZipFile(packed) as archive:
            self.assertEqual(archive.namelist()[0], "[Content_Types].xml")
        self.assertIn("Service agreement", [p.text for p in docx.Document(packed).paragraphs])
        self.assertNotEqual(self.tool("pack", folder, "-o", packed).returncode, 0)
        with open(os.path.join(folder, "word", "document.xml"), "a", encoding="utf-8") as handle:
            handle.write("<broken")
        self.assertNotEqual(self.tool("pack", folder, "-o", self.out("bad.docx")).returncode, 0)

    def test_check_rejects_damaged_files(self):
        bad = self.out("bad.docx")
        with open(bad, "wb") as handle:
            handle.write(b"not a zip")
        self.assertNotEqual(self.tool("check", bad).returncode, 0)
        broken = self.out("broken.docx")
        rewrite(self.src, broken, "word/document.xml", lambda xml: xml.replace("</w:body>", "</w:bodyy>"))
        self.assertNotEqual(self.tool("check", broken).returncode, 0)

    @need_programs("soffice", "pdftoppm")
    def test_the_edited_file_renders_to_pages(self):
        self.tool("replace", self.src, "-o", self.out(), "--tracked", "--find", "within 30 days", "--replace", "within 45 days")
        profile = os.path.join(self.dir, "profile")
        seen = subprocess.run(["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--convert-to", "pdf", "--outdir", self.dir, self.out()],
                              capture_output=True, text=True, timeout=240)
        self.assertEqual(seen.returncode, 0, seen.stderr)
        pdf = os.path.join(self.dir, "out.pdf")
        self.assertTrue(os.path.exists(pdf))
        subprocess.run(["pdftoppm", "-png", "-r", "50", pdf, os.path.join(self.dir, "pg")], check=True, timeout=120)
        self.assertTrue(any(n.startswith("pg") for n in os.listdir(self.dir)))


if __name__ == "__main__":
    unittest.main()
