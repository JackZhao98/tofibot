# TOFI 路线图

> 截至 2026-10-20 20:45 PDT，依据 `main` @ `748c774`（`v0.1.4-rc.29`）。
> 每做完一项，就在同一个提交里把它移到“已完成”，并写上提交号和 rc 版本（决策 [D27](../decisions/0027-docs-are-the-system.md)）。
> `Dxx` 是 owner 决策编号，每条在 [`docs/decisions/`](../decisions/) 里有同编号的文件。各领域现在的样子见 [status.md](status.md)，版本记录见 [../releases.md](../releases.md)。
> 没能核实的内容标为 **待确认**。

## 1. 已完成（最近，v0.1.3 之后，都在 `v0.1.4-rc.29` 里）

按领域排列。“最早的 rc”一列的意思是：从这个 rc 起包含该改动，可以用 `git merge-base --is-ancestor <提交> <rc 的提交>` 核对。rc.26 之前的标签已按 [D25](../decisions/0025-github-release-cleanup.md) 删除，各 rc 对应的提交取自 [../releases.md](../releases.md) 的一览表（来源是 `release.yml` 的 Actions 运行记录）。

| 事项 | 提交 | 最早的 rc | 依据 |
|---|---|---|---|
| 打字开销和对话长度无关，并用 `test:composer-typing` 守住预算 | `9920ae4` | rc.6 | [D02](../decisions/0002-measure-interaction-cost.md) |
| 人设生成器里不再放例子 | `dbc62ea` | rc.1 | [D01](../decisions/0001-persona-generator-no-examples.md) |
| 结构化输出第 0–3 阶段：只用一个 `say` 工具，工具调用之间的文字只给内部看；Haiku 在一对一对话里必须调用工具 | `31fb1cd`、`d4ac003`、`8d06d18` | rc.11 / rc.11 / rc.12 | [D04](../decisions/0004-structured-output-say.md)；[structured-output.md](../agent-plan/structured-output.md) 第 11–12 节 |
| 工具调用之间的内部文字不再出现在聊天里 | `3b2cf35`、`57e68a1` | rc.15 | [D08](../decisions/0008-internal-text-never-in-chat.md) |
| 用户提到的每个 Bot 都按提到的顺序各自回答 | `2e46dc9` | rc.16 | [D05](../decisions/0005-every-mentioned-bot-answers.md)；structured-output.md 第 14 节 |
| 已经结束的群聊讨论，以摘要形式交给错过它的 Bot | `4f94dd4` | rc.13 | [D06](../decisions/0006-closed-group-exchange-gist.md)；structured-output.md 第 13 节 |
| Bot 的职责范围不是拒绝闲聊的理由 | `70fea8b` | rc.17 | [D07](../decisions/0007-scope-is-not-a-refusal.md) |
| 说明假设再交付；“写”就是把成稿交给用户，发送由用户自己来；群聊只给一个合并后的结果；说某件事“已核实”必须有查询依据 | `977b1ec`、`55c5515` | rc.20 / rc.21 | [D09](../decisions/0009-reliability-principles.md) |
| Bot 向人提问统一走一个机制；需要用户私下输入的内容也并进来；用户看不到的对话里拒绝向人索取输入 | `73cbfe2` | rc.18 | [D10](../decisions/0010-one-human-input-rendezvous.md) |
| 2026-10-19 审计的九组发现：安全、Bot 运行可靠性、群聊、后端性能、任务执行到底、前端性能、聊天界面、手机界面、韧性与体验 | `78c51bb`、`26dbf13`、`6be0784`、`464ea88`、`50544b6`、`e18819c`、`bbba69c`、`d234690`、`3d0bb9d` | rc.22（前七组）/ rc.23（`d234690`、`3d0bb9d`） | 合并说明 “audit findings (…)” |
| 每条消息下方有一行低调的操作按钮，消息之间留出更宽的间距 | `17bb4b7` | rc.24 | [D17](../decisions/0017-quiet-message-action-row.md) |
| 乐观发送和发送中、已送达、已读、失败回执 | `a063c2c` | rc.25 | [D16](../decisions/0016-optimistic-send-receipts.md) |
| 启动屏改成模糊的骨架加一个呼吸的 logo | `477b41f` | rc.26 | [D20](../decisions/0020-phone-push-and-boot-screen.md) |
| 安全跟进：审批卡上的事实信息由服务端生成；连接时也检查内网地址；登录限流的计数分开；可退还的审批 | `2befb5b` | rc.27 | `AGENTS.md` 第 89–102 行 |
| 可靠性第二步：服务端联网搜索；定时任务的时间带上时区；在做决定的那一步说明假设 | `97cfa6a` | rc.27 | [D09](../decisions/0009-reliability-principles.md) |
| 收到指令就执行，先把事做完再报告；放弃之前把能走的路都试一遍 | `6bc0103` | rc.27 | [D09](../decisions/0009-reliability-principles.md) |
| 设置 › 自动行为页；对话默认用 Sonnet，后台默认用 Haiku | `24af5a1` | rc.27 | [D14](../decisions/0014-sonnet-conversation-haiku-background.md)、[D15](../decisions/0015-automatic-behaviour-settings.md) |
| `publish_file` 返回可以直接打开的附件链接 | `ccb0eb8` | rc.28 | |
| 交接卡片：请用户接管屏幕，或请用户连接一个服务 | `4afa77e` | rc.28 | [D11](../decisions/0011-handover-cards.md) |
| 界面流畅度方案：消息列表行 memo、切换对话先画最新的几块、面板按需加载、动效 token、跟底滚动的补间、手机推入切换、猫头像空闲时暂停 | `fd76fdd`、`979a386`、`df71c96` | rc.28 | [D02](../decisions/0002-measure-interaction-cost.md)、[D19](../decisions/0019-arrival-spring-cascade.md)、[D20](../decisions/0020-phone-push-and-boot-screen.md) |
| 新消息到达的级联动画；输入框上方的渐变模糊；顶栏的磨砂玻璃 | `a7814b3`、`a5866aa` | rc.28 | [D18](../decisions/0018-progressive-blur-scope.md)、[D19](../decisions/0019-arrival-spring-cascade.md) |
| 侧边栏拖动排序、置顶区、置顶的大猫卡片 | `d4d0fa4` | rc.28 | [D21](../decisions/0021-sidebar-reorder-and-pins.md) |
| 用 `dev:remote` 连生产环境预览；局域网 HTTPS 预览 | `e6aa7b1`、`55fb70d` | rc.27 / rc.28 | [D22](../decisions/0022-preview-via-dev-remote.md) |
| 依赖更新：vite 8.3.3 | `a067264` | rc.29 | [D26](../decisions/0026-vite-8-3-3-dependabot.md)（提交说明只写了 #15；GitHub 上 #15–17 三条告警都是 `fixed`，`fixed_at` 同为 2026-10-21T00:42:42Z，即 10-20 17:42 PDT） |
| 问题列表轮询：已结束的问题压缩显示、支持 ETag/304、空闲时降低频率 | `a028e49` | rc.29 | |
| “始终允许”：给某一个 Bot 长期授权一个写入工具 | `748c774` | rc.29 | [D24](../decisions/0024-standing-tool-allowance.md)；`AGENTS.md` 第 103–120 行 |
| GitHub 清理：保留 4 个稳定版和最近的几个 rc | 不是代码提交 | | [D25](../decisions/0025-github-release-cleanup.md)；`v*` 标签现在只剩 `v0.1.0`–`v0.1.3`、`v0.1.4-rc.26`–`rc.29`（`git ls-remote --tags origin`；另有两个 `live-*` 标签不在此列） |

