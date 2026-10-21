# 0014 模型分工：对话用 Sonnet，后台轻活用 Haiku

> 编号 D14，索引见 [README](README.md)。

## 决定

反转之前的模型分工：

- 对话（以及所有需要自主执行的工作：工具调用、定时任务）默认用 Sonnet。
- 后台的轻活（摘要、分诊、审查等）用 Haiku。
- 一次运行中途不按触发条件切换模型。

## 日期

2026-10-20（Pacific）。合并于 10-20 10:36（`24af5a1`），首次进入 `v0.1.4-rc.27`。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 之前默认模型是 Haiku 5.5（`docs/agent-plan/structured-output.md` 第 1 节："The model (Haiku 5.5 by default)"）。
- Haiku 在结构化输出上明显弱于 Sonnet：第 3 阶段后整体 `say` 遵守率 Haiku 86-88%、Sonnet 95-97%；群里的辩论/委派轮次 Haiku 只有 33-50%（方案第 12 节）。任务完成评测里两者完成数接近（rc.21 都是 11 条，rc.26 Haiku 10、Sonnet 11；出处为上下文文件）。

## 理由

- 用户直接面对的是对话质量和任务完成度，这里用更稳的 Sonnet。
- 后台工作对质量要求低、调用多，用便宜的 Haiku。
- 运行中途换模型会让同一个任务前后表现不一致，也让问题难以复现，所以不做。

## 影响

- 默认值（`internal/app/automation_settings.go:47-55`）：`defaultConversationModel = "claude-sonnet-5-5"`（对话、定时运行、工具使用）；`anthropicBackgroundModel = "claude-haiku-5-5"`（对话默认是 Anthropic 模型时的后台默认）；只有 Codex 登录的工作区用 `codex-gpt-5.6-luna`。
- 后台模型的选择（`backgroundDefaultModel`，`automation_settings.go:298`）：对话默认是 Anthropic 时用 Haiku；Codex 工作区的审查用 `codex-auto-review`；其他情况用对话默认模型本身。环境变量 `TOFI_BACKGROUND_MODEL` 优先。
- 后台用途目前在代码里有两类（`internal/app/providers.go:35-36`）：`triage`（分诊，`internal/app/collaboration.go:938`）和 `review`（MCP 自动审查，`internal/app/mcp_auto_review.go:537`）。决定里说的"摘要、gist"：群对话摘要（[0006](0006-closed-group-exchange-gist.md)）不调用模型，是模板拼的；其他摘要类工作（比如上下文压缩）用哪个模型，待确认。
- 两个默认都能在"自动行为"设置里改（见 [0015](0015-automatic-behaviour-settings.md)），键 `conversation_model`、`background_model`（`automation_settings.go:82-83`）。Bot 自己指定的模型优先于两个默认（测试 `TestBotModelOverrideBeatsBothDefaults`）。
- 改默认只影响下一次运行，不影响正在跑的运行（`automation_settings.go:28-30` 注释 "a change applies to the next run without a restart"；测试 `TestFollowingBotRunSendsTheConversationModel`）。"运行中途不按触发条件切换"在代码里没有专门的测试，待确认。
- 评测和开发流程里的模型选择见 [0012](0012-no-opus-unless-needed.md)。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 默认值 | `internal/app/automation_settings.go:47-55`、`:82-83`、`:298`（`backgroundDefaultModel`） |
| 路由 | `internal/app/providers.go:33-36`、`:205`（`backgroundModel`）；`internal/app/model_settings.go` |
| 后台调用点 | `internal/app/collaboration.go:938`（triage）、`internal/app/mcp_auto_review.go:537`（review） |
| 测试 | `internal/app/automation_settings_test.go`：`TestConversationRunsUseTheConversationDefault`、`TestFollowingBotRunSendsTheConversationModel`、`TestBackgroundHelpersUseTheBackgroundDefault`、`TestBotModelOverrideBeatsBothDefaults`、`TestBackgroundDefaultIsTheConversationModelOffAnthropic`、`TestBackgroundModelEnvironmentWins`、`TestCodexOnlyWorkspaceFallsBackToCodexModel` |
| 合并提交 | `24af5a1` feat/automation-settings（内含 `ef59d81` Automatic behavior settings page, conversation/background model split, live retry count） |
