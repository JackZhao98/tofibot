# 0054 按收件人看到的样子验收：Bot 要能真的“看见”自己做的东西

> 编号 D54，索引见 [README](README.md)。

## 决定

交付评测（Codex terra，线上 rc.32 一轮、rc.35 两轮）里，幻灯片、落地页、周监测三项每轮都挂。查证后根因不在模型“懒”，而在**工具链让它看不见**：Bot 手里没有任何一条路能把电脑上的一张图片变成它自己的视觉输入。本决定只改通用能力和通用原则，不针对任何评测题：

1. **`computer_files` 新增 `files.view`**：读电脑上的一张 PNG / JPEG，作为图片（视觉输入）返回给模型，和屏幕截图走同一条路（结果里的 `image_url` 由 agent 循环抽成视觉输入，文本里只留 `image_attached`）。有界：文件 ≤ 8 MiB、解码后 ≤ 24 MP 且边长 ≤ 16384；长边超过 1568 px 或编码后超过 640 KiB 就按面积平均缩小（先试 PNG，再试 JPEG 85/70/55，仍不行再缩 3/4），小图原字节送回；每个 run 最多 16 次；GIF、文本、缺失文件都拒绝并给出下一步。`files.view` 在恢复身份里按只读观察处理（失败不会封住后续调用）。
2. **保留最近 4 张工具图片**（原 2 张）：核对一页要同时看桌面和手机两张表，核对一套幻灯片要同时看总览和细节。
3. **`web-page/scripts/preview_page.py` 改成能出图**：此前它只写一个并排 iframe 的 HTML，说明里让 Bot 用 `file://` 在电脑浏览器里打开，而浏览器工具只收 http(s)。现在它用无头 Chrome 按每个宽度把页面切成屏、横向拼成 PNG 图（`<页面>.<宽度>px-<n>.png`，每宽度最多 4 张，打印页面总高度），用 `files.view` 看；原来的并排 HTML 仍然写出。幻灯片和文档的 `render_preview.py` 只改了说明（用 `files.view` 看 PNG），两份仍逐字节相同。
4. **SKILL.md 的原则收紧**（仍然只写原则）：`slides`、`web-page`、`document-report` 的“验证”都写明“渲染出的图只有你看过才算到了你手里，`files.view` 返回图片，路径和目录列表什么也看不出”；`slides` 与 `web-page` 加上“逐张看图、发现问题就改、再渲染再看”，`slides` 加上“按请求和来源核对内容：它们强调的每个数字、名称、章节都在终稿的某一页上”。
5. **完成复核加一段“按收件人看到的样子判断”**（`deliveredAsRecipientPrinciple`，只进聊天回复的完成复核，位置在“回复对记录”之后、“要的东西是否做完”之前）：

   > When the work produced something the person will open, read or run, judge the product as they will meet it. Confirm that you looked at it that way in this conversation: rendered images you viewed, pages as displayed, output as run. Confirm that each item the request names is present in it. A defect you saw is fixed before delivery, and anything you did not look at is reported as unchecked.

6. **“留下的东西在哪”点名状态类**（`replyMatchesRecordPrinciple` 加一句，三处复核共用；`say` 说明和 `create_schedule` 说明各加一句）：

   > State that a later run reads or compares against is left behind for the person too: the answer names where it is kept and what it holds, whether it sits in a file, in a schedule's instructions or in memory, even when the person never asked to receive it.

   `say`：“……names where anything you leave for the person to rely on lives, including state a later run reads.”；`create_schedule`：“When the runs compare against saved state, the instructions name where that state is kept, and your confirmation to the person says so too.”

## 证据（查证过程）

材料：`tofibot-taskeval/eval/results/deliv-terra-r{3,4,5}/`，每个任务的 `*.context.json`（工具调用与结果、回复）和 `results.json` 里的评分备注。我们读的是**工具记录**，没有重放；“模型当时看到的图”记录里没有，只能看到 `image_attached:true` 这类结果。

### 一、幻灯片（每轮挂：约 6 个文本框溢出 + 一个关键数字缺失）

