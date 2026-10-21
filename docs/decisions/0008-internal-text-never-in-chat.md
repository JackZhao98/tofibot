# 0008 内部文字永远不出现在聊天里

> 编号 D08，索引见 [README](README.md)。

## 决定

结构化运行中，Bot 在工具调用之间写的工作文字（内部文字），在任何情况下都不作为聊天消息显示，只能在"查看活动（View activity）"里看到。

## 日期

2026-10-19（Pacific）。合并于 10-19 11:53（`3b2cf35`）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里；问题与做法记录在 `docs/agent-plan/structured-output.md` 第 13 节后续 "working text is never a chat row (rc.14)"。

## 背景

- [0004](0004-structured-output-say.md) 第 2 阶段已经把工具间的纯文字变成内部文字，但当时存成 `progress` 类型的折叠消息。
- 界面只在有可见最终回答时才把 `progress` 折叠起来。一个运行如果没有最终回答（例如：领头先发了消息、又把活转出去，随后的状态回答被拒绝），它末尾的工作文字就露在了聊天里。rc.14 之前的评测里，12 次中有 2 次出现，其中一次是一整段辩论立场加"等结果来了我再发综合意见"（方案同一节）。

## 理由

- 内部文字是 Bot 给自己看的，不是对人说的话。是否显示不能取决于这次运行恰好有没有最终回答。
- 用一个独立的消息类型从根上区分，而不是在界面上靠条件折叠。

## 影响

- 新增消息类型 `internal`（`internal/app/progress_tag.go:34` `messageKindInternal`）。结构化运行的工作文字（工具间的纯文字、转交后没有 `say` 的文字）都存为 `internal`，不再是 `progress`（`internal/app/stream.go:256` 附近）。
- 界面：`foldCompletedProgress`（`ui/src/runProgress.ts:7`）在任何运行状态下都不把 `internal` 当聊天行；有最终回答时把它挂到回答的活动里。
- 服务端在未读计数、表情回应、任务状态、审计、导入中把 `internal` 当 `progress` 处理，并且不进会话预览（`internal/app/conversation_task_state.go`、`reactions.go`、`run_audit.go`、`portability_import.go`）。
- `say(note)` 仍是 `progress`；关掉开关时的 `<progress>` 便签不变。
- 转交并已发言之后的 `say(answer)` 被拒绝时，本轮直接结束（`EndTurn`），不再生成尾部文字。
- 实测（Haiku，辩论/委派/@ 探针各 3 次）：留下 progress 行却没有最终消息的情况，12 次中 2 次降到 0 次（方案同一节）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 方案 | `docs/agent-plan/structured-output.md` 第 13 节 "Follow-up: working text is never a chat row (rc.14)" |
| 消息类型 | `internal/app/progress_tag.go:28-34` |
| 写入 | `internal/app/stream.go`、`internal/app/app.go`、`internal/app/say.go` |
| 其他服务端处理 | `internal/app/conversation_task_state.go`、`internal/app/reactions.go`、`internal/app/run_audit.go`、`internal/app/portability_import.go` |
| 界面 | `ui/src/runProgress.ts:7`（`foldCompletedProgress`）、`ui/src/types.ts` |
| 测试 | `internal/app/internal_text_test.go`：`TestInternalTextAfterAHandoffIsNeverAChatKind`、`TestInternalAndProgressKindsStayDistinct`、`TestInternalRowsDoNotCountAsUnreadOrPreview`、`TestRefusedAnswerAfterAHandoffEndsTheTurn`；`ui/scripts/test-run-progress.mjs`（`npm --prefix ui run test:run-progress`） |
| 合并提交 | `3b2cf35` fix/internal-text-never-visible（内含 `57e68a1`） |
