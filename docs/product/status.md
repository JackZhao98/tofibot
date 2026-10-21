# TOFI 产品现状

> 截至 2026-10-20 20:45 PDT，依据 `main` @ `748c774`（即 tag `v0.1.4-rc.29`）。
> 每次合并、发版或 owner 拍板，都要在同一个提交里更新本页（决策 [D27](../decisions/0027-docs-are-the-system.md)）。
> 文中 `Dxx` 是 owner 决策编号，每条在 [`docs/decisions/`](../decisions/) 里有同编号的文件。没能核实的内容标为 **待确认**。
> 路线图见 [roadmap.md](roadmap.md)，版本记录见 [../releases.md](../releases.md)，工作流程见 [../process.md](../process.md)。

## 1. TOFI 是什么，给谁用

TOFI 是一个自托管的 AI Bot 工作台：后端是 Go 服务（`internal/`），网页端用 React 19（`ui/`）。Bot 在一个持久的工作区里保存身份、对话、定时任务和工具连接（`README.md` 第 3–13 行）。

| 项目 | 现状 | 依据 |
|---|---|---|
| 部署形态 | 一台有 KVM 的 Linux x86_64 服务器（Ubuntu 22.04/24.04、Debian 12/13）。每个账户有一台独立的 Firecracker 电脑 | `README.md` 第 15–20 行 |
| 使用者 | 服务器的所有者（Admin）和受邀账户。只能凭邀请加入，账户之间数据隔离 | `README.md` 第 12 行 |
| 客户端 | 网页端（本仓库）。桌面端 Electron 在单独的私有仓库 `tofi-app`，它只使用带版本号的网页构建产物，不引用这里的源码 | `AGENTS.md` 第 16–18 行；`tofi-app` 提交 `73ff751` |
| 界面语言 | 7 种：de、en、fr、ja、ko、zh-CN、zh-TW | `ui/src/locales/`；`npm --prefix ui run test:i18n` |

## 2. 当前版本

| 项目 | 版本 | 依据 |
|---|---|---|
| 最新稳定版 | `v0.1.3`（`3b6a885`，2026-10-15 09:32 PDT） | `git tag`；GitHub release 标为 latest |
| 最新候选版 | `v0.1.4-rc.29`（`748c774`，2026-10-20 20:10 PDT） | `git tag` |
| 线上环境 | 记录为 `v0.1.4-rc.29`，地址 tofi.speedowagon.com。**待确认**：本页没有登录服务器去核对 | 协调者的上下文记录 |
| v0.1.3 之后的改动 | 219 个提交，其中 55 个是合并 | `git log v0.1.3..v0.1.4-rc.29` |
| 还保留的 GitHub 版本 | `v0.1.0`–`v0.1.3` 四个稳定版，加上 `v0.1.4-rc.26`–`rc.29`；更早的 rc 版本、它们的 tag 和容器镜像都已删除 | `git tag`；[D25](../decisions/0025-github-release-cleanup.md) |

`v0.1.4` 正式版还没发。打 `v*` 正式 tag 必须先问 owner（[D30](../decisions/0030-standing-operating-rules.md)），详见 [roadmap.md](roadmap.md) 的“下一步”。

## 3. Owner 的评分标准和当前自评

**评分标准（owner 定）**：85/100 可以公开发布，95/100 可以对外宣传。

**2026-10-20 自评：68/100**（协调者给出，来源是上下文记录）

