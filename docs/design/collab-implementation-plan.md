# 协作与技能（C1–C4）实施方案（草案，待 owner 审）

依据：`main` @ `7e05873`（设计稿 `docs/design/collab-todo.md`），线上 `v0.1.4-rc.40`。行号是当时代码里的位置。本文只做规划，没有改代码。第 3 节的问题逐个和 owner 过完后，结论写回本文并标“已定”。

## 0. 阶段顺序

| 顺序 | 内容 | 规模 |
|---|---|---|
| 阶段 1 | C4 技能库与按 Bot 启用 | M |
| 阶段 2 | 统一的任务 + 等待模型（表、工具、上下文列出等待、超时即结果、成环检查、清单对账、版本号、保留期）；可拆 2a/2b 各发一个 rc | L |
| 阶段 3 | C2 命令结束事件、定时回看、重启与休眠 | M |
| 阶段 4 | 调度改造：每个 Bot 一条线、Bot 间并行、排队插话不取消 | L 偏 XL（风险最高） |
| 阶段 5a | 群：按角色分诊、二次分诊与认领、reaction 进上下文、任务卡片 | M |
| 阶段 5b | 群：拆任务与依赖解锁、reaction 叫醒、停滞上报、按任务树限总量 | M |

C2（阶段 3）放在调度改造前：它只依赖阶段 2 的等待模型。Bot 起命令、登记 `command_exit` 等待、结束这一轮；命令结束后叫醒新 run，不需要先改调度。前提是先改一处：登记了等待的终端不随 run 清理（`terminal_cleanup.go:93` 现在会杀掉被取消/失败/中断 run 的 PTY）。

5a 不依赖并行调度，可在阶段 4 前后发；5b 需要阶段 4，否则“并发”仍是排队轮流。

### 共用规矩

- 迁移只做加法（`CREATE TABLE IF NOT EXISTS` + `ensureColumn`；`docs/process.md:348` 降级无闸、无 `schema_version`）。旧表（`run_waits`、`skill_access`、`builtin_skill_off`）至少保留一个正式版。扩 `work_items` 状态 CHECK 照 `work_items.go:84-116` 在事务里重建表。
- 提示词：英文、只写原则、STE 写法，不放例句和关键词清单（D01）；每处改动量 `prompt_budget_test.go`；按 `structured-output.md` 做前后评测。
- 每阶段落地同一提交里：新决定文件（D58 起）、`AGENTS.md`、`docs/releases.md`、`status.md`。
- 单连接 SQLite（`app.go:338`）：不跨 guest 调用或模型调用持有 rows/tx；递归 CTE 只在短事务里跑。
- 实测：每阶段在新建「验证群(可删)」里，3 个职责不同的 Bot（如研究 / 写作 / 运维，运维有电脑），合成数据。

## 1. 决定对应的现有代码

### C1

