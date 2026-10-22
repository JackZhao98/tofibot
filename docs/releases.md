# 版本记录

TOFI 每个版本改了什么，最新的在最上面。写给以后接手的人和 AI 会话：打开这一页就能知道线上跑的是哪一版、正式版停在哪、每个 rc 带进了什么。

## 现在的状态（2026-10-20 晚，PDT）

| 项 | 值 | 依据 |
|---|---|---|
| 线上（tofi.speedowagon.com）在跑 | `v0.1.4-rc.29`（`748c774`） | 协调者记录（owner 决定与上下文清单）。本仓库内无法核对线上版本：`/health`（`internal/app/app.go:2745`）不返回版本号，且站点在 Cloudflare 校验页后。主机上核对：`sudo tofi status`，待确认 |
| 最近的正式版 | `v0.1.3`（`3b6a885`，2026-10-15） | `git tag`；`gh release list` 标为 Latest |
| `main` 顶端 | `748c774` = `v0.1.4-rc.29` | `git log -1 main` |
| v0.1.4 正式版 | 未发。正式 `v*` 标签必须 owner 确认（决定 D30） | — |
| GitHub 上还留着的 Release | v0.1.0、v0.1.1、v0.1.2、v0.1.3、v0.1.4-rc.26 至 rc.29 | `gh release list`（10-20 晚查询）；其余 rc 的 Release、git 标签和镜像版本已按决定 D25 清理 |
| 还在的 git 标签 | v0.1.0、v0.1.1、v0.1.2（附注标签）；v0.1.3、v0.1.4-rc.26、rc.27、rc.28、rc.29（轻量标签）；live-20261005-b35ead1、live-20261006-93db5f8 | `git for-each-ref refs/tags` |

## 这份记录怎么来的

- **rc 边界是查出来的，不是猜的。** 标签删了，但每次推标签触发的 `.github/workflows/release.yml` 运行记录还在 GitHub Actions 上，每条记录带着标签名（`headBranch`）和提交（`headSha`）。复查命令：
  ```sh
  gh run list --workflow release.yml --limit 200 \
    --json headBranch,headSha,createdAt,conclusion \
    --jq '.[] | "\(.headBranch) \(.headSha[0:7]) \(.createdAt) \(.conclusion)"'
  ```
  所以下面各表的"提交"一列都是精确值。唯一例外是 v0.1.0-rc.1：它没有 Actions 记录，信息来自本机对象库里残留的附注标签对象（`git fsck --unreachable --no-reflogs` 能看到 v0.1.0-rc.1 到 rc.8 的标签对象）。
- **时间**一律是 PDT，取自 Actions 运行的创建时间（即推标签的时刻，换算自 UTC）。保留标签的提交时间可能早几分钟，例如 v0.1.3 的提交是 09:32，正式版的发布运行是 09:54。
- **"包含"** 指 `git log --first-parent <上一个 rc>..<这个 rc>` 列出的 main 上的合并。每条后面括号里是分支名，可以用 `git log --first-parent --grep '<分支名>' main` 找到合并提交。
- **"推断"**：本页只有一处是推断——版本名后"大意"一栏是根据合并信息归纳的，不是当时写的发布说明（当时没有单独的发布说明，GitHub Release 正文是自动生成的）。
- 决定编号 D*NN* 对应 `docs/decisions/00NN-*.md`（例如 D24 = `docs/decisions/0024-standing-tool-allowance.md`）。
- 版本规则：`v0.1.N` 是正式版，安装器默认装它；`-rc.N` 是候选版，安装器装之前要再确认一次（`deploy/self-host/test_install_sh.py:865` 起的用例）。一次 rc 可以只带一个修复，所以 rc 号会涨得很快。
- 以后每次合并、发版，都在同一个提交里更新这一页（决定 D27）。

---

## 未发布

还没进任何 rc 的改动写在这里；打下一个 rc 时挪进那个 rc 的条目（决定 D27）。

- **每条回复的“查看活动”里可以看到模型实际收到的上下文（分支 `feat/view-context`，[D53](decisions/0053-view-context.md)）。** owner：“What our context holds is a total mystery.” 运行装配好上下文后，把各部分（系统指引、Bot 指令、工具定义、记忆、摘要、历史窗口里的每条消息、早先轮次提示、能力引用、日程状态、请求附注）的种类、字符数、估算 token 数和原文存成快照，存入新表 `run_context_snapshots`（脱敏常见密钥形状；每个运行压缩前最多 256 KB，超出先丢最大的原文；每个会话留最近 50 次，随运行删除）。`GET /api/runs/{id}/context` 仅所有者（和工具活动同一道会话闸），没有快照返回 404 `context_snapshot_unavailable`。网页端在“查看活动”末尾加“上下文”，懒加载，显示总量、按种类的占比、按顺序的部分（等宽原文加复制）、历史消息的紧凑行，以及被窗口漏掉的消息。不改任何提示词字节（`assembleRunSystemSegments` 与旧实现逐字节对比）。只做了网页端；桌面客户端的活动行未加。`internal/app/context_snapshot.go`、`context_snapshot_test.go`、`ui/src/RunContext.tsx`。
- **先应一声再干活；定时任务卡住的结尾也过评分员（分支 `night/ack-and-blocker-review`，[D50](decisions/0050-acknowledge-then-work.md)）。** 人在等的运行（私聊、群里人发的消息）在请求旁多一条原则：需要工具工作时先用 `say(message)` 简短应一声，发现了值得说的随时说，最后 `say(answer)`；`say` 说明和完成指引里的“只在做完后宣布”改成“只在做完后说做完了”（D45 的意图不变）；每个运行 3 条 `say(message)` 的上限不变；定时、隐藏、分诊和委派类运行不加。定时任务以纯文字结尾（卡住）时，这段文字也用 D43 的评分员评一次（与 `say(answer)` 共用 `answer_gate` 轮数），有 gap 就私下退回再来一轮；评分员失败或到上限照旧。`internal/app/context.go`、`say.go`、`app.go`、`answer_review.go`、`blocker_ack_test.go`。**只有合成测试，未用真模型实测**；纯文字结尾受 Agent 循环一次续跑名额限制，最多评一轮。
- **Bot 干活的过程行改安静（分支 `night/quiet-process-line`，owner 10-22 的要求）。** 聊天里 Bot 正在做的事默认只有一行（加载标记、在做什么、秒数），点击或按 Enter/空格展开工具步骤，跑完变成完成摘要；任务块的活动行、网页工具行和精简活动行共用同一个写法（`live-line.css`），一律是真按钮、有 `aria-expanded` 和焦点环，长文字省略号截断不换行。字改成 400 的 13px 正文体、`--muted` 灰（新令牌 `--text-process`、`--type-status-regular`），去掉光泽扫过动画；对比度浅/深色都不低于 AA。活动行左边的猫换成 14px 的墨环加湖蓝笔画加载标记（只动 `transform`，减少动效时静止）；输入框上的猫和群聊头像列不动。"Thinking: …"的标题不再在 40 个字符处被切断。目录页第 1 节新增过程行各状态。
- **定时任务的时间可以直接改（分支 `fix/schedule-edit-timing`，owner 要求“直接补”）。** 以前日程只能改标题、说明和指令，想把每天 02:30 的报告挪到 06:00 只能删掉重建、丢掉历史。现在 `PATCH /api/schedules/{id}` 和 Bot 工具 `update_schedule` 都可以带可选的 `kind`、`run_at`、`interval_seconds`、`daily_time`、`weekdays`、`timezone`；服务端用创建时的同一套校验和下次时间计算，从当前时刻重算 `next_at_utc`，已领取或正在跑的那一次不动，暂停的保持暂停，已跑过的一次性任务返回 409 `schedule_timing_locked`。无效时间返回稳定错误码（`schedule_kind_invalid`、`schedule_time_invalid`、`schedule_weekdays_invalid`、`schedule_run_at_*`、`schedule_interval_invalid`、`schedule_timezone_invalid`、`schedule_timing_conflict`），创建接口也用同一批码；`expected_timing` 是可选的时间基线，冲突仍是 `edit_conflict`。时间修改和内容修改一样记入授权日志（`content_edit`），所以授权链允许各版本的时间不同（身份和最初锚点仍须一致），评审员能看到每一版的时间。网页端在日程的“管理 › 编辑”里加了时间区，和创建表单共用同一组控件（`ScheduleTimingFields`）。测试：`TestPatchSchedule*`、`TestScheduledMCPWriteSurvivesTimingEdit`、`test:schedule-timing`。