“顶栏不再放第二只猫”（[D23](../decisions/0023-no-second-cat-in-header.md)）落在哪个提交：**待确认**，`git log` 里没找到对应的提交说明。

## 2. 进行中

| 事项 | 现状 | 依据 |
|---|---|---|
| 文档体系：总索引、现状、路线图、决策记录、版本记录、工作流程，`AGENTS.md` 加“从这里开始” | 正在分支 `docs/system-opus` 上写，还没合并 | [D27](../decisions/0027-docs-are-the-system.md) |
| 设计系统规范和迁移方案 | 正在 [`docs/design/`](../design/) 里写。owner 看过、点头之后才开始第一步迁移 | [D28](../decisions/0028-headless-design-system-migration.md) |
| 连接 Codex（或增删 API Key）后模型列表不刷新：服务端不再把非实时快照缓存 5 分钟、连接变化立即丢弃内存目录；网页端三处选择器共用 `useModelCatalog`，提供方变化、窗口重新聚焦时重取 | 在分支 `fix/model-catalog-refresh` 上，还没合进 `main` | `internal/app/model_catalog_refresh_test.go`；`ui/scripts/test-model-catalog-refresh.mjs`（`test:model-catalog-refresh`） |
| 任务完成度评测脚本 | 在分支 `eval/task-completion` 上（`c9f3517`…`820121f`），还没合进 `main`；结果目录被 git 忽略，只存在本机 | [status.md](status.md) 第 5.1 节 |