| 决定 | 现在的代码 | 改法 |
|---|---|---|
| 1 等待 = 异步调用，三套合一 | `run_waits`：`group_respond.go:200-206,230,244,254,281,360-378`，10 分钟 `:25`，表 `app.go:579`；委派回传 `app.go:1789-1840`、`1846-1920`、`child_failure_return.go`；B1 回传 `fanout_return.go:29-51`；问人 `questions.go` + `run_input_waits` | 新 `waits` 与 `bot_inbox` 表，三套写入点改写 `waits`；问人收编为 `kind=human_answer`（“一个约会点”） |
| 2 消息 + 任务状态 | `work_items.go:54-117`、工具 `:503-568`、`work_execution.go`、`TeamBoard.tsx`/`WorkPanel.tsx`、`work_context.go:37` | 扩 `work_items` 为任务对象 |
| 3 中途提问 | 无 | `task_ask`，状态 `input_required` |
| 4 用户改需求系统同步 | 无（`collaboration.go:163-176` 只取消被 @ 那条线） | ingress 时投 `task_update` 给派活人，版本号加一 |
| 5 先挂起不打断 | `run_steering`（`collaboration.go:216-234`）、`interruptInTransaction :1170`、`steeringBoundary app.go:3866-3882`、摘要 `context.go:308-317`、`supersedeInputWaitsTx input_continuation.go:189` | 阶段 4：边界处领取收件箱并接着跑 |
| 6 一个 Bot 一次一件 | 每对话一条线 `runConversationWorker collaboration.go:1089`；同一 Bot 可同时私聊和群里跑 | 阶段 4 |
| 7 停止连带 | `cancelTreeInTransaction collaboration.go:1235`、`CancelRunTree :1326`（等待只在群里过期） | 扩到任务和全部等待 |
| 8 pending 要有真实等待 | `say.go:313,328-330,510-517`（豁免只有 `runMadeSchedule`） | 豁免加“本 run 有未完成等待” |
| 9 超时即结果 | 只有固定文字 `group_respond.go:227` | `wait_result{timed_out}` 进收件箱 |
| 10 死锁与空转 | 只有 `maxHandoffRounds` 8（`collaboration.go:44`）、wake 最多 3（`group_respond.go:174`） | 成环检查、停滞计数、按任务树限总量 |
| 11 主 Bot = 树根 | `roundFamilySQL group_chat.go:15`、`lineageKey group_respond.go:122` | 统一到 `root_task_id` |
| 12 派活人没了 | 回传前查成员（`app.go:1802-1810,1877-1888`）；`terminal_cleanup.go:93` 杀终端 | 删除/移出群连带取消并提示 |
| 13 版本号 | 无 | `spec_version` / `based_on_version` |
| 14 保留期 | 无（D38 失败 run 文件要保留） | 每日清理，设置可改 |

### C2

| 决定 | 现在的代码 | 改法 |
|---|---|---|
| 命令结束上报 | guest `terminal.go:219-253` 有 `exited/exitCode`，关闭终端只留 5 分钟 `:22-30`；host 只能拉（`computer/client.go:297`） | 阶段 3 |
| 交互 shell 的命令 | 无退出事件；`hibernate.go:116-120` 能用 `TIOCGPGRP` 判前台作业 | 前台回到 shell 算结束，无退出码 |
| 定时回看 | 只能建一次性定时任务 | `kind=timer` 等待 |
| 重启 | “Sessions end on VM restart”（`app/terminal.go:181`），输出缓冲在内存 | host 缓存尾部输出 |
| 休眠 | 有作业时拒绝休眠（`hibernate.go:58-121`），快照恢复（`microvm.go:156`） | 基本满足，实测 |
| 升级前排空 | `computers upgrade` 跳过忙机（`process.md:356`） | 有未完成 `command_exit` 等待也算忙 |

### C3

| 决定 | 现在的代码 |
|---|---|
| 1 Bot 发起任务 | `handoff app.go:4572`、`AddForwardHandoff collaboration.go:292`、`message app.go:4524`、`send_group_message team.go:320` |
| 2 汇报挂任务、叫醒派活人 | `group_followup app.go:1828`；`review` 状态已有 |
| 3 清单对账 | 无清单；可复用 `pending` 退回 `say.go:510-517` |
| 4 拆任务与依赖 | 无依赖；`parent_goal_id` 只两层 |
| 5 选择器分诊 | 无模型分诊（`defaultGroupRunSpecTx collaboration.go:279`）；`runKindTriage` 执行路径还在（`app.go:3601,3613-3650,4242-4268`，`strictTriageTarget collaboration.go:956`）；名单 `context.go:913-926` |
| 5 认领、@ 回派活人 | `respond` `group_respond.go:140-225`、去重 `group_dedupe.go:28`、`group_mention.go:37` |
| 6 串行单位改 Bot | 同 C1-6；group_chat 去重 `collaboration.go:1138-1149` |
| 7 reaction | `reaction_tool.go:11`；`SetMessageReaction reactions.go:125` 只写事件；`hydrateMessageReactions :90` 只给界面 |
| 8 任务卡片 | 参照 `QuestionCard.tsx`、D52 工单、`timelineRow.ts` |

### C4

