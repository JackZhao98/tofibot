# 0015 每个自动行为都有可见、可调的设置和出厂默认值

> 编号 D15，索引见 [README](README.md)。

## 决定

系统替用户自动做的每一件事，都要在设置里有一项看得见、改得了的开关或数值，并带一个出厂默认值。入口是"设置 › 自动行为"（英文界面 Automatic behavior）。

## 日期

2026-10-20（Pacific）。合并于 10-20 10:36（`24af5a1`），首次进入 `v0.1.4-rc.27`。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 这几天加了很多自动行为：结构化回复、Haiku 私聊强制调用工具、网页搜索、群对话摘要、完成审查、MCP 自动审查、重试次数等。合并前它们分散在代码常量、环境变量和几个旧设置入口里（注册表文件头注释提到默认模型、结构化回复、MCP 自动审查模式"live in older tables"，`internal/app/automation_settings.go:24-26`），没有一处能看全系统替用户做了哪些事。
- 合并前哪些项只能通过改代码或环境变量调整，逐项情况待确认。

## 理由

- 用户应该知道系统替他做了什么，并且能自己调整。
- 一个清单集中列出所有自动行为，开发者加新行为时也有固定的位置登记。

## 影响

- 注册表（`internal/app/automation_settings.go:82-94`）是唯一清单，设置页按它原样渲染（文件头注释 `:19-21`）。当前各项：

| 键 | 分组 | 类型 | 默认 | 环境变量覆盖 |
|---|---|---|---|---|
| `conversation_model` | models | model | `claude-sonnet-5-5` | — |
| `background_model` | models | model | 按对话模型推导（见 [0014](0014-sonnet-conversation-haiku-background.md)） | `TOFI_BACKGROUND_MODEL` |
| `structured_output` | replies | bool | true（见 [0004](0004-structured-output-say.md)） | — |
| `force_say_haiku_dm` | replies | bool | true | `TOFI_FORCE_SAY` |
| `web_search` | replies | bool | true | `TOFI_WEB_SEARCH` |
| `group_exchange_gists` | history | bool | true（见 [0006](0006-closed-group-exchange-gist.md)） | — |
| `own_replies_verbatim` | history | int 1-20 | 3（`internal/app/history_gist.go:7`） | — |
| `completion_review` | checks | bool | true | — |
| `mcp_auto_review` | checks | enum off/shadow/auto | off | — |
| `remembered_approval_hours` | checks | int 1-720 小时 | 24（`internal/app/mcp_remembered_approval.go:39`） | — |
| `provider_retries` | reliability | int 0-10 | 5 | `TOFI_PROVIDER_RETRIES` |
| `schedule_receipt_review` | schedules | bool | true | — |
| `desktop_idle_seconds` | computer | 只读 | — | — |

- 取值顺序：环境变量（生效并锁住界面控件）> 存储的值 > 默认值。几项住在旧表里（默认模型、结构化回复、MCP 自动审查模式），与旧入口互相联动，两边显示一致。值缓存在内存里，写入时更新缓存，下一次运行即生效，不用重启（文件头注释 `:22-30`）。
- 接口：`GET/PUT /api/automation-settings`（路由 `internal/app/app.go:2844`，界面 `ui/src/AutomationSettings.tsx:132`、`:143`）。
- 后来的"始终允许"授权（D24）也放在同一页撤销（`AGENTS.md` "Security gates"："Revoke (Settings > Automatic behavior, `/api/standing-allowances`)"）。
- 以后新增任何自动行为，都要在注册表里加一项，带默认值、界面文案（7 种语言，`ui/src/locales/*/settings.json`）和测试。
- 是否所有现有自动行为都已登记，待确认：例如跟进（follow-ups）影子模式在 Settings → Follow-ups 单独一页（`docs/agent-plan/wave-notes.md:66`），不在这张注册表里。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 注册表与读写 | `internal/app/automation_settings.go`（常量 `:33-45`、注册表 `:82-94`、`backgroundDefaultModel` `:298`） |
| 路由 | `internal/app/app.go:2844` |
| 界面 | `ui/src/AutomationSettings.tsx`、`ui/src/settings/automation.css`、`ui/src/settings/SettingsPages.tsx`、`ui/src/SettingsShell.tsx`；中文名"自动行为"在 `ui/src/locales/zh-CN/settings.json:81` |
| 重试次数接入 | `internal/runtime/runtime.go`、`internal/runtime/types.go`、`internal/provider/retry.go` |
| 测试 | `internal/app/automation_settings_test.go`：`TestAutomationSettingsInventoryAndDefaults`、`TestAutomationSettingsReadWriteEachItem`、`TestAutomationSettingsSurviveRestartOfTheCache`、`TestAutomationSettingsValidation`、`TestAutomationSettingsEnvironmentOverridesWin`、`TestProviderRetriesZeroStaysZero`；`internal/runtime/retries_setting_test.go`；`ui/scripts/test-automation-settings.mjs`（`npm --prefix ui run test:automation-settings`） |
| 合并提交 | `24af5a1` feat/automation-settings（内含 `ef59d81`）；文案修订 `5035a5d`（描述结构化回复时不出现内部工具名） |
