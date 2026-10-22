# 0044 早先说过的事：记忆在说的时候存，历史被截断时留下摘要和"可搜索"的提示

> 编号 D44，索引见 [README](README.md)。D43 在分支 `feat/answer-gate`（未合并）。

## 决定

照 Anthropic 公开的做法补三处缺口，不自创机制，没有逐轮搜索，没有关键词触发：

1. **记忆在学到时就存。** `save_memory` 的工具说明写明何时用：学到关于这个人或他的世界的、以后会用到的持久信息（本人的事实、身边的人和动物和物、偏好、计划、决定）时，当场存，不等人开口，这样对话往后走、早先的轮次离开上下文后它还在。系统提示词加一条同方向的原则（`memoryAndRecallGuidance`）。
2. **记忆在每轮开头已经在上下文里**（`[memory data]` 块，`internal/app/context.go`），这就是 Anthropic 记忆工具"开工前先看一眼记忆目录"的效果，而且比让模型先调一次 view 更省一轮。所以不加"先 view"的原则，只在说明里告诉模型"已存的记忆列在每轮开头的上下文里"。
3. **历史被截断时，除摘要外再留一条提示**（`earlierTurnsNote`）：对话比这里给出的历史更长，早先的轮次不显示（摘要除外），`search_history` 能找到原文。只在确有早先轮次没进窗口（或有摘要覆盖了更早的轮次）时出现；短对话没有。窗口为它预留 400 个字符（`earlierNoteReserve`），总历史预算不变。
4. **`search_history` 的说明写明何时用、怎么用**：上下文只有近期轮次加较早轮次的摘要；人提到早先说过的、在可见对话、摘要和记忆里都看不到的事时，先搜再答或再说不知道；匹配的是字面文本（不区分大小写），用人可能用过的有区分度的词，没有结果换个说法。

> 后续（[D53](0053-view-context.md)）：上下文现在可以检查。每条回复的“查看活动”里有“上下文”，列出该运行第一次调用模型时实际收到的各部分（记忆块、摘要、历史窗口、早先轮次提示等）、大小和原文，窗口漏掉了多少条也会写明。

## 日期

2026-10-21（太平洋时间）。

## 谁拍板

协调者按 owner 的规则下的工单："以 Anthropic 为大哥 / 我们不要自创"；修复针对模型行为、提示词、流程和通用能力，不硬编码、不做关键词匹配、提示词只写原则、不让正常回复变慢（不逐轮搜索历史）。owner 没有就这一条的具体做法说过话；属于需要事后确认的实现决定。

## 背景

owner 的反馈：早先说的事实（例：猫吃什么）被挤出 Bot 的 32k 字符历史窗口，之后问起，Bot 泛泛而谈或说不知道，也不去搜。分支 `feat/answer-gate` 的评分器实验说明复核救不了：写的人和评的人都不知道那件事存在。

调查结果（改动前的 `main`，`f2cacb0`）：

| Anthropic 的做法 | TOFI 改动前 |
|---|---|
| 压缩：旧轮次换成摘要留在上下文 | **已有。** 摘要任务 `scheduleLongTermSummary` / `prepareLongTermContext`（`internal/app/memory.go`，每次 run 结束后后台跑，Haiku 级模型，日志 `[summary] queued/started/finished`）把最近约 12000 字符 / 80 条之外的历史增量写进 `summaries`；`buildContextPartsWith`（`context.go`）把它作为 `[derived history data]` 放进上下文，被摘要覆盖的消息只留 10 条重叠。摘要提示词要求保留"稳定事实、最新更正、偏好、决定、承诺、任务进展、未完事项"，上限 8000 字符。**缺口：** 摘要没有覆盖到的被截断消息（任务延迟、失败后 5 分钟冷却、旧 Bot 没有摘要行），模型完全不知道它们存在。 |
| 记忆工具：学到时存、开工前先看 | 记忆**已注入**每轮上下文（`[memory data]`，上限 8000 字符，放不下的记数并提示 `list_memory`）。**缺口：** `save_memory` 说明只讲格式，系统提示词里没有任何关于何时存的话，存不存全凭模型。 |
| 往期对话搜索：有工具，说明里写何时用 | `search_history` 在 `app.go`，说明只有一句 "search exact conversation history"；底层是 SQL `LIKE '%q%'` 字面子串匹配（`Store.Search`），说明里没写。 |
| 工具说明写清何时、为何用 | 同上，三处都没有。 |

## 理由

来源（已读原文核对，2026-10-21）：

- Memory tool 文档（platform.claude.com，agents-and-tools › tool-use › memory-tool）："Claude automatically checks its memory directory before starting a task. As it works, Claude stores what it learns in files under /memories"；API 自动加入的指令："ALWAYS VIEW YOUR MEMORY DIRECTORY BEFORE DOING ANYTHING ELSE … ASSUME INTERRUPTION: Your context window might be reset at any moment, so you risk losing any progress that is not recorded in your memory directory"。同页 "Using with compaction"："compaction keeps the active context small … and memory preserves the information that must survive summarization"，以及 "Memory supports just-in-time context retrieval … an agent records what it learns in memory files and reads them back on demand"。对应第 1、2 条。
- Compaction 文档（platform.claude.com，build-with-claude › compaction）："Compaction replaces the older turns of a conversation with a summary"，近期轮次可逐字保留；《Effective context engineering for AI agents》（anthropic.com/engineering）："Compaction is the practice of taking a conversation nearing the context window limit, summarizing its contents, and reinitiating a new context window with the summary"；agents 保留"lightweight identifiers"并在运行时用工具取回数据（just-in-time retrieval）。TOFI 的摘要已经是这个做法；第 3 条补的是"轻量引用"这一半：告诉模型有更早的内容、用哪个工具取。
- 《Writing effective tools for AI agents》（anthropic.com/engineering）："Think of how you would describe your tool to a new hire on your team … make it explicit"（专门的查询格式、隐含的上下文）。对应第 4 条，以及把"字面匹配"写进说明。
- Claude.ai 的往期对话搜索：帮助中心文章只描述用户怎么请求（"What did we discuss about [topic]?"），没有公开 Claude 何时自己决定搜索的规则，所以"何时搜"这条原则是照上面几篇的精神自己写的措辞，不是 Anthropic 的原话；这一点待确认，没有伪称出处。