| 决定 | 现在的代码 |
|---|---|
| 1 全局库 | `<DataDir>/skills/<name>/`；`InstallSkill management.go:409`（同名拒绝 `:447`）、`DeleteSkill :512`；无元数据表 |
| 2-4 按 Bot 勾选 | `skill_access skill_access.go:19`（无行 = 全部可用 `:64-86`）；`extensions/mcp.go:117,491,515`；`app.go:2698` |
| 5 套装 | 无 |
| 6/7 官方技能 | `builtin_skill_off` 全局（`builtin_skills.go:30-67`） |
| 9 删除/版本 | `manage_extensions` `extension_tools.go:40,233-251`；闸 `extension_gate.go:24-80`；`restrictedSkillGuard skill_access.go:154` |
| 10 谁何时勾 | 无记录 |
| UI | `ExtensionPanel.tsx:27-68`、`settings/SkillAccess.tsx`、`skillCatalog.ts`、`BotInspector.tsx` |
| 文档 | `contracts/alpha-api.md:23,55` 与 C4 一致、代码相反；`settings-ia.md:33-46` 与 C4 冲突要改稿 |

## 2. 各阶段

### 阶段 1：C4

- 表：`skill_library(name, installed_by, installed_at, current_version, official, bundle_id, source, digest)`；`skill_versions(name, version, digest, created_by, created_at, note)`，旧版本归档 `<SkillsDir>/.versions/<name>/<n>/` 可回滚；`skill_bundles`；`skill_enablement(bot_id, skill_name, enabled, set_by, set_at)`——普通技能无行 = 关，官方技能无行 = 开。
- 迁移：旧“全部 Bot”技能给每个现存 Bot 打勾；有名单的只给名单；全局关闭的官方技能给每个 Bot 写关；补第 1 版。旧表保留。
- 工具：`skill_list {scope}`；`skill_enable/skill_disable` 只改自己那列、不需批准、记录 `set_by`；`skill_install` 进库只勾自己；`skill_publish_version` 只有安装者能发；删除 Bot 端 `skill_delete`。
- HTTP：`POST /api/extensions/skills/{name}/bots/{bot_id}`（按契约）、`GET /api/bots/{id}/skills`、套装批量开关；删除只走 owner 会话；`PUT …/access` 保留一版标弃用。
- 提示词：技能目录只列已启用的，多数 Bot 变短。
- UI：设置 › Skills 改成库（Codex 式一键添加，套装为主可展开，显示安装者与版本，“移除”只在这里）；Bot 设置加技能勾选列表与“由谁何时勾”；`SkillAccess.tsx` 退役；7 种语言。
- 风险：迁移后新建 Bot 只有官方技能，要有清楚空状态；文件与表不一致时以文件为准并启动对账。
- 测试：迁移等价性；未启用不进目录、`read_skill` 拒绝；不能改别人那列；版本与回滚；套装；实测 A 装 B 看不到、勾上后能用、A 发新版 B 拿到。

### 阶段 2：任务 + 等待

- 任务：扩 `work_items`（`assigner_kind/assigner_bot_id/root_task_id/parent_task_id/origin_run_id/spec_version/result_message_id/result_based_on_version/depends_on/stall_count/message_count/closed_at`），状态加 `input_required`、`failed`（对 A2A 生命周期）。
- 清单：`task_checklist(task_id, item_id, ord, text, status open|done|skipped|blocked, reason, …)`；只有建任务的人能增删条目，被派的人只能改状态和原因（对标长任务文章“只许改 passes”）。
- 等待：`waits(…, kind bot_post|task_result|human_answer|command_exit|timer, group_mode any|all, status pending|fulfilled|timed_out|cancelled, deadline_at, result_ref)`。
- 收件箱：`bot_inbox(…, kind user_message|task_update|task_question|task_result|wait_result|progress|reaction, wakes, needs_reply, priority, consumed_by_run)`。阶段 2 仍用新 run 叫醒，阶段 4 才中途插入。
- 数据迁移：`run_waits` 的 pending 行复制成 `waits`；在跑的委派补任务行和等待；已有 `work_items` 视为给自己记的任务。
- 工具：`delegate`（统一 handoff/message/wake 派活语义，旧名先做别名）、`wait_for`（立即返回回执）、`cancel_wait`、`task_update`、`task_ask`、`say` 加可选 `task_id` 交结果；`create_work_item` 去掉“不开始执行”。
- 规则：登记等待时同事务成环检查（≤64 步，稳定码 `wait_cycle`）；统一 15 秒扫描器处理超时，启动时扫一遍；有未完成等待时 pending 不退回；`outcome=done` 但清单有 open 项退回一次（与 pending 共用每 run 一次的名额）；停止按 `root_task_id` 连带任务和等待（私聊也算）；改需求版本号加一并通知两边；每日清理（排除 D38 文件）。
- 提示词原则：调用立即回执、结果作为事件送回；每轮“在等什么”一块 ≤1600 字符；只有真在等才能说稍后；超时要判断；派活写清目标、产出格式、工具与来源、边界；清单一次一项、每项写原因。
- UI：看板加 `input_required`、`failed`，卡上显示在等什么。
- 风险：run 树与任务树并存，新连带逻辑一律按 `root_task_id`；工具 schema 增大，部分放进 `tofi_tool_search` 延迟加载。
- 实测：派三项清单任务（一项受阻）、1 分钟超时、等待期间重启服务。

