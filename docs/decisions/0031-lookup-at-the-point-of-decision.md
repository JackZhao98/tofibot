# 0031 查证原则放到决策点，并让 Bot 能读网页

> 编号 D31，索引见 [README](README.md)。

## 决定

1. **Anthropic 模型多一个服务端读网页工具**（`web_fetch`，类型 `web_fetch_20250910`），与 `web_search` 一起在每次模型请求里声明，搜索排第一、读网页第二、Bot 自己的工具在后。它和联网搜索共用同一个开关（设置 › 自动行为 › 联网搜索，注册表键 `web_search`），页面上只改了这一项的说明文字，没有新增设置项。
2. **每读一个网页，在 run 的证据里记一条 `web_fetch` 工具调用**（网址、标题、读取时间，失败则记错误码），和 `web_search` 的做法一致。费用记进 `usage_calls`，`kind = web_fetch`，读一页记一行，金额为 0：提供方不对读网页单独收费，页面文本按普通输入 token 计在读它的那次请求里。每次请求最多读 3 页，每页最多 10000 个 token 的文本。
3. **账户没开读网页，就退回到不带它，并记住。** 提供方返回 400/403 且报错点名 `web_fetch` 时，同一请求去掉读网页重发一次，并按凭证记住，之后不再声明；记忆和 `web_search` 的各记各的，搜索被拒不会连带读网页。
4. **查证原则放在做决定的那一步**，并且并入已有的那一行，不另起一条：
   - 共享指引 `conversationWorkGuidance` 里原来的查询句改写为：人提到的作品、产品、人或事件，先用搜索确认身份和现状再回答，名字看着眼熟也一样；说成“已核实/最新”的结论，以及说成有出处的数字或归属，要落在本次 run 里打开过的页面上（搜索结果只是线索），否则说明是凭记忆。
   - `say` 工具说明（`sayToolDescription`）里同样一句，写在 `purpose=answer` 的语义旁边。
   - 紧贴请求的那一行：原来只有未被 @ 的群聊消息有 `groupLatestMessageLine`，现在查证句抽成 `lookupNoteLine`，私聊和被 @ 的群聊也在请求旁边带上同一句（定时任务不带）。
   - 服务端工具（`web_search`、`web_fetch`）的描述由 Anthropic 定义，请求里不能改，所以“在工具描述里写原则”这一条只能落在 `say` 上。
5. **提示词预算上调**：普通私聊 6750 → 6920，结构化私聊 6700 → 6870，固定部分 7450 → 7620（都是 +170）。理由：这是把原有的一行改写，不是新增一行；实测增长 171 个字符。请求旁边多了一句之后，`TestContextBudgetKeepsOriginalSearchable` 超出总预算 12 个字符（总计 48012 > 48000），所以历史预算改为 `maxHistoryRunes - handoffReserve - requestNoteReserve`，给请求旁边的说明留 600 个字符。

## 日期

2026-10-21（太平洋时间）。

## 谁拍板

协调者按 owner 的“先做到 $199/年：好用、稳健”标准派的工单；owner 本人没有就这一条说过话（原话待确认）。属于需要 owner 事后确认的实现决定。

## 背景

生产 rc.29、Sonnet 做 Bot 的任务评测（`prod-sonnet-r4`、`prod-lookup3-sonnet`、`prod-resourceful2-sonnet`）暴露两件事：

- “VisionQuest好看吗”（私聊和群里 @）：Sonnet 凭记忆答成 1985 年的电影 *Vision Quest*，没有查。要找的是 2026 年的漫威剧《VisionQuest》。
- `lookup_compare_cities` 只跑了一次搜索，结果里只有标题和链接，却把具体人口数字说成有出处；`own_link_recall` 里的归属不在读到的文本里。

## 理由

- 搜索结果是线索，不是读过的页面；有出处的具体数字应该落在读过的页面上。读网页的工具补上这一步，同时也能读人发来的链接。
- 原则只写在系统提示词里，模型认为自己认识的名字会绕过去；写在做决定的地方（`say` 和请求旁边）效果更好（`docs/agent-plan/structured-output.md:287`：同一句话放在系统文本末尾 45%，放在请求旁边 77–88%）。

## 影响

实施状态：已实现，在分支 `fix/verification-rigor`，未合并、未发版。

本机实测（真实网络，Sonnet 5.5 做 Bot，Opus 5.5 评分，每项 3 轮，全新 Bot）。“前”是 `main` 在 `5f4396f` 的二进制，“后”是本分支：