| 轮 | 读了 slides 技能 | 渲染 | 看图 | 看到问题后 | 评分 |
|---|---|---|---|---|---|
| r3 rc.32 | 是 | 把“重新生成 + `render_preview.py` + `pptx_outline.py`”串成一条命令，120 s 超时被杀（exit 137，只留下 PDF，没有 PNG）；之后自己 `pdftoppm` 出页图并拼了 10 页的总览图 | `browser.navigate file://…` 被拒（“browser URL must be an http or https URL”）；起 `http.server 8765`，端口已被**别的 Bot 的服务**占着（Traceback），第一次打开的是 Error response；换 8766 才打开总览图，`browser.snapshot` 返回 `image_attached:true`，看到的是 960×1345 的一张总览图 | 之后只跑 `pptx_outline.py` 并 grep `outside\|overflow\|warning`，无输出就发布；没有任何修改 | 估算 6 个溢出（第 2 页 4 个、第 5 页 2 个）；Q1/Q2 收入没出现在幻灯片上 |
| r4 rc.35 | 是 | 自己 `pdftoppm`；**没有**调 `render_preview.py` / `pptx_outline.py` | 同样被 `file://` 拒绝，同样撞端口（8765），绕了一圈（`readlink /proc/…/cwd`、换 8766）才用 `browser.snapshot` 看到总览图 | 发布，无修改 | 6 个溢出、第 1 页 3 个形状越界；Q2 收入 58.2 万缺失 |
| r5 rc.35 | 是 | 自己 `pdftoppm` 出 10 张页图并拼总览图 | `browser.navigate file://` 被拒后**直接放弃**，没有再试，**一张图都没看** | 直接发布（14 次工具调用就结束） | 6 个溢出（第 1/3/4/5 页）；最强的产品名没出现在任何一页 |

结论：
- 技能读了，渲染也做了；断在**“把图变成自己的视觉输入”这一步**。shell 的输出只能是文本，`render_preview.py` 只打印 PNG 路径；浏览器工具不收 `file://`；剩下的路是自己起 HTTP 服务再让共享浏览器打开，在共享 VM 里端口会撞、浏览器标签会被别的 Bot 占（r4 落地页的 `browser.snapshot` 标题竟是另一个 Bot 的 `contact.png (2934×4125)`）。r5 在第一道墙就停了。
- 看了也没改：r3 看过总览图后没有任何修改。一张拼到 960 px 宽的 10 页总览图，文字几乎看不清溢出——这是“怎么看”的问题（要能逐页看），不是意愿问题。
- `pptx_outline.py` 只标“形状越界”，不判断文字溢出，所以 grep 不到东西不能证明没问题。
- 缺的数字每轮不同（Q1/Q2 收入、Q2 收入、最强产品名）：模型在写代码时按“大意”挑数字，没有一步把“请求和来源强调的每个数字”逐个对到成品上。

### 二、落地页（r4、r5 挂，r3 过）

- 三轮都读了 web-page 技能、都跑了 `preview_page.py`（它输出的是一个 `file://` 预览页路径）。
- `browser.navigate file://…` 在 r3 / r4 / r5 全被拒；r3 起了 http.server 后用 `browser.snapshot` 看到并排页（过关）；r4 用 Ctrl+L 键入 `file://` 地址、再起 http.server、再滚动、再向右滚动去找 390 px 的那一格；r5 用 `desktop.capture` 看 1280 的一格，试图用无头 Chrome 直接截 390 的图存成文件（命令以 exit 127 结束，`file` 命令不存在），文件**无法被它看到**。
- 并排预览页本身就是问题：两格并排，1280 px 的窗口里 1280 的那一格占满屏，390 的那一格在屏外右侧。
- r4/r5 的 390 px 图里有“标签压在杯子上”“徽标压在杯子边”；r5 的回复写“已查看桌面与 390px 手机宽度下的呈现”，**记录里没有任何一次 390 px 的图像被取回**，这一句与 D45 的“回复对记录成立”相违。完成复核当时问的是“请求的东西做完没有”和“回复对记录”，没有问“你是不是按收件人看到的样子看过它”。

### 三、周监测（每轮挂，同一条评分：回复没说基线存在哪）