## 3. 下一步

按建议顺序排列。每一项都写了怎么才算做完，验收时以测试的退出码为准（[D30](../decisions/0030-standing-operating-rules.md)）。

### 3.1 设计系统迁移（[`docs/design/`](../design/)，[D28](../decisions/0028-headless-design-system-migration.md)）

- **内容**：引入无头组件库（候选有 Radix、Motion、dnd-kit），同时保持 TOFI 现在的样子。第一步做层级刻度和弹层：把现在 1 到 1200 随手写的 z-index（最高是 `ui/src/onboarding.css:5`）统一到一套刻度上。
- **前提**：owner 先看过方案并点头。每一步都要在 `dev:remote` 预览里给 owner 看（[D22](../decisions/0022-preview-via-dev-remote.md)）。
- **怎么算做完**：每一步都按方案里写好的文件范围、风险和检查方法完成。动效和性能相关的现有测试（`test:motion-tokens`、`test:shell-budget`、`test:sidebar-pins`、`test:chat-switch` 等）全部通过。

### 3.2 Haiku 在一对一对话里的可靠性

- **问题**：Haiku 的 `draft_move_plan` 从 r0 到 r3 每次都失败（最近一次 r3 跑在 rc.26 上）：列一串问题，而不是说明假设后直接给结果。一对一的 `dm_visionquest` 没去查，就把它当成了 Vision Pro 的应用（`prod-lookup2-haiku-5-5`）。`no_question_units` 在 r3 出现了换算错误。
- **现状**：对话默认已经改用 Sonnet（[D14](../decisions/0014-sonnet-conversation-haiku-background.md)，`automation_settings.go:49`），但 Haiku 仍然可以选作对话模型，也负责后台轻活。Haiku 在一对一对话里使用 `say` 的比例已经是 100%（structured-output.md 第 12 节），所以问题出在内容，不在协议。
- **做法**：先在 rc.29 或之后的版本上，用全新的 Bot 把完整 15 题和名字查询专项都跑一遍（[D03](../decisions/0003-test-live-immediately.md)、[D13](../decisions/0013-test-with-fresh-bots.md)），确认 rc.27 那批可靠性修复对 Haiku 有没有用，再决定要不要改提示词或生成器。不在运行中途切换模型（[D14](../decisions/0014-sonnet-conversation-haiku-background.md)）。
- **怎么算做完**：Haiku 在完整 15 题上不低于 Sonnet；`draft_move_plan` 不再卡在提问上。

### 3.3 模型“以为自己认识”的名字也要先查

- **问题**：[D09](../decisions/0009-reliability-principles.md) 要求遇到不熟的名字先查再问，`717e0d8` 也已经让群聊和一对一一样先查。可是模型自以为认识的名字（VisionQuest）它不会去查：Haiku 认成了 Vision Pro 的应用，Sonnet 认成了 1985 年的同名电影（`prod-lookup2-*`，rc.26）。
- **现状**：`8d83e30`（rc.27）加上了服务端联网搜索，提示词里查询的那一行也覆盖了“可能比模型更新的作品”，但还没复测。
- **怎么算做完**：名字查询专项（`dm_visionquest`、`group_visionquest`、`dm_trafalgar`、`group_trafalgar`）在 Haiku 和 Sonnet 上都是 4/4，并且每次都能看到在回答之前做了查询。

### 3.4 审计剩下的项

