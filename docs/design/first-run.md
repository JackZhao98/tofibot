# 首次使用：60 秒内知道能把什么交给 TOFI

> 写于 2026-10-22 PDT。基线 `main` @ `256d4b9`（rc.34 待打标签）。设计稿，**没有代码落地**；每条现状都带 `file:line`，没核实的标 **待确认**。
> 起因：`docs/product/next-plan.md` §7 的对标表里“首次使用”一行——“只讲连模型、建 Bot，不展示能交什么”；owner 的最高优先级是自托管 + Web。
> 规则：建议只从 [`docs/product/trust.md`](../product/trust.md) 的 **放心交** 档来；“看一眼”“先别”两档一个字都不进首屏。提示词只写原则不写例句（[D01](../decisions/0001-persona-generator-no-examples.md)）；普通聊天不许变慢（[D02](../decisions/0002-measure-interaction-cost.md)）；每个自动行为有设置（[D15](../decisions/0015-automatic-behaviour-settings.md)）。
> 审批边界那一句本页只放占位，归 `docs/design/approval-boundary.md`（待写）所有。

## 0. 一句话

**60 秒不是加屏，是换第 45–60 秒屏上的内容。** 今天新用户在第 60 秒看到的是 Bot 的两个反问（“叫我什么？帮你做什么？”），他答不上来，因为没人告诉他 TOFI 会做什么。目标是：同一条私信里，Bot 只问一件事，旁边摆着三件**评测里从没失手**的活，点一下就能交出去；Bot 的第一次回复以“现在就能交给我的事”收尾，由模型按它的职责和那份清单生成。

## 1. 新用户现在的 60 秒（逐屏）

计时从安装器给的地址第一次登录算起。时间是估的（**待确认**：没有录过真实用户），其中连模型那一步的 30–40 秒是硬成本（ChatGPT 设备码登录要换窗口）。

| 秒 | 屏 | 用户看到什么 | 他得到的关于“能交什么”的信息 |
|---|---|---|---|
| 0–5 | 登录页 | 用户名密码 | 无 |
| 5–12 | **欢迎**（`ui/src/Onboarding.tsx:97` `Welcome`） | 三只猫挥手，“欢迎来到 Tofi”，“先准备好一个 Bot，用来聊天。大约两分钟，之后所有设置都能改。”按钮“开始搭建你的团队”/“先跳过” | **无**。文案只讲“聊天”和“团队”，一个任务都没提 |
| 12–50 | **连接模型**（`ModelStep`，`:141`） | “用 ChatGPT 登录（最省事）”或“使用 API key”；登录后翻成“连接成功 · 你的第一个 Bot 已经就绪，正在等你”，默认模型和思考强度两枚 chip，折叠的“高级” | 无。服务端此刻已建好第一个 Bot（`api.firstBot`，`ui/src/api.ts:75` → `internal/app/onboarding.go:99` `createOnboardingBot`），名叫 “New Bot”，私信里已塞了开场白 |
| 50–58 | **挑几个要连接的服务**（`ui/src/OnboardingServices.tsx`） | GitHub / Notion / Linear / Robinhood / Context7 / Microsoft Learn 六块瓷砖；lede：“你的 Bot 可以自己读取这些服务，**改动任何内容前都会先问你**。”按钮“稍后再说，开始聊天” | 瓷砖下的一句用途（“Issue 和代码”）是服务的用途，不是任务。**lede 那句是超额承诺**：按 `trust.md`“逐条核实”表，MCP 写操作可被服务方只读标注、审核模型放行、“始终允许”三处绕过，不是硬保证 |
| 58–60+ | **第一个 Bot 的私信** | Bot 的开场白（`onboarding.go:25` `onboardingWelcomes`）：“你好！先问一句：你想叫我什么？不想费心的话说「你来取」，我自己挑一个。另外，你希望我帮你做什么？” 输入框空着 | **两个开放问题，零提示**。他要自己发明一个职责，而他还不知道这东西会不会跑脚本、会不会定提醒、会不会碰他的文件 |