---

## v0.1.4（候选中，未发正式版）

从 v0.1.3 之后开始，到 rc.29 为止共 29 个候选版，全部构建成功。主线是三件事：让 Bot 说话更像人、在群里配合得更像团队（10-15 至 10-19）；把"回复"改成结构化输出（10-17 至 10-19）；一轮大审计之后集中打磨界面的流畅和外观（10-19 至 10-20）。

### 一览

| rc | 时间（PDT） | 提交 | 大意（推断） |
|---|---|---|---|
| rc.34 | 10-22 凌晨 | `b112fd9` | 备份带电脑磁盘并可验证、`tofi security audit`、个人 Google 加 Calendar/Drive、运行健康汇总、OAuth 完成页按语言、服务端错误改码、夜间审查修复 |
| rc.33 | 10-22 凌晨 | `e6e8943` | 连接页重做（一行一个、弹窗设置、Gmail 走 gogcli、启用 API 按钮、Google 客户端只传一次、官方图标、GitHub 星数）、Google agentic/预览提示、回复对得上做过的活、插件删除不再卡 busy、社区安装加固与 Discord 自装指南 |
| rc.32 | 10-21 晚 | `a10a4fb` | 群聊按职责分工、记忆与历史搜索、定时任务交付前评审、技能包只留通用能力、代码约定与 CI 守卫 |
| rc.31 | 10-21 | `57d1952` | 设计系统迁移（Radix 菜单/对话框、层级刻度、右滑返回）、置顶拖拽重做、发文件卡住修复、MCP 参数先看结构 |
| rc.30 | 10-21 早上线 | `0b4b948` | 上传文件进 Bot 电脑、英文办公工具、五个内置交付技能、瞬时错误重试、搜索误关修复、模型列表刷新 |
| rc.29 | 10-20 20:15 | `748c774` | "始终允许"某个 Bot 用某个写工具；审批卡片列表轻量化 |
| rc.28 | 10-20 17:01 | `55fb70d` | 接手卡片、侧栏拖拽置顶、渐进模糊、消息到达动画、整体流畅度 |
| rc.27 | 10-20 10:47 | `5035a5d` | 自动行为设置页、Sonnet/Haiku 分工、安全与可靠性第二轮 |
| rc.26 | 10-20 01:38 | `477b41f` | 启动画面换成毛玻璃骨架 + 呼吸的 logo |
| rc.25 | 10-20 01:14 | `a063c2c` | 发送立刻上屏，带"发送中/已送达/已读"回执 |
| rc.24 | 10-20 00:46 | `17bb4b7` | 消息下方的低调操作行，消息间距加大 |
| rc.23 | 10-20 00:15 | `3d0bb9d` | 审计修复（手机界面、断线恢复） |
| rc.22 | 10-20 00:01 | `bbba69c` | 审计修复（安全、可靠性、群聊、性能、聊天界面） |
| rc.21 | 10-19 19:23 | `55c5515` | 说"已核实"必须真查过；群里先查再问 |
| rc.20 | 10-19 19:18 | `2a05a44` | 带着假设交付；修工作区加载卡死 |
| rc.19 | 10-19 16:10 | `63a5254` | 电脑窗口在电脑唤醒时等待而不是报错 |
| rc.18 | 10-19 15:42 | `73cbfe2` | 向人提问只走一条通道 |
| rc.17 | 10-19 14:56 | `70fea8b` | Bot 的职责范围不再是拒绝闲聊的理由 |
| rc.16 | 10-19 13:37 | `2e46dc9` | 被 @ 的每个 Bot 都按顺序回答 |
| rc.15 | 10-19 11:56 | `3b2cf35` | 工具之间的内部文字永不出现在聊天里 |
| rc.14 | 10-19 11:12 | `e52f786` | 群里叫醒同事显示为一行交接 |
| rc.13 | 10-19 02:28 | `4f94dd4` | 错过的已结束群讨论只给要点 |
| rc.12 | 10-19 00:54 | `8d06d18` | 结构化输出第三阶段：回复、发消息、完成定时任务都并入 `say` |
| rc.11 | 10-17 16:11 | `d4ac003` | 结构化输出上线（`say` 工具，默认开） |
| rc.10 | 10-16 17:14 | `385050e` | Safari 聊天窗格修复 |
| rc.9 | 10-16 16:13 | `0875f60` | 不认识的名字先搜；默认模型显示正确 |
| rc.8 | 10-16 15:49 | `7e4de6f` | 旧的自己回复只给要点，减少自我模仿 |
| rc.7 | 10-16 15:07 | `37cd3cc` | 时间戳排序修复、新闻链接要具体、界面审计 |
| rc.6 | 10-15 21:31 | `a6a7969` | 修输入框打字卡顿；不引用正上方那条 |
| rc.5 | 10-15 14:59 | `8fb1377` | 群里交接显示成"X 请 Y 接手" |
| rc.4 | 10-15 14:27 | `23fdd22` | 交接的私下备注 |
| rc.3 | 10-15 14:06 | `472256f` | 交接写得像对同事说话 |
| rc.2 | 10-15 13:44 | `7e21e5e` | 群里的闲聊有人接 |
| rc.1 | 10-15 12:24 | `7f3ab75` | 像人说话第一轮：修人设生成器 |

### rc.34 — 10-22 凌晨，`b112fd9`

验证：gofmt、vet、Go 全套、自托管 Python 测试、tsc、i18n、连接页/社区安装/设置/接口错误码/OAuth 完成页/引导/菜单/对话框/层级/组件目录/品牌图标/集成目录/动效令牌、入口包结构、构建，退出码全 0（计时项按 CI 方式跳过：机器负载高）。

