> 2026-10-23：全局开关部分已被 [0058](0058-skill-library-per-bot.md) 取代（官方技能现在按 Bot 开关，`builtin_skill_off` 只在一次性迁移里读）。

# 0041 内置交付技能：随产品发布的 Skill

> 编号 D41，索引见 [README](README.md)。

## 决定

产品自带五个“交付技能”，嵌在二进制里，所有 Bot 默认可用，在设置 › Skills 里标为“内置”，可以关闭，但不能当作用户安装的 Skill 删除或覆盖。沿用现有 Skills 的渐进披露形状：提示词里只有一行描述，正文在 Bot 读它时才加载，脚本在 Bot 读它时才复制到电脑。

| 项 | 内容 |
| --- | --- |
| 机制 | 复用 `internal/extensions` 的 Skills（`list_skills` / `read_skill` / `read_skill_file`，按 Bot 的“谁能用”过滤）。新增 `internal/extensions/builtin.go`：`go:embed` 只嵌 `builtin_skills/*/SKILL.md` 和 `builtin_skills/*/scripts/*.py`（Python 测试在 `builtin_skills/tests/`，不进二进制）。内置 Skill 与用户 Skill 一起进目录；同名的用户 Skill 被忽略并给出诊断，安装和删除同名内置 Skill 被拒绝 |
| 旧机制 | `internal/agent` 里的 `SkillTools` / `tofi_load_skill`（“Copied scripts into sandbox”）不在 App 的运行路径上（`internal/runtime/runtime.go:27` 的注释），所以没有用它 |
| 默认与开关 | 默认全开。关闭记在表 `builtin_skill_off`（有行 = 已关闭），经 `PUT /api/extensions/skills/<name>/enabled` 写入，下一次运行生效。设置 › Skills 里每个内置 Skill 有“内置”标签和开关，没有“移除”；“谁能用”仍然适用。读不到开关表时，内置 Skill 整体不提供（宁可少给，不默认放开） |
| 技能 | `spreadsheet`、`document-report`、`slides`、`web-page`、`code-delivery` |
| 脚本到电脑 | Bot 读一个内置 Skill 时，`read_skill` 的包装（`internal/app/builtin_skills.go`）把它的全部文件经 `files.import_chunk` 复制到 `/workspace/home/uploads/.skills/<name>/<内容摘要前 12 位>/`，并在正文前写出这个目录。放在 uploads 之下是因为 guest 只接受这个目录，所以不用改 guest、不用重建镜像；路径里的摘要让新版本不会撞上旧副本（guest 从不覆盖已有文件）。没有电脑的 Bot 只读到正文；复制失败只记日志并在结果里说明，下次读取会重试 |
| 写法 | SKILL.md 只写原则：不放例句、不写 Not/But 对照、不列关键词；具体的做法在脚本里，`--help` 即用法。每个 Skill 都带同一条横切原则：回复里的每一句话都必须对交付的文件成立，并且是打开文件验证过的；只发布最终文件。这些规则由 `builtin_skills/tests/test_skill_text.py` 检查 |
| 脚本 | Python 3 加标准库和镜像预装的库：`spreadsheet`（`build_workbook.py`、`xlsx_summary.py`、`reconcile.py`）、`document-report`（`build_report.py`、`render_preview.py`）、`slides`（`build_deck.py`、`check_deck.py`、`deckkit.py`、`render_preview.py`）、`web-page`（`check_page.py`，含 1280 / 390 px 双宽度预览页）、`code-delivery`（`run_and_record.py`、`package_project.py`） |
| 测试 | Go：`internal/extensions/builtin_test.go`、`internal/app/builtin_skills_test.go`（列出、可关、按需加载、脚本到达电脑并能运行）。Python：`internal/extensions/builtin_skills/tests/test_*.py`，CI 的 python 作业新增一步；缺库或缺 soffice 时响亮地跳过（stderr 和报告里都写 SKIPPED） |
| 提示词预算 | 目录里五行（名字加一行描述）共 +616 字符（实测：`TestProductionPromptSizeMatrix` 的目录段 94 → 710）。`prompt_budget_test.go` 里“普通私聊”上限 6933 → 7549，“结构化私聊”上限 6883 → 7499；“固定开销”上限 7633 不含目录，不动 |

## 日期

2026-10-21（PDT）

## 谁拍板

协调者按生产评测的发现派工并写明要求（把技能做成产品内置、默认可用、设置里可见可关）；owner 的原话不在仓库里，待确认。

## 背景

8 个交付任务的生产评测只完成 1 个。技能能解决的失败：

- 季报：Bot 发布了 PDF 并声称“含…图表”，PDF 里一张图都没有；没有 matplotlib，也从没检查自己的文件。
- 幻灯片：10 页 PPTX，第 1 页有形状跑到页面外，没有看过渲染；要求的数字缺了几个。
- 发票：合计与真值相差很大，4 张不一致的只找出 2 张；没有把提取的数字与来源对账。
- 落地页（HTML）：做得好，保持。
- 代码交付：必须在电脑上运行并给出真实输出。

上传文件（[D36](0036-chat-uploads-on-the-computer.md)）和办公工具（[D37](0037-english-only-office-tooling.md)）在别处解决。行业做法是 Anthropic 的 Agent Skills（docx/xlsx/pptx/pdf）：一个文件夹，里面是 SKILL.md 和脚本，上下文里只有一行描述，任务需要时才加载全文。

## 理由

