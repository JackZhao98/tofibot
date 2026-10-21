import os
import sys
import tempfile
import unittest
import zipfile

from _support import load, run_script

package_project = load("code-delivery", "package_project")


def touch(root, rel, text="x"):
    path = os.path.join(root, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)


class RunAndRecord(unittest.TestCase):
    def test_transcript_carries_command_exit_code_and_output(self):
        with tempfile.TemporaryDirectory() as folder:
            out = os.path.join(folder, "t.txt")
            result = run_script("code-delivery", "run_and_record", "--out", out, "--", sys.executable, "-c", "print('hello'); import sys; sys.stderr.write('warn')")
            self.assertEqual(result.returncode, 0)
            with open(out, encoding="utf-8") as handle:
                transcript = handle.read()
            self.assertEqual(transcript, result.stdout)
            self.assertIn("exit code: 0", transcript)
            self.assertIn("hello", transcript)
            self.assertIn("warn", transcript)
            self.assertTrue(transcript.startswith("$ " + sys.executable))

    def test_failing_command_returns_its_own_status(self):
        result = run_script("code-delivery", "run_and_record", "--", sys.executable, "-c", "import sys; print('boom'); sys.exit(3)")
        self.assertEqual(result.returncode, 3)
        self.assertIn("exit code: 3", result.stdout)

    def test_timeout_and_missing_command(self):
        slow = run_script("code-delivery", "run_and_record", "--timeout", "1", "--", sys.executable, "-c", "import time; time.sleep(5)")
        self.assertEqual(slow.returncode, 124)
        self.assertIn("timed out", slow.stdout)
        self.assertEqual(run_script("code-delivery", "run_and_record", "--", "no-such-command-xyz").returncode, 127)

    def test_long_output_keeps_head_and_tail(self):
        result = run_script("code-delivery", "run_and_record", "--max-chars", "100", "--", sys.executable, "-c", "print('A'*300 + 'Z'*300)")
        self.assertIn("characters cut", result.stdout)
        self.assertIn("AAAA", result.stdout)
        self.assertIn("ZZZZ", result.stdout)


class Package(unittest.TestCase):
    def test_zip_leaves_out_junk_and_secrets_and_verifies(self):
        with tempfile.TemporaryDirectory() as folder:
            project = os.path.join(folder, "app")
            for rel in ("README.md", "src/main.py", "tests/test_main.py", "node_modules/dep/index.js", ".git/config", "__pycache__/m.pyc",
                        ".env", ".env.production", "keys/server.pem", "id_rsa", "debug.log"):
                touch(project, rel)
            out = os.path.join(folder, "app.zip")
            result = package_project.package(project, out)
            self.assertEqual(result["problems"], [])
            self.assertEqual(result["files"], ["README.md", "src/main.py", "tests/test_main.py"])
            excluded = dict(result["excluded"])
            self.assertEqual(excluded[".env"], "possible secret")
            self.assertEqual(excluded["keys/server.pem"], "possible secret")
            with zipfile.ZipFile(out) as archive:
                self.assertEqual(sorted(archive.namelist()), ["app/README.md", "app/src/main.py", "app/tests/test_main.py"])
                self.assertIsNone(archive.testzip())

    def test_cli_output_and_missing_readme_warning(self):
        with tempfile.TemporaryDirectory() as folder:
            project = os.path.join(folder, "p")
            touch(project, "a.py")
            result = run_script("code-delivery", "package_project", project, os.path.join(project, "out.zip"))
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertIn("files: 1", result.stdout)
            self.assertIn("WARNING: no README", result.stdout)
            self.assertNotIn("out.zip\n  +", result.stdout)  # the archive never packs itself
            self.assertEqual(run_script("code-delivery", "package_project", os.path.join(folder, "nope"), "x.zip").returncode, 2)


if __name__ == "__main__":
    unittest.main()
