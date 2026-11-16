import os
import shutil
import subprocess
import tempfile
import unittest
import zipfile

from _support import SKILLS, need_programs, run_script

PATH = os.path.join(SKILLS, "file-convert", "SKILL.md")
MD = "# 报告\n\n正文 paragraph.\n\n| 名称 | 数量 |\n|---|---|\n| 苹果 | 3 |\n| 梨 | 5 |\n\n- 一\n- 二\n"


class FileConvertText(unittest.TestCase):
    def test_covers_the_tools_and_the_losses(self):
        with open(PATH, encoding="utf-8") as handle:
            text = handle.read()
        for needle in ("pandoc", "soffice", "pdftotext", "pdftoppm", "convert", "zip", "tar", "inspect_file.py", "What conversion loses", "document-report", "files.view"):
            self.assertIn(needle, text)


class InspectScript(unittest.TestCase):
    def test_markdown_zip_and_docx_counts(self):
        with tempfile.TemporaryDirectory() as d:
            md = os.path.join(d, "a.md")
            with open(md, "w", encoding="utf-8") as handle:
                handle.write(MD)
            out = run_script("file-convert", "inspect_file", md)
            self.assertEqual(out.returncode, 0, out.stderr)
            self.assertIn("table rows 4", out.stdout)
            self.assertIn("headings 1", out.stdout)
            archive = os.path.join(d, "a.zip")
            with zipfile.ZipFile(archive, "w") as z:
                z.write(md, "a.md")
            out = run_script("file-convert", "inspect_file", archive)
            self.assertIn("entries: 1", out.stdout)
            docx = os.path.join(d, "a.docx")
            with zipfile.ZipFile(docx, "w") as z:
                z.writestr("word/document.xml", '<w:document><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>报告</w:t></w:r></w:p>'
                           '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>')
            out = run_script("file-convert", "inspect_file", docx)
            self.assertIn("tables: 1", out.stdout)
            self.assertIn("headings: 1", out.stdout)

    def test_missing_file_fails(self):
        self.assertNotEqual(run_script("file-convert", "inspect_file", "/nonexistent/x.docx").returncode, 0)


@need_programs("pandoc")
class PandocRoundTrip(unittest.TestCase):
    def test_markdown_to_docx_keeps_the_table(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "a.md"), "w", encoding="utf-8") as handle:
                handle.write(MD)
            r = subprocess.run("pandoc a.md -o a.docx", shell=True, cwd=d, capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            out = run_script("file-convert", "inspect_file", os.path.join(d, "a.docx"))
            self.assertIn("tables: 1", out.stdout)
            self.assertIn("table rows: 3", out.stdout)
            self.assertIn("报告", out.stdout)


@need_programs("soffice")
class SofficeScript(unittest.TestCase):
    def test_docx_to_pdf(self):
        if shutil.which("pandoc") is None:
            self.skipTest("pandoc missing")
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "a.md"), "w", encoding="utf-8") as handle:
                handle.write(MD)
            subprocess.run("pandoc a.md -o a.docx", shell=True, cwd=d, check=True)
            r = run_script("file-convert", "soffice_convert", os.path.join(d, "a.docx"), "pdf", "--outdir", os.path.join(d, "out"))
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertTrue(os.path.getsize(os.path.join(d, "out", "a.pdf")) > 1000)
            same = run_script("file-convert", "soffice_convert", os.path.join(d, "a.docx"), "pdf", "--outdir", d)
            self.assertNotEqual(same.returncode, 0)


@need_programs("unzip", "tar")
class Archives(unittest.TestCase):
    def test_zip_tar_roundtrip(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "f"))
            with open(os.path.join(d, "f", "文件.txt"), "w", encoding="utf-8") as handle:
                handle.write("hi")
            for cmd in ("python3 -m zipfile -c o.zip f/", "mkdir x && unzip -q o.zip -d x", "tar -czf o.tar.gz -C x f", "tar -tzf o.tar.gz"):
                r = subprocess.run(cmd, shell=True, cwd=d, capture_output=True, text=True)
                self.assertEqual(r.returncode, 0, r.stderr)
            self.assertIn("文件.txt", r.stdout)


if __name__ == "__main__":
    unittest.main()