- 失败的共同点是回复说了文件里没有的东西。所以每个 Skill 都把“打开文件验证”变成脚本能做的事（图片数、页数、形状越界、合计对账、命令的真实退出码），并让回复引用脚本的输出。
- 内置而不是让用户安装：评测里的任务是所有人都会提的，默认可用才有意义；放进二进制，随版本发布、不依赖数据目录。
- 只放一行描述进提示词，保住预算；正文和脚本按需。
- 脚本放进 uploads 之下，是为了不动 guest 二进制和镜像；代价是路径里有个 `.skills` 目录。
- 关闭用“有行 = 关闭”，使全新工作区和读不到表的情况都不会改变用户已做的选择之外的任何东西。

## 补记 2026-10-21（PDT）：内置技能只留通用能力

owner 原话：“我们不能有任何的这种硬编码或者是硬为他定制化工具来通过测试。我们要改的是模型、提示词、思考流程本身，然后提供必要的工具，但不是给他写好脚本喂给他，说你这样做就能过。”

判据：这个脚本放进任何公司的通用办公或开发工具箱、对任何文档都说得通吗？是才留。对照 Anthropic 自己的 xlsx / docx / pptx / pdf 技能：它们给的是通用能力（重算公式、把文档渲染成图片让人看、转格式），不给某类任务的做法。上面“脚本”一行和“背景”里按评测任务写的检查（图片数断言、形状越界规则、发票对账）作废，按下表处理；SKILL.md 改成只写原则（好交付长什么样、按收件人会看到的方式验证、回复里每句话对文件成立），不规定步骤，也不要求必须运行某个脚本（`test_skill_text.py` 现在也检查这一点）。

| 脚本 | 处理 | 理由 |
| --- | --- | --- |
| `slides/render_preview.py`、`document-report/render_preview.py`（两份保持一致） | 保留并缩减 | 文档 / 幻灯片转 PDF 再转页面图片，是通用的“看自己的产出”；去掉 `--expect-pages/images/text`，那是在复刻评测的断言 |
| `spreadsheet/xlsx_summary.py` | 保留并缩减 | 重算公式加结构转储（表、行数、公式数、错误单元格、数值列合计）是通用能力；去掉 `--expect-sum/rows/sheets` |
| `code-delivery/run_and_record.py` | 保留 | 运行命令并记录原样命令、退出码和输出，任何开发者都用得上 |
| `code-delivery/package_project.py` | 保留 | 打包并排除依赖目录、缓存、密钥，再回读校验；通用打包 |
| `slides/check_deck.py` | 删除，换成 `slides/pptx_outline.py` | 原脚本是带阈值的评分器（溢出估算、字号、重叠、期望张数）；新脚本只转储每个形状的位置、文字、备注，并标出越界，由模型判断 |
| `slides/deckkit.py` | 删除 | 只为 `build_deck.py` / `check_deck.py` 估算文字高度 |
| `slides/build_deck.py` | 删除 | 替模型决定版式和内容的模板生成器；python-pptx 已装，模型自己写 |
| `document-report/build_report.py` | 删除 | 同上，替模型生成报告结构和图表；matplotlib、docx、reportlab 已装 |
| `spreadsheet/build_workbook.py` | 删除 | 同上，替模型生成工作簿；openpyxl 已装 |
| `spreadsheet/reconcile.py` | 删除 | 围绕发票概念（行、小计、税、总计、参照表）写的，复刻评测的对账判分 |
| `web-page/check_page.py` | 缩减为 `web-page/preview_page.py` | 规则清单像评分标准；只保留“按多个视口宽度并排渲染页面”，由模型看、模型判断 |

提示词预算：五行描述共少 60 个字符（目录段 710 到 650，实测），“普通私聊”上限 7719 到 7659，“结构化私聊”上限 7669 到 7609。审计了 Go 代码、提示词和工具返回：没有发现按发票、幻灯片、落地页写的逻辑；`read_skill` 返回的只是技能目录位置和 `run with python3`。该规矩同时写进 [`conventions.md`](../conventions.md) 第 7 节。

## 影响

- 实施状态：已实现（分支 `feat/deliverable-skills`）。
- 未证明：`soffice` 转换路径（`xlsx_summary.py --recalc`、`render_preview.py` 转 Office 文件、`slides` 渲染）在本机没有 LibreOffice，只在逻辑上写好；要在装了 D37 镜像的 Bot 电脑上跑一次才算证明。技能对真实模型任务的效果（评测前后对比）也还没有测。
- 提示词多 616 个字符，每次运行都付（有缓存）。
- 内置 Skill 也可以用“谁能用”限定给部分 Bot；用户 Skill 的安装、删除和 `manage_extensions` 行为不变。
- 新增一张表（`CREATE TABLE IF NOT EXISTS`，没有迁移文件），无需 owner 额外操作。

## 相关代码与文档

- `internal/extensions/builtin.go`、`internal/extensions/builtin_skills/`（五个 Skill 和脚本、`tests/`）、`internal/extensions/mcp.go`（`BuiltinSkillsOff`、合并进目录）、`internal/extensions/management.go`（安装、删除、列表）
- `internal/app/builtin_skills.go`（开关表、`PUT .../enabled`、复制脚本）、`internal/app/extension_management.go`（列表带 `builtin`、`enabled`）、`internal/app/app.go`（接线）
- `ui/src/ExtensionPanel.tsx`（“内置”标签和开关）、`ui/src/locales/*/extensions.json`、`ui/scripts/test-settings-shell.mjs`（7c 一段）
- 测试：`internal/extensions/builtin_test.go`、`internal/app/builtin_skills_test.go`、`internal/app/prompt_budget_test.go`、`internal/extensions/builtin_skills/tests/`
- 对照：[D15](0015-automatic-behaviour-settings.md)（自动行为要有可见开关）、[D36](0036-chat-uploads-on-the-computer.md)、[D37](0037-english-only-office-tooling.md)
