# TOFI 下一步：合并路线图

> 写于 2026-10-22 02:10 PDT。基线：`main` @ `61f4c3a`（rc.32 之后、rc.33 打标签之前；`feat/discord-connector` 已在这个提交合入）。
> 五条线（可用性、回答与交付质量、设置与界面、能力、可靠性与发布）各出了一份计划，本页把它们合成一份。各线要点在[附录](#6-附录五条线各自的要点)。
> 规则沿用 [D27](../decisions/0027-docs-are-the-system.md)：做完一项就改 [status.md](status.md) / [roadmap.md](roadmap.md)，本页只在拍板后更新一次“结果”列。没能核实的标 **待确认**。

## 1. 一句话结论

**离 85 分差的不是功能，是三样能拿出来的证据：rc.33 上的可靠性数字（15 题 ≥ 12、交付 ≥ 5/8）、升级路径 v0.1.3 → rc.3x 在干净 KVM 上验过、设置页按 IA 清干净；外加 owner 自己真用两天。** 今晚能无人值守补上其中的“升级验收”“运行健康数”“Google 日历/Drive”“中文串出海”，评测要钱和 owner 的预算批准，留到早上。

| 维度（status.md §3） | 现分 | 挡在哪 | 本页对应 |
|---|---|---|---|
| 可靠性 18/30 | rc.26 以来没跑完整评测；交付 4/8；群分工话语外露 | §2 A5/A6、§3 Q1–Q3、§4 第 1 周 |
| 稳定性 12/20 | 升级路径未验；安装器未过 KVM 验收；失败消息刷新即丢 | §2 B1、§4 第 1 周 |
| 对话质量 11/15 | 回复超出记录（D45 已进 rc.33，线上未复测） | §3 Q2 |
| 界面流畅度 13/20 | 设置页 IA 提案未实施；103 条中文串直出 | §2 A2/A3、§3 Q6–Q7 |
| 功能 14/15 | 个人 Google 只开了 Gmail | §2 A1 |

## 2. 今晚 7 小时（02:00–09:00 PDT）无人值守任务清单

**规则**：全部 Sonnet（[D12](../decisions/0012-no-opus-unless-needed.md)），每个 ≤ 2 小时，各自 worktree，验收以退出码为准（[D30](../decisions/0030-standing-operating-rules.md)）。只列**不需要 owner 拍板**的事。线上操作（部署、打标签）只由协调者做；今晚协调者在 02:30 前后上 rc.33，所有线上评测**不跑**（要预算，见 Q2）。**不打 v0.1.4**。

### 2.0 先说清两条前提（五份计划都没看到）

| 事实 | 依据 | 影响 |
|---|---|---|
| Discord 分支**已经合进 `main`**（`61f4c3a`，02:00 PDT），包括搬到新连接页（`03da947`）、D47、社区安装加固 | `git merge-base --is-ancestor feat/discord-connector main` 为真；`main..feat/discord-connector` 0 个提交 | “搬 Discord 进 rc.34”不再是工程任务，变成 B2 的**验证**；但 `main` 上 `status.md:160` 仍写“未合并”，B4 要改 |
| `prod-sonnet-r4`（10-21 01:47 PDT，Sonnet 11/15，`group_wiki_decision` 通过、`draft_move_plan` 通过、`watch_go_release` 失败）、`prod-lookup3-sonnet`、`prod-resourceful2-sonnet`、`deliv-terra-r0..r3` 都在本机结果目录，**没进 status.md** | `tofibot-taskeval/eval/results/*/summary.md` | B4 补表；A6 的信任清单要把 r4 算进去 |

### 2.1 第一波 02:15–04:30（并行，文件不交叉）

| # | 任务 | 改哪些文件（只许这些） | 验收（退出码 0） | 量 |
|---|---|---|---|---|
| **A1** | **gogcli 开 Calendar + Drive**：`--allow-tool` 按账户已授权的 services 生成（先在 gog v0.40.0 上核对 `gog mcp --help` 的工具名）；`GogCheck` 按服务各做一次只读请求；连接对话框加服务多选；“启用 API”按钮按服务拼；状态行写明已授权哪些服务；7 语言 | `internal/mcprunner/gog.go`、`install.go` 及其测试；`ui/src/connections/GogSetup.tsx`、`google.ts`、`status.ts`；`ui/src/locales/*/extensions.json` | `go test ./internal/mcprunner/`（新增：授权 gmail+calendar 时参数含两者、未授权不含）；`npm --prefix ui run test:connections`（加服务选择用例）、`test:i18n`、`test:integration-catalog` | M |
| **A2** | **服务端中文串改成码 + 英文兜底 + 7 语言**：29 个非测试 `.go` 文件里 103 条（`grep -rlP '"[^"]*[\x{4e00}-\x{9fff}]' internal --include='*.go' \| grep -v _test`）；每条配稳定 `code`，`message` 改英文；`ui/src/i18n/errors.ts` 与 `common.json` 补翻译；加 Go 守卫测试：非测试 `.go` 字符串字面量不许出现 CJK（提示词里的英文不受影响） | 上述 29 文件**减去** `oauth_completion.go`、`oauth_service_page.go`（归 A3）；`ui/src/i18n/errors.ts`；`ui/src/locales/*/common.json`；新 `internal/app/no_cjk_literals_test.go` | 守卫测试通过；上面那条 grep 只剩 A3 的两个文件；`go test ./internal/...`；`test:i18n` | M |
| **A3** | **OAuth 完成页按语言渲染**：去掉 `lang="zh-CN"` 硬编码（`oauth_completion.go:137`）和“外部服务”默认名（`oauth_service_page.go:15`），按 `Accept-Language` / 账户语言选 7 语言文案 | `internal/app/oauth_completion.go`、`oauth_service_page.go` 及测试 | `npm --prefix ui run test:oauth-completion` 加 7 语言各一条断言；中文行为不变 | S |
| **A4** | **运行健康汇总（只读）**：`GET /api/usage/runs?days=7`：按状态计数、失败 `stop/finishing_reason` 前 5、工具超时次数（exit 137 / `timed_out`）；由 `runs` + `usage_calls` 聚合；设置 › 用量加一块；`tofi status` 顺带打印；`/health` 带版本号 | 新 `internal/app/usage_runs.go` + 测试；`internal/app/app.go` 只加路由行；`ui/src/settings/` 的用量页组件；`deploy/self-host/tofi_host.py` 的 `status` | 撤掉实现测试变红（process §3.2）；本机 `curl` 得到数；`test:settings-shell`、`test:shell-budget` | M |
| **A5** | **评测器先审、方法先定（不花线上钱）**：① 用 r3 `deliverables/` 的 pptx，LibreOffice 渲染第 2、5 页对照 `inspect_deliverables.py:394` 的字数估算；python-pptx 读图表 categories/values 补进 inspection；查 r3 工具记录 terra 有没有调 `render_preview.py`；② `run_tasks.py` 结果记 git sha / rc；③ 汇报改成 k/N + Wilson 区间；④ 查评测账号 `userTimezone`（监测题默默用了 LA，`context.go:827` 要求未配置要问）；⑤ 评分员一致性：同一份证据包 Sonnet 评 3 次、Opus 评 1 次（上限 $2）；⑥ 以上规则写进 `process.md` §5 | `tofibot-taskeval` 工作区 `eval/*.py`；本仓库只许 `docs/process.md` §5 | 估算与渲染一致或估算器已修；图表数字能被检出；`summary.md` 带版本和区间；一致性数字进 process §5；时区事实一句写进 A6 的 trust.md 附注 | M |
| **A6** | **“能放心交给 TOFI 的任务”清单**：脚本从 `eval/results/` 按任务输出三档（≥ 8/9 放心 / 5–7 看一眼 / ≤ 4 先别），**只统计 n ≥ 3**，含 r4 和 deliv r0–r3；每条附结果目录、版本、失败原因、对应修复分支 | `tofibot-taskeval/eval/trust_report.py`；本仓库新 `docs/product/trust.md` + `docs/README.md` 加一行索引 | 文件每项都能追到结果目录；没有 n < 3 的判断；`docs/README.md` 索引有它 | S |
| **A7** | **定时抖动扫尾**：55 个 `internal/app/*_test.go` 用 `time.Sleep`；`GOMAXPROCS=2 go test -count=20 ./internal/app -run <含 Sleep 的用例>`，改确定性时钟；关 installer-backlog #3 | 只许 `internal/app/*_test.go`、测试用的时钟辅助文件、`docs/agent-plan/installer-backlog.md` #3 | `GOMAXPROCS=2 go test -count=50 ./internal/app` 退出码 0 | M |
| **A8** | **回滚成文**：读 `tofi_host.py` 判定 `tofi update --version <旧 rc>` 是否被接受；写 `process.md` §7.6：起不来 / 起来但坏 / Guest 镜像三种情况各一条命令；实证留给 B1 | `docs/process.md` §7 | §7.6 存在，每条命令有 `tofi_host.py` 行号出处；降级可行与否写明 | S |
| **A9** | **连接详情写“这能做什么”**：从 `tools/list` 按读/写分组（按提供方注解，社区连接不信只读注解）生成能力清单，不手写文案；社区连接写明“写入每次先问” | `ui/src/connections/RowView.tsx`、`ToolTierPanel.tsx`、`shared.ts`；`ui/src/locales/*/extensions.json` 里**只加新键**（A1 也改这个文件，各加各的键，合并时按键合） | 任一连接详情里能看到读/写分组的工具清单；无服务器时渲染通过；`test:connections`、`test:catalog` | S |

**冲突说明**：A2 与 A4、A7 都碰 `internal/app/`，但 A2 只改字符串与错误码、A4 只加新文件和一行路由、A7 只改测试文件；A1 与 A9 共用 `extensions.json`，按键合并。A5 与 A8 都改 `process.md`，一个 §5 一个 §7，顺序合即可。

### 2.2 第二波 04:30–07:00

| # | 任务 | 前提 | 验收 | 量 |
|---|---|---|---|---|
| **B1** | **升级验收 v0.1.3 → rc.33**：Proxmox 上把 VM 106 复位到快照 `fresh-debian13`（**只碰 106**），跑 `sudo python3 deploy/self-host/upgrade_acceptance.py --from v0.1.3 --to v0.1.4-rc.33`，再过 `one-command-install.md` 验收清单；按 A8 写的 §7.6 **实跑一次降级**到 rc.32；中间 33 个 rc 含 Guest 镜像换办公工具（D37），有 `--allow-schema-change` 提示就记下，不加参数硬过 | rc.33 已发布（协调者 02:30 前后）；VM 106 是既定验收机，不需新授权 | 脚本退出码 0；证据 JSON 入 `docs/acceptance/upgrade-v0.1.3-rc.33/`；降级结果写进 §7.6；失败就写明卡在哪，正式版顺延 | M |
| **B2** | **Discord / 社区安装在 `main` 上验证**（已合并，不再搬）：`go test ./internal/mcprunner/ ./internal/extensions/ ./internal/app/ -run 'Community\|Discord\|Provenance'`；`npm --prefix ui run test:community-install`、`test:connections`、`test:catalog`；连接页社区行显示“社区 · 包名 版本” | 无 | 全部退出码 0；真实 Discord token 的验证**留给 owner**（Q9） | S |
| **B3** | **设置 IA 第 3 步“权限”页（只出分支，不合）**：审批方式三段控件、记住的批准有效期、始终允许列表合成一页；审批值只剩一处写入口 | 代码不依赖 Q6–Q8；**形状要 owner `dev:remote` 预览后再合** | `test:auto-review`、`test:standing-allowance`、`test:settings-shell` 绿；分支名 `ui/settings-permissions` | M |

### 2.3 收尾 07:00–09:00

| # | 任务 | 验收 |
|---|---|---|
| **B4** | **文档追平**：`status.md` / `roadmap.md` / `releases.md` 头部版本改到 rc.33（rc.34 攒 A1–A4、A7、A9 + Discord）；`status.md:160` “未合并”改为已合（`61f4c3a`）；§5 补 r4、lookup3、resourceful2、deliv r0–r3 五行；写出 **v0.1.4 两张清单**（已验证 / 未验证，每项带出处）；把本页 §2 各任务结果填回 roadmap“已完成/进行中” | 三页头部 = rc.33；清单每项有出处；`docs/README.md` 索引含 `trust.md` 和本页 |
| **协调者** | 第一波全绿的合进 `main`（顺序：A7 → A2 → A3 → A4 → A1 → A9 → A5/A8 文档），跑全套，**打 rc.34 标签**（D22 攒一批）；线上升级前查在跑的 run（process §7.4，不用 `--force`）；早上给 owner 的汇报附 §3 的问题单 | `gh run watch` 绿；`sudo tofi status` 为 rc.34 |

**今晚不做**（需要拍板或要钱）：线上跑任何评测（Q2）；👎 信号（Q5）；“试试交给它”建议条（Q4）；IA 第 1/2/4 步（Q6–Q8）；引导收尾改提示词（要线上全新 Bot 验证，D13，$）；打 v0.1.4（Q1）。

## 3. 早上要 owner 拍板的问题

每题推荐在前。五条线的 25 个问题去重成 10 个；另有 6 个不问，按推荐默认执行（见本节末）。

| # | 问题 | 选项（推荐在前） | 来自 |
|---|---|---|---|
| **Q1** | **v0.1.4 正式版什么时候打** | **A** 条件制：B1 升级验收过 + rc.33/34 上 15 题 ≥ 12、交付 ≥ 5/8，满足后 48 h 内打 / B 两天试用后再定 / C 现在从 rc.33 打 / D 等 85 分 | 可用性 Q4、可靠性 Q1 |
| **Q2** | **评测预算** | **A** 今天批一次 ≤ $25（rc.34 上 8 题交付 ×3 + 15 题 ×1，Sonnet 做 Bot），之后每周 ≤ $30（门禁 8×3 + 每周 15×2）/ B 只跑门禁 8×3 / C 不跑 | 可用性 Q5、质量 Q5 |
| **Q3** | **评分模型** | **A** 看 A5⑤ 的一致性数字：一致就换 Sonnet 评分，省 Opus / B 保持 Opus | 质量 Q2 |
| **Q4** | **“能放心交什么”放哪** | **A** `docs/product/trust.md`（今晚 A6 已生成）+ 产品内新 Bot 空状态放 6 条第 1 档真实任务的建议条，不进提示词 / B 只 docs / C 做成产品里一页“它能做什么” | 可用性 Q1、质量 Q4 |
| **Q5** | **真实使用的质量信号** | **A** 操作行加 👎 + 三个原因（答非所问 / 让我再问 / 做错了），存表，设置 › 用量列出最近 👎 带对话和活动链接 / B 每周从 owner 的运行里抽 10 条离线按同一标准评，不进产品 / C 两者都做 / D 不加 | 可用性 Q2、质量 Q3 |
| **Q6** | **D15 边界（IA 第 1 步前提）** | **A** 改为“用户能决定的才是设置；只对某个型号成立的规则是内部默认”：下线 `force_say_haiku_dm` 与听写型号选择（固定 mini，通用页加“语音输入”开关）/ B 维持原文，只改文案 | 界面 Q1、Q5 |
| **Q7** | **技能“谁能用”（修订 D41）** | **A** 删掉，一个技能一个开关，全 Bot 生效；五个内置合成一个“文件交付”开关 / B 保留，收进“更多”菜单 | 界面 Q2 |
| **Q8** | **设置左栏形状与合并策略** | **A** 扁平 10 项（照 Claude / ChatGPT）；第 1–3 步测试绿即合进 rc.35，第 4 步只在 `dev:remote` 预览后合 / B 保留分组只改组名 | 界面 Q3、Q4 |
| **Q9** | **Google 与连接器评测** | **A** 日历 + Drive 一起上（今晚 A1 已按此做，早上用你个人账号试：先 Gmail、再加日历不传 JSON，Bot 能列今天日程）；给一个评测专用 Google 账号跑 4 道连接器题；Discord 给一个测试服务器 token 验一次 / B 只开日历、不给账号 | 能力 Q1、Q2 |
| **Q10** | **Codex terra 费用口径** | **A** 标“订阅计费”不算 $，评测单列“按 GPT-5.5 挂牌估算”/ B 直接按 GPT-5.5 价入账 / C 维持“无价格” | 可靠性 Q2 |

**不问、按推荐默认执行**（owner 想改随时说）：群公告评测标准改为“事实有人查过、结果完整”，不再要求三人各动一次（质量 Q1）；Slack 搁置到 Discord 有结论（能力 Q5）；办公镜像不拆、先量 guest 包大小（能力 Q4）；目标办公栈先按 Google Workspace（能力 Q3）；`--allow-schema-change` 每次问 owner（可靠性 Q4）；发版节奏 rc 每晚攒一批、正式版每两周必过升级验收（可靠性 Q5）；VM 106 作验收机（可靠性 Q3，已是既定事实）。

另有一条**不是产品问题但有截止日**：GCP 项目的 OAuth 客户端要在 **11-11 前**标为 AI 智能体，11-12 起不能再改（[D46](../decisions/0046-google-agentic-oauth-client.md) owner 动作）。

## 4. 接下来两周

### 第 1 周（10-22 → 10-28）：拿到证据，发正式版

| 事 | 前提 | 怎么算做完 |
|---|---|---|
| rc.34 上线（今晚一批） | §2 全绿 | `tofi status` = rc.34；`releases.md` 有条目 |
| rc.34 上评测：8 题交付 ×3、15 题 ×1 | Q2 | `summary.md` 带 sha 和区间；发票/监测 C4 ≥ 2/3；完成数不低于 r3；进 status §5 |
| owner 两天试用（`docs/product/dogfood.md`）：第 1 档任务各一次 + 2 件真活，每个 👎 当天修 | Q5、Q1 | 两天后能数出 👎 数、修了几、复发几 |
| 👎 信号（幂等迁移、进导出/删除闸、ID 可点） | Q5=A/C | 迁移测试撤掉变红；设置 › 用量能看到列表 |
| IA 第 1、2 步（去重去型号；技能一刀切，约 740 行消失） | Q6、Q7 | `test:automation-settings`、`test:settings-shell`、`test:i18n` 绿；`grep -i haiku ui/src/locales` 设置文案为 0 |
| IA 第 3 步合并（B3 分支） | owner `dev:remote` 预览 | 审批值只剩一处写入口 |
| Codex 计费口径落地 | Q10 | 用量页不再出现“无价格” |
| **打 v0.1.4** | Q1 条件满足 | `release.yml` 绿；`tofi update --check` 看到新版本；v0.1.4 两张清单在 releases.md |
| 建议条“试试交给它”（6 条第 1 档任务，7 语言） | Q4=A | `test:i18n`、`test:shell-budget` 绿；owner 预览 |

### 第 2 周（10-29 → 11-04）：把“放心交”的范围推宽

| 事 | 前提 | 怎么算做完 |
|---|---|---|
| 连接器评测 4 题（邮件分诊 / 下周空档 / Drive 找文件摘要 / 草稿待发）首轮基线 | Q9 评测账号 | `deliv-*` 同口径 summary；进 status §5.4；trust.md 多一档 |
| 幻灯片：按 A5 的结论修（评测器错就修评测器，产品错就在 `slides` 技能原则里补“看渲染图”） | A5 | `deliv_slides` ≥ 2/3 |
| 引导以一件真事收尾（`onboardingSystemPrompt` 原则，无例句，D01） | Q2 预算 | 全新 Bot ×5：5/5 收尾是动手或具体提议；owner 亲读（D03） |
| IA 第 4 步（左栏扁平、改名、功能预览总开关） | Q8 + 预览 | 控件数 ≈ 55；`test:settings-shell`、`test:shell-budget`、`test:catalog` 绿 |
| 发送失败的消息刷新后还在（roadmap 3.4） | 无 | `test:send-receipts` 加刷新场景 |
| 每周例行：15 题 ×2（门禁同批）、owner 运行抽样 10 条 | Q2、Q5 | 进 status §5，每周一行 |
| 升级验收进发版清单：每个正式版前 `upgrade_acceptance.py` 必跑 | B1 | process §7.2 第 4 条改为“已有证据目录” |

### 砍掉的（不做无用功）

| 砍什么 | 为什么 |
|---|---|
| 后台模型自动判“笨回答” | 多一项自动行为要设置、要钱；owner 点 👎 更准。两周后看 👎 数据再议 |
| 回答闸扩到聊天 | D43 实测延迟翻倍，owner 已否 |
| 幻灯片 / 发票专用检查器 | D41 禁止为评测定制工具 |
| 追 `gate_recall_name` | 无记忆无摘要是人造场景，走产品流程 18/18 已对 |
| 再推群里三方分工 | 结果 4/5 对；再推只会多出分工话语外露 |
| 模型对比实验（terra vs Sonnet 做 Bot） | 要钱，是 owner 的决定；评测口径先统一 |
| “死 CSS” | 70 个 CSS 文件全部被导入；无测量工具去猜是无用功 |
| Slack 官方连接 | 每家公司要自建 Slack 应用，门槛在 owner 侧；等 Discord 自装路径有结论 |
| 办公镜像拆能力包 | 870 MB 是估算；拆分多一条分发链路。先量 |
| 让 Bot 直接发 Gmail | D09：发送由用户自己来 |
| 监控 / 告警平台 | 一个只读聚合端点（A4）够改决定 |
| 再发单修复 rc | D22：攒一批 |
| **暂缓不砍**：`computer_shell` ↔ CDP 隔离、Guest zram | 要重建 Guest 镜像 + 对抗审查，进 85 分清单但不进这两周 |

## 5. 风险与对策（前 5）

| # | 风险 | 对策 |
|---|---|---|
| 1 | **正式版会被别人机器自动装上，而 v0.1.3 → rc.3x 的升级路径从没验过**（33 个 rc、Guest 镜像换过） | 今晚 B1 在 VM 106 实跑；Q1 把它设为硬门禁；A8/B1 把降级路径写成文并实证 |
| 2 | **线上失败率没人看见**：`runs.failed` 无汇总，重试只写日志，日志重建即清零（`retry.go:99`） | A4 只读汇总端点 + `tofi status`；第 1 周起每周一行进 status.md |
| 3 | **评测 n=1、不带版本号，一题翻盘就是 12.5%，会误导决定**（status.md 多处“待确认”就是这么来的） | A5 把 sha / 区间 / 配对比较写进脚本和 process §5；Q2 定预算后门禁改 8×3 |
| 4 | **Google 两条线有外部截止**：Workspace MCP 11-11 起拒绝未标 agentic 的客户端（D46）；gogcli 日历/Drive 在真账号上没验过 | D46 的 owner 动作放进 Q 单顶部；A1 的真账号验证是早上第一件事（Q9） |
| 5 | **今晚 9 个并行分支合进 `main` 相撞，或部署打断 owner 在跑的 run** | §2.1 每个任务只许改列出的文件；协调者按固定顺序合并、全套绿再打 rc.34；部署前 `tofi update` 自带的运行检查，不用 `--force` |

次一级：terra 无价格、依赖 Codex OAuth（Q10）；标签推送要 owner 过分类器，热修延迟（攒批可缓解）；A2 改 103 条文案可能碰到被测试断言的中文原文（守卫测试会暴露，不会静默）。

## 6. 附录：五条线各自的要点

（各线原文在本次规划的上下文里；要点如下，“↑”指向本页采纳的位置。）

**可用性（能放心交给 TOFI 的任务）**：15 题里 5 题 8 轮从未失手，交付 r3 4/8 完成、7/8 文件可用；“不对”集中在群分工话语外露和回复超出记录；引导从人设开始、不从任务开始；真实使用没有任何质量信号；v0.1.4 卡在没复测 + 安装器未验收。提了能力地图（↑A6）、建议条（↑Q4）、引导收尾一件真事（↑第 2 周）、👎（↑Q5）、两天试用（↑第 1 周）、v0.1.4 前置清单（↑B4、Q1）。先别做：自动分类器、改设置 IA、提示词塞清单。

**回答与交付质量**：交付在涨但 n=1 看不出因果；r3 剩 4 个失败性质各不同（发票=D45 待线上复测、监测=少一句基线在哪 + 时区来源待查、幻灯片=溢出是估算先怀疑评测器、公告=三人分工）；15 题自 rc.26 没跑。提了 rc.33 上 8×3（↑Q2）、先审评测器（↑A5）、评测方法改成能下结论的最便宜做法（↑A5、Q3）、信任清单（↑A6）、时区核实（↑A5④）。先别做：回答闸扩到聊天、专用检查器、`gate_recall_name`、硬推分工、模型对比。

**设置与界面**：`settings-ia.md` 是三个痛点里唯一有完整方案的（11 页 90 控件 → 10 页 55 控件，5 步）；连接页已按新形状过了 owner；103 条中文串直出前端（↑A2）、OAuth 完成页硬编码 zh-CN（↑A3）；“死 CSS”没核实到（砍）。第 1 步（↑Q6）、第 2 步（↑Q7）、第 3 步（↑B3）、第 4 步（↑Q8、第 2 周）。不做：新组件、单开“图标丑”。

**能力（连接器、技能、电脑）**：连接器从未被评测；Google 个人账号只开 Gmail、授权层已支持三服务但 `install.go:152` 钉死（↑A1）；Slack 要每家自建应用；Discord 已合（↑B2）；办公镜像已被证明可用，870 MB 是估算。提了连接器评测 4 题（↑第 2 周、Q9）、连接详情写能做什么（↑A9）。不做：拆镜像、Slack、Bot 直接发信、再加内置技能。

**可靠性、发布、运营**：发版门禁写了没验（↑B1）；回滚只覆盖“起不来”（↑A8）；线上无可观测性（↑A4）；terra 无价格（↑Q10）；`fix/flaky-go-tests` 已合但 55 个测试文件仍用 `time.Sleep`（↑A7）；文档停在 rc.29（↑B4）。不做：现在打 v0.1.4、监控平台、专用检查器、动 CDP 隔离和 zram、单修复 rc。

## 相关代码与文档

- [status.md](status.md) §3 打分、§5 评测；[roadmap.md](roadmap.md) 3.4–3.6；[../releases.md](../releases.md) rc.30–rc.32 条目
- [../process.md](../process.md) §5 评测、§7 发版（7.2 门禁、7.4 部署）、§9 模型、§10 文档同步
- [../design/settings-ia.md](../design/settings-ia.md) §7 落地顺序；[../agent-plan/installer-backlog.md](../agent-plan/installer-backlog.md) #3–#5；[../agent-plan/one-command-install.md](../agent-plan/one-command-install.md)
- 决定：[D12](../decisions/0012-no-opus-unless-needed.md)、[D13](../decisions/0013-test-with-fresh-bots.md)、[D15](../decisions/0015-automatic-behaviour-settings.md)、[D22](../decisions/0022-preview-via-dev-remote.md)、[D27](../decisions/0027-docs-are-the-system.md)、[D30](../decisions/0030-standing-operating-rules.md)、[D41](../decisions/0041-builtin-deliverable-skills.md)、[D45](../decisions/0045-reply-matches-the-work.md)、[D46](../decisions/0046-google-agentic-oauth-client.md)、D47（`main` @ `61f4c3a`）
- 代码：`internal/mcprunner/gog.go:61`、`install.go:152`；`internal/app/oauth_completion.go:137`；`internal/app/automation_settings.go:36`；`internal/provider/models.go`（无 `codex-gpt-5.6-terra` 条目）；`deploy/self-host/upgrade_acceptance.py`；`tofibot-taskeval/eval/results/`

## 7. 02:20 追加：自托管 + Web 为最高优先级；对标 Grok Bot / WorkBuddy / Muse

owner 10-22 01:58：「以在个人服务器 selfhost 作为最高优先级，selfhost 加上 web」；规划对标 grokbot、WorkBuddy、Muse。

**对标结论**（Fable 竞品研究，来源见会话汇报）：三者都不能装在个人服务器上（Grok Bot＝xAI 云电脑；WorkBuddy＝腾讯云账号+桌面应用，私有化年费数十万起；Muse＝Meta 云 VM）。自托管参照取 OpenClaw（及 Hermes / NanoClaw / ZeroClaw / TrustClaw）。

| 自托管用户在意的事 | TOFI 现状 | 参照 |
|---|---|---|
| 安装 | 需 KVM；无 swap 主机可能卡死（installer-backlog #5） | OpenClaw 一条命令、$5 VPS 可跑 |
| 升级/备份 | 升级前自动备份，但**电脑磁盘不在内**；无 `backup --verify` | OpenClaw `backup create --verify`、分阶段更新 |
| 安全自检 | 无一条命令的体检 | OpenClaw `security audit` |
| 首次使用 | 只讲连模型、建 Bot，不展示能交什么 | Grok「名字即开场白」+模板；WorkBuddy 角色+技能包；Muse 示例任务 |
| 成本控制 | 只有用量页，无预算上限 | Muse 用量表首屏 |
| 资源 | 每账户 1–4 GiB 微 VM | ZeroClaw/NanoClaw 轻量 |
| 模型 | 仅 OpenAI/Anthropic/ChatGPT 登录，无本地/兼容端点 | WorkBuddy、OpenClaw 多提供方 |

TOFI 领先：多租户隔离（每个账户一台 Firecracker 微 VM，账户之间隔离）；**同一账户内的多个 Bot 和 Grok Bot 一样共用一台电脑，不是安全边界**（owner 10-22 更正竞品研究的说法）。其余优势：服务端生成的审批事实与绑定工具定义的「始终允许」、接管卡、单一提问通道、多账户+7 语言。

**不抄**：公开技能市场（OpenClaw 恶意技能/CVE）、积分制、录屏教技能。

**对今晚清单的调整**（协调者 02:20 执行）：
- 新增 **S1** 备份含电脑磁盘 + `tofi backup --verify` + `tofi restore`（分支 `night/s1-backup-verify`）。
- 新增 **S2** `tofi security audit`（只读体检，PASS/WARN/FAIL + `--json`），与 A8 回滚成文同分支 `night/s2-audit-rollback`。
- 第一波同时只跑 7 个任务（机器负载），实际派出：A1、A2、A4、S1、S2+A8、A6、A3+A9。
- **延后**：A7 定时抖动扫尾（高 CPU，与并行任务互相拖慢）、A5 评测器审查（下一轮）、B3 权限页分支。B1 VM 106 升级验收由协调者在 rc.33 上线后亲自做（只碰 106）。
- 两周路线里，自托管项（安装 swap 预检、备份/恢复/体检、首次使用给出可点的首个任务、预算上限、OpenAI 兼容端点）整体前移到第 1 周，排在 IA 第 4 步与连接器扩充之前；Electron/iOS 相关（渐变模糊等）降到「之后」。
