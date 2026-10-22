# 0050 先应一声再干活；定时任务的“卡住”结尾也过评分员

> 编号 D50，索引见 [README](README.md)。

## 决定

两件事，都是通用原则，不针对任何任务：

1. **有人在等的运行，先应一声再干活。** 在结构化模式、用户可见的会话里，由人发起且人在等的运行（私聊里人发的消息，群里人发的未点名消息），提示词在请求旁边加一条原则（`sayAcknowledgeLine`，和 `sayPrincipleLine` 在同一处）：请求需要工具工作时，先用 `say(purpose=message)` 以 Bot 自己的口吻简短应一声，再去做；做的过程中发现人会想知道的东西，随时用 `say(purpose=message)` 说出来；最后用 `say(purpose=answer)` 收尾；活做完了才说做完了。
   - 不加这一条的运行：定时任务（含群里的定时任务）、隐藏或内部会话、分诊、非结构化模式，以及委派类运行（`team`、`message`、`group_task`、`group_followup`、`group_mention`）。委派的交接已有自己的“先应一声”原则（`context.go` 的交接说明和群成员说明），原文不动。
   - 把两处“只在做完后才宣布”的措辞改成禁止“声称没做完的活”，不再读成禁止“应一声”：`say` 工具说明里的 “announce work only once it is done” 改为 “say that work is done only once it is done”；`taskCompletionGuidance` 里的 “Announce work only after doing it this turn” 改为 “Say that work is done only after doing it this turn”。[D45](0045-reply-matches-the-work.md) 的意图不变：回复要和做过的活对得上。
   - 每个运行最多 3 条 `say(message)` 的上限不变（`maxChatSegments`）：应一声占 1 条，剩 2 条给中途发现。
2. **定时任务以纯文字结尾（卡住了）时，这段文字也过评分员。** [D43](0043-answer-gate.md) 的评分员原来只评 `say(answer)`；定时任务卡住时按现有规则以纯文字结尾（`say(answer)` 会记成功），这一段从来没人评。现在在 `answerGateScope` 的运行里、评分轮数还没用完时，这段文字用同一个评分员、同一份评分标准评一次；有 gap，就把 gap 清单私下交还 Bot（沿用现有的“终稿复核”续跑机制，替换这一次的回执提醒），Bot 再来一轮，之后若用 `say(answer)` 交付，照常记成功；仍然卡住就照旧以纯文字结尾，不记成功。
   - 轮数与上限和 `say(answer)` 共用一个计数器（设置 `answer_gate`，默认 2，0 关闭）。到了上限、评分员失败或超时，运行照今天的样子结束（还是走原来的回执提醒）。
   - 评分员仍然看不到工具记录（D43 的规则），评分标准仍是通用的四条，没有加任何针对任务的标准。
   - 不评的：回执修复那一次尝试、非定时任务的纯文字、已经把活交给同事的运行、已经有回执的运行。
   - 这对应 owner 说的 Notion 日报那次：Bot 卡住时若被退回一次，多半就修好了。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner 的反馈（2026-10-22，原话大意）：让 Bot 做事时，应该像人一样先回一句简短的“好的，我去弄”，再去做；做的过程中发现值得说的东西要打字在聊天里说出来、跟他互动，不要闷头干；做完再汇报。另外，定时的 Notion 日报失败了一次，问“你不能修好吗”就修好了，正常的复核步骤退回一次大概就能修好。并重申：照 Anthropic 的做法、提示词只写原则（英文、无例句、无“不是……而是”、无关键词表、无强制话术）、不针对具体任务或测试写死、测试只用合成数据。具体措辞、适用范围和轮数是实现决定，待 owner 事后确认。

## 背景

查证过的现状（读代码，未上线实测）：

- 结构化模式下，工具调用之间的纯文字是私有的（存为 `internal` 类），人只能通过 `say` 看到：`say(message)`（最多 3 条，定时任务和不可见会话里不可用）、`say(note)`（折叠的瞬时进度）、`say(answer)`（收尾）。
- 但 `say` 说明里写着 “announce work only once it is done”，`taskCompletionGuidance` 里写着 “Announce work only after doing it this turn”。两句本意是 D45 的“不要声称没做的事”，读起来却像禁止先应一声，于是人发出请求后，Bot 会一直静默到收尾。
- 群里的交接已有“先应一声”的原则（`context.go` 的交接说明），私聊和群聊里人发的消息没有。
- D43 的评分员只评定时任务的 `say(answer)`。定时任务卡住后的纯文字结尾（`sayPrincipleLineScheduled`、`scheduledResultGuidanceStructured` 规定的结尾）从不评分，于是定时任务过早放弃的结果无人复核就交付了。