- **备份带上电脑磁盘，备份可以验证（分支 `night/s1-backup-verify`，[D48](decisions/0048-backups-carry-computer-disks-and-verify.md)）。** `tofi backup` 默认把每台电脑的工作区磁盘（zstd 压缩）一起备份，`--no-disks` 可关；备份前打印空间估算（压缩比是假定值），空间不够就拒绝；带磁盘的备份只留最近 2 份，更老的只删磁盘、数据和设置留着（owner 决定）；更新前和还原前的自动备份不带。新增 `tofi backup --verify [ID|all]`（不还原，查文件、大小、sha256、归档可读、`tofi.env`），`tofi backup` 做完自动验一遍。`tofi restore` 对带磁盘的备份连磁盘还原，被换下的盘留作 `workspace-before-restore-*.ext4`，失败回滚时换回。`deploy/self-host/tofi_host.py`、`test_safe_updates.py`（`DiskBackupTests`）。**未在真机验证**：磁盘实占大小、reflink 是否生效、`manager.lock` 拒绝逻辑都要在安装测试 VM 上看。
- **`tofi security audit` 和回滚成文（分支 `night/s2-audit-rollback`，[D49](decisions/0049-security-audit-levels.md)）。** 新的只读主机命令，检查暴露面（HTTPS 与证书类型、监听网卡、公网地址、明文 HTTP 登录）、管理员口令是否已设、登录限流与受信代理、数据目录/保险库/备份/证书私钥的权限与属主、`tofi.env` 里的疑似密钥（只报名字）、明文模型密钥、版本与最新版、Guest 镜像与休眠电脑、最近一次备份；输出 PASS/WARN/FAIL 和每条一行修法，`--json`，有 FAIL 退出码 2，`--online` 才联网。`docs/process.md` 新增 §7.6（回滚三种情况各一条命令，写明降级被接受但没有数据闸）和 §7.7。**未在真机上跑过**，只有合成测试（`deploy/self-host/test_security_audit.py`）。
- **个人 Google 连接加 Calendar 和 Drive（分支 `night/a1-gog-services`，夜间计划 A1）。** 连接对话框新增服务多选（Gmail / Calendar / Drive，已授权的预选，再加一个不用重传 OAuth JSON，增量授权保留旧授权）；`gog mcp --allow-tool` 不再钉死 `gmail`，每次启动 MCP 子进程时按账户已授权的服务生成（`gmail,calendar,drive` 的子集，未授权的不含；手写参数不改），授权完成或断开后回收空闲子进程使新工具集生效。工具名与服务标志已在固定的 gog v0.40.0 上核对（`gog mcp --list-tools`：`gmail_search/get_message/get_thread`、`calendar_events`、`drive_search/get`，全是只读）。"验证"按服务各做一次只读请求，"API 未启用"带出具体服务的 API，"启用 … API"按钮按服务拼；状态行写 `邮箱 · Gmail, Google Calendar`，详情里列出已授权服务（7 语言）。Gmail 选"读+发"时 Drive 仍只读，Calendar 在 gog 里没有更窄的模式，只靠 MCP 只暴露读工具兜住。状态接口 `/gog/status` 多一个 `services`。`internal/mcprunner/gog.go`、`runner.go`、`http.go`、`ui/src/connections/`、`ui/src/MCPSettings.tsx`。**未用真实 Google 账号验证 Calendar 和 Drive。**
- **运行健康汇总，只读（分支 `night/a4-run-health`，next-plan A4）。** `GET /api/usage/runs?days=7`（1–30 天）按状态数运行、列出前 5 个具名失败原因（取自 `Run.failure()` 的稳定码，不含任何上游原文）、数工具超时（失败的工具调用里 `[Timed out after`、`timed_out`、exit 137）和模型调用；设置 › 用量顶部多一张安静的卡片，7 语言；`tofi status` 多一行 `Runs`（直接只读各账户库，只有计数）；`/health` 带 `version`。不写库、不加迁移。
- **OAuth 完成页按语言渲染 + 连接详情写“这能做什么”（分支 `night/a3-a9`，计划 A3、A9）。** 完成页去掉写死的 `lang="zh-CN"` 和“外部服务”默认名：账户保存了界面语言就用它，否则按 `Accept-Language`，都没有则英文（原先永远是中文，标题永远是英文）；7 语言文案是内嵌 JSON（`internal/app/oauth_pages/<语言>.json`，Go 源码里不再有展示文字，不碰 A2 的 CJK 守卫），提供方诊断按 `code` 本地化，未知 code 才透传原文。`npm run test:oauth-completion` 加 7 语言各一条断言（`html lang`、标题、读取到 N 个工具、配置说明）和目录键一致性检查。连接详情顶部加“这能做什么”：从 `tools/list` 与服务端已解出的分级生成“可读取 / 可写入”两组工具名（不写死文案，超过 6 个折叠为“另 N 个”）；社区连接不采信其自带的只读标注（D47，只有 owner 标过的才算读），并写明“写入每次先问”。新键在 `extensions.json` 的 `capabilities`（只加不改）；`ToolTierPanel` 加可选 `community` 属性，`MCPSettings.tsx` 传一个值。
- **服务端中文串改成码 + 英文兜底 + 7 语言（任务 A2，分支 `night/a2-error-codes`）。** `internal/` 非测试 `.go` 里的固定中文字符串（约 100 处、24 个文件）全部改成稳定 `code` 加英文 `message`；界面文案按码查 `common.json` 的 `apiError.<code>`（7 语言，新增 40 个码，如 `computer_busy`、`draft_conflict`、`work_stop_first`、`vm_oauth_denied`、`api_disabled`）。同一个码原本带多句不同话的，改成一码一义：待办冲突原先一个码 `work_execution_unavailable`，现在按原因拆成 `work_stop_first` 等 7 个；邮件草稿校验错误不再冒充 `draft_conflict`，分出 `draft_invalid`、`recipient_limit`、`recipient_invalid`。随固定句子给出码的载荷字段：`failure_code`（任务状态）、`error_code` / `result_code`（待办执行）、`error_code`（共享电脑授权会话，不再带 `error` 文字，对话框按状态取 `extensions:vm.end.*`）。`ui/src/i18n/errors.ts` 加 `codeText()` 查同一份目录。守卫测试 `internal/app/no_cjk_literals_test.go` 用 AST 扫 `internal/` 非测试文件的字符串字面量，出现 CJK 即失败；白名单只有 `onboarding.go`（按语言的开场白表）和 A3 负责的两个 OAuth 页面文件。**接线：** `ui/src/api.ts` 构造 `ApiError` 时，若 `error.code` 在 `common:apiError` 里有译文，`message` 就用译文（英文原文留在 `data.message`）；`failure_code` 仅进类型（界面今天不显示 `failure_reason`），`error_code` / `result_code` 由待办执行面板渲染，`error_code` 由共享电脑授权对话框渲染，都经 `ui/src/i18n/codeText.ts`；转交通知（“X 已将任务转交给 Y”“已交给 Y 处理”）由界面按 `notice` 结构用 `message.handed_over` / `message.routed_to` 按语言组句。`ssh_keys` 一码两义，格式错误改为 `ssh_key_format`。测试 `npm --prefix ui run test:api-error-codes`。**仍是英文的已存文本（后续）：** 审批过期结论（带计数的整段，且常与模型文字拼在一起）、无最终总结的进度提示、只发附件时的“See the attachments.”、`ui_card` 的模型侧文字稿；OAuth 完成页的 6 句由 A3（`night/a3-a9`，`MessageKey` + `diag_<code>`）接管，合并时取 A3 一侧并删守卫白名单里的两个 OAuth 文件。
- **夜间审查修复（分支 `night/review-fixes`，对 Fable 对抗审查已确认的发现）。** (1) `tofi security audit` 的备份检查区分目录和文件：`disks/` 目录允许 0700 并递归检查其中文件（0600、属主 root），修法行对目录给 `chmod 0700`；之前任何带磁盘的手动备份都会让审计 FAIL 退出码 2，且 `disks/*.ext4.zst` 从未被审计。(2) `tofi restore` 还原磁盘时，每台电脑的 `snapshot/` 改名为 `snapshot-before-restore-*` 并记入事务日志，成功后才删，失败回滚由 `undo_disk_swaps` 换回（之前先删，回滚后休眠快照丢失）。(3) `tofi backup` 在停服务之前检查 `zstd`，缺失就带 `--no-disks` 提示拒绝，什么都没动。(4) gog MCP 子进程忙时的回收改为置 `restartWhenIdle`，最后一个租约释放时关闭子进程，新授权的 Calendar/Drive 工具不必等 10 分钟空闲回收。(5) Google 401/403 提示改为每次调用自带状态容器（经请求 context 传给传输层），同一会话并发调用不再串状态。(6) 引导第 3 步（`chat.services_body`，7 语言）去掉“改动任何内容前都会先问你”的过度承诺，改写为目前确实保证的三条：发邮件必经你、扩展变更需你批准、登录和两步验证交还给你（依据 `docs/product/trust.md`）。测试：`test_security_audit.py`、`test_safe_updates.py`（`DiskBackupTests`）、`gog_test.go`、`inspection_diagnostic_test.go`。第 5 条的测试是传输层单元测试（整条链路上 SDK 在任何非 2xx 后会关会话，难以端到端复现并发）；撤掉修复时表现为编译失败。
- 设计审查（docs/design/review-2026-10.md、proposals/）、批准边界、首次运行、自托管审查等设计文档：只是提案，待 owner 拍板，产品代码未改。

### rc.33 — 10-22 凌晨，`e6e8943`

验证：gofmt、vet、Go 全套、tsc、i18n、连接页/社区安装/设置/菜单/对话框/层级/组件目录/品牌图标/集成目录/动效令牌、入口包结构，退出码全 0（计时项按 CI 方式跳过：机器负载高）。