| 指标 | 前 | 后 |
|---|---|---|
| 先搜再答（`dm_title`、`group_unaddressed_title`、`group_mention_title`，9 次） | 6/9（被 @ 的群里 0/3） | 9/9 |
| VisionQuest 任务识别正确（私聊 3、群 3，三条标准都满足） | 6/6 | 6/6 |
| VisionQuest 任务先搜再答 | 6/6 | 6/6 |
| 小闲聊上不必要的搜索（15 次） | 0 | 0 |
| 评分器标 `wrong_or_unverified`（`lookup_compare_cities`、`research_requests_maintained`、`research_bun_node`，9 次） | 5/9 | 5/9（没有改善；`lookup_compare_cities` 前后都是 3/3） |
| 每个探针估算费用 | 约 $0.078 | 约 $0.084 |

要点和限制：

- **本机上“前”的版本在 VisionQuest 任务里就已经 6/6 先搜**，和生产的 0 次不同。同一份提示词和代码，说明生产没搜很可能不是模型“没想到”，而是生产的凭证没有联网搜索（被拒并按凭证记住，见 `anthropic.go` 的 `anthropicWebSearchRefused`），模型根本没有这个工具。**待在生产主机日志里查 `[anthropic] web search refused`**；本任务不碰服务器，未核实。
- 变化最大的是被 @ 的群聊（0/3 → 3/3）。只改系统提示词和 `say` 说明（不加请求旁的一行）已经到 3/3，再加请求旁的一行也是 3/3，样本太小，分不出两者差别。
- 读网页工具在所有搜索类任务里**一次都没被模型主动用**（原则只靠文字的几个版本，56 个用了搜索的 run 里 0 次）；它在人明确要求打开某个网址时会被用，并出现在证据里。
- 评分器只能看到证据包里的网址和标题，看不到页面文本，所以“读了页面”并不会让 `wrong_or_unverified` 变少。这一项数字的升降在 n=9 时基本是噪声；换过几版措辞，在 3/9 到 6/9 之间摆动。所以**第 (b) 条的第二半（有出处的数字要读过页面）只靠文字没有做到**，要做到需要更强的结构性办法（见下）和一个能看到页面文本的评分器。

试过没上线的做法（`fix/verification-rigor-lookup-review` 分支）：run 搜索过但没打开任何页面时，在回答前加一次性的私下提醒（由工具事件触发，不匹配文本）。结果：读网页在全部搜索类 run 里出现（0 → 100%），但输入 token 变成 2.5–3 倍（任务每次估算 $0.08 → $0.30），评分器的 `wrong_or_unverified` 没有改善（VisionQuest 任务 2/12 → 5/12，因为读到的文本不在证据里，数字更具体了反而更难核对），还出现回答里提到“我之前给的数字”（被退回的草稿用户没看过）。没有合进主线；是否值得为它付 2.5–3 倍费用，需要 owner 定。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 提供方 | `internal/provider/anthropic.go`（`anthropicWebFetchType`、`attachWebFetchResult`、`anthropicWebFetchRefused`、`isAnthropicWebFetchRejection`），`internal/provider/provider.go`（`WebFetchSpec`、`WebFetch`） |
| 引擎和证据 | `internal/agent/agent.go`（`WebFetchMaxUses`、`webFetchMaxContentTokens`），`internal/runtime/runtime.go`、`trace.go`（`webFetchToolEvent`） |
| 开关和记账 | `internal/app/web_search.go`（`webFetchMaxUses`），`internal/app/automation_settings.go`（`fetchUses`），`internal/app/usage.go`（`usageKindFetch`、`recordWebFetchUsage`），`internal/app/app.go` |
| 提示词 | `internal/app/context.go`（`conversationWorkGuidance`、`groupLatestMessageLine`、`lookupNoteLine`），`internal/app/say.go`（`sayToolDescription`） |
| 测试 | `internal/provider/anthropic_webfetch_test.go`，`internal/runtime/web_fetch_test.go`，`internal/app/web_fetch_test.go`，`internal/app/verification_guidance_test.go`（`TestLookupRuleSitsBesideTheRequestInEveryStructuredTurn`），`internal/app/prompt_budget_test.go` |
| 界面 | `ui/src/locales/*/settings.json`（`web_search.what`，只改说明文字） |
| 相关决定 | [D09](0009-reliability-principles.md)、[D15](0015-automatic-behaviour-settings.md)、[D01](0001-persona-generator-no-examples.md) |