跳过路径（“先跳过”）更差：主区是 `FirstRunHero`（`ui/src/FirstRunHero.tsx:32`）：“创建你的第一个 Bot · 每个 Bot 都有唯一私信。把多个 Bot 放进群里，让它们围绕真实消息协作。”——讲的是架构，不是能干什么。侧栏空态：“发条消息，或者交给它一件事。”（`empty.start_chat_body`）——“一件事”是哪件，没说。

用量/成本：整个流程里一个字没有；要到 设置 › 用量 才看到“API 参考等值”（`ui/src/locales/zh-CN/settings.json` `usage.*`）。

结论：今天的 60 秒里，“能交什么”的信息量是零，而且第 50 秒那句承诺比代码实际能保证的多。

## 2. 目标 60 秒（逐屏 mock）

原则：**不加屏、不加模型调用、不加常驻控件**（[P1](principles.md)）。三步还是三步；改四处文案、把服务步挪到任务之后（拍板题 Q2）、在私信里加一组“交给它”的建议条，Bot 的首次回复按原则收尾。

### 屏 1 · 欢迎（5–12 秒）

```
┌──────────────────────────────────────────────┐
│  ○ ● ○                            先跳过      │
│                                              │
│        (三只猫)   嗨!   欢迎   你好            │
│                                              │
│          欢迎来到 Tofi                        │
│   两分钟后你会有一个 Bot，和几件现在就能交给它的事。│
│   它跑在你自己的服务器上。                      │
│                                              │
│   🔒 {{approval_boundary_line}}   [开始 →]     │
└──────────────────────────────────────────────┘
```

- 改一句 lede（`onboarding.welcome_body`）：把结果说出来（“几件现在就能交给它的事”），抄 Muse 首屏“示例任务”的做法，但不在这里列任务——这里还不知道它有没有电脑、有没有模型。
- 左下角那一行沿用现有 `onb-lock` 的位置（今天是“登录信息只保存在你的服务器上”，`Onboarding.tsx:375`）放审批边界一句（§4，占位）。

### 屏 2 · 连接模型（12–50 秒，不动）

连接成功面板加一行计费口径（§5），其余不改：

```
┌──────────────────────────────────────────────┐
│  ● ● ○   第 2 步，共 3 步 · 已完成      稍后再说 │
│  连接成功                                     │
│  (猫)  ✓ ChatGPT                              │
│        你的第一个 Bot 已经就绪，正在等你。        │
│        [默认：GPT-5.5] [思考：中]               │
│        计费：ChatGPT 套餐 · 不另收费 · 用量在 设置›用量│
│  ▸ 高级 · 默认模型和思考强度                     │
│                                   [下一步 →]   │
└──────────────────────────────────────────────┘
```

### 屏 3 · 第一个 Bot 的私信（50–60 秒）

把“挑几个服务”从第 3 步挪到任务之后（Q2），第 3 步直接是私信，sheet 关闭：

```
┌─ 侧栏 ────────┬─ New Bot ──────────────────────────────────┐
│ New Bot   ●   │                                              │
│               │  (猫) 你好！你希望我帮你做什么？                 │
│               │       一句话就行。名字我可以自己取，你也可以给我起一个。│
│               │                                              │
│               │  现在就能交给我的事（评测里没失手过）：             │
│               │  ┌────────────────────┐ ┌──────────────────┐  │
│               │  │ 在我的电脑上写个小脚本 │ │ 做一页能打印的清单 │  │
│               │  │ 跑一遍，把输出给你    │ │                  │  │
│               │  └────────────────────┘ └──────────────────┘  │
│               │  ┌────────────────────┐ ┌──────────────────┐  │
│               │  │ 几分钟后提醒你一件事  │ │ 查一个开源项目    │  │
│               │  │                    │ │ 还有没有人维护    │  │
│               │  └────────────────────┘ └──────────────────┘  │
│               │                     这份清单怎么来的 ↗          │
│               │                                              │
│ ⚙ 本周 $0.00  │  ┌──────────────────────────────────┐ [发送] │
│   参考等值     │  │ 发条消息，或点上面任意一件…          │        │
└───────────────┴──────────────────────────────────────────────┘
```