- r3（rc.32）：`computer_files` 写入结果只有相对路径（那时 guest 镜像还没带 `location`）；基线写在摘要文件的“基线”一节，`create_schedule` 的指令里写了绝对路径，回复只说“摘要文件”和下次时间。
- r4（rc.35）：写入结果带了 `location`，`publish_file` 结果带了 `source_path` 与 `attached_to_reply`；`answer_review` 的文字就是 D45 的版本（记录里能看到），退回后模型重写了一遍，仍然没有说基线在哪（摘要文件里有“监测基线”一节，同时 `create_schedule` 指令里指向这个文件）。
- r5（rc.35）：基线干脆写进了 `create_schedule` 的指令文字里，没有基线文件；回复同样没提。
- 所以“位置从来没告诉过模型”这个猜测对 r3 成立、对 r4 / r5 不成立：路径就在工具结果里，复核也要求“说明有什么、在哪”，仍然没说。原因是**类别判断**：D45 的原则写的是“人之后要依靠的东西（文件、存下的状态、定时任务、电脑上的位置）”，模型把“基线”当作定时任务自己的内部机件，不是“留给人的东西”，复核时就没把它归进去。抽象规则没拦住这一类，要把它点名为“后续运行读取或对照的状态”。

## 为什么这样改（对 Anthropic 做法的参照）

- Claude Code 最佳实践（Best practices for Claude Code）：“给 Claude 一个验证自己工作的办法”（测试、截图对照、预期输出），并明确说视觉类改动要让它截图、对比、再迭代。我们的 Bot 有渲染的办法，没有“看”的办法，第一步就缺了。
- 计算机使用（computer use）：每一步的依据是真实的屏幕截图，作为图片回到模型；本决定把“文件里的图”接到同一条图片通路上，而不是让它去读路径或自己起服务。
- 《Writing effective tools for AI agents》：工具返回对模型有意义、足以决定下一步的内容；有界（大小、数量）并给出下一步。`files.view` 的拒绝都是带下一步的 `invalid_arguments`；脚本的输出里有“页面总高、屏数、写出几张”。
- Managed Agents 的 outcomes：评分员只看产物、未达标带着缺口退回。完成复核加的那一段是同一思路的**原则版**：没有看过就标“未检查”，看到缺陷就改。本决定没有加评分器，也没有指定必须运行哪个脚本。
- 内部：[D41](0041-builtin-deliverable-skills.md)（只留通用能力、SKILL.md 只写原则）、[D45](0045-reply-matches-the-work.md)（回复对记录、说明在哪）、[D40](0040-lookup-at-the-point-of-decision.md)（原则放在决策点）。

## 为什么是通用的

- `files.view` 对任何图片都成立：任何任务生成的图表、截图、渲染页、图标，都能让模型看一眼；它不知道什么是幻灯片或落地页。
- `preview_page.py` 是“把一个 HTML 按几种宽度渲染成图”，放进任何公司的前端工具箱都说得通；不判断、不打分、不规定宽度（默认 1280、390，可以改）。
- SKILL.md 和复核加的是原则：看过再说、发现了就改、请求点名的东西都在。没有示例句、没有“不是……而是”、没有关键词表，也没有“必须运行某个脚本”（`tests/test_skill_text.py` 与 `reply_matches_work_test.go` 守着）。
- 位置这一句点名的是“类别”（后续运行读取或对照的状态），不是某个任务的词。

## 偏离与未做

- **没有给总览图加高分辨率逐页视图的脚本**：`render_preview.py` 本来就输出每页 PNG 和总览图；逐页看用 `files.view` 即可，不再添新工具。
- **图片保留数 2→4 会多占上下文**：每张约 1.5k token 估算（`imageTokenEstimate`），一个 run 最多 16 次 `files.view`，保留最近 4 张。如果线上发现成本明显上升，再调。
- **`files.view` 在宿主侧实现**，复用 `files.export_chunk`，不改 guest、不用重建电脑镜像；代价是宿主解码图片（有像素和字节上限）。
- **`preview_page.py` 依赖电脑里有 Chrome**（镜像里装的是 `google-chrome-stable`）；找不到时只写并排 HTML 并说明。无头 Chrome 在不同平台会隐藏一部分窗口高度（本机 macOS 实测 87–155 px），脚本先量出来再补，并把补的那一条做成标题条；Linux 镜像里的偏差没有实测（本机没有 Linux 镜像），这一点要在装了镜像的电脑上跑一次确认。
- **共享浏览器与端口冲突没有处理**：多个 Bot 共用一个浏览器和端口空间是电脑的既有设计；有了 `files.view`，Bot 不再需要为看图起 HTTP 服务。
- **没有改评分员、评测或 Bot 模型**。效果要等线上重跑 `deliv_slides` / `deliv_landing_page` / `deliv_watch_weekly` 才能确认；这里只有单元测试和本机脚本实测，不是线上结论。
- **复核新增的一段只在聊天回复的完成复核里**；定时运行的两条复核只新增“状态位置”那一句，不加“按收件人看到的样子”（定时运行里没有人在等一张可看的成品）。