- **设置 › 连接重做**（分支 `ui/connections-simplify`）：一个列表、一行一个连接（图标、人话名字、一个诚实的状态、一个主操作，其余进 ⋯ 菜单）；不再出现 `local_gog`、Runner、休眠字样，插件卡和服务器行合并；工具列表和"来自哪里"（官方 / 社区 / 自定义）进"查看详情"；"添加连接"和所有配置表单都在对话框里（目录 → 单步设置）。没授权 Google 账号的 Gmail 读作"需要授权"而不是"已连接"。目录只收官方连接器，社区项走"添加社区或自定义连接"（带第三方维护、写入先问的说明）；Gmail 默认走 gogcli；Google Workspace 官方 MCP 收进"Workspace 预览（企业）"并写明预览计划要求。`ui/src/MCPSettings.tsx`、`ui/src/connections/`、`ui/scripts/test-connections.mjs`。
- **Google 连接**：从上传的 OAuth 客户端 JSON 读出项目（`internal/googleapi`），"API 未启用"由 Google 结构化错误（`SERVICE_DISABLED` 的 ErrorInfo，不看文字）判定，在连接检查、工具调用失败和 gogcli 校验时记入状态，行上显示"API 未启用"和直达该项目该 API 的"启用 … API"按钮（链接由 TOFI 用已知 API 和校验过的项目自己拼，不转发错误里的网址）。第一次上传的客户端封存在工作区保险库里，之后加第二个 Google 服务（gogcli 多服务增量授权带 `include_granted_scopes`，或带 `google_client` 的 MCP 条目）不再要 JSON。OAuth 成功页现场读一次工具：能用就只给结果和返回，缺什么才给对应的那一个启用按钮或测试用户入口。
- 回复对得上做过的活（分支 `fix/reply-matches-work`，[D45](decisions/0045-reply-matches-the-work.md)）：完成复核先逐句对照工具记录（范围、数字、结果）再查完成度；`say` 说明加一句；`computer_files` 写入结果带绝对 `location`，`publish_file` 结果带 `source_path` 和 `attached_to_reply`，说明写明只发布人要收的、工作文件留在电脑上。系统提示词未改。`location` 随下一次电脑镜像构建才在线上出现。
- **Google Workspace MCP 的 agentic 客户端要求和 Developer Preview（分支 `fix/google-agentic-client`，D46）。** Google 自 2026-11-11 起拒绝未标为 AI 智能体的 OAuth 客户端，11-12 起该设置固定。设置页和 OAuth 完成页的 Google 引导加了“勾选 Use this client for an AI-powered agent”、固定说明，以及“把 Cloud 项目加入 Workspace Developer Preview Program”（7 语言）；Google MCP 主机连接或工具调用被 401/403 拒绝时，诊断按主机加 HTTP 状态分类，工具调用时 Preview Program 排第一、agentic 第二。Gmail MCP 不能发信，发信走 `gog`。**Owner 动作：** 在 GCP 项目 `tofi-self-hosted` 确认已加入 Preview Program，并在 2026-11-11 前标记现有 OAuth 客户端。
- **移除本地插件不再报 “plugin is busy”（分支 `fix/local-plugin-remove-busy`）。** `Runner.Remove` 先把插件置为退役（不再发出新租约，连接检查和探测随即被拒），再有界等待（5 秒）在途租约结束，然后才删；只有超过等待的真实调用才拒绝，返回稳定码 `plugin_busy`，界面显示“正在被某个任务使用，稍后再试”（7 语言），连接和插件原样保留。旧实现在租约非零的瞬间就拒绝，连接检查或冷启动正好在飞时必败。
- **自己安装社区 MCP，试验案例 Discord（分支 `feat/discord-connector`，[D47](decisions/0047-curated-official-community-user-installed.md)）。** 政策：精选只收官方连接，社区服务由使用者自己装（Discord 仍是目录里的 held）。本地 MCP 安装：可选 npm 包摘要（`sha512-…`，不符拒装）；环境变量值改为密码框、装好后清空；Runner 状态和连接带出处，“查看详情”里写“社区 · 包名 版本”，出处进配置指纹。**社区连接的工具不再信提供方的只读标注**，默认按写批准，owner 在工具面板手动标只读。使用者安装的带脚本 Skill，`read_skill` 现在也把脚本复制到 Bot 电脑。资格记录与步骤见 `docs/integrations/discord-*.md`（推荐 `@discord-mcp/cli` 0.32.1、10 个工具、邀请权限 66560/68672）。Runner 镜像没有改。**未用真实 Discord token 验证**，见资格记录“What is proven”。
- 工作区 Google 客户端不进「密钥与机密」：不列出、不能在那里删除、不装到电脑上（owner 批准）。

### rc.32 — 10-21 晚，`a10a4fb`

验证：Go 全套、gofmt、vet、16 项界面检查退出码全 0；群分工与记忆在本地 terra 上实测（见 D42、D44）。

- 回答闸（D43）：定时任务的结果在交付前先由评分员（后台模型）按评分标准逐条打分，不合格的草稿私下退回 Bot 重写，补救办法由 Bot 自己想；最多退回次数在设置 › 自动行为 › “定时和后台任务交付前先复核”里（默认 2，0 关闭）。聊天回复不受影响。
- 早先说过的事：`save_memory` 说明写明学到持久信息时当场存，系统提示词加一条同向原则；历史被截断时在上下文里加一条“早先轮次存在，`search_history` 可找到原文”（预留 400 字符，不加搜索调用）；`search_history` 说明写明何时用以及按字面匹配。系统提示词核心 +416 字符，只上调了提示词大小测试里的三个上限各 +416（[D44](decisions/0044-recall-context-memory-and-search.md)；分支 `feat/recall-context`）。
- 内置交付技能只留通用能力：删除按评测任务写的生成器（`build_deck`、`build_report`、`build_workbook`）、检查器（`check_deck`、`check_page` 的规则清单）和发票对账（`reconcile`）；`render_preview`、`xlsx_summary` 去掉断言参数，新增 `pptx_outline`（形状转储）和 `preview_page`（多宽度预览）；五个 SKILL.md 改成只写原则。目录描述少 60 字符，提示词上限各降 60（[D41](decisions/0041-builtin-deliverable-skills.md) 补记）。
- 群聊分工：成员名单每项带该成员的职责（持久指令开头一行，最多 140 字符），提示词里 “自己回答” 一句换成按职责分工的原则（自己职责内的自己做，同事职责更合适的部分交给同事并并入同一条回复，单职责请求和闲聊不拆）。群里仍只显示最终结果和简短转交行。群聊最长提示词 +296 字符，固定开销上限 7803 到 8196；只在需要同事职责时才转交；同事的贡献只发一次，协调者只发一条整合结果（[D42](decisions/0042-group-roster-roles-and-delegation.md)）。
- 代码约定与新页面指南：`docs/conventions.md`、`docs/design/new-page.md`；CI 新增 `gofmt`（全部 Go 文件已格式化，无基线）、`go vet`、界面守卫（层级、对话框、菜单、组件目录页、动效令牌、入口包结构），`go test` 时限 30 分钟。
- 规矩写进文档：评测只用来发现缺陷，修的是模型行为、提示词、流程和通用能力，不为测试定制工具（`docs/conventions.md` §7）。

### rc.31 — 10-21，`57d1952`

设计系统迁移合入（owner 10-21 预览通过），加上两个线上修复。