- **开场白只问一件事**（职责），名字降为“我可以自己取”（Q3）。Grok Bot 的做法是“名字决定开场动作”，我们反过来：职责决定名字和开场；对新用户，“帮你做什么”比“叫什么”容易答，而且答完就有了人设生成器要的全部输入（`onboardingSystemPrompt` 已经允许 Bot 自己取名，`onboarding.go:18`）。
- **建议条**（`HandoverChips`，新组件）：2×2 四条，来源见 §3；点一下把那句话填进输入框（`composerDraft.ts` 的草稿路径），**由人按发送**——Bot 不替人发第一条消息，和 [D09](../decisions/0009-reliability-principles.md) 第 2 条同理。只在 `bot_onboarding.status='pending'` 且是账户第一个 Bot 时渲染（`onboarding.go:73` `onboardingPending`），发出第一条消息即消失；不是常驻控件。
- “这份清单怎么来的 ↗”指到 `trust.md`（自托管用户能读自己服务器上的文档；网页里是一个只读页，**待拍板**是否做成产品内页面，next-plan Q4 选项 C）。
- 侧栏账户行多一个读数（§5）。

### 第 60 秒之后（不计入，但决定首个任务能不能成）

| 秒 | 发生什么 |
|---|---|
| 用户点一条或自己打一句 | 第一条消息发出，Bot 第一次运行（也是今天就有的那次运行，**零新增模型调用**） |
| Bot 回复 | 按 `onboardingSystemPrompt` 取名、定人设、`set_bot_profile`；**新增一条原则**：收尾说出此刻就能交给它、在它职责内的两三件事（§3 第二来源）。如果用户点的是建议条，它就直接做那件事（指令即确认，`taskCompletionGuidance`，`context.go:220`） |
| 需要连接时 | Bot 用已有的接管/连接卡（[D11](../decisions/0011-handover-cards.md) `request_connection`）在任务里把服务拉进来，而不是开头推六块瓷砖。原第 3 步保留为侧栏 chip“连接服务”（`FinishSetupChip` 已有同形状，`App.tsx:2418`） |

## 3. 首个任务建议怎么来

两个来源，同一份数据，都不写任务文案进提示词。

### 3.1 数据：一份机器可读的“放心交”登记表

`trust.md` 是人读的；给产品一份它的机器版，由同一个脚本生成，**生成即版本化**：

```
eval/trust_tiers.json            （生成物，进仓库；tofibot-taskeval 的 trust_report.py --json 输出）
{
  "generated_at": "...", "source_sha": "82ff7e6", "rc": "rc.33",
  "tasks": [
    { "id": "computer_fibonacci",         "tier": "safe", "passes": 4, "runs": 4,
      "needs": ["computer"],              "touches": [] },
    { "id": "file_packing_checklist",     "tier": "safe", "passes": 4, "runs": 4,
      "needs": ["computer"],              "touches": ["own_files"] },
    { "id": "remind_stretch_3min",        "tier": "safe", "passes": 4, "runs": 4,
      "needs": ["schedules"],             "touches": [] },
    { "id": "research_requests_maintained","tier": "safe", "passes": 4, "runs": 4,
      "needs": ["web_search"],            "touches": [] },
    { "id": "no_question_units",          "tier": "safe", "passes": 4, "runs": 4,
      "needs": [],                        "touches": [], "not_for": ["claude-haiku-5-5"] },
    ...
  ]
}
```

- `tier` 只会是 `safe | glance | hold | insufficient`，按 `trust.md`“怎么分的档”的规则由脚本算，不手填。
- `needs` 是这条任务要的能力（`computer` / `schedules` / `web_search` / 某个连接），`touches` 是它会碰的东西（`own_files` = Bot 自己电脑上的文件；外发、人的文件、付钱这些在 safe 档里**本来就不存在**，这是 `trust.md` 那档的共同点）。
- `not_for`：旁证里失手的模型（Haiku 算错面粉那条），默认模型是它时这条不出现。
- 标签文案（“在我的电脑上写个小脚本…”）**不在 JSON 里**：按 `id` 落在 `ui/src/locales/<lang>/chat.json` 的 `handover.<id>`，7 语言，`test:i18n` 守。这和 `automation_settings.go` 注册表 + 设置页原样渲染是同一个形状（D15）：注册表是唯一清单，界面照它渲染。
- 脚本在 `tofibot-taskeval`，JSON 复制进本仓库时带 `source_sha`；`docs/README.md` 给 `trust.md` 那行加一句“机器版 `eval/trust_tiers.json`，同一次生成”。