### 阶段 3：C2

- host 只对有 pending `command_exit` 等待的 Bot 每 5 秒 `terminal.list`（命令在跑时电脑不会休眠，不会被轮询叫醒）。
- 进程退出有退出码；交互 shell 用前台进程组回到 shell 判结束（guest `terminal.list` 加 `foreground_job` 字段，旧 guest 无此字段则只认进程退出），无退出码时写明。
- 每次轮询缓存最后约 8 KB 输出；重启后终端找不到就送回“被重启中断，最后输出在此”。
- 登记了等待的终端不被 `reconcileTerminalCleanup` 清理；急停与取消等待时照常清理。
- `wait_for{kind:timer}`；主信号先到由 Bot 自己 `cancel_wait`。
- `tofi update` 与 `computers upgrade` 把有 `command_exit` 等待算忙；设置里重启电脑先提示。
- `computer_terminal` 加 `notify_on_exit`；改掉“end on VM restart”笼统说法。
- UI：终端标签显示“结束时通知 Bot”；对话里一行“命令结束”通知。
- 实测：`sleep 90 && echo done` 期间继续聊天，90 秒后被叫醒汇报；跑命令时重启电脑收到中断结果。

### 阶段 4：调度改造

- 推荐：对话调度器 + 每个 Bot 一把全局租约（渐进式，不重写成按 Bot 的队列）。对话 worker 挑出“自己的 Bot 空闲”的排队 run，goroutine 启动后继续挑；租约覆盖 Bot 的所有对话（一台电脑）。保留按对话的不变量（审批过期、定时先后、问人等待）。
- 跨对话优先级：同任务树续跑 > 人的消息 > 委派 > 定时任务。
- 排队插话：ingress 时被点名 Bot 正在跑，就不建新 run、不取消，写 `bot_inbox(user_message)`；`steeringBoundary` 改成 `drainInbox`，在模型调用前和工具结束后原子领取，经 `OnCheckpoint` 落盘；run 在领取前结束时在 `finishRunOpts` 同事务建续跑 run。
- 停止两档：默认排队插话；“打断当前步骤”取消当前工具的 context 但 run 不结束；“全部停止”沿用 `CancelRunTree` 并连带任务。
- 预算：每接一条插话补一份时间与调用预算；一个 run 最多接 5 条或上下文超阈值就带检查点开续跑 run。
- 群消息顺序：插话进来时正在流的草稿先作为一段发布，插话后开新草稿；并行草稿发布时是否重排序号，实测后定。
- UI：输入框常开，“已排队 / 已读到”小标；Stop 分“打断当前步骤”“全部停止”；群里可多条 LiveMark。
- 风险：单连接 SQLite 压测（6 Bot 同时流式 + 工具，测连接获取 p99）；群 round 语义（group_chat 去重、`groupRoundContext`、`coAddressed*`）默认一轮串行，要逐个改；重启恢复按租约串行；大量 steered 测试要改写。
- 实测：写长文时插一句“改成英文”，不中断、无重复、最终英文；同时 @ 另一个 Bot 确认并行。

