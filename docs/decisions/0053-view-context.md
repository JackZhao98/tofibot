# 0053 每条回复的“查看活动”里可以看到模型这一轮实际收到的上下文

> 编号 D53，索引见 [README](README.md)。

## 决定

在每条回复已有的“查看活动”（`WebToolActivityRun` 的抽屉）末尾加一行“上下文”，点开后显示这一轮运行第一次调用模型时实际收到的东西：每个部分的种类、大小（字符数和估算 token 数）和可展开的原文。只有账户所有者能看。

- **记录**：运行装配好上下文后，把各部分存成一份结构化快照（每个运行一份，只记第一次调用；重试或续跑不覆盖）。不重新装配，不改提示词的任何字节。
- **部分的种类是开放的字符串**：`system_guidance`、`bot_instructions`、`agent_report_prompt`、`tools`、`tools_on_demand`（按需加载、这次没发）、`memory`、`summary`、`history_message`（带消息 id、角色、种类、是否只留概要、是否被截断、是否是本次请求）、`earlier_turns_note`、`capability_refs`、`schedule_states`、`run_context`、`request_note` 等；`buildContextPartsWith` 里新增一块，只需在 `contextMarkerKinds` 加一行，否则显示为 `other` 并以首行作标签。
- **接口**：`GET /api/runs/{id}/context`，返回按顺序的部分、总量和按种类的汇总、历史窗口漏掉了多少条。没有快照返回 404 和稳定码 `context_snapshot_unavailable`（运行早于这个功能、或已被清理），绝不返回空的成功。
- **界面**：“上下文”按需懒加载（`RunContext.tsx`，独立块，入口包只多一个按钮）；顶部一行总量，一条按种类的占比条加图例，按顺序的部分列表（等宽原文加复制），历史消息一行一条（角色、时间、概要或原文、大小）；窗口漏掉的消息、更早历史未读取、原文因体积被省略、因形似密钥被替换，各有一句明说。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner 原话：“What our context holds is a total mystery.” 决定在每条回复的“查看活动”里加“查看上下文”，仅所有者可见。

## 安全审查记录（process §8 的对照）

这个功能不放宽任何闸，但它把提示词内容存进了库，所以按 §8 的问题逐项写明：

- **谁能读**：只有通过所有者会话闸（`handle` 里的 `ownerAuthorized`）的请求，与工具活动详情同一道闸；没有导出（`portable*` 不带这张表）、Bot 自己的工具读不到、没有其他读者。测试 `TestRunContextRequiresTheOwnerSession`：无会话、伪造会话 401，所有者 200。本部署只有一个所有者账户，没有“另一账户”的情形。
- **里面可能有什么**：系统提示词、Bot 指令、记忆、摘要、聊天历史、工具定义（含 MCP 工具的 schema）、被打断工作的工具结果。这些本来就在所有者的库里，快照是第二份副本。保险库的值从不进提示词：私密表单字段和 `request_secret_input` 的值封存在 `secrets.go`，模型只见 `secret_ref` 句柄，句柄不是密钥，原样保留。`tool_carryover` 里的工具结果是上游返回的文字，可能含凭据，故过一遍脱敏。
- **脱敏**：仓库里没有现成的通用文字脱敏函数（只有 URL 的 `extensionPublicURL` 和请求头的 `maskedHeaders`），所以新写 `redactContextText`：私钥块、JWT、`sk-` / `ghp_` / `xox` / `AKIA` / `AIza` 等常见密钥前缀、`Bearer` 令牌、URL 里的用户名密码、`password=`/`api_key:` 这类字段的值。它抓常见形状，不保证抓全；展示的原文被替换后会标出替换数量，大小仍按原文算。图片的 URL（可能是 data URL）不存，只存张数。
- **保留**：每个会话保留最近 50 次运行的快照（`contextSnapshotKeep`，写入时同事务清理），运行或会话被删时随外键级联删除。每份快照压缩前不超过 256 KB（`maxContextSnapshotBytes`）：超出时先丢最大的原文，只留大小。
- **仍有的风险**：形状没覆盖到的秘密（例如用户在聊天里贴的自由格式口令）会出现在快照里，与它在聊天记录里的暴露面相同；备份会带上这张表。

## 影响

- 新表 `run_context_snapshots`（`migrateRunContext`：`CREATE TABLE IF NOT EXISTS` + `ensureColumn`）。
- `assembleRunSystem` 现在由 `assembleRunSystemSegments` 拼出，段落拼接结果与旧实现逐字节相同（测试里带着旧实现的副本对比）。
- `runtime.SystemPromptSuffix` 暴露 agent 循环追加的汇报提示，`agent.EstimateTextTokens` / `EstimateToolDefinitionTokens` 暴露现有的 token 估算。token 数是估算，不是提供方的计数。
- 只做了网页端（`WebToolActivityRun`）；桌面客户端的活动行（`DesktopToolActivityRun`）未加，降级是按 [最高优先级：自托管+Web] 的排序。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 记录与存储、脱敏、接口 | `internal/app/context_snapshot.go` |
| 采集点 | `internal/app/context.go`（`buildContextPartsWith` 里的 `r.ctxCapture.*` 调用）、`internal/app/app.go`（`execute` 里装配系统提示词处的 `recordRunContext`）、`internal/app/prompt_budget.go`（`assembleRunSystemSegments`） |
| 界面 | `ui/src/RunContext.tsx`、`ui/src/run-context.css`、`ui/src/App.tsx`（`RunContextEntry`）、目录页 `ui/src/catalog/sections/process.tsx` 的 `context-*` 状态 |
| 测试 | `internal/app/context_snapshot_test.go`：`TestRunCapturesContextSnapshotInOrderAndSumsToTheRequest`、`TestAssembleRunSystemSegmentsKeepPromptBytes`、`TestContextCaptureDoesNotChangePromptBytes`、`TestRedactContextText`、`TestSnapshotRedactsStoredTextNotThePrompt`、`TestBoundContextSnapshotDropsLargestTextsKeepsSizes`、`TestContextSnapshotRetentionKeepsNewestPerConversation`、`TestRouteRunContext`、`TestRunContextRequiresTheOwnerSession` 等 |
| 相关决定 | [D44](0044-recall-context-memory-and-search.md)、[D51](0051-notebook-brutal-style.md) |