| 维度 | 得分 / 满分 | 主要扣分点（本页根据评测和已知问题整理，不是 owner 原话） |
|---|---|---|
| 可靠性 | 18 / 30 | 完整 15 题评测只完成 10–11 题（第 5 节）；遇到模型“以为自己认识”的名字时不去查（VisionQuest 题两种模型都失败）；群聊里会把 Bot 之间的分工话语露给用户（`group_wiki_decision`） |
| 稳定性 | 12 / 20 | 发送失败的消息刷新后就没了；`computer_shell` 和 Chrome 调试端口没有隔离（`AGENTS.md` 第 121–122 行）；安装器还没通过干净 KVM 机器上的验收（`AGENTS.md` 第 25–26 行） |
| 对话质量 | 11 / 15 | 回答前列一串问题，而不是说明假设后直接给结果（`draft_move_plan`）；提醒时间没写明时区（`remind_dentist`，rc.27 已经改了，但还没复测） |
| 界面流畅度 | 13 / 20 | 动效和性能预算已经有测试守着（第 4.8 节），但设计系统还没统一，弹层和层级是各写各的（[D28](../decisions/0028-headless-design-system-migration.md)，[`docs/design/`](../design/)） |
| 功能 | 14 / 15 | 主要功能都在，见第 4 节 |
| **合计** | **68 / 100** | 离可以公开发布的 85 分还差 17 分 |

## 4. 现在能用的功能（按领域）

每个领域都分“能用的”和“已知缺口”两部分，“依据”一列都可以去查证。

### 4.1 对话

| 能用的 | 依据 |
|---|---|
| 一对一和群聊、流式回复、附件、Bot 生成的文件 | `README.md` 第 8 行 |
| 结构化输出：Bot 通过唯一的 `say` 工具发出回答、备注或消息（`answer`/`note`/`message`）。工具调用之间的文字只在“查看活动”里显示，不进聊天；模型没按规则调用 `say` 时，发布它最后一段文字并标记为 `implicit`。这项默认开启 | `internal/app/say.go:339`；`internal/app/automation_settings.go:84`；[structured-output.md](../agent-plan/structured-output.md) 第 10–12 节；[D04](../decisions/0004-structured-output-say.md)、[D08](../decisions/0008-internal-text-never-in-chat.md) |
| Haiku 在用户可见的一对一对话里必须调用工具，保证回答经过 `say`（默认开启，可以用 `TOFI_FORCE_SAY` 关掉） | `automation_settings.go:85`；`ca3ab01`；structured-output.md 第 12 节 “Haiku adherence” |
| 乐观发送和回执：按回车后气泡立刻出现；状态依次是发送中、✓ 已送达、✓✓ 已读（Bot 已经开始处理）、失败（“未发送 · 重试”）。回执显示在操作行里 | `97323b9`、`1d2b322`；`test:send-receipts`；[D16](../decisions/0016-optimistic-send-receipts.md) |
| 每条消息下方有一行低调的操作按钮，消息之间留出更宽的间距，避免遮挡 | `bfab118`；[D17](../decisions/0017-quiet-message-action-row.md) |
| 新消息到达时有弹簧链式的级联动画；读者靠近底部时跟着滚动 | `a7814b3`；`test:arrival-glide`；[D19](../decisions/0019-arrival-spring-cascade.md) |
| Anthropic 模型可以在服务端联网搜索（默认开启），来源记入证据，费用记入 `usage_calls` | `8d83e30`；`automation_settings.go:86` |
| `publish_file` 返回一个只有所有者能打开的附件链接 | `internal/app/artifact_publish.go:176`；`ade3968` |
| 失败的运行可以重试；重试时保留之前已经完成的工具结果 | `38dd844`、`75b3e2c` |

| 已知缺口 | 依据 |
|---|---|
| 发送失败的消息只存在内存里（`sendPayloads` 是一个 `useRef`）：切换对话不会丢，刷新页面就没了 | `ui/src/App.tsx:510`、`App.tsx:1899-1926`；`97323b9` 的说明 |
| 附件不是乐观发送：要先上传完，气泡才会出现 | `ui/src/App.tsx:3398-3411`（`submit` 里按顺序 `await api.uploadAttachment`） |

### 4.2 群聊