### 阶段 5a

> **进度（2026-10-23，分支 `feat/p5a-triage-reactions`）**：已做：按角色分诊（含设置、1 人跳过、失败退回旧规则）、reaction 摘要进历史、人点 reaction 低优先级合并叫醒（原属 5b 的“reaction 叫醒”一并提前做完，限群；见 D60）。未做（等阶段 2 的任务与等待合入）：`claim_task` / `pass_task`、认领界面、任务卡片，以及分诊提示里的“本群未完成任务和负责人”。

- 分诊：复用 `runKindTriage`，无 @ 的群消息建 triage run 取代 `defaultGroupRunSpecTx`；模型拿名单、角色、本群未完成任务和负责人，严格解析；1 个成员跳过；后台模型不可用退回现行规则。
- 认领与转交：`claim_task` / `pass_task`；转交后负责人变、派活人不变。
- reaction 进上下文：历史里每条消息带紧凑 reaction 摘要。
- 任务卡片：一任务一张卡，`task` 事件原地更新，默认一行“负责人 · 3/7 · 进行中”；每秒最多更新一次；7 种语言。
- 实测：“今天特斯拉怎么样”分到看股票的 Bot。

### 阶段 5b

- `delegate` 支持 `depends_on[]`，前置完成同事务解锁并叫醒负责人；依赖也查环。
- 提示词原则：并发写代码前先定接口约定；按复杂度定投入。
- reaction 叫醒：低优先级、`needs_reply=0`、合并后叫醒。
- 派活人收件箱：被派 Bot 在任务里每句话都进收件箱；只有提问、交结果、停滞叫醒。
- 停滞 N 轮上报根 Bot；按任务树限消息总量，取代 `maxHandoffRounds` 与 wake 上限。
- 实测：“帮我搭个后端”拆成接口约定、两个并行实现、一个合并。

## 3. 待 owner 拍板（第一项为推荐）

**owner 2026-10-23 已定**：Q3 按推荐（每个 Bot 全局一条线）。Q8 不在群聊里发认领通知，改为在群成员列表里显示每个成员认领了哪件任务。Q11 Bot 装技能不需要批准；发新版本会更新到全局，需要用户批准。其余各题按推荐；补充两条也按推荐（清理不碰 D38 文件；等待中的说明不占 D50 名额）。

1. 任务结果默认保留期：30 天 / 14 天 / 90 天 / 永久手动清。
2. 任务对象放哪：扩 `work_items` / 新建 `tasks` 表、`work_items` 留作承诺看板 / 新表并下线看板。
3. “一次一件”的范围：每个 Bot 全局一条线（一台电脑），忙时私聊进收件箱在间隙读到 / 每个 Bot 每个对话一条线 / 全局一条线但私聊可开不用电脑的轻量 run 插队。
4. 阶段顺序：1→2→3→4→5a→5b / 原建议 A→B→C→D→E。
5. 等待时 run 怎么处理：混合（短的全部到齐挂起同一 run，长等待结束这一轮再叫醒）/ 一律结束再叫醒 / 一律挂起。
6. 问人卡片还在时人又发消息：卡片保留、消息进收件箱 / 照旧新消息取代卡片。
7. 谁点的 reaction 会叫醒：只有人点的 / 所有 reaction 低优先级叫醒 / 都不叫醒。
8. “我接了”怎么呈现：系统渲染的紧凑通知、别人不回应 / 叫醒其他成员点 reaction（每次多 N−1 次模型调用）/ 认领者发一句聊天。
9. 现有“全部 Bot 可用”技能迁移：给现有每个 Bot 打勾、新 Bot 不带 / 全部不勾 / 只给用过的打勾。附：Bot 导出是否带启用列表（推荐只带名字）。
10. 现有全局“关闭官方技能”：转成每个 Bot 的关闭并去掉全局开关 / 保留全局开关作第二层。
11. Bot 装技能和发新版本要不要批准：都要 / 只给自己用时不用 / 只有带脚本时要。
12. 别的 Bot 已启用的技能发了新版：自动跟最新、标“已更新”可回滚 / 锁定版本手动升级。
13. 套装格式：与 Claude Code 插件目录一致 / TOFI 自己的清单。
14. 命令结束怎么检测：host 轮询 + guest 加字段 / 新 guest 长轮询动作 / 两样都做。
15. 怎样算“用户改了需求”：任务进行中人发给被派 Bot 的消息都自动转给派活人并加版本号 / 只在被派 Bot 声明时算。
16. 分诊成本：每条无 @ 群消息调一次后台模型、1 个成员跳过 / 成员 ≥3 才分诊 / 不做。
17. 停滞阈值：3 轮 / 2 / 5。
18. 每棵任务树消息上限：60 / 30 / 120。
19. 默认超时：按类型（等 Bot 15 分钟、等任务结果 2 小时、等命令不限但必须配定时回看）/ 统一 10 分钟。
20. 任务卡片出现在哪：群里和派过活的私聊 / 只在群里。

