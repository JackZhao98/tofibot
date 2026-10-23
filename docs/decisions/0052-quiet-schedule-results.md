# 0052 定时任务可以安静完成：结论入记录，不发聊天消息；工单显示结果，后续轮次看得到

> 编号 D52，索引见 [README](README.md)。

## 决定

1. **发不发，跟着任务自己的指令。** 默认定时结果照旧发进聊天。任务指令写明“有某条件才通知”、而条件不成立时，这一轮安静完成：把结论记下来，不发聊天消息。
2. **协议里的最小改动：`say(answer)` 在定时运行里多一个可选布尔 `quiet`。** 带 `quiet=true` 时，答案文字和来源照常写进 `run_say_answers`（同样的回执语义，运行状态 `done`，完成评审与 D43/D50 评分不变），只是不发布成聊天消息。`quiet` 只在定时运行的 schema 里出现；其他运行用它被拒绝，稳定错误码 `quiet_not_available`（`tooloutcome.Validation`，下一步 `repair_arguments`），不记录任何东西。
3. **`runs.final_origin` 新增 `quiet`**：定时答案作为运行结论入记录、没有发布。与 `say`（发了）、`none`（什么都没发、没有答案）区分开，统计时不会把“安静完成”和“失败无答案”混在一起。
4. **工单显示结果。** 聊天里带时钟和时间的定时任务工单，完成后外观变化：`done`（已发）绿色实心对勾、`quiet`（安静完成）绿色空心圈、`failed` 红色“!”，三者互不相同；点开工单，除原有的“指令”外，多一块“结论”：状态标签、完成时间、结论文字（Markdown）、来源。失败的仍显示具名失败原因（`status_error`）。
5. **后续轮次看得到。** 每个用户可见对话的每一轮，在运行上下文备注里多一块 `[recent scheduled results from records…]`：本对话各定时任务最近的几次运行（每个任务最多 3 次、最多 6 个任务）、标题、时间、状态、以及安静或已发的结论文字（每条 ≤600 字符，总计 ≤2400）。标明是记录数据、不是指令。已发的结果若原消息还逐字在历史窗口里，只列一行“posted in the chat above”，不重复文字；被压成要点的旧回复或窗口外的，仍带文字。
6. **占用预算**：定时结果指引多一句，`prompt_budget_test.go` 的固定开销上限 8620 → 8843（+223，最长的是定时子运行）。上下文备注块在没有定时任务的对话里为空；有时最多约 3000 字符，计在备注里（历史预留 600 之外），不挤历史窗口。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner。2026-10-22 对线上问题“watch stock: morning check”的决定：发消息跟着指令走；工单要显示结果并区分失败与安静完成；后续轮次要能看到定时运行的结果。具体字段名（`quiet`、`result_*`）与上下文上限是实现决定，待 owner 确认。

## owner 的话

线上问题：每天 09:00 的任务，指令是“只有补货才通知”。09:00 查了没货、保持安静；11:30 用户问这次检查结果，Bot 答“没有可验证的执行结果”，又自己重新查了一遍。根因（只读追查）：定时运行只能靠 `say(answer)` 完成、而它必发消息，没有答案就判失败；后续私聊轮次看不到定时运行的任何结果；占用 API 也没有结果文字。

## Anthropic 参照

Claude Code 的后台任务（background tasks）：结果总会回到对话、给模型看，即使没有对用户宣布。这里对应第 5 条：安静完成的结论进记录，并进后续轮次的上下文；是否对用户宣布（发消息）是另一件事，由任务指令决定。

## 偏差

- Claude Code 的后台任务结果是对话流里的一条消息；这里不往对话里塞消息（那就是要避免的“发了”），而是备注块，来自记录表，不改历史。
- 备注块不进历史窗口预留；没有定时任务的对话零开销。
- 定时运行的 `say(answer)` 文字不再流式显示（`holdAnswers`）：是否安静由可能跟在文字后面的 `quiet` 字段决定，所以整条答案在工具执行时才一次出现。定时运行没人盯着，影响很小。
- 旧的 `complete_scheduled_task` 回执路径（结构化输出关闭时）不支持安静，维持原样。
- 没有真模型实测 `quiet` 的使用率；只有合成测试。

## 影响