- **界面层级统一成一套刻度**（分支 `ds/step1-layers`，设计系统迁移第 1 步，决定 D28、D31、D32）：所有 z-index 换成 13 层 `--z-*` 令牌，弹层都放进同一个根节点 `#tofi-layers`。看得见的变化：出错的 toast 在对话框和设置窗上面也看得到；设置窗不再被启动画面、菜单、身份卡盖住；同时开着的几个对话框按打开顺序叠；电脑小窗排到 Bot 设置浮卡和抽屉下面。完整清单 L1–L14 在 `docs/design/migration.md` 第 1 步。新守卫 `test:layers`；入口包 +49 B。
- **菜单和弹出卡片换成 Radix**（分支 `ds/step2-menus`，设计系统迁移第 2 步，决定 D28、D31）：新建、顶栏"⋯"、侧栏右键/长按、账户、输入框附件、设置里 MCP 行的菜单，以及表情反应菜单、Bot 身份卡，改用懒加载的 Radix DropdownMenu / Popover（入口包 +5 B，懒加载块 30,699 B）。外观和位置不变；所有菜单一套键盘规则（方向键、Home/End、Enter/空格/↓ 打开时焦点在第一项、Esc 回到按钮、Tab 往后走）；贴边时翻转；账户菜单、附件菜单、@菜单不再被侧栏和输入框切掉；设置里的菜单在设置窗之上；群聊"已读：……"改成和图标提示同一套（悬停半秒后出现，外观不变）。完整清单 M1–M8 在 `docs/design/migration.md` 第 2 步。新测试 `test:overlays`。
- 测试：`test:sidebar-pins` 的"减弱动效时放下不滑动"一项不再把减弱动效下 0.01 ms 的过渡算成滑动（以前约三次里挂一次，`main` 上也一样）。
- **对话框统一换成 Radix**（分支 `ds/step3-dialogs`，设计系统迁移第 3 步，决定 D28、D31）：删除确认、选接任者、表单 sheet、技能访问、全屏只读会话、VM 授权、新账户引导，以及设置里"离开前保存"和"电脑重启"两个确认框，共用一个实现（`ui/src/overlays/ModalDialog.tsx`，懒加载）。Tab 不出框，Esc 只关最上面一层，关掉后焦点回到打开它的按钮（删除确认以前还不回去），背景对读屏隐藏，`aria-modal` 齐全；桌面客户端的菜单快捷键在删除确认开着时也不再生效。外观不变（逐像素对照过）。看得见的变化：引导和两个确认框叠放顺序变了，toast 和提示现在在它们上面（清单 S1–S6 在 `docs/design/migration.md` 第 3 步）。新测试 `test:dialogs`；入口包 −1,148 B。
- 测试：`test:settings-shell` 的"确认后口令消失"一项等列表刷新完再数（以前和刷新赛跑，在第 1 步的分支上也会挂）。
- **组件目录页**（分支 `ds/step6-catalog`，设计系统迁移第 6 步，决定 D28、D34）：开发时打开 `/catalog.html`，能看到每个组件的每种状态（浅色/深色、电脑/手机宽度、7 种语言），全是真组件配假数据，不连任何服务器。只在开发时可用，不进正式版；新守卫 `test:catalog` 证明构建产物里没有它。用户看不到任何变化。
- **侧栏拖动保留自研引擎**（分支 `ds/step5-dnd`，设计系统迁移第 5 步，只改文档）：用 dnd-kit 重做了一版对照（实验分支 `ds/step5-dndkit-trial`，不合并），`test:sidebar-pins` 能过，但一次拖动里 React 要渲染侧栏行约 800 次（引擎 0 次），懒加载块 16.9 KB（引擎 3.5 KB），块加载后整份列表重挂一次（1 倍 CPU 多一个约 70 ms 的长任务），也没带来新能力。界面和包都没变。
- **手机上可以右滑返回列表**（分支 `ds/step4-motion`，设计系统迁移第 4 步，决定 D33、D35）：在对话里用手指按住往右拖，对话跟着手指走，过半松手或快速一甩就回到列表，不到一半弹回；松手时带着手指的速度走，途中可以再接住。只在手机宽度、只认手指。点返回按钮的推入动画不变。Motion 库做了对照实验，没有引入。新测试 `test:swipe-back`、`test:motion-experiment`；入口包 +244 B。
- **设计系统迁移六步合在一起**（集成分支 `ds/integrate`，已合入 `main`）：上面第 1–6 步的分支按 2、3、6、5、4 的顺序合进第 1 步，冲突按意图解决（细节在 `docs/design/migration.md`"集成"一节）。合并后能看到的新东西只有一处：从侧栏右键菜单打开的删除确认或选接任者对话框，关掉后焦点回到那一行（以前两步各自都对，合在一起时会落到页面上）。其余是修合并弄坏的：组件目录页的导入路径和"已读名单"那一格、`test:dialogs` 的菜单选择器，以及第 4 步误提交的一个测试临时目录。菜单和对话框共用一份 Radix 底层代码（`test:dialogs` 守着）。入口包 315,113 → 314,194 B（预算 315,805 B）。
- owner 对迁移方案的四个回答记成决定 D31–D34（入口预算不动、接受第 1 步的层级变化、手机右滑返回要做实验、组件目录页只在开发时可用）。

- 修复 `publish_file`：一次路径写错（`uncertain_effect`）之后，换路径或换 `request_key` 再发仍被回放第一次的错误。现在只拦参数完全相同的重放；源文件不存在改为确定失败 `file_not_found`（guest 返回结构化码，主机按码分类），文件建好后可原样重试；相对路径基准写明为 `/workspace/bots/<bot_id>`，与 shell 工作目录一致。分支 `fix/publish-file-replay`。
- 修复 Bot 反复用错参数调用 MCP 工具（Notion `notion-update-page` 被退回 “replace_content 需要 new_str”）：Notion 一侧没变，问题在 TOFI。模型直接用确切工具名调用、而本次运行没展示过参数结构时，第一次调用只返回完整 schema、不执行；`search_mcp_tools` 一页降到 22 KiB 以内，不再在无人值守运行里被截掉中间的嵌套字段；参数校验失败和远端退回参数时给出完整 schema（上限 4 KiB 提到 16 KiB）。分支 `fix/mcp-tool-schema`。
- **置顶拖拽（owner 预览后改）**：拖动时整片已置顶的大头像区域就是放置目标，一个虚线框框住所有大头像，提示在框内；没有置顶时搜索框下出现一排高的空框；任何东西都不盖住它，只有被拖的行在上面；松手直接落定不再跳一下（`ds/integrate` `2666fe0`、`937861b`）。
- 小电脑窗口在样式块晚到时会飞出屏幕、把消息栏挤成一字宽（`main` 上早有），窗口定位样式改为随入口加载（`dbc000e`）。
- 定时任务卡片左侧时间栏在计划时间未知时显示 `--:--`，不再把"Unconfirmed / 待确认"挤出栏外（说明仍在悬停提示里）。

### rc.30 — 10-21 夜，`0b4b948`（10-21 早 owner 放行后上线）

起因是 10-21 的交付类摸底（8 个真实交付任务，默认模型 codex-gpt-5.6-terra，只完成 1 个，花费 $6.98，结果在 `../tofibot-taskeval/eval/results/deliv-terra-r0/`）。

- **聊天上传的文件在 Bot 的电脑上**：zip、PDF、DOCX、XLSX 等放到 `/workspace/home/uploads/<对话 id>/`，第一次用电脑时懒复制，提示词写出路径（feat/computer-files-and-office，决定 D36）。
- **Bot 电脑预装英文办公工具**：LibreOffice、tesseract（英文）、poppler、pandoc、Python 办公与图表库；其他语言的数据和字体由 Bot 按需取到用户目录（同上，决定 D37）。
- **五个内置交付技能**：表格、报告、幻灯片、网页、代码交付，带对账、渲染预览、越界检查脚本；默认开，设置 › Skills 里可关不可删（feat/deliverable-skills，决定 D41）。
- **瞬时错误先重试**：流内过载、限流、5xx 按错误码有界重试（约 2 分钟，服从 `Retry-After`）；失败的运行不丢已发布的文件（fix/run-failure-deliverables，决定 D38）。
- **`sandbox:` 链接**输出时改成可点的附件地址；引用回复标记前有空白也能识别（同上，决定 D39）。
- **查证**：Anthropic 模型可以读网页（`web_fetch`）；查证原则放到决策点；格式错误的 400 不再把整个账户的联网搜索关掉（这是线上不搜索的真正原因）；`<cite>` 标记不再进正文（fix/verification-rigor，决定 D40）。
- **模型列表**：连接 Codex 或增删 API Key 后各处模型选择器立即刷新（fix/model-catalog-refresh）。

### rc.29 — 10-20 20:15，`748c774`