## 4. 与现有架构或其他决定的冲突

1. 每个 Bot 一条线 vs 现在同一 Bot 可同时私聊和群里跑：Bot 在群里干活时私聊会变慢（问题 3）。
2. “在群里说一声我接了” vs D42-6 / D09（群里只显示最终合并结果和转交行）：推荐系统通知（问题 8）。
3. “其他人用 reaction 回应” vs “reaction 默认不需要回复、防客套”加成本：要每个成员都叫醒一次。
4. 排队插话 vs 问人约会点规则（`input_continuation.go:185-189` 新消息取代卡片；“一个约会点”）：统一等待必须收编 `run_input_waits`（问题 6）。
5. 边跑命令边说话 vs `terminal_cleanup.go:93` 杀掉取消/失败/中断 run 的 PTY；也违背 C1-12。
6. “最后输出在此” vs guest 输出缓冲在内存：host 必须自己缓存。
7. 命令结束只对 `open command=` 进程成立；交互 shell 只能判前台进程组，无退出码。
8. 保留期清理 vs D38（失败 run 文件保留）与 D48 备份：清理要排除，或新决定明确改 D38。
9. pending 要有真实等待 + 清单退回 vs `say.go` 每 run 一次退回名额：两者共用，先后要写清。
10. 没有全局总开关 vs 现在的 `mode:"all"` 和 `settings-ia.md`；`alpha-api.md` 反而一致，`settings-ia.md` 要改稿。
11. 官方技能可按 Bot 关 vs `builtin_skill_off` 全局；三个 `tofi-*` 起步模板算不算官方（推荐不算）。
12. 删除只由用户 vs Bot 现有 `skill_delete` 与 `restrictedSkillGuard`。
13. 全局看板不做 vs 已有按对话的 `TeamBoard`：是否显示任务（问题 2）。
14. 任务树根 vs 现在两种树（`roundFamilySQL`/`lineageKey` 与 `parent_run_id`）：新代码统一到 `root_task_id`。
15. 流式草稿一开始占序号（`stream.go:74`）vs 并行与插话：回答可能排到它所回答的消息上面。
16. “复杂任务 10 个以上 Agent” vs `maxHandoffRounds` 8、wake 最多 3：换成按任务树总量（问题 18）。
17. 等待中先跟用户说一句 vs D50（“先应一声”最多占 3 条 `say(message)` 之一）：是否放宽。
18. 用户可直接跟任何 Bot 说话 vs 隐藏的 Bot 对 Bot 对话（`requireHumanPresent`）：改需求只发生在人看得到的对话里。

## 5. 成本与提示词预算（估算，实施时实测）

| 阶段 | 每 run 提示词 | 额外模型调用 |
|---|---|---|
| 1 | 多数 Bot 变短 | 无 |
| 2 | 工具 schema 约 +1.5–2.5K 字符（部分延迟加载）；“在等什么” ≤1600 字符 | 无 |
| 3 | 终端说明多一句 | 每次命令结束叫醒一次 |
| 4 | 插话在同一 run 里接着跑，缓存命中更高，多数更省 | 无 |
| 5a | reaction 摘要占历史窗口 | 每条无 @ 群消息多一次 Haiku 分诊 |
| 5b | 无 | 人点 reaction 叫醒（合并）、停滞上报 |
