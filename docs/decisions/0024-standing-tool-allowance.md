# 0024 “始终允许”：给某个 Bot 长期授权某一个写操作工具

> 编号 D24，索引见 [README](README.md)。

## 决定

- 审批卡片上可以选“始终允许”，长期授权某一个 Bot 使用某一个写操作工具。
- 这份授权绑定两样东西：工具定义和 MCP 服务器身份。任意一样变了，授权就作废。
- 以下情况永远不能授权：高风险工具、只读工具、人看不到的对话。
- 自动审查（AutoReview）的拒绝结果仍然生效。
- 定时任务里也会用这份授权，卡片上会写明这一点。
- 可以在 Settings 里撤销。每次使用都记审计记录。

## 日期

上下文记为 2026-10-21。实现提交时间是 2026-10-20 19:29–20:10 PDT（即 10-21 UTC），以哪个为准待确认

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

同一个 Bot 反复调用同一个写操作工具时，每次都要人点批准，打断自动化。这和 D15 的“自动行为可见、可调”是同一类诉求。

## 理由

- 免去重复批准，但不放宽安全边界：
  - 授权只按 (账户, Bot, MCP 服务器, 工具) 生效，和每次调用的参数无关；
  - 工具定义或服务器一变就作废；
  - 服务器身份变化是不可逆的（记 `server_changed_at`），把地址改回去也不会恢复授权。
- 这是一项放宽安全限制的改动，按 D30 合并前做了对抗性审查，发现的问题逐条在 `5592eb3` 中修完。

## 影响

- 实施状态：已实施：`6f45df4`；对抗性审查发现的问题在 `5592eb3` 中修完，合并为 `748c774`
- 首个仍存在的含此改动的标签：v0.1.4-rc.29（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 数据：`mcp_standing_allowances` 表和 `mcp_standing_allowance_uses` 表，由 `migrateStandingAllowances` 创建（`internal/app/mcp_standing_allowance.go:71`）。
- 授权只能由 owner 通过提问路由创建，并且卡片必须被闸门标为 `standing_eligible`。模型的输入不能创建授权（`approval.standing` 字段是保留字段）。
- 人看不到的对话：
  - 永远匹配不到授权（`matchStandingAllowance` 要求 `UserVisible`）；
  - 即使存在有效授权，也会以 `conversation_not_user_visible` 拒绝。
- AutoReview 的 auto 模式下，授权只替换“交给人决定”这一种结论，审查器的拒绝照样拦截（`finishMCPReview`，`internal/app/mcp_auto_review.go:791`）。
- 撤销何时生效：每次使用前都会重新校验（`standingClaimValid`，`internal/app/mcp_standing_allowance.go:257`），所以撤销在进行中的 run 的下一次调用就生效。
  - 有人在场：卡片退回给人决定；
  - 无人在场：卡片关闭，调用以 `mcp_needs_human_unattended` 拒绝。
- 以下操作会撤销授权，但保留授权记录和使用记录：
  - 把工具改为“始终询问我”（`revoked_by=tier_raised`）；
  - 归档或删除 Bot（`bot_archived` / `bot_deleted`）；
  - 删除 MCP 服务器（`server_removed`）。
- 界面：Settings › 自动行为里的授权列表（`ui/src/StandingAllowances.tsx`），卡片提示（`ui/src/ApprovalCard.tsx`、`ui/src/QuestionCard.tsx`），7 种语言的文案。

## 相关代码与文档

- `internal/app/mcp_standing_allowance.go`、`internal/app/mcp_call_approval.go`、`internal/app/mcp_approval_claim.go`、`internal/app/mcp_auto_review.go`、`internal/extensions/tool_tiers.go`
- 测试：`internal/app/mcp_standing_allowance_test.go`，例如：
  - `TestStandingAllowanceNeverForHighRiskOrReadOnly`
  - `TestStandingAllowanceVoidedByToolDefinitionChange`
  - `TestStandingAllowanceAddressChangedAndRestoredStaysVoid`
  - `TestStandingAllowanceHiddenConversationStillRefuses`
  - `TestStandingAllowanceAutoModeKeepsReviewerDeny`
  - `TestStandingAllowanceRevokeAsksAgainAndBitesAtClaim`
  - `TestStandingAllowanceUseRecordedInClaimTransaction`
- 前端测试：`npm --prefix ui run test:standing-allowance`、`test:automation-settings`
- `AGENTS.md` 中以 “MCP write-tier calls can be allowed for good” 开头的段落；`docs/agent-plan/mcp-tool-tiers.md`
- 相关决定：[D15](0015-automatic-behaviour-settings.md)、[D30](0030-standing-operating-rules.md)