- **"始终允许"**：审批卡片上可以让某一个 Bot 以后直接使用某一个写工具，不再每次问。授权绑定工具定义和服务器身份，工具定义一变或服务器地址一变就失效；高风险工具、隐藏对话里都不能用；自动审查说"拒绝"时照样拦；Bot 自己定时干活时也生效（卡片上写明）；在设置里能撤销，每次使用都留审计（feat/standing-tool-allowance，决定 D24）。
- 审批/提问卡片列表变轻：已处理的卡片只带摘要，列表支持 ETag/304，空闲时轮询从 3 秒逐步放慢到 15 秒。原先打开页面 25 秒内要拉 10 次、0.81 MB（perf/questions-poll，`a028e49`）。
- 构建工具 vite 升到 8.3.3，关掉 Dependabot #15–17 三条 launch-editor 告警（提交说明只写了 #15，`a067264`，决定 D26）。

### rc.28 — 10-20 17:01，`55fb70d`

- **接手卡片**：Bot 碰到登录、二次验证、验证码时，在聊天里请人接过屏幕；需要连接某个服务时，直接在聊天里给出连接卡片（feat/handover-cards，决定 D11）。
- **侧栏**：拖动排序，顺序保存在服务器；拖到搜索区上方的区域就置顶，置顶项是大猫头像加小名字；支持触屏长按和键盘 Alt+方向键（ui/sidebar-pins，决定 D21）。
- **消息区外观**：输入框上方是真正的渐进模糊，顶栏变成透明毛玻璃，消息从顶栏下面滑过；"有新消息"的按钮始终在最上层，不被任何东西盖住（`a5866aa`，决定 D18）。
- **新消息到达**：整条回复到达时像牛顿摆一样从下往上依次弹到位；离底部不远的读者会被自动带下去，刚手动往上翻过的不会被拽走；开启"减少动态效果"时只淡入（`a7814b3`，决定 D19）。
- **已送达/已读**不再在每条消息下占一行，只在悬停、聚焦或点按时出现在操作行里（`1d2b322`，决定 D16）。
- 登录卡片和启动画面在屏幕上居中（`36b2b12`，决定 D20）。
- **整体流畅度**：手机上列表和聊天之间是 iOS 式推入；设置、电脑桌面、引导等面板按需加载；20 秒无操作后猫停止动画；切换对话先画最新的消息（ui/smooth-shell、ui/smooth-timeline、ui/smooth-motion）。
- Bot 用 `publish_file` 发布的文件会返回一个只有主人能打开的链接，Bot 可以直接把链接给你（fix/publish-file-link）。
- 开发用：本地开发服务器可以用 HTTPS 给局域网设备预览（`TOFI_DEV_HTTPS_CERT/KEY`，`55fb70d`，决定 D22）。

### rc.27 — 10-20 10:47，`5035a5d`

- **设置 › 自动行为**：每个自动行为都有可见、可调的开关和默认值（feat/automation-settings，决定 D15）。
- **模型分工**：对话和所有要动手干活的任务默认用 Sonnet，后台轻活（摘要、要点、分诊、审查）用 Haiku（同上，决定 D14）。
- **说了就做**：你的指示本身就是确认，Bot 先把事做了再汇报，碰到阻碍前先把每条路都试过（fix/act-dont-ask，决定 D09）。
- **可靠性第二轮**：被一起点名的几个 Bot 不会以全员沉默收场；Anthropic 模型在服务器端联网搜索，来源进证据，费用记入用量；定时任务告诉你下次运行时间时带上时区；需要假设时在决定那一刻说明假设（fix/reliability-step2，决定 D09）。
- **安全跟进**：审批卡上的事实由服务器生成，不由模型填；连接 MCP 时拦截指向内网地址的主机；登录限流按账户和 IP 分桶；没真正执行的审批可退回（fix/security-followups）。
- 开发用：`npm run dev:remote` 让本地界面直连线上数据预览，不必为每个小改动发 rc（`e6aa7b1`，决定 D22）。

### rc.26 — 10-20 01:38，`477b41f`

- 启动画面换成模糊的骨架屏加一只呼吸的灰色 logo，加载完溶解进工作区（ui/blurred-preloader，决定 D20）。

### rc.25 — 10-20 01:14，`a063c2c`

- **发送立刻上屏**：气泡马上出现，发送中是半透明加时钟，送达一个勾，已读（有 Bot 开始处理）两个勾；群里悬停能看到谁读了；失败显示"未发送 · 重试"（feat/optimistic-send-receipts，决定 D16）。

### rc.24 — 10-20 00:46，`17bb4b7`

- 每条消息下方一行低调的操作按钮，没有底框；消息之间的间距加大，操作行不会压到下一条（ui/action-bar-below，决定 D17）。

### rc.23 — 10-20 00:15，`3d0bb9d`

- 手机界面：窗格切换动画、输入框留白、点按出工具条（含复制）、输入框字号 16px、表格可横向滚动（fix/audit-ui-mobile）。
- 断线恢复：心跳和卡流检测、唤醒后重连、断网 4 秒后才提示、网络错误用人话说；失败的任务给"重试"；填模型 key 时会说清为什么不能用（额度用完、没有可用模型）（fix/audit-resilience-ux）。

### rc.22 — 10-20 00:01，`bbba69c`

一次分九路的审计，前七路在这一版合入。

- 安全：浏览器点击审批必须匹配确切的元素文字且只能用一次；扩展改动要人批准确切内容，无人值守时不改；TLS 代理后面的会话 cookie 加 Secure 和 HSTS（fix/audit-security）。
- 任务可靠性：重试保留之前已完成的工具结果；被委派的子任务失败或超预算会回报给委派它的人；等共享桌面不再消耗任务的预算（fix/audit-agent-reliability）。
- 群聊：不带 @ 的回复发给被回复那条消息的 Bot；"停止"会停掉整条接力；非成员的 @ 当普通文字（fix/audit-groups）。
- 性能：大账户的对话列表、清扫和草稿开销降低；界面资源 gzip 压缩并长期缓存；不用电脑时停掉空轮询（fix/audit-backend-perf、fix/audit-frontend-perf）。
- 任务收尾：可能被完成审查否掉的回答先不流式显示，避免显示了又删（fix/audit-task-followthrough）。
- 聊天界面：内联打字光标、复制按钮、表情反应立刻生效、每个对话缓存最新一页（悬停预取）（fix/audit-ui-chat）。

### rc.21 — 10-19 19:23，`55c5515`

- 说某件事"已核实/最新"，必须在这次任务里真的查过；在群里先查再问（fix/verified-needs-lookup，决定 D09）。

### rc.20 — 10-19 19:18，`2a05a44`

- 带着说明清楚的假设直接交付，而不是反复问；"写一封"就是把写好的内容交给你，发不发由你决定；群里只显示最终合并的结果（fix/deliver-with-assumptions，决定 D09）。
- 修复工作区一直"加载中"：事件流从当前位置开始而不是重放全部历史，刷新合并，Bot 列表不再等模型目录（fix/workspace-loading-livelock）。

### rc.19 — 10-19 16:10，`63a5254`

- 打开电脑窗口时，如果电脑正在从休眠恢复，会等它醒来再连；电脑短暂掉线后自动重连；没有桌面时显示安静的说明（fix/desktop-viewer-unreachable）。

### rc.18 — 10-19 15:42，`73cbfe2`

- Bot 向人要输入只走一条通道（提问卡片）；私密输入（如密码）也并入这里；隐藏对话里不弹任何要人输入的卡片（refactor/one-input-rendezvous，决定 D10；规则见 `AGENTS.md` "Asking a human"）。

### rc.17 — 10-19 14:56，`70fea8b`

- Bot 的人设里写的职责范围只决定它主动做什么，不再拿来拒绝闲聊（fix/persona-scope-is-not-a-refusal，决定 D07）。
- 评测：在这一版前后对 Haiku/Sonnet/Opus 做了 15 题任务完成评测，结果见 `../tofibot-taskeval/eval/results/prod-*-r1/summary.md`（Haiku 10/15、Sonnet 10/15、Opus 11/15）。三份 r1 结果的生成时间是 10-19 17:01（Haiku）、17:14（Sonnet）、17:28（Opus）PDT（各 `results.json` 的 `generated_at`）。这时 rc.18（15:42）和 rc.19（16:10）都已发布，所以 r1 实际测的是 rc.17、rc.18 还是 rc.19：待确认（上下文清单记为 rc.17）。

### rc.16 — 10-19 13:37，`2e46dc9`