| 项 | 问题 | 依据 | 怎么算做完 |
|---|---|---|---|
| `computer_shell` 和 CDP 的隔离 | `computer_shell` 和共享 Chrome 的 DevTools 端口使用同一个 uid 和回环地址，shell 可以直接操控浏览器 | `AGENTS.md` 第 121–122 行 | shell 连不上 DevTools 端口，并有一个测试，把修复撤掉它就会失败（[D30](../decisions/0030-standing-operating-rules.md)）。这类改动放宽了安全边界，合并前要做一次对抗式审查（[D30](../decisions/0030-standing-operating-rules.md)） |
| 消息列表性能的后续 | 主体已经上线（`6535016`、`39918f5`）。还剩真机 GPU 追踪：无头浏览器测不出模糊的成本，所以 rc.28 加的渐变模糊还没在真机上量过 | `AGENTS.md` 第 124–136 行；协调者的界面流畅度方案第 9 项（目前只在协调者的临时目录里，没进仓库）；剩下的完整清单 **待确认** | 真机上滚动长历史时，超过 20 ms 的帧不超过 2% |
| 发送失败的消息要能保存下来 | 失败和发送中的消息只存在内存里（`sendPayloads` 是 `useRef`），刷新页面就丢了 | `ui/src/App.tsx:510`、`:1899-1926` | 刷新后仍然显示“未发送 · 重试”，点了能用同一个 `client_message_id` 重发；`test:send-receipts` 加一项刷新的场景 |
| 附件也要乐观发送 | 附件要先上传完，气泡才出现，和 [D16](../decisions/0016-optimistic-send-receipts.md) “按回车立刻有气泡” 不一致 | `ui/src/App.tsx:3398-3411` | 带附件的消息按回车后马上出现气泡，上传进度显示在气泡里，上传失败时走“未发送 · 重试” |

### 3.5 `v0.1.4` 正式版（需要 owner）

- **规则**：打 `v*` 正式 tag 是对外的动作，必须先得到 owner 确认（[D30](../decisions/0030-standing-operating-rules.md)）。部署前要检查有没有正在跑的运行（[D30](../decisions/0030-standing-operating-rules.md)）。
- **问 owner 之前要准备好**：已经验证了哪些、还没验证哪些，分成两张清单。至少包括：在最后一个 rc 上跑完整 15 题加专项的结果（第 3.2、3.3 节）；按 [installer-backlog.md](../agent-plan/installer-backlog.md) 第 4 项用真 Bot 跑一遍的清单；安装器在干净 KVM 机器上的验收状态（`AGENTS.md` 第 25–26 行）；[../releases.md](../releases.md) 里的 v0.1.4 改动清单。
- **怎么算做完**：owner 确认后打 tag，发布流程 `.github/workflows/release.yml` 通过，`tofi update --check` 能看到新版本。

### 3.6 其他已知的待办（还没排顺序）

| 项 | 依据 |
|---|---|
| 群聊里不再露出 Bot 之间的分工话语（`group_wiki_decision` 在 r2、r3 上全部失败） | [status.md](status.md) 第 4.2 节；[D09](../decisions/0009-reliability-principles.md) |
| 结构化输出第 4 阶段：不再写扫描器和 `stream_assistant_turns`，去掉开关 | structured-output.md 第 9 节 |
| 安装器待办的第 3–5 项 | [installer-backlog.md](../agent-plan/installer-backlog.md) |
| 旧分支 `fix/chat-history-reload`（`e896e3a`，2026-10-10）没合并：要不要合，**待确认** | `git branch --no-merged main` |

## 4. 暂缓

| 事项 | 为什么暂缓 | 依据 |
|---|---|---|
| Electron 桌面端和 iOS 上的渐变模糊 | 这两个客户端不在本仓库：Electron 在私有仓库 `tofi-app` 里，iOS 源码的位置 **待确认**。网页端只在消息区底部用真正的渐变模糊，顶栏用磨砂玻璃（这是例外，已经在 `a5866aa` 落地），其他地方保持现有的磨砂玻璃。要等真机 GPU 数据（第 3.4 节）出来再做 | [D18](../decisions/0018-progressive-blur-scope.md)；`AGENTS.md` 第 16–18 行 |
| 修改账户密码 | owner 认为不急 | [D29](../decisions/0029-password-change-not-urgent.md) |
| Haiku 在群聊里强制调用工具 | 量过之后决定不上：强制后 `say` 的比例从 38% 升到 86%，但分工话语泄露从 11% 升到 28%，平均分从 1.78 降到 1.59 | structured-output.md 第 12 节 “Numbers” |
| 主动跟进从影子模式改成真的发消息 | 设计上要先在影子模式下跑一周；什么时候转正 **待确认** | [proactive-followups.md](../agent-plan/proactive-followups.md) |
| 运行续跑的第 0、3、4、5 阶段 | 第 1 阶段（`25a739c`、`4a169df`）和第 2 阶段（`526a979`）已合并；其余阶段没找到实现，是暂缓还是放弃 **待确认** | [run-resume.md](../agent-plan/run-resume.md) 状态行（写的"Phases 0, 1 … not started"已过时，见 [../README.md](../README.md) 第 6.1 节） |
| 设置页重做第 3 阶段 | 要等工具重做的 WS2、WS6 先完成 | [settings-redesign.md](../agent-plan/settings-redesign.md) 状态行 |
