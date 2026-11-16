import os
import re
import shutil
import subprocess
import tempfile
import unittest

from _support import SKILLS, need_programs, run_script

PATH = os.path.join(SKILLS, "video-edit", "SKILL.md")


def shell(command, cwd):
    return subprocess.run(command, shell=True, cwd=cwd, capture_output=True, text=True, timeout=180)


class VideoEditText(unittest.TestCase):
    def test_keeps_apart_from_the_reading_skill_and_covers_the_jobs(self):
        with open(PATH, encoding="utf-8") as handle:
            text = handle.read()
        self.assertIn("video Skill", text)
        for needle in ("-ss", "concat", "atempo", "-vn", "gif_budget.py", "compress_to_size.py", "subtitles=", "ffprobe", "tile=", "files.view"):
            self.assertIn(needle, text)
        self.assertNotIn("imageio", text)


@need_programs("ffmpeg", "ffprobe")
class VideoEditCommands(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.mkdtemp()
        r = shell("ffmpeg -v error -y -f lavfi -i testsrc=s=320x240:d=6:r=24 -f lavfi -i sine=d=6 -shortest -c:v libx264 -pix_fmt yuv420p -c:a aac in.mp4", cls.dir)
        assert r.returncode == 0, r.stderr

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.dir, ignore_errors=True)

    def duration(self, name):
        out = shell("ffprobe -v error -show_entries format=duration -of csv=p=0 " + name, self.dir).stdout
        return float(out)

    def test_cut_speed_audio_and_join(self):
        self.assertEqual(shell("ffmpeg -v error -y -ss 1 -to 4 -i in.mp4 -c:v libx264 -c:a aac cut.mp4", self.dir).returncode, 0)
        self.assertAlmostEqual(self.duration("cut.mp4"), 3.0, delta=0.3)
        self.assertEqual(shell("ffmpeg -v error -y -i in.mp4 -filter_complex '[0:v]setpts=PTS/2[v];[0:a]atempo=2[a]' -map '[v]' -map '[a]' fast.mp4", self.dir).returncode, 0)
        self.assertAlmostEqual(self.duration("fast.mp4"), 3.0, delta=0.4)
        self.assertEqual(shell("ffmpeg -v error -y -i in.mp4 -vn -c:a libmp3lame -q:a 4 a.mp3", self.dir).returncode, 0)
        self.assertTrue(os.path.getsize(os.path.join(self.dir, "a.mp3")) > 1000)
        with open(os.path.join(self.dir, "list.txt"), "w") as handle:
            handle.write("file 'cut.mp4'\nfile 'cut.mp4'\n")
        self.assertEqual(shell("ffmpeg -v error -y -f concat -safe 0 -i list.txt -c copy joined.mp4", self.dir).returncode, 0)
        self.assertAlmostEqual(self.duration("joined.mp4"), 6.0, delta=0.5)

    def test_gif_budget_fits_and_shrinks(self):
        out = os.path.join(self.dir, "o.gif")
        big = run_script("video-edit", "gif_budget", os.path.join(self.dir, "in.mp4"), out, "--duration", 3, "--max-bytes", 5000000)
        self.assertEqual(big.returncode, 0, big.stderr)
        loose = os.path.getsize(out)
        budget = int(loose * 0.5)
        tight = run_script("video-edit", "gif_budget", os.path.join(self.dir, "in.mp4"), out, "--duration", 3, "--max-bytes", budget)
        self.assertIn(tight.returncode, (0, 2), tight.stderr)
        if tight.returncode == 0:
            self.assertLessEqual(os.path.getsize(out), budget)
            self.assertGreater(tight.stdout.count("try "), 1)
        with open(out, "rb") as handle:
            header = handle.read(6)
        self.assertEqual(header[:3], b"GIF")

    def test_gif_budget_exit_code_when_impossible(self):
        out = os.path.join(self.dir, "x.gif")
        result = run_script("video-edit", "gif_budget", os.path.join(self.dir, "in.mp4"), out, "--max-bytes", 500)
        self.assertEqual(result.returncode, 2)
        self.assertFalse(os.path.exists(out))

    def test_compress_to_size(self):
        out = os.path.join(self.dir, "small.mp4")
        result = run_script("video-edit", "compress_to_size", os.path.join(self.dir, "in.mp4"), out, "--target-mb", 0.15)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLessEqual(os.path.getsize(out), 150000)
        refused = run_script("video-edit", "compress_to_size", os.path.join(self.dir, "in.mp4"), os.path.join(self.dir, "z.mp4"), "--target-mb", 0.001)
        self.assertNotEqual(refused.returncode, 0)

    def test_scripts_refuse_to_overwrite_the_input(self):
        src = os.path.join(self.dir, "in.mp4")
        self.assertNotEqual(run_script("video-edit", "gif_budget", src, src).returncode, 0)
        self.assertNotEqual(run_script("video-edit", "compress_to_size", src, src, "--target-mb", 1).returncode, 0)

    def test_burn_subtitles(self):
        with open(os.path.join(self.dir, "s.srt"), "w", encoding="utf-8") as handle:
            handle.write("1\n00:00:01,000 --> 00:00:03,000\nhello\n")
        r = shell("ffmpeg -v error -y -i in.mp4 -vf 'subtitles=s.srt:force_style=Fontsize=22\\,Outline=2' -c:a copy sub.mp4", self.dir)
        if "No such filter" in r.stderr or "subtitles" in r.stderr and "not found" in r.stderr:
            self.skipTest("ffmpeg without libass")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertAlmostEqual(self.duration("sub.mp4"), 6.0, delta=0.3)


if __name__ == "__main__":
    unittest.main()
