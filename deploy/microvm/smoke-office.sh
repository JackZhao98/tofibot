#!/bin/bash
# Smoke test for the Bot computer image's office tooling. Run it inside the
# image as the guest user:
#   docker run --rm -u 1000:1000 -v "$PWD/deploy/microvm/smoke-office.sh:/smoke.sh:ro" <image> bash /smoke.sh
# It never uses root, the network is needed only for the language-pack step
# (set SMOKE_ALLOW_OFFLINE=1 to turn that one step into a loud skip).
set -euo pipefail
export HOME="$(mktemp -d)" LANG=C.UTF-8
work="$HOME/work"; mkdir -p "$work"; cd "$work"
ok() { echo "PASS $*"; }

# 1. every Python library imports
python3 - <<'PY'
import openpyxl, docx, pptx, matplotlib, pandas, reportlab, pypdf, PIL
print("imports ok", pptx.__version__, matplotlib.__version__)
PY
ok python-libraries

# 2. LibreOffice converts a generated docx and pptx to PDF
python3 - <<'PY'
import docx, pptx
d = docx.Document(); d.add_heading("Smoke report", 1); d.add_paragraph("Quarterly total 4242"); d.save("report.docx")
p = pptx.Presentation(); s = p.slides.add_slide(p.slide_layouts[1]); s.shapes.title.text = "Smoke deck"; s.placeholders[1].text = "Slide body 4242"; p.save("deck.pptx")
PY
soffice --headless --convert-to pdf --outdir out report.docx deck.pptx >/dev/null 2>&1
pdftotext out/report.pdf - | grep -q "Quarterly total 4242"
pdftotext out/deck.pdf - | grep -q "Slide body 4242"
ok libreoffice-docx-pptx-to-pdf
pandoc report.docx -t plain | grep -q "Quarterly total 4242"
ok pandoc

# 3. English OCR works from a writable per-user tessdata directory
sys_dir=$(ls -d /usr/share/tesseract-ocr/*/tessdata | tail -1)
export TESSDATA_PREFIX="$HOME/.local/share/tessdata"; mkdir -p "$TESSDATA_PREFIX"
for f in "$sys_dir"/*; do ln -s "$f" "$TESSDATA_PREFIX/$(basename "$f")"; done
python3 - <<'PY'
from PIL import Image, ImageDraw, ImageFont
f = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 56) if __import__("os").path.exists("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf") else ImageFont.truetype("/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf", 56)
im = Image.new("RGB", (900, 140), "white"); ImageDraw.Draw(im).text((20, 30), "Invoice total 1234", font=f, fill="black"); im.save("en.png")
PY
tesseract en.png - 2>/dev/null | grep -q "Invoice total 1234"
ok tesseract-english
touch "$TESSDATA_PREFIX/.writable" && rm "$TESSDATA_PREFIX/.writable"
ok tessdata-dir-writable

# 4. a non-English pack added by the user (no root) is used
cjk=$(fc-list : file family | grep -i "Noto Sans CJK" | head -1 | cut -d: -f1)
python3 - "$cjk" <<'PY'
import sys
from PIL import Image, ImageDraw, ImageFont
f = ImageFont.truetype(sys.argv[1], 72)
im = Image.new("RGB", (700, 160), "white"); ImageDraw.Draw(im).text((20, 30), "发票金额", font=f, fill="black"); im.save("zh.png")
PY
if curl -fsS -m 120 -o "$TESSDATA_PREFIX/chi_sim.traineddata" https://github.com/tesseract-ocr/tessdata_fast/raw/main/chi_sim.traineddata; then
  tesseract zh.png - -l chi_sim 2>/dev/null | tr -d ' \n' | grep -q "发票"
  ok tesseract-user-installed-chinese-pack
elif [ "${SMOKE_ALLOW_OFFLINE:-}" = 1 ]; then
  echo "SKIP tesseract-user-installed-chinese-pack (offline)"
else
  echo "FAIL could not download chi_sim.traineddata"; exit 1
fi

# 5. fonts: stock matplotlib font, and a font the user installs without root
python3 - <<'PY'
import io, matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import font_manager as fm
def render(ch, family=None):
    fig = plt.figure(figsize=(1, 1), dpi=60)
    kw = {"fontfamily": family} if family else {}
    fig.text(0.1, 0.1, ch, fontsize=40, **kw); b = io.BytesIO(); fig.savefig(b, format="png"); plt.close(fig); return b.getvalue()
# Stock default: missing glyphs draw the same empty box for any character.
assert render("中") == render("文"), "stock font unexpectedly has CJK glyphs"
# The CJK fonts already in the image give distinct glyphs when asked for by name.
names = {f.name for f in fm.fontManager.ttflist if "CJK" in f.name}
assert names, "matplotlib does not see the image's CJK fonts"
fam = sorted(names)[0]
assert render("中", fam) != render("文", fam), "CJK font renders boxes"
print("matplotlib stock font and CJK family ok:", fam)
PY
ok matplotlib-fonts
python3 - <<'PY'
import os
from fontTools.ttLib import TTFont
src = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
if not os.path.exists(src):
    src = "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf"
f = TTFont(src)
for r in f["name"].names:
    if r.nameID in (1, 4, 16): r.string = "TofiSmokeFace"
    if r.nameID == 6: r.string = "TofiSmokeFace-Regular"
d = os.path.expanduser("~/.local/share/fonts"); os.makedirs(d, exist_ok=True); f.save(d + "/TofiSmokeFace.ttf")
PY
fc-list | grep -q TofiSmokeFace
ok fontconfig-sees-user-font
rm -rf "$HOME/.cache/matplotlib"
python3 - <<'PY'
import matplotlib
from matplotlib import font_manager as fm
path = fm.findfont("TofiSmokeFace", fallback_to_default=False)
assert "TofiSmokeFace" in path, path
print("matplotlib sees the user font:", path)
PY
ok matplotlib-sees-user-font

# 6. poppler and chart rendering
python3 - <<'PY'
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt
plt.plot([1, 2, 3], [3, 1, 2]); plt.savefig("chart.png")
PY
test -s chart.png; pdftoppm -v >/dev/null 2>&1; ok chart-and-poppler
echo "ALL PASS"