| 能用的 | 依据 |
|---|---|
| 用户提到的每个 Bot 都会按提到的顺序各自回答，第一个 Bot 不再负责协调其他 Bot | `237f5ce`、`2e46dc9`；structured-output.md 第 14 节；[D05](../decisions/0005-every-mentioned-bot-answers.md) |
| 一个 Bot 错过的、已经结束的群聊讨论，以摘要形式交给它，不会重新翻出旧话题（默认开启） | `internal/app/group_exchange_gist.go`；`b253449`；`automation_settings.go:87`；structured-output.md 第 13 节；[D06](../decisions/0006-closed-group-exchange-gist.md) |
| 回复某条消息时不加 @，就交给发那条消息的 Bot | `abf8fab` |
| 提到不是群成员的 @名字 时按普通文字处理；中日韩文名字后面紧跟文字也能匹配 | `9be534d` |
| 点“停止”会停下整条调用链，并让其中还在等待的项过期 | `9e24534` |

| 已知缺口 | 依据 |
|---|---|
| Bot 之间的分工话语会露给用户，比如“我的部分做完了，笔记留给 Sage 汇总”（rc.26，Haiku 和 Sonnet 都有） | 评测 `prod-haiku-r3`、`prod-sonnet-r3` 里的 `group_wiki_decision`，评分标准第 2 条；与 [D09](../decisions/0009-reliability-principles.md) “群聊里只给最终合并的结果” 冲突 |
| Haiku 在群聊里使用 `say` 的比例低（辩论、分工题只有 33–50%），因为群聊不强制调用工具 | structured-output.md 第 12 节 “Still open after Phase 3”、第 14 节末 |

### 4.3 Bot 和人设

| 能用的 | 依据 |
|---|---|
| 人设由引导流程里的人设生成器产生（`set_bot_profile`）。要改人设的毛病，就改生成器，不单独改某个 Bot；提示词里不放例子 | `internal/app/onboarding.go:186`；[D01](../decisions/0001-persona-generator-no-examples.md) |
| Bot 的职责范围只决定它主动做什么，不是拒绝闲聊的理由 | `4a27d5c`、`3d387cd`；[D07](../decisions/0007-scope-is-not-a-refusal.md) |
| “说明假设再交付”的原则已经写进 `ask_user_question`、`say` 和人设生成器 | `f342b83`、`6f2340e`；[D09](../decisions/0009-reliability-principles.md) |
| 主助理：每个账户固定一个；主动跟进功能在影子模式下运行（只记录，不发消息） | `92d5ee8`（已在 v0.1.3 中）；[proactive-followups.md](../agent-plan/proactive-followups.md)；`internal/app/followups_sweep.go:6` |
| 模型分工：对话和所有需要自主行动的工作默认用 `claude-sonnet-5-5`；摘要、分诊、审核等后台轻活用 `claude-haiku-5-5` | `automation_settings.go:49`、`:52`；`ef59d81`；[D14](../decisions/0014-sonnet-conversation-haiku-background.md) |
| Bot 的记忆，以及 Bot 之间的协作 | `internal/app/memory.go`、`internal/app/collaboration.go`；`README.md` 第 10 行 |

### 4.4 电脑和浏览器

| 能用的 | 依据 |
|---|---|
| 每个账户一台 Firecracker 电脑。工具有 `computer_shell`、`computer_files`、`computer_desktop`、`computer_browser`、`computer_terminal`、`computer_ssh_keys`、`computer_action` 等 | `internal/app/microvm.go:468`、`:479`、`:517`、`:575`；`terminal.go:181`；`ssh_keys.go:278`；`computers.go:1167` |
| 看门狗会重启没有响应的电脑；桌面查看器会等电脑醒来，掉线后自动重连 | `internal/app/computer_watchdog.go`；`51c95a2` |
| 交接卡片：Bot 可以请用户接管屏幕，完成登录、两步验证、验证码这类步骤（`request_takeover`），也可以请用户连接一个服务（`request_connection`） | `internal/app/handover.go:119`、`:156`；`d3e4ff9`；`test:handover-cards`；`AGENTS.md` 第 75–87 行；[D11](../decisions/0011-handover-cards.md) |