## 提示词预算

系统提示词没有改。变长的是：`say` 说明 +35 字符、`create_schedule` 说明 +140 字符、`computer_files` 说明约 +280 字符（工具说明）、完成复核文字约 +1.1k 字符（含状态位置一句和“按收件人看到的样子”一段；只在武装的 run 里随那一次退回出现一次）。`prompt_budget_test.go` 全绿，上限没有动。

## 测试

- Go：`TestComputerFilesImageResultReachesTheModelAsAnImage`（agent 循环把 `computer_files` 的图送进下一次请求的视觉输入，撤掉 `agent.go` 里的 `computer_files` 条件会变红，已验证）；`TestViewImageResultPassesASmallImageThroughUnchanged`、`TestViewImageResultScalesALargeImageToTheBound`、`TestViewImageResultRefusesWhatIsNotAViewableImage`、`TestComputerFilesViewReadsAnImageFromTheComputer`（经真实 guest）、`TestComputerFilesToolOffersViewAndCapsViewsPerRun`；`TestChatCompletionReviewAsksForJudgingTheProductAsItsRecipientMeetsIt`、`TestSayAndScheduleToolsNameWhereSavedStateLives`，以及 `TestEveryFinalReviewHoldsTheReplyToTheRecordAndNamesWhereResultsLive` 新增的状态位置断言；`TestDropOldToolImagesKeepsNewestFourAndCountsImages`。
- Python：`test_web_page.PreviewPage.test_sheets_are_png_images_when_chrome_exists`（没有 Chrome 时响亮地跳过）；`test_skill_text` 继续检查 SKILL.md 无示例、无对比、无“必须运行”。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner 的约束由协调者转达：不为评测定制工具、不加“必须运行 X”的步骤、提示词和 SKILL.md 只写英文原则（无例句、无“不是……而是”、无关键词表）、照 Anthropic 的做法；只改模型行为的原则、流程以及通用工具的可发现性与可用性。具体的字段名、上限（8 MiB / 24 MP / 1568 px / 640 KiB / 16 次 / 保留 4 张）和措辞是实现决定，待 owner 事后确认。分支 `fix/verify-as-recipient`，未合并、未发版。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 看图 | `internal/app/computer_view_file.go`（`viewComputerFile`、`viewImageResult`）；`internal/app/microvm.go`（`computer_files` 的 `files.view`、每 run 上限）；`internal/app/computer_recovery.go`；`internal/agent/agent.go`（`computer_files` 的图抽取、`keepRecentToolImages`） |
| 预览脚本 | `internal/extensions/builtin_skills/web-page/scripts/preview_page.py`；两份 `render_preview.py` |
| 技能文字 | `internal/extensions/builtin_skills/{slides,web-page,document-report}/SKILL.md` |
| 复核与说明 | `internal/app/completion_final_review.go`（`deliveredAsRecipientPrinciple`、`replyMatchesRecordPrinciple`）；`internal/app/say.go`；`internal/app/schedule.go` |
| 测试 | `internal/app/computer_view_file_test.go`；`internal/agent/computer_files_view_test.go`；`internal/app/reply_matches_work_test.go`；`internal/agent/loop_latency_test.go`；`internal/extensions/builtin_skills/tests/test_web_page.py` |
| 证据 | `tofibot-taskeval/eval/results/deliv-terra-r{3,4,5}/`（本机，未归档） |