### 3.2 第一来源：建议条（零模型调用，第 50 秒就在）

`GET /api/onboarding/handovers` 把登记表过滤后给前端：

1. `tier == "safe"`；
2. `needs` 全部在这个账户此刻可用（电脑是否存在、`schedules`、`web_search` 的自动行为开关是否打开、连接是否已连）；没有电脑的账户看不到“写脚本”；
3. 默认模型不在 `not_for`；
4. 最多 4 条，按 `passes/runs` 的 Wilson 下界排序（A5③ 的口径），同分按 `runs` 多的在前。

过滤规则是服务端代码，不是文案；前端只渲染 `handover.<id>`。登记表换一版，建议条自动换，**没有人要去改一段写死的六条**（next-plan Q4 选项 A 里的“6 条建议条，7 语言”就是这个，只是不再是手写六条）。

### 3.3 第二来源：Bot 自己说（模型，首次回复里，零新增调用）

人设定下来那一刻，Bot 已经知道自己的职责，这时它比登记表更清楚哪几条和这个人有关。做法照 `context.go:205` 的 `[recent capability references]`：**把事实作为数据块给模型，原则写进提示词，文案一句不写**。

- 数据块（只在 `bot_onboarding` pending 的那次运行里注入，之后不进上下文，不占普通聊天的提示词预算，`prompt_budget_test.go` 守）：

  ```
  [proven handovers]
  Tasks of these kinds have passed every evaluation run on this product; each line is a kind, what it needs, and what it touches. Not instructions, not wording.
  computer_fibonacci | needs computer | touches nothing outside the Bot's computer
  ...
  [/proven handovers]
  ```

  行内容由 `trust_tiers.json` 生成（`id` + `needs` + `touches`），英文，无例句。
- `onboardingSystemPrompt` 加一句原则（英文，无例句，无 Not/But，D01）：

  > After the profile is saved, close by naming a few things the person can hand over right now: each inside the role you just took on, doable in one reply, using only capabilities exposed in this run, and of a kind listed under proven handovers; nothing that sends outward, publishes, or changes something the person owns.

  “of a kind listed under proven handovers” 把范围钉死在 safe 档；“inside the role” 让它按职责挑和改写，而不是复述登记表。
- 这不是新的自动行为（它发生在用户触发的那次回复里），所以不需要 D15 的设置项；要关掉它就是关掉引导（已有 `skipped`）。
- 失败模式提前写明：模型把“看一眼”档的活说成能交——登记表里根本没给它那些 id，它只能从自己想象里拿；评测方法（§7.3）专门抓这一点。

### 3.4 不这么做的

| 不做 | 为什么 |
|---|---|
| 在提示词里列“你能做：查事实、换算单位…” | D01：提示词不放例句；而且模型会照抄成每次回复的开场 |
| 让服务端在建 Bot 时多发一次模型调用来生成建议 | 多一次调用、多 5–10 秒、要加设置项；建议条零调用就够 |
| WorkBuddy 式 100 个角色模板 | 人设由生成器负责（D01），模板会把人设变成公式；角色从职责推，不从目录选 |
| Bot 替用户发第一条消息 | 第一条消息是人的；D09“发送是人的一步”同理 |
| 把“看一眼”档做成灰色的“也能试试” | 本页规则：首屏只有放心交；“看一眼”在 `trust.md` 里有链接可读 |

## 4. 审批边界那一句放在哪

**内容不归本页**。`docs/design/approval-boundary.md`（待写）会定这句话的字和它背后哪些是硬闸；本页只定**位置**和**占位键**，落地时两份文档共用同一个 i18n 键，谁先合都不会出现两种说法。

- 键：`chat.json` `approval.boundary_line`（占位值先放 `trust.md`“能对外说的版本”第一句的缩写：“起草归它，发出去那一下归你；买、发、删、改账户这类动作它会先判断是不是你要的，不是就先问。”——**待 approval-boundary 定稿替换**）。
- 位置（一句话，不是段落）：
  1. 欢迎屏左下角的锁形行（§2 屏 1），替换今天“登录信息只保存在你的服务器上”？**不替换**，两行并列太挤——登录那句挪到连接模型屏（它本来就是说登录的，`Onboarding.tsx:375`），欢迎屏只剩边界这一句。
  2. 原第 3 步 lede（`onboarding.services_body`）“改动任何内容前都会先问你”**必须删**，换成同一键。这是本页唯一要求立刻改的文案：它今天就是超额承诺。
  3. 建议条区块**不放**这句：四条 safe 任务本来就不碰外发，再贴一句边界会让人以为它们需要审批。