| 已知缺口 | 依据 |
|---|---|
| `computer_shell` 和共享 Chrome 的 DevTools 端口使用同一个 uid 和回环地址，没有隔离 | `AGENTS.md` 第 121–122 行 |
| 让 Bot 找一张网页配图的地址时，Haiku 没把能试的办法都试一遍，就让用户自己去右键复制 | 评测 `prod-resourceful-haiku-5-5` 里的 `reuters_image` |

### 4.5 定时任务

| 能用的 | 依据 |
|---|---|
| `create_schedule` 支持四种：`once`、`interval`、`daily`、`weekly`。返回结果里带 `next_at_local`，即下一次运行在该任务所在时区的时间并附上时区名；确认消息要写出这个时间 | `internal/app/schedule.go:1420`；`1a0b123`（从 rc.27 起） |
| 同一个群里的定时任务按到期顺序运行，一次只跑一条调用链 | `4664b56` |
| 定时运行中向人提问时，问题会挂起等待，到时过期 | `AGENTS.md` 第 86–87 行 |

| 已知缺口 | 依据 |
|---|---|
| 提醒里没写时区。这是在 rc.26 上测到的；时区修复 `1a0b123` 从 rc.27 起才有，还没复测 | `prod-*-r3` 里的 `remind_dentist`，评分标准第 2 条 |

### 4.6 审批和安全

| 能用的 | 依据 |
|---|---|
| Bot 向人提问统一走一个可持久的“问题”机制：会暂停运行，服务重启后还在，会过期。用户看不到的对话里拒绝弹出向人索取输入的卡片 | `internal/app/questions.go`；`AGENTS.md` 第 57–73 行；[D10](../decisions/0010-one-human-input-rendezvous.md) |
| 改动扩展（MCP、Skill）之前，要用户先批准完全相同的内容；卡片上显示服务端生成的事实信息；Bot 创建的 MCP 不能连内网地址 | `internal/app/extension_gate.go`；`30edc86`；`test:approval-facts`；`AGENTS.md` 第 89–102 行 |
| MCP 工具分级，以及在同一个对话里“批准并记住” | `internal/app/mcp_tool_tiers.go`；[mcp-tool-tiers.md](../agent-plan/mcp-tool-tiers.md)；`778880d`、`d444e58` |
| “始终允许”：给某一个 Bot 长期授权一个写入工具。授权绑定工具定义和服务身份；高风险工具不能授权；用户看不到的对话里不生效；自动审核拒绝时照样拦下；可以在设置里撤销；每次使用都留审计记录 | `internal/app/mcp_standing_allowance.go`；`748c774`；`test:standing-allowance`；`AGENTS.md` 第 103–120 行；[D24](../decisions/0024-standing-tool-allowance.md) |
| 登录限流同时按来源地址（IPv6 按 /64）和账户标识计数；用 https 访问时 Cookie 带 Secure 标记；Caddy 发送 HSTS | `AGENTS.md` 第 100–102 行、第 121 行 |

| 已知缺口 | 依据 |
|---|---|
| `computer_shell` 和 CDP 没有隔离（同 4.4） | `AGENTS.md` 第 121–122 行 |

### 4.7 设置

| 能用的 | 依据 |
|---|---|
| 设置 › 自动行为：每个自动行为都能看到、能调整，并且有出厂默认值。注册表共 13 项：对话模型、后台模型、结构化回复、Haiku 强制调用工具、联网搜索、群聊摘要、自己回复保留原文的条数、完成后审核、MCP 自动审核（默认关闭）、“记住”的有效时长、模型请求重试次数、定时任务回执审核，以及只读的电脑空闲时长；页面上还有“始终允许”的授权列表 | `ui/src/AutomationSettings.tsx`；`internal/app/automation_settings.go:81-95`（注册表）；`ui/src/StandingAllowances.tsx`；`test:automation-settings`；[D15](../decisions/0015-automatic-behaviour-settings.md) |
| 模型提供方、MCP、语音输入、主动跟进、语言等设置页 | `ui/src/ModelSettings.tsx`、`MCPSettings.tsx`、`DictationSettings.tsx`、`FollowupsSettings.tsx`、`LanguageSetting.tsx` |
| 设置页重做：第 1、2 阶段已经上线，第 3 阶段要等工具重做的部分完成 | [settings-redesign.md](../agent-plan/settings-redesign.md) 状态行 |