- 一条消息里 @ 了几个 Bot，每个都按被 @ 的顺序回答；第一个 Bot 不再替别人分派（feat/wake-every-mentioned-bot，决定 D05）。

### rc.15 — 10-19 11:56，`3b2cf35`

- Bot 在两次工具调用之间写的内部文字，永远不会出现在聊天里（只在"查看过程"里）（fix/internal-text-never-visible，决定 D08）。

### rc.14 — 10-19 11:12，`e52f786`

- 群里 Bot 叫醒同事时显示为一行交接；给同事的具体要求只放在叫醒请求里，不在群里贴出来（fix/say-text-mentions-do-not-wake）。

### rc.13 — 10-19 02:28，`4f94dd4`

- 一个 Bot 错过的、已经结束的群讨论，它只看到要点，不会把旧话题重新捡起来（fix/group-finished-exchange-context，决定 D06）。

### rc.12 — 10-19 00:54，`8d06d18`

- 结构化输出第三阶段：回复、发聊天消息、完成定时任务三条路都并入 `say`；Haiku 私聊时必须通过 `say` 回复（feat/structured-output-phase3，决定 D04，规格见 `docs/agent-plan/structured-output.md`）。

### rc.11 — 10-17 16:11，`d4ac003`

- **结构化输出上线**：Bot 的回复通过一个 `say` 工具发出（回答、备注、消息三种）；工具之间的文字算内部文字；模型没按约定走时，把最后一段文字当回复发出。工作区开关默认开，在设置 › 通用 › 聊天里（design/structured-output、feat/structured-output-p0、feat/structured-output-say，决定 D04）。

### rc.10 — 10-16 17:14，`385050e`

- Safari 修复：列表不再因定位锚点没渲染而一直隐藏；发送前先滚到底；日期格式化缓存（WebKit 卡顿 862 ms 降到 60 ms 以下）；复制消息不再带上时间戳；点已选中的文字不会丢失选择（fix/safari-pane-select-send）。
- 仓库清理：误提交的 `ui/node_modules` 链接移出版本控制（fix/untrack-node-modules-link）。

### rc.9 — 10-16 16:13，`0875f60`

- 碰到不认识的名字或术语先搜，再决定要不要问你（fix/look-up-unknown-names）。
- 工作区和 `/api/config` 报告的默认模型是你保存的设置，不是内置的兜底值（fix/effective-default-model）。

### rc.8 — 10-16 15:49，`7e4de6f`

- Bot 看到的历史里，自己最近 3 条回复保留原文，更早的只给一句要点，减少它模仿自己的旧腔调（fix/history-own-replies-gist；依据是 10-16 的 A/B：新 Bot 7.1–7.6 分，老 Bot 6.6 分，见 `docs/agent-plan/wave-notes.md` 下半"Next wave"第 13 条）。

### rc.7 — 10-16 15:07，`37cd3cc`

- 时间戳统一为定长格式，修复写入时间相近的记录排序会颠倒的问题；取消对无响应 MCP 服务器的调用不再卡 5 秒（fix/flaky-go-tests）。
- 新闻类回答的链接必须指向说明该事实的具体页面，没打开过的新闻不报（fix/news-link-specific）。
- 修复聊天窗格里几个组件共用同一个 key 的问题（fix/ui-audit-and-keys）。

### rc.6 — 10-15 21:31，`a6a7969`

- **修打字卡顿**：群里每按一个键要 37.5 ms 脚本加 66 次排版，现在每键约 0.44–2.07 ms（fix/composer-typing-lag，决定 D02，测试 `npm --prefix ui run test:composer-typing`）。
- Bot 不再引用紧挨在上面的那条消息（fix/quote-directly-above）。
- 调研类回答不再在结尾单独加一行"我没核过……"（fix/research-no-caveat-line）。

### rc.5 — 10-15 14:59，`8fb1377`

- 群里的委派显示成"X 请 Y 接手"，具体交代折叠起来（"查看交代"）（fix/handoff-compact-line）。

### rc.4 — 10-15 14:27，`23fdd22`

- 交接可以带私下备注，只给接手的同事看，不贴在群里（feat/handoff-private-notes）。rc.5 改为折叠显示后，这个备注字段又撤掉了（见 wave-notes "Next wave" 第 5 条）。

### rc.3 — 10-15 14:06，`472256f`

- 委派任务写得像对同事说的一句话，不再像提示词（fix/visible-delegation-text）。

### rc.2 — 10-15 13:44，`7e21e5e`

- 群里没 @ 任何人的消息由主助理接；打招呼、道别、闲聊会得到简短回应（fix/group-small-talk）。

### rc.1 — 10-15 12:24，`7f3ab75`

- **像人说话第一轮**：修的是人设生成器（`set_bot_profile` 和引导流程），不是单个 Bot；生成器只问身份、工作、范围、需要确认的事和性格，不许写回复格式、固定说法和示例；说话规则里删掉所有例句（fix/persona-generator-no-examples，决定 D01）。起因是 owner 10-15 对 14 个问题打分 5.9/10。

---

## v0.1.3 — 2026-10-15 09:54（正式版，`3b6a885`）

与 rc.30 是同一个提交。当天线上升级到此版（`docs/agent-plan/wave-notes.md` "Wave v0.1.3"段首）。逐条变化和验证见该文件 1–42 条，这里只列用户能感觉到的：

- **任务更稳**：服务崩溃或重启后任务从上一个检查点继续，中断的写操作标为"未核实"让 Bot 先核对而不是重做，最多自动续 3 次（第 32 条）；一个任务内的思考过程全程保留（第 3 条）；SQLite 遇锁等待而不是退出（第 31 条）。
- **工具审批更合理**：MCP 提供方标了只读的工具直接运行，其余每次审查，只有你设"总是问我"的才算高风险（第 10 条）；同一聊天里可以"批准并记住"24 小时（第 15 条）；重复读取不再被拒（第 9 条）；审查失败会说出原因（第 8 条）；用量页单列安全审查的花费（第 12 条）。
- **定时任务**：改过几次的定时任务不再丢失写权限（第 16 条，线上阻断问题）；支持按星期几的每周定时（第 18 条）；汇报时以记录为准，不再重复早已过时的"02:30 失败"（第 37、41、42 条）。
- **群聊像团队**：被 @ 的 Bot 各做各的部分，不重复互相委派（第 18 条）；群里定时任务按时间顺序跑，组长的汇报能看到成员的结果（第 19 条）；气泡样式统一，名字在上、换人时留间距（第 23、36 条）。
- **聊天更安静**：完成的任务默认只显示 Bot 的消息，过程收在"查看过程"里（第 24 条）；中途的步骤旁白会折叠（第 35 条）；不再重复同一个最终答案（第 21、34 条）；回复去掉"审计腔"（第 33 条）。
- **界面**：文件卡片和应用内预览（md、代码、csv、pdf、图片等）（第 26 条）；焦点框只在键盘操作时出现（第 27 条）；流式回复结尾不再一下跳出整段（第 38 条）；切换对话第一帧就在底部（第 39 条）；电脑窗口不再自动弹出，打开时自动唤醒休眠的电脑（第 6、14 条）。
- **安装器**：先问是否已经有反向代理，并给出 Caddy/Nginx 配置片段（第 13 条）。
- 新增"主助理"角色和跟进事项的影子模式（只记录不发帖）（第 25 条）。

<details>
<summary>v0.1.3 的 30 个候选版（10-10 至 10-15）</summary>

时间 PDT。rc.15 和 rc.27 构建失败：rc.15 失败在 `images` 任务，原因待确认；rc.27 失败在 `test / go`，即 wave-notes 第 40 条修掉的审查竞态。