- 这句话在哪都是链接：点开到 `trust.md`“它到底什么时候会先问你”那一节（ID 要么可点要么消失）。

## 5. 用量 / 成本在首屏怎么露

Muse 把用量表放首屏；我们不做表，做**一个读数和一句口径**，数据全是今天就有的：

| 处 | 显示 | 来源 | 读法 |
|---|---|---|---|
| 连接成功面板（屏 2） | ChatGPT 登录：“计费：ChatGPT 套餐 · 不另收费”；API key：“计费：按用量 · 参考等值见 设置 › 用量” | `via`（`Onboarding.tsx:144`）已知是哪种 | 静态文案，无请求。Codex 口径按 next-plan Q10 推荐 A（“订阅计费”不算 $） |
| 侧栏账户行（屏 3 起常驻） | “本周 $0.00 参考等值”，Codex 账户显示“订阅”；未报价模型显示“— ”不显示 0 | `GET /api/usage`，`UsagePeriod.EquivalentUSD` / `UnpricedRequests`（`internal/app/usage.go:66`） | 一次读（`conventions.md` §8：没东西在动就不轮询）；一次运行结束的事件（已有的 run 终态事件）后再读一次；不加新端点 |
| 第一次回复之后 | 不做“这次花了 $0.0x”的逐条提示 | — | 逐次请求已在 用量页 `usage.calls_title`；放到聊天里是多一个常驻元素（P1） |
| 预算上限 | **本页不做** | — | next-plan §7 已把“预算上限”单列进第 1 周；它是设置项（D15），不是首屏的事 |

读数点开直接进 设置 › 用量（`onUsage`，`App.tsx:2418` 已有）。

## 6. 对标表

| 事 | Grok Bot | WorkBuddy | Meta Muse | OpenClaw | TOFI 今天 | TOFI 目标 | 抄谁 |
|---|---|---|---|---|---|---|---|
| 引导顺序 | 起名 → 名字决定开场 → 模板 | 选角色 → 技能包 | 示例任务 + 用量表首屏 | 模型 → 频道 → 守护进程 → 面板 | 欢迎 → 模型 → 服务 → 私信两个反问 | 欢迎 → 模型 → 私信（一问 + 建议条）；服务改为任务里拉取 | 顺序抄 OpenClaw（先能跑再接频道）；服务后置抄我们自己的 D11 |
| 第一句问什么 | 名字 | 角色 | — | — | 名字 + 职责 | 只问职责，名字可自取 | Grok 反过来用 |
| 任务示例 | 模板 +“教它一个任务” | 技能包 | 首屏示例任务 | 无 | 无 | 建议条，从评测登记表过滤，只给放心交 | Muse；来源是我们自己的 trust.md |
| 示例的诚实度 | 产品宣称 | 产品宣称 | 产品宣称 | — | — | 每条可追到结果目录和版本 | 没人这么做；这是自托管用户会检查的东西 |
| 角色库 | 模板 | 100+ 专家角色 | — | — | 生成器 | 生成器（不变） | D01，不抄 WorkBuddy |
| 用量 | — | 账号计费 | 首屏用量表 | — | 设置页 | 连接成功一句口径 + 侧栏一个读数 | Muse，缩成一行 |
| 审批边界 | — | — | — | — | 第 3 步一句超额承诺 | 一句占位，两处，内容归 approval-boundary | 自己的 trust.md |
| 能装在自己服务器 | 否 | 否（私有化数十万起） | 否 | 是 | 是 | 是 | — |

**不抄**（沿用 next-plan §7）：公开技能市场、积分制、录屏教技能。

## 7. 落地步骤与验收

每步一个分支、一个验收命令、退出码为准（[D30](../decisions/0030-standing-operating-rules.md) 第 5 条）。量：S ≤ 半天，M ≤ 1 天。