修改账户密码这件事 owner 认为不急（[D29](../decisions/0029-password-change-not-urgent.md)）。

### 4.8 界面和动效

| 能用的 | 依据 |
|---|---|
| 统一的动效规范：时长和缓动都定成 token，有测试守着，旧的写法只许减少 | `ui/src/design-tokens.css`、`ui/src/motion.ts`；`test:motion-tokens`；`AGENTS.md` 第 34–44 行 |
| 消息列表的性能不随对话变长而变差：每行做了 memo，流式文字不经过 Workspace 状态，切换对话时先画最新的几块 | `ui/src/timelineRow.ts`、`ui/src/draftStream.ts`；`6535016`、`39918f5`；`test:stream-cost`、`test:switch-budget`、`test:chat-switch`；`AGENTS.md` 第 124–136 行 |
| 打字的开销和对话长度无关 | `9920ae4`；`test:composer-typing`；[D02](../decisions/0002-measure-interaction-cost.md) |
| 设置、Bot 桌面、引导等面板按需加载；限制首屏体积、面板打开速度、手机页面推入速度 | `05d75ec`、`3e2de40`；`test:shell-budget` |
| 手机上列表和聊天之间是 iOS 式的推入切换；启动屏是模糊的骨架加一个呼吸的灰色 logo；登录卡片和启动屏都居中 | `58da838`、`927a970`、`ea39209`、`36b2b12`；`test:preloader`；[D20](../decisions/0020-phone-push-and-boot-screen.md) |
| 侧边栏可以拖动排序（保存在服务端），有置顶区；置顶的项显示为大猫卡片 | `internal/app/sidebar_order.go`、`ui/src/sidebarDndEngine.ts`；`9a92f4a`；`test:sidebar-pins`；`AGENTS.md` 第 46–55 行；[D21](../decisions/0021-sidebar-reorder-and-pins.md) |
| 输入框上方是真正的渐变模糊，顶栏是半透明磨砂玻璃，“新消息”提示不会被任何东西遮住 | `a5866aa`；[D18](../decisions/0018-progressive-blur-scope.md) |
| 用户 20 秒没有操作时，猫头像停下待机动画 | `39a1822` |
| 改界面时用 `npm run dev:remote` 连生产环境预览，也可以在局域网用 HTTPS 预览 | `e6aa7b1`、`55fb70d`；`AGENTS.md` 第 138–140 行；[D22](../decisions/0022-preview-via-dev-remote.md) |

| 已知缺口 | 依据 |
|---|---|
| 设计系统没有统一：层级（z-index）、弹层的位置和关闭方式都是各写各的 | [D28](../decisions/0028-headless-design-system-migration.md)；规范和迁移方案见 [`docs/design/`](../design/) |
| “顶栏不再放第二只猫”（[D23](../decisions/0023-no-second-cat-in-header.md)）落在哪个提交：**待确认** | `git log` 里没找到对应的提交说明 |

### 4.9 安装和更新

| 能用的 | 依据 |
|---|---|
| 一条命令安装：`install.sh` → `deploy/self-host/tofi_host.py`，交互界面是 `deploy/self-host/tofi_tui.py`；由 `.github/workflows/release.yml` 发布 | `AGENTS.md` 第 24–28 行；`README.md` 第 15–60 行 |
| `tofi status / update / computers / doctor ...`；`tofi update` 会先备份，新版本起不来就自动回滚 | `README.md` 第 55–67 行；[safe-updates.md](../agent-plan/safe-updates.md) |
| 前面已经有反向代理时也能安装（`--behind-proxy`、`tofi config trusted-proxies`） | [installer-backlog.md](../agent-plan/installer-backlog.md) 第 1 项（已完成）；`deploy/self-host/test_install_proxy.py` |

