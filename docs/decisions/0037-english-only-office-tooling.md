# 0037 Bot 电脑预装办公工具，只预装英文

> 编号 D37，索引见 [README](README.md)。

## 决定

Bot 电脑镜像（`deploy/microvm/Dockerfile`）预装办公、PDF、OCR 和画图工具，语言相关的东西只预装英文，其余由 Bot 按需自己取。

| 项 | 内容 |
| --- | --- |
| 预装 | `libreoffice-core` + writer / calc / impress（`--no-install-recommends`）；`tesseract-ocr`（只带英文和方向检测数据）；`poppler-utils`；`pandoc`；Python：`openpyxl`、`docx`、`pptx`、`matplotlib`、`pandas`、`reportlab`、`pypdf`、`PIL` |
| 不装 | `tesseract-ocr-chi-sim`、`tesseract-ocr-chi-tra` 以及任何其他语言包 |
| matplotlib | 保持默认字体，不做 CJK 默认。要画中文，Bot 自己指定字体 |
| 已有的 CJK 字体 | 镜像里本来就有 `fonts-noto-cjk`（Chrome 渲染网页要用），不动，也不是本次加的 |
| 不用 root 补语言 | guest 给 shell、终端、桌面进程设 `TESSDATA_PREFIX=~/.local/share/tessdata`（共享主目录），目录里已有指向系统英文数据的链接，Bot 把别的 `.traineddata` 下载进去即可；字体装进 `~/.local/share/fonts`，`/etc/fonts/local.conf` 把它列入 fontconfig |
| 提示词 | 电脑段落一句原则：办公、PDF、OCR、画图工具已预装英文版，其他语言的数据或字体自己取到用户目录。不写例子、不列清单 |
| python-pptx | noble 没有 `python3-pptx`。用同版本的纯 Python 轮子 `python-pptx==1.0.2`，构建时通过一次性 venv 装进系统 `dist-packages`（`--no-deps`，依赖 lxml、XlsxWriter、Pillow、typing-extensions 走 apt），pip 本身不进镜像 |

## 日期

2026-10-21（PDT）

## 谁拍板

owner（jackzhao），经协调者转达：镜像只预装英文，语言相关的补充留给 Bot 按用户的语言和请求临时取。原话不在仓库里，待确认。

## 背景

评测里 zip、PDF、DOCX、XLSX 无法处理，镜像没有办公工具（见 [D36](0036-chat-uploads-on-the-computer.md)）。原方案还预装中文 OCR 包并把 matplotlib 默认设为 CJK 字体。

## 理由

镜像是所有用户共用的：每多一种语言都是所有人付的体积，而大多数用户用不到。Bot 有网络和用户目录，需要时下载一个 `.traineddata` 或一个字体只要几秒，所以“按需取”比“全部预装”便宜，也不会偏向某一种语言。强制把 matplotlib 默认成 CJK 字体同样会让所有英文图表换字体。

## 影响

- 实施状态：Dockerfile、guest（`TESSDATA_PREFIX`）、冒烟脚本 `deploy/microvm/smoke-office.sh` 已写好；**镜像尚未在本机构建**（构建环境的 Docker 起不来），体积和冒烟结果待协调者构建后补。
- 镜像体积：按 noble 包索引估算，新增约 140 个包、约 870 MB 解压后体积（pandoc 189 MB、LibreOffice 约 250 MB、`python3-scipy` / `python3-sympy` 因 `python3-fonttools` 的硬依赖进来约 80 MB）；这是估算，不是实测。
- 装了字体之后，已经建过缓存的 matplotlib 要清 `~/.cache/matplotlib`（或调用 `font_manager.fontManager.addfont`）才能看到新字体；`fc-list` 不受影响。
- 需要重新构建 guest 和镜像才生效。

## 相关代码与文档

- `deploy/microvm/Dockerfile`、`deploy/microvm/smoke-office.sh`
- `internal/computer/guest/tessdata.go`、`internal/computer/guest/actions.go`（`cleanEnv`）、测试 `internal/computer/guest/import_test.go`（`TestUserTessdataHoldsShippedDataAndStaysWritable`）
- `internal/app/microvm.go`（电脑段落提示词）、`internal/app/prompt_budget_test.go`（三个上限各按实测 +183 字符，含 D36 的附件那一句）