## 理由

Anthropic 的做法（照抄，不发明）：

- **应一声**：Claude Code 的提示词要求在第一次调用工具之前，先用一句话说明接下来要做什么，并在关键时刻给简短更新（发现了什么、改变了方向、遇到了阻碍）；更新短、说事实，不是每一步都播报。这里的 `sayAcknowledgeLine` 是同样的三件事：开工前一句、值得说的发现随时说、最后收尾。
- **未达标就带着缺口退回**：Managed Agents 的 outcomes（[Define outcomes](https://platform.claude.com/docs/en/managed-agents/define-outcomes)）里，评分员认为未满足就返回 `needs_revision` 和缺口说明，交给 agent 下一轮；诚实的失败报告并不能让“解决了请求”这一条通过。D43 已按此做了 `say(answer)`；卡住的纯文字结尾是同一个产物，不该被排除在外。
- 评分员上下文独立、不看过程、只看产物，沿用 D43。

## 与 Anthropic 做法的偏离（逐条）

1. **应一声用的是 `say(message)`，不是普通助手文字。** Claude Code 的助手文字直接显示；这里结构化模式下工具之间的纯文字是私有的（D04），所以走 `say`。
2. **这一条放在请求旁边（上下文的“当前提示”里），不放系统提示。** 系统提示要缓存稳定，且 D40 已经定了“规则放在决策点”。每轮多约 330 个字符；提示词预算测试量的是系统提示，没有为此调上限。
3. **只对“人发的消息”这两种运行加。** 委派类运行有自己的原则，定时和隐藏运行没人看着第一步。
4. **3 条 `say(message)` 的上限没动。** 应一声一条加两条发现，对一般任务够；若线上发现挡住了，再放宽，到时单独记。
5. **纯文字结尾只能被退回一次。** Agent 循环对“终稿复核”只给一次续跑名额（`finalResponseRepaired`），评分与回执提醒共用这一次。所以纯文字结尾最多评一轮，`answer_gate` 的其余轮数留给后面的 `say(answer)`；没有为此另造续跑机制，也没有改 Agent 循环。有 gap 时评分员的清单替换了这一次的回执提醒；评分通过或失败时回执提醒照旧。
6. **评分员的失败/超时发出原稿**，沿用 D43 的取舍。

## 影响

实施状态：已实现，在分支 `night/ack-and-blocker-review`，未合并、未发版。**只有合成测试，没有用真模型实测**：先应一声是否被各模型照做、评分员是否抓得到“过早放弃的卡住报告”，都要上线后用评测题看。

- 提示词：新增 `sayAcknowledgeLine` 和 `acknowledgesBeforeWork`（`internal/app/context.go`）；改两处措辞（`internal/app/say.go`、`internal/app/context.go`）。系统提示的固定开销多 8 个字符，`prompt_budget_test.go` 的 8612 上限提到 8620。
- 评分：`internal/app/app.go` 的定时任务终稿复核外包一层评分（共用 `gradedRounds`、`gateRounds`、`inReceiptRepair`）；`internal/app/answer_review.go` 加 `blockerRevisionTail`。
- 测试：`internal/app/blocker_ack_test.go`（纯文字结尾被评、被退回、再用 `say(answer)` 交付并记成功；卡住不变则不记成功；轮数上限与 `say(answer)` 共用；评分员失败时仍是原来的回执提醒；人发起的纯文字不评；应一声这一条在各种运行种类下出现与否）；`deliver_with_assumptions_test.go` 跟着改措辞。
- 文档：本文件、`README.md` 的索引、[D43](0043-answer-gate.md) 的范围、`docs/releases.md` 的未发布段。

## 相关代码与文档

[D04](0004-structured-output-say.md)、[D40](0040-lookup-at-the-point-of-decision.md)、[D43](0043-answer-gate.md)、[D45](0045-reply-matches-the-work.md)；`internal/agent/agent.go` 的 `BeforeFinalResponse`（一次续跑名额）。