## 影响

实施状态：已实现，在分支 `feat/recall-context`，未合并、未发版。

提示词预算（`TestRunPromptBudgetPreservesDMAndGroupPoliciesWithComputer -v` 在 `main` 与本分支上实测）：系统提示词的核心部分私聊 2813 → 3229，群聊 5098 → 5514（各 +416 字符，全部来自第 1 条的系统原则）。最终系统提示词在两个分支上都在 16000 的上限内（最长用户指令时都是 15630），所以 `maxSystemRunes`、`maxHistoryRunes` 没有上调；`TestProductionPromptSizeMatrix` 里三个按实测长度算的上限各上调恰好这个增量：普通私聊 7659 → 8075，结构化私聊 7609 → 8025，固定开销 8196 → 8612（均 +416）。系统预算里这 416 个字符在用户指令超长时从它的份额里出（多截 416）；历史预算不变，提示留 400 字符的预留从历史窗口里出（`historyLimit` 减 400，窗口 32000 → 31600 的有效量级，`TestContextBudgetKeepsOriginalSearchable` 等现有预算测试照常通过）。工具说明不在系统预算里。

本机实测（真实模型，Sonnet 5.5 做 Bot 与评分，每项 3 轮，全新 Bot，"前"=`main` 二进制，"后"=本分支）：

| 任务 | 情形 | 前 完成/错/查过 | 后 完成/错/查过 |
|---|---|---|---|
| `live_recall_name`（早先的轮次走产品：API 发送、Haiku 回复、摘要任务跑完） | 猫的事实在第 1 条，后面 5 条长消息 | 3/0/0 | 3/0/0 |
| `gate_recall_name`（直接写库的历史，无摘要无记忆） | 同一事实 | 0/2/0 | 0/0/0（仍然没完成：回复泛泛而谈） |
| `gate_recall_link` | 同上，找一个链接 | 3/0/3 | 3/0/3 |
| `gate_guard_direct` | 直接问题 | 3/0/0 | 3/0/0 |
| `gate_guard_smalltalk` | 闲聊 | 1/0/0 | 0/0/0（评分器判闲聊回复不合格 3 次，调用数同为 2.0，未见差别来源，样本小，待复核） |

走产品的流程里：记忆保存 3/3 → 3/3（Haiku 回复时本来就存了，不论有没有新说明），摘要在场 3/3 → 3/3，摘要里含事实 3/3 → 3/3。也就是说，**走产品的流程里 "前" 已经全对**，新说明没有可见增益；owner 描述的故障在没有记忆也没有摘要的旧会话里才出现，那种情形 "后" 仍没有修好（问题是"路上有什么该避开的"并没有字面提到猫，模型不认为需要回头找）。这个结论没有被解决，也没有再迭代（预算用完）。

首条可见回复时间（`answer_latency_s`，均值，秒）："前" `live_recall_name` 2.54、`gate_guard_direct` 1.66、`gate_guard_smalltalk` 1.86、`gate_recall_link` 5.09、`gate_recall_name` 4.22；"后" 2.93、2.17、2.27、4.89、5.24。差值在这些单次延迟噪声范围内（前后两批并行跑，机器负载相同，但样本只有 3，没有显著性），不能证明没变慢，也不能证明变慢；代码上没有新增任何每轮调用或搜索，只多了约 400 字符的提示和（在被截断时）一条 300 字符的提示。

估算花费：约 $8.0（`live_smoke` 0.50、`live_before` 1.46、`live_after` 1.48、`seeded_before` 2.28、`seeded_after` 2.29）。

未证明的：对 terra 默认模型的效果；`gate_recall_name` 型（没有字面提到早先事实）的失败；"何时搜"措辞的真实效果；闲聊守卫的 1→0 是否噪声。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 提示和预留 | `internal/app/context.go`（`earlierTurnsNote`、`earlierNoteReserve`、`memoryAndRecallGuidance`、`buildContextPartsWith`） |
| 工具说明 | `internal/app/app.go`（`save_memory`、`search_history`） |
| 摘要与记忆 | `internal/app/memory.go`（未改） |
| 测试 | `internal/app/recall_context_test.go`：`TestTrimmedHistoryKeepsSummaryAndPointsAtSearch`、`TestEarlierTurnsNoteOnlyWhenHistoryIsCutOff`、`TestMemoryAndRecallGuidanceIsInTheSystemPrompt`、`TestMemoryAndHistoryToolsSayWhenToUseThem`（回退对应改动都会失败，已逐项验证） |
| 相关决定 | [D06](0006-closed-group-exchange-gist.md)、[D14](0014-sonnet-conversation-haiku-background.md)、[D01](0001-persona-generator-no-examples.md) |

## 后续补充（D52，2026-10-22）

上下文里除 `[memory data]`、`[derived history data]` 外，每个用户可见对话的每一轮还带 `[recent scheduled results from records…]` 块（`scheduleResultsNote`，`schedule_state_note.go`）：本对话最近的定时运行及其结论文字，含安静完成（没发聊天消息）的结论。见 [D52](0052-quiet-schedule-results.md)。
