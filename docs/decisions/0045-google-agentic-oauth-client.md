# 0045 Google Workspace MCP 要求 OAuth 客户端标为“AI 智能体”（agentic）

> 编号 D45，索引见 [README](README.md)。这是 Google 的外部要求，不是产品取舍；owner 的指示是照要求改引导、诊断和文档，并自己去标记现有客户端。

## 决定

1. **要求（Google 2026-10-12 邮件与 Workspace 公告）。** Google OAuth 客户端新增设置 `is_agentic`，在 Google Cloud 控制台的 Google Auth Platform → Clients → 编辑客户端里，是勾选项 “Use this client for an AI-powered agent”。
   - **2026-11-11 起**，Workspace MCP 服务（`gmailmcp`、`drivemcp`、`docsmcp`、`sheetsmcp`、`slidesmcp`、`calendarmcp` 的 `.googleapis.com`）拒绝未标记的客户端，错误信息写 “the client isn't marked as an AI agent” 并附修复步骤链接。
   - **2026-11-12 起**，该设置在现有客户端上固定。要改只能新建 OAuth 客户端 ID，并在创建时选 agentic。
   - 只调用 Workspace REST API 的应用不受影响，所以本机 `gog` 路径（`internal/mcprunner/gog.go`，设置里的“本机 Google”面板）不加这一步。
2. **引导。** 所有教人为 Workspace MCP 创建或连接 Google OAuth 客户端的地方都加上这一步和“固定”的说明：设置页的服务清单步骤（`extensions:catalog.google_setup.agentic_client`）、设置页的 Google 配置提示（`extensions:google.agentic_hint`），两者在 7 个语言里都有；OAuth 完成页的 Google 连接设置段（`internal/app/oauth_completion.go`）。
3. **诊断。** 连接 Google Workspace MCP 主机失败、且传输层看到 HTTP 401 或 403 时，给人看的诊断（`permission_denied`）在原有检查（API / MCP 启用、预览资格、账号权限）之外点名“OAuth 客户端是否已标为 AI 智能体”。
   - 分类只看主机加结构化的 HTTP 状态：传输层（`mcpHeaderTransport`）记下最后一个非 2xx 状态，连接失败时包成 `mcpHTTPStatusError`（只带数字，不带提供方文字），`errors.As` 取出。
   - **没有找到 Google 为这一错误文档化的结构化原因码**（公告只给了消息文字 “the client isn't marked as an AI agent”，按仓库规则不匹配消息文字），所以用主机加 401/403；诊断因此写明“401 / 403 本身不能确定是哪一项”。若 Google 日后公布稳定的 reason / code，把它加到 `mcpInspectionDiagnostic` 里，让这条提示变精确。
   - **工具调用时同样处理。** 会话的传输层记下的状态按会话保存（`Manager.mcpStatus`）；Google 主机对 `tools/call` 回 401/403 时，工具结果带固定英文说明（代码 `mcp_permission_denied`；写类调用保持 `mcp_result_unknown` 的不确定语义，只在其文字后追加同一说明），**第一项是“Cloud 项目已加入 Google Workspace Developer Preview Program”，第二项才是 agentic 客户端设置**，第三项是 API / MCP 启用、范围和测试用户。
- 提供方文字不进给人看的提示，沿用 `inspection_diagnostic.go` 的约定。
   - 已知局限：状态只在连接握手阶段记录；握手之后 `tools/list` 或工具调用才被拒的情形不带状态，仍走原有的文字兜底。

## Developer Preview 和 Gmail 不能发信

- Workspace MCP 服务现在是 Developer Preview。Cloud 项目必须加入 [Google Workspace Developer Preview Program](https://developers.google.com/workspace/preview)；没加入时 OAuth 授权成功、工具也能列出，但每次工具调用都返回权限错误（owner 实际连接时撞到，公开文档同说法）。引导（7 语言的 `preview_access`、`preview_hint`）和 OAuth 完成页都把它排在 API 启用、agentic 设置和测试用户旁边。**没有找到 Google 为“未加入”文档化的结构化原因码**，诊断仍按主机加 401/403，并写明状态本身分不出是哪一项。
- Google 的 Gmail MCP 只有读、搜索、草稿和标签工具，**没有任何一个能发信**。要发信走本机 `gog`（gogcli，REST API）路径。

## Owner 动作

- [ ] **在 GCP 项目 `tofi-self-hosted` 里，把现有 OAuth 客户端标为 AI 智能体**（Clients → 编辑 → 勾选 “Use this client for an AI-powered agent”）。**2026-11-11 前**必须做，否则 Workspace MCP 开始拒绝；**2026-11-12 起**现有客户端无法再改，那时只能新建客户端 ID（并在创建时勾选），再把新 ID 和密钥换进 TOFI。
- 同一个项目还要确认已加入 Developer Preview Program（见上）。
- 自托管用户自己的客户端同理；引导文案已覆盖。

## 日期

2026-10-21（太平洋时间）记录；截止日 2026-11-11，固定日 2026-11-12。

## 影响的代码

`internal/extensions/inspection_diagnostic.go`、`mcp_protocol.go`、`mcp.go`（`openMCPClient`）；`internal/app/oauth_completion.go`；`ui/src/MCPSettings.tsx`、`ui/src/integrationCatalog.ts`、`ui/src/locales/*/extensions.json`。测试：`inspection_diagnostic_test.go`、`google_agentic_locale_test.go`、`oauth_completion_test.go`。