| rc | 时间 | 提交 | rc | 时间 | 提交 |
|---|---|---|---|---|---|
| rc.30 | 10-15 09:32 | `3b6a885` | rc.15 | 10-13 11:16（失败） | `e605821` |
| rc.29 | 10-15 09:02 | `3c761ef` | rc.14 | 10-13 04:15 | `eceae09` |
| rc.28 | 10-14 21:34 | `9f32d1c` | rc.13 | 10-13 03:50 | `d827146` |
| rc.27 | 10-14 20:55（失败） | `4e07fe1` | rc.12 | 10-13 03:27 | `ed9bd52` |
| rc.26 | 10-14 18:17 | `4fedb7f` | rc.11 | 10-13 02:48 | `a1a6d47` |
| rc.25 | 10-14 18:10 | `095a2c9` | rc.10 | 10-13 02:00 | `c967438` |
| rc.24 | 10-14 17:47 | `2f254ac` | rc.9 | 10-13 01:29 | `1936595` |
| rc.23 | 10-14 17:34 | `bba6713` | rc.8 | 10-13 00:47 | `988e893` |
| rc.22 | 10-14 17:27 | `605c725` | rc.7 | 10-12 11:40 | `672b82e` |
| rc.21 | 10-14 11:19 | `0fd8a1f` | rc.6 | 10-12 00:25 | `72fc1a5` |
| rc.20 | 10-14 02:14 | `9e072ad` | rc.5 | 10-11 03:31 | `100422c` |
| rc.19 | 10-13 19:51 | `4f50e92` | rc.4 | 10-10 21:50 | `1d63214` |
| rc.18 | 10-13 16:37 | `02ffadb` | rc.3 | 10-10 15:52 | `7e1ca21` |
| rc.17 | 10-13 14:37 | `77addb2` | rc.2 | 10-10 13:51 | `1d5f826` |
| rc.16 | 10-13 11:47 | `d10eb0e` | rc.1 | 10-10 12:04 | `7e2c47f` |

每个 rc 的最后一个提交多半是 `docs: wave notes item N`，可以按条目号对回 wave-notes。

</details>

## v0.1.2 — 2026-10-10 02:05（正式版，`c35841c`）

- 批准之后继续执行任务时，不再因为缓存的 token 而失败（`c35841c`，标签说明 "resume after approval no longer fails on cached tokens"）。v0.1.1 之后只有这一个修复。

## v0.1.1 — 2026-10-10 01:34（正式版，`1ec7e00`）

owner 批准后发布，当天线上升级（`docs/agent-plan/wave-notes.md` 第一段）。候选版只有 v0.1.1-rc.1（10-09 23:50，`6948cfa`），在测试虚拟机上做了全新安装验收。

- **首次使用引导**：欢迎 → 连接模型（ChatGPT 设备登录或 API key，必填）→ 选择连接（可选）→ 和第一个 Bot 私聊；中途离开能接着来。没连模型时输入框上方有提示，侧栏有"完成设置"。
- 字体（Fredoka、JetBrains Mono）改为本地打包，原来从 Google Fonts 加载，失败时没有任何提示。
- 首屏和 19 个空白页里放上会动的猫（同时最多 6 只）。
- 管理后台重做：容量卡片、账户列表和详情（角色、电脑磁盘、重置密码）。
- **删除账户**：先导出加密备份（30 天内可用口令导入），再删除电脑和磁盘、释放配额和数据。
- 连接目录加入 Robinhood（官方 MCP）。
- 设置页清理掉所有原生控件和兜底字体（审计违规 1,119 → 0）。

## v0.1.0 — 2026-10-09 21:17（第一个正式版，`ab51eba`）

TOFI 的第一个自托管正式版：一条命令在 Linux 主机上装好 Server、Web 界面和每个账户的隔离电脑（microVM）。从 10-07 23:54 的 rc.1 到 10-09 20:57 的 rc.23，共 23 个候选版。

- **一条命令安装**（rc.3，feat/one-command-install）和交互式安装器（rc.8，feat/interactive-install）；默认在所有网卡上开自签名 HTTPS（rc.4）。
- **Web 界面 7 种语言**（rc.5，feat/i18n）。
- **每个账户一台电脑**：工具调用有超时、每台电脑有看门狗、空闲时快照到磁盘约 2 秒恢复、每台最多 2 个标签页、内存回收（rc.5 至 rc.6）。
- **主机命令**：`tofi status`、`tofi update --check`、电脑升级（rc.7，feat/cli-upgrade）。
- **设置页重做**第 1、2 阶段：新的设置外壳、按 Bot 分配技能（rc.9 至 rc.12，`docs/agent-plan/settings-redesign.md`）。
- 两步设置、密码规则、代理后的登录限流（rc.14）；管理员更新提示和版本卡片。
- **安全更新**：更新前自动备份、可恢复、有任务在跑时不更新、自动更新；新版启动不健康就回滚（rc.15 至 rc.23；合约 `docs/agent-plan/safe-updates.md`，提交 `f4b4bb1`）。
- 在此之前（10-01 至 10-07，未走发布流程）已有：多模型提供方（Codex、OpenAI key、Claude key）、Bot 默认跟随全局模型、账户和 Bot 数据加密导出导入、扩展与 MCP 目录、自动审查。

<details>
<summary>v0.1.0 的 23 个候选版（10-07 至 10-09）</summary>

rc.1 至 rc.8 的说明来自残留的附注标签对象；之后是轻量标签，只有提交。rc.10、rc.11 失败在 `images, guest`，换镜像源后 rc.12 成功（`80993b5`）；rc.17 失败在 `test / go`，同一提交以 rc.18 重跑成功（`docs/agent-plan/installer-backlog.md:40`）。同一提交被打两次标签的（rc.15/16、rc.17/18、rc.20/21、rc.22/23）是重跑。

| rc | 时间（PDT） | 提交 | 说明 |
|---|---|---|---|
| rc.23 | 10-09 20:57 | `ab51eba` | 被拒的新版崩溃时回滚照样完成 |
| rc.22 | 10-09 20:57 | `ab51eba` | 同上 |
| rc.21 | 10-09 20:39 | `0281025` | 升级验收可模拟坏版本 |
| rc.20 | 10-09 20:27 | `0281025` | 同上 |
| rc.19 | 10-09 20:12 | `7cd68a6` | Docker 报健康才算启动成功 |
| rc.18 | 10-09 19:11 | `7cd68a6` | 同上（rc.17 重跑） |
| rc.17 | 10-09 19:11（失败） | `7cd68a6` | 同上 |
| rc.16 | 10-09 18:55 | `70058c5` | 更新前备份、恢复、任务守护、自动更新 |
| rc.15 | 10-09 18:55 | `70058c5` | 同上 |
| rc.14 | 10-09 16:52 | `221ce0c` | 两步设置、密码规则、代理后限流 |
| rc.13 | 10-09 16:00 | `b5c8e1b` | `tofi status` 精简 |
| rc.12 | 10-09 14:51 | `80993b5` | CI 换 BuildKit 镜像源 |
| rc.11 | 10-09 14:36（失败） | `d43d8dc` | 按 Bot 分配技能 |
| rc.10 | 10-09 13:55（失败） | `d43d8dc` | 同上 |
| rc.9 | 10-09 13:37 | `fb74527` | 设置页重做第 1 阶段 |
| rc.8 | 10-09 00:43 | `6846500` | 交互式安装器、CLI 升级命令、休眠/看门狗/rootfs 修复 |
| rc.7 | 10-08 22:28 | `55816e2` | `tofi status`、`update --check`、电脑升级 |
| rc.6 | 10-08 19:20 | `fbdd131` | 休眠 AppArmor 修复、看门狗停止修复、只读 rootfs |
| rc.5 | 10-08 16:52 | `9dd50d8` | 多语言、工具超时、看门狗、内存回收、2 标签上限、快照休眠 |
| rc.4 | 10-08 01:37 | `311d89a` | 默认 HTTPS、安装横幅 |
| rc.3 | 10-08 00:56 | `63b8c36` | 自托管候选版（一条命令安装合入） |
| rc.2 | 10-08 00:12 | `b4075cf` | 自托管候选版 |
| rc.1 | 10-07 23:54（标签时间，无 Actions 记录） | `b4075cf` | 第一个自托管候选版 |

</details>

## 发布流程之前

两个 `live-*` 标签标记的是发布流程建立前的源码快照：`live-20261005-b35ead1`（`b35ead1`，10-04 22:16）和 `live-20261006-93db5f8`（`93db5f8`，10-06 12:06）。它们当时是否对应线上部署：待确认。仓库第一个提交是 `36a9920`（10-01 19:06）。
