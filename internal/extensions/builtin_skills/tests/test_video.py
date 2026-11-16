import os
import re
import shutil
import subprocess
import tempfile
import unittest

from _support import SKILLS, need_programs

PATH = os.path.join(SKILLS, "video", "SKILL.md")


def commands():
    with open(PATH, encoding="utf-8") as handle:
        return re.findall(r"`(ffmpeg [^`]+)`", handle.read())


def shell(command, cwd):
    return subprocess.run(command, shell=True, cwd=cwd, capture_output=True, text=True, timeout=120)


class VideoSkillText(unittest.TestCase):
    def setUp(self):
        with open(PATH, encoding="utf-8") as handle:
            self.text = handle.read()

    def test_covers_each_part_of_reading_a_video(self):
        for needle in ("ffprobe", "scene", "showinfo", "tile", "files.view", "tesseract", "transcribe_audio", "Settings"):
            self.assertIn(needle, self.text)

    def test_has_no_script_and_no_deliverable_wording(self):
        self.assertFalse(os.path.exists(os.path.join(SKILLS, "video", "scripts")))
        self.assertNotIn("Publish only the final", self.text)


@need_programs("ffmpeg", "ffprobe")
class VideoSkillCommands(unittest.TestCase):
    """The commands in the Skill run on a synthetic three-scene video and report the cut times."""

    @classmethod
    def setUpClass(cls):
        cls.folder = tempfile.mkdtemp()
        make = ("ffmpeg -loglevel error -y -f lavfi -i color=c=red:s=320x240:d=2 -f lavfi -i color=c=blue:s=320x240:d=2 "
                "-f lavfi -i color=c=green:s=320x240:d=2 -filter_complex '[0][1][2]concat=n=3:v=1:a=0,format=yuv420p' in.mp4")
        result = shell(make, cls.folder)
        assert result.returncode == 0, result.stderr

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.folder, ignore_errors=True)

    def find(self, needle):
        for command in commands():
            if needle in command:
                return command
        self.fail("no command with " + needle)

    def test_scene_command_prints_the_cut_times(self):
        result = shell(self.find("gt(scene"), self.folder)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(), ["pts_time:2", "pts_time:4"])

    def test_even_spread_command_prints_every_frame_time(self):
        result = shell(self.find("fps=1/2,showinfo"), self.folder)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(), ["pts_time:0", "pts_time:2", "pts_time:4"])

    def test_contact_sheet_command_writes_a_picture(self):
        result = shell(self.find("tile="), self.folder)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertGreater(os.path.getsize(os.path.join(self.folder, "sheet.png")), 0)

    def test_audio_command_fails_cleanly_on_a_silent_video(self):
        result = shell(self.find("-vn"), self.folder)
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
