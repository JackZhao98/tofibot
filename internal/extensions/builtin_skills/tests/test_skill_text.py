"""Rules every built-in SKILL.md and script must keep (no libraries needed)."""
import glob
import os
import re
import subprocess
import sys
import unittest

from _support import SKILLS

SKILL_FILES = sorted(glob.glob(os.path.join(SKILLS, "*", "SKILL.md")))
EXPECTED = {"spreadsheet", "document-report", "slides", "web-page", "code-delivery"}
# Principles only: no sample sentences, no contrasts, no keyword lists.
BANNED = [r"\be\.g\.", r"\bi\.e\.", r"for example", r"for instance", r"such as", r"rather than", r"instead of", r"\bnot\b[^.\n]*\bbut\b",
          r"\bbut not\b", r"\bnever\b[^.\n]*\balways\b", r"[\"“”]"]


def split(path):
    with open(path, encoding="utf-8") as handle:
        text = handle.read()
    head, body = text[3:].split("\n---", 1)
    return dict(line.split(": ", 1) for line in head.strip().splitlines()), body


class SkillText(unittest.TestCase):
    def test_the_expected_skills_exist(self):
        self.assertEqual({os.path.basename(os.path.dirname(p)) for p in SKILL_FILES}, EXPECTED)

    def test_frontmatter_is_a_one_line_description(self):
        for path in SKILL_FILES:
            meta, _ = split(path)
            self.assertEqual(meta["name"], os.path.basename(os.path.dirname(path)))
            self.assertLessEqual(len(meta["description"]), 100, path)
            self.assertNotIn("\n", meta["description"])

    def test_every_skill_states_the_verification_principle(self):
        for path in SKILL_FILES:
            _, body = split(path)
            self.assertIn("Every claim in the reply is true of the delivered", body, path)
            self.assertIn("Publish only the final", body, path)

    def test_text_keeps_to_principles(self):
        for path in SKILL_FILES:
            meta, body = split(path)
            for pattern in BANNED:
                for line in (meta["description"] + "\n" + body).splitlines():
                    self.assertIsNone(re.search(pattern, line, re.I), "%s: %r matches %s" % (path, line, pattern))

    def test_every_script_documents_itself(self):
        for path in glob.glob(os.path.join(SKILLS, "*", "scripts", "*.py")):
            if os.path.basename(path) == "deckkit.py":
                continue
            result = subprocess.run([sys.executable, path, "--help"], capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 0, "%s: %s" % (path, result.stderr))
            self.assertIn("usage:", result.stdout.lower(), path)

    def test_scripts_run_on_python_39(self):
        for path in glob.glob(os.path.join(SKILLS, "*", "scripts", "*.py")):
            with open(path, encoding="utf-8") as handle:
                source = handle.read()
            self.assertIn("from __future__ import annotations", source, path)
            compile(source, path, "exec")


if __name__ == "__main__":
    unittest.main()