- 后端：`internal/app/say.go`（`quiet` 字段与列、schema、拒绝码、`holdAnswers`）、`say_stream.go`、`app.go`（完成路径 `quietDone`）、`final_origin.go`（`quiet`）、`schedule.go`（占用 API 新字段 `result_text`、`result_sources`、`result_posted`、`completed_at`，只给对话所有者读，与原占用 API 同权限）、`schedule_state_note.go`（`scheduleResultsNote`）、`context.go`（提示词与接入）。
- 前端：`ScheduledRun.tsx`、`ScheduledTaskRow.tsx`、`scheduled-run.css`、`scheduleOccurrences.ts`、七种语言的 `schedules.json`、catalog 两个新状态（`scheduled-done-posted`、`scheduled-done-quiet`）。
- 提示词原文：`sayPrincipleLineScheduled` 与 `scheduledResultGuidanceStructured`（`context.go`）、`sayQuietDescription`（`say.go`）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 测试（Go） | `internal/app/schedule_quiet_result_test.go`：`TestQuietScheduledAnswerCompletesWithoutAChatMessage`、`TestDefaultScheduledAnswerIsStillPosted`、`TestOccurrenceAPIReturnsTheQuietResult`、`TestQuietIsRefusedOutsideScheduledRuns`、`TestQuietAnswerIsStillGradedByTheAnswerGate`、`TestLaterTurnContextCarriesQuietAndSkipsPostedResultsInTheWindow`、`TestScheduleResultsBlockIsAbsentWithoutSchedulesAndBounded` |
| 测试（UI） | `ui/scripts/test-schedule-occurrences.mjs`、`test:catalog`（两个新状态） |
| 相关决定 | [D04](0004-structured-output-say.md)、[D43](0043-answer-gate.md)、[D44](0044-recall-context-memory-and-search.md)、[D50](0050-acknowledge-then-work.md)、[D51](0051-notebook-brutal-style.md) |

## 补记（2026-10-22，太平洋时间）：评分安静记录、拒绝不可见文字

线上 rc.35 的“watch stock”定时任务（指令：只有补货才通知）暴露三处缺陷，都是 D43 回答闸与本决定相撞：

1. **评分员不知道这是私下记录。** `say(answer, quiet:true)` 的结论“两款均无货，未发提醒”被当作发给人的回复，按“没补货别发消息”判 `answer_revise`。现在评分员的输入多一节“投递方式”：`posted`（人在聊天里读到）或 `quiet`（私下的完成记录，不发给人）；评分标准（`answer_rubric.md`）开头加一段原则：安静记录按记录评分，写明查了什么、结果对照任务自己的条件即满足所有条目，是否对人说话、任务是否要求安静属于投递方式，不在评分范围；C3 补一条：如实陈述工作产出的结果或确实存在的阻碍不算过早放弃（D50 的纯文字结尾路径以 `posted` 评分，同样受益）。评分员的系统文字只多一句“还能看到回复的投递方式”。
2. **Bot 为了“保持安静”写出不可见文字**（rc.34 发出一条零宽空格消息；rc.35 先写零宽空格、再写 NUL）。`say` 现在对去掉空白、格式字符（Cf：U+200B–U+200D、U+2060、U+FEFF 等）和控制字符（Cc，含 NUL）后为空的文字一律拒绝，稳定码 `say_text_empty`，`tooloutcome.Validation`，下一步 `repair_arguments`，计入同一操作的修复次数（3 次后 `repair_budget_exhausted`）；对 answer、message、note 都适用；存入的文字去掉 NUL。拒绝发生在回答闸之前，所以闸的轮数用完后也一样。
3. **轮数用完后不再无条件收草稿。** 仍保持“上限之后最新的一稿直接发出、不再评分”，但那一稿必须先过上面的可见性校验；定时运行的所有答案草稿都不可见时，运行以 `say_text_empty:` 开头的错误失败，不记录空结论。安静想表达“没什么可报”，用 `quiet=true` 加一句写明查了什么与结果的记录。

测试：`internal/app/answer_quiet_gate_test.go`（`TestQuietRecordIsGradedAsAPrivateRecord`、`TestPostedReplyCarriesPostedModeToTheGrader`、`TestSayTextVisible`、`TestSayRefusesInvisibleTextForAnswerMessageAndNote`、`TestInvisibleDraftAfterTheRoundCapIsRefusedAndTheRunFails`、`TestValidDraftAfterTheRoundCapStillShips`）。