| # | 做什么 | 改哪里（只许这些） | 验收 | 量 |
|---|---|---|---|---|
| 1 | `trust_report.py --json` 输出登记表；复制进仓库 | `tofibot-taskeval/eval/trust_report.py`；本仓库 `eval/trust_tiers.json`、`docs/README.md` 一行 | JSON 里每个 `id` 都能在 `trust.md` 找到同名条目；`tier` 按规则重算一致（脚本自带断言）；没有 n < 3 的 `safe` | S |
| 2 | `GET /api/onboarding/handovers` + 过滤 | 新 `internal/app/onboarding_handovers.go` + 测试；`app.go` 一行路由 | 测试：无电脑账户不含 `needs: computer` 的条；默认模型在 `not_for` 的条不出现；非 pending 账户返回空；撤掉过滤测试变红 | S |
| 3 | 建议条组件 + 7 语言标签 + 开场白只问一件事 | `ui/src/HandoverChips.tsx`、`chat.json` 七份（只加 `handover.*`、改 `onboarding.welcome_body`）、`onboarding.go:25` 七条开场白（CJK 守卫已豁免该表，`no_cjk_literals_test.go:24`） | `test:i18n`；`test:shell-budget`（组件懒加载，不进入口包）；`test:switch-budget` 不变；目录页加一个状态（`docs/design/components.md` 登记）；点条 → 输入框有字、未发送 | M |
| 4 | `[proven handovers]` 数据块 + 一句原则 | `internal/app/onboarding.go:18`、`context.go` 注入点及测试 | `TestOnboardingClosesWithProvenHandovers`：pending 运行的 system 含数据块且无例句；非 pending 运行不含；`prompt_budget_test.go` 绿 | S |
| 5 | 审批边界键 + 删超额承诺 + 登录句挪位 | `chat.json` 七份、`Onboarding.tsx`、`OnboardingServices.tsx` | `grep "先问你" ui/src/locales` 只剩 `approval.boundary_line`；`test:i18n` | S |
| 6 | 计费口径一句 + 侧栏读数 | `Onboarding.tsx`（连接成功面板）、`SidebarAccount`、`settings.json` 七份 | 读数只在挂载和 run 终态事件后请求（测试数请求次数）；Codex 显示“订阅”；`test:shell-budget` | S |
| 7 | 服务步后置（**拍板后才做**，Q2） | `onboardingFlow.ts`、`Onboarding.tsx`、`App.tsx` chip | `ONBOARDING_STEPS` 的测试改为新顺序；chip 文案；`test:onboarding`（若有，**待确认**脚本名） | S |

### 7.1 顺序

1 → 2 → 4 可以并行于 3 → 5 → 6；7 等拍板。全部合进一个 rc（[D22](../decisions/0022-preview-via-dev-remote.md) 攒一批），合前 owner 在 `dev:remote` 看屏 3。

### 7.2 不许变慢

- 建议条组件在 `lazyPanels.tsx` 登记，只在 pending 时取；普通聊天路径上不增加一个请求、一个常驻元素。
- 数据块只进 pending 运行；`prompt_budget_test.go` 的 system 预算不动。
- 回归：`test:stream-cost`、`test:switch-budget`、`test:shell-budget` 三个预算脚本和 `main` 同值。

### 7.3 新 Bot 实测的评测方法

两层，便宜的那层不花钱。

**A. 确定性（不调模型，CI 可跑）**
- 从新账户登录起，模拟连模型（本地 fake provider，`eval/run_eval.py` 已有的本地模式），计时到建议条可见：**≤ 60 秒**（减去模型登录那段用桩代替的时间后，剩余 UI 路径 **≤ 8 秒**）。
- 建议条每一条的 `id` 都在 `trust_tiers.json` 里 `tier == "safe"`，且 `needs ⊆` 账户能力。撤掉过滤，断言变红。