| 已知缺口 | 依据 |
|---|---|
| 在干净的 Linux/KVM 机器上通过验收之前，安装器只能算候选版本 | `AGENTS.md` 第 25–26 行；[one-command-install.md](../agent-plan/one-command-install.md) 的 “End-to-end acceptance” 一节 |
| 安装器待办的第 3–5 项还没关：第 3 项是 GitHub 上偶发失败的 Go 测试（可能已经被 `7a18d87` 修掉，**待确认**），第 4 项是每次发版都要用真 Bot 跑一遍，第 5 项是电脑没有 swap/zram，内存紧张时会卡死 | [installer-backlog.md](../agent-plan/installer-backlog.md) 第 3–5 节 |

## 5. 评测记录

### 5.1 评测是怎么做的

- 评测脚本在分支 `eval/task-completion` 上（`c9f3517`…`820121f`），**还没合进 `main`**。文件有 `eval/run_tasks.py`、`eval/tasks.json`、`eval/task_rubric.md`。
- 用 15 个一句话任务，每次都开全新的 Bot 和群（[D13](../decisions/0013-test-with-fresh-bots.md)），通过 HTTP 在生产环境上跑（结果里 `remote: true`、`with_computer: true`）。由 `claude-opus-5-5` 按 `task_rubric.md` 打分：只看结果，不看 Bot 说了什么，一律以记录为准。
- 结果目录 `eval/results/` 被 `.gitignore` 第 57 行忽略，**只存在本机**的 `tofibot-taskeval` 工作区里。下面的表就是它们在仓库里的唯一留存。
- 结果文件里没有记录版本号。下表的版本对应关系来自协调者的上下文，并和 `release.yml` 的 Actions 运行时间（见 [../releases.md](../releases.md)）核对过；标为“按时间推断”的版本 **待确认**。例外：r1 三份结果生成于 10-19 17:01–17:28，此前 rc.18（15:42）、rc.19（16:10）都已发布，上下文记的“rc.17”与时间不符，r1 实际版本 **待确认**。表中时间是 `results.json` 的 `generated_at`（跑完的时刻）换算成 PDT。
- 另外有一套离线评测（24 个探针），专门量 `say` 的使用比例，数字记在 [structured-output.md](../agent-plan/structured-output.md) 第 11–14 节。

### 5.2 完整 15 题（完成数 / 15）

| 结果目录 | 时间（PDT） | 版本 | 模型 | 完成 | 卡在提问 | 错误或未核实 | 任务花费（上限） |
|---|---|---|---|---|---|---|---|
| `prod-haiku-r0` | 10-19 16:43 | 待确认 | Haiku 5.5 | 8 | 2 | 3 | $1.37（有 1 个评测脚本自身的错误） |
| `prod-haiku-r1` | 10-19 17:01 | rc.17（待确认，见上） | Haiku 5.5 | 10 | 3 | 2 | $1.30 |
| `prod-sonnet-r1` | 10-19 17:14 | rc.17（待确认） | Sonnet 5.5 | 10 | 2 | 2 | $2.11 |
| `prod-opus-r1` | 10-19 17:28 | rc.17（待确认） | Opus 5.5 | 11 | 0 | 1 | $5.50 |
| `prod-haiku-r2` | 10-19 19:43 | rc.21 | Haiku 5.5 | 11 | 2 | 1 | $1.14 |
| `prod-sonnet-r2` | 10-19 19:55 | rc.21 | Sonnet 5.5 | 11 | 0 | 1 | $2.56 |
| `prod-haiku-r3` | 10-20 08:54 | rc.26 | Haiku 5.5 | 10 | 1 | 2 | $1.08 |
| `prod-sonnet-r3` | 10-20 09:07 | rc.26 | Sonnet 5.5 | 11 | 0 | 1 | $2.41 |

