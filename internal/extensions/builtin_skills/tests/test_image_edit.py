import json
import os
import shutil
import tempfile
import unittest

from _support import SKILLS, load, need_modules, need_programs, run_script

PATH = os.path.join(SKILLS, "image-edit", "SKILL.md")


class ImageEditText(unittest.TestCase):
    def test_covers_the_jobs(self):
        with open(PATH, encoding="utf-8") as handle:
            text = handle.read()
        for needle in ("-crop", "-resize", "-rotate", "+append", "montage", "annotate.py", "files.view", "send_message", "original"):
            self.assertIn(needle, text)


@need_modules("PIL")
class AnnotateScript(unittest.TestCase):
    def setUp(self):
        from PIL import Image
        self.dir = tempfile.mkdtemp()
        self.src = os.path.join(self.dir, "in.png")
        Image.new("RGB", (600, 400), "white").save(self.src)
        self.out = os.path.join(self.dir, "out.png")

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def spec(self, items, **extra):
        path = os.path.join(self.dir, "spec.json")
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(dict(items=items, **extra), handle)
        return path

    def test_draws_boxes_arrows_badges_and_blur_without_touching_the_input(self):
        from PIL import Image
        with open(self.src, "rb") as handle:
            before = handle.read()
        spec = self.spec([{"type": "box", "x": 50, "y": 80, "w": 200, "h": 100, "label": "A1"},
                          {"type": "arrow", "from": [400, 300], "to": [300, 200]},
                          {"type": "number", "x": 40, "y": 70, "n": 2},
                          {"type": "blur", "x": 300, "y": 20, "w": 100, "h": 60},
                          {"type": "text", "x": 10, "y": 360, "text": "note"}])
        result = run_script("image-edit", "annotate", self.src, spec, self.out)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("wrote", result.stdout)
        with open(self.src, "rb") as handle:
            self.assertEqual(handle.read(), before)
        image = Image.open(self.out).convert("RGB")
        self.assertEqual(image.size, (600, 400))
        self.assertEqual(image.getpixel((50, 130))[0:3], (225, 29, 72))  # box outline color on the left edge

    def test_fraction_units(self):
        from PIL import Image
        spec = self.spec([{"type": "box", "x": 0.5, "y": 0.5, "w": 0.25, "h": 0.25}], units="fraction")
        self.assertEqual(run_script("image-edit", "annotate", self.src, spec, self.out).returncode, 0)
        self.assertEqual(Image.open(self.out).convert("RGB").getpixel((300, 250)), (225, 29, 72))

    def test_refuses_to_overwrite_the_input_and_bad_regions(self):
        spec = self.spec([{"type": "box", "x": 1, "y": 1, "w": 5, "h": 5}])
        self.assertNotEqual(run_script("image-edit", "annotate", self.src, spec, self.src).returncode, 0)
        far = self.spec([{"type": "blur", "x": 900, "y": 900, "w": 5, "h": 5}])
        result = run_script("image-edit", "annotate", self.src, far, self.out)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("outside", result.stderr)

    def test_cjk_label_either_renders_or_fails_loudly(self):
        spec = self.spec([{"type": "box", "x": 50, "y": 80, "w": 200, "h": 100, "label": "销售额"}])
        result = run_script("image-edit", "annotate", self.src, spec, self.out)
        if result.returncode == 0:
            self.assertTrue(os.path.exists(self.out))
        else:
            self.assertIn("font", result.stderr)


@need_programs("convert", "identify")
class MagickCommands(unittest.TestCase):
    def test_crop_resize_append(self):
        import subprocess
        with tempfile.TemporaryDirectory() as d:
            def sh(cmd):
                r = subprocess.run(cmd, shell=True, cwd=d, capture_output=True, text=True)
                self.assertEqual(r.returncode, 0, r.stderr)
                return r.stdout
            sh("convert -size 600x400 xc:red a.png && convert -size 300x400 xc:blue b.png")
            sh("convert a.png -crop 200x100+10+10 +repage c.png")
            self.assertEqual(sh("identify -format '%w x %h' c.png").strip(), "200 x 100")
            sh("convert a.png b.png -background white -gravity center +append s.png")
            self.assertEqual(sh("identify -format '%w x %h' s.png").strip(), "900 x 400")


if __name__ == "__main__":
    unittest.main()