**B. 真模型（全新 Bot，[D13](../decisions/0013-test-with-fresh-bots.md)；Sonnet 当 Bot，[D12](../decisions/0012-no-opus-unless-needed.md)）**
- 在 rc 上线后用 `run_tasks.py --remote` 的 `make_fresh_world` 建 **5 个**新 Bot，各发一句职责（五种不同职责：写作、家务提醒、代码、调研、群公告），不点建议条。
- 判定（Sonnet 评分，k/N + Wilson 区间，A5③ 口径）：
  1. 首次回复**收尾**是否说出 ≥ 2 件可交的事；
  2. 每件是否能映射到 `trust_tiers.json` 的某个 `safe` id（评分员拿到登记表，判“同一类”）；**出现任何 glance/hold 类（发邮件、改线上、盯竞品页）即该 Bot 整题不通过**；
  3. 每件是否在它刚定的职责内；
  4. 没有例句照抄（五个 Bot 的收尾不得两两同句）。
- 门槛：5/5 通过第 2 条（这是诚实性，不容失手）；≥ 4/5 通过其余。
- owner 亲自读五条回复（[D03](../decisions/0003-test-live-immediately.md)）——像不像人话，评分员判不了。
- 预算：5 Bot × 2 轮 Sonnet，估 **< $1**；仍要 owner 点头（next-plan Q2 的预算口径）。
- 结果进 `status.md` §5 一行，登记表重生成一次。

### 7.4 怎么算做完

- 一个新账户，第 60 秒屏上有：一问、四条可点的放心交任务、一个用量读数、一句审批边界（占位）。
- `grep "都会先问你" ui/src/locales` 为 0。
- 7.3-A 在 CI 绿；7.3-B 的五条回复 owner 读过并记在 `status.md`。

## 8. owner 拍板题（推荐在前）

| # | 问题 | 选项 |
|---|---|---|
| **Q1** | 建议从哪来 | **A** 两层：登记表建议条（零调用）+ Bot 首次回复按原则收尾 / B 只要 Bot 说（省一个组件，但第 60 秒屏上仍是空的）/ C 只要建议条（不动提示词） |
| **Q2** | “挑几个服务”那一步 | **A** 后置：第 3 步直接进私信，服务由任务里的连接卡拉取，侧栏留 chip / B 保留为第 3 步，只换 lede / C 保留但默认折叠，一键“稍后” |
| **Q3** | 开场白问几件事 | **A** 一件（职责），名字可自取或随后给 / B 维持两件 |
| **Q4** | 用量读数放哪 | **A** 连接成功一句口径 + 侧栏账户行一个读数 / B 只加连接成功那一句 / C 不动，等预算上限一起做 |
| **Q5** | 审批边界那一句 | **A** 先上占位键（`trust.md` 可对外版缩句），approval-boundary 定稿后只换值 / B 等定稿一起上 / C 只删超额承诺，不加新句 |
| **Q6** | “这份清单怎么来的” | **A** 链接到仓库 `trust.md`（自托管用户看得到）/ B 做成产品内只读页（next-plan Q4-C）/ C 不放链接 |
| **Q7** | 7.3-B 的 $1 评测 | **A** rc 上线后跑一次 / B 并进 next-plan Q2 的每周门禁 / C 不跑，只要 A 层 |

## 相关代码与文档

- 现状：`ui/src/Onboarding.tsx`（`Welcome` :97、`ModelStep` :141、锁形行 :375）、`ui/src/OnboardingServices.tsx`、`ui/src/onboardingFlow.ts`、`ui/src/FirstRunHero.tsx:32`、`ui/src/App.tsx:2414-2420`；`internal/app/onboarding.go`（`onboardingSystemPrompt` :18、`onboardingWelcomes` :25、`onboardingPending` :73、`createOnboardingBot` :99、`botProfileInstructionsDescription` :175）、`internal/app/onboarding_state.go:109-111`；`internal/app/context.go:205`（数据块样式）、`:220`（`taskCompletionGuidance`）；`internal/app/usage.go:66`（`UsagePeriod`）；`internal/app/no_cjk_literals_test.go:24`
- 文档：[`../product/trust.md`](../product/trust.md)、[`../product/next-plan.md`](../product/next-plan.md) §3 Q4/Q10、§7；[`principles.md`](principles.md) P1/P5；[`new-page.md`](new-page.md)；[`../conventions.md`](../conventions.md) §8；[`../process.md`](../process.md) §5
- 决定：D01、D02、D03、D09、D11、D12、D13、D15、D22、D30
- 待写：`docs/design/approval-boundary.md`