Opus 只在 r1（记为 rc.17）上测过一次。之后 owner 定下评测里不再用 Opus 当 Bot 模型（[D12](../decisions/0012-no-opus-unless-needed.md)）。

**rc.27 及以后的改动还没有跑过完整 15 题。** 这些改动包括：说明假设再交付、服务端联网搜索、写明时区、自动行为设置（都从 rc.27 起，可以用 `git merge-base --is-ancestor` 核对）。

### 5.3 每题结果（Y = 完成）

| 任务 | H-r0 | H-r1 | S-r1 | O-r1 | H-r2 | S-r2 | H-r3 | S-r3 | rc.26 上失败的原因（评分说明摘要） |
|---|---|---|---|---|---|---|---|---|---|
| `computer_fibonacci` | Y | Y | Y | Y | Y | Y | Y | Y | |
| `file_packing_checklist` | Y | Y | Y | Y | Y | Y | Y | Y | |
| `recurring_ai_digest` | Y | Y | Y | Y | Y | Y | Y | Y | |
| `remind_stretch_3min` | Y | Y | Y | Y | Y | Y | Y | Y | |
| `watch_go_release` | Y | Y | Y | Y | Y | Y | Y | Y | |
| `research_requests_maintained` | n | n | Y | Y | Y | Y | Y | Y | |
| `draft_email_heater` | n | n | n | n | Y | Y | Y | Y | |
| `group_wasm_intro` | n | Y | n | n | Y | Y | Y | Y | |
| `lookup_compare_cities` | n | Y | n | Y | n | Y | Y | Y | |
| `research_bun_node` | Y | n | Y | Y | Y | n | Y | Y | |
| `no_question_units` | Y | Y | Y | Y | Y | Y | n | Y | Haiku：3 杯面粉换算成 360 mL，算错了 |
| `clarify_ambiguous_sam` | n | n | Y | Y | Y | n | n | n | 预设的用户回答没有送到运行里（0 张问题卡），草稿只能是泛泛的内容。可能是评测脚本的问题，**待确认** |
| `draft_move_plan` | n | n | n | n | n | Y | n | n | 回答以一串问题结尾，而不是说明假设；两个周末之间的工作日没有安排 |
| `group_wiki_decision` | Y | Y | Y | Y | n | n | n | n | 把 Bot 之间的分工话语露给了用户 |
| `remind_dentist` | n | Y | n | n | n | n | n | n | 时间给对了（10-28 早上），但没写时区 |

H = Haiku，S = Sonnet，O = Opus。

### 5.4 专项小评测

| 结果目录 | 时间（PDT） | 版本 | 内容 | Haiku | Sonnet | 失败点 |
|---|---|---|---|---|---|---|
| `prod-lookup-*` | 10-19 19:38 | 按时间推断为 rc.21 | 名字查询（VisionQuest、トラファルガー・ロー），一对一和群聊各测 | 2/4 | 2/4 | |
| `prod-lookup2-*` | 10-20 09:20 | 按时间推断为 rc.26 | 同上 | 3/4 | 2/4 | 一对一的 VisionQuest：Haiku 没去查，把它当成了 Vision Pro 的应用；Sonnet 不确定是哪个，反问“你指哪个？”。群聊里 Sonnet 凭记忆答成了 1985 年的同名电影 |
| `prod-resourceful-*` | 10-20 11:03–11:07 | 按时间推断为 rc.27 | 想办法完成任务（找配图地址；发布文件并给出链接） | 1/2 | 1/2 | Haiku 没把办法试完就交还给用户；Sonnet 只看了搜索结果的标题就写摘要，没有打开页面 |

`smoke1`、`remote-proof`、`remote-proof2`、`local-regress`、`fix-proof` 是检查评测脚本本身的小规模运行（1–3 题），不算产品成绩。
