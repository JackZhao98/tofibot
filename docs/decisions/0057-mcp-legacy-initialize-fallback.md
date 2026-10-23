# 0057 MCP 连接允许旧版 initialize 回退（只放宽协议版本，不放宽传输）

> 编号 D57，索引见 [README](README.md)。

## 决定

所有 MCP 连接都允许 go-sdk 原生回退：先试 `server/discover`（2026-07-28），服务端不支持时走旧版 `initialize`，协商到 2025-11-25 / 2025-06-18 / 2025-03-26 / 2024-11-05。会话不再钉死 2026-07-28，连上后也不再断言版本。只有服务端给出的版本全都在 SDK 支持范围之外时，才报 `unsupported_protocol`。

旧 HTTP+SSE 传输仍然拒绝（`unsupported_transport`）：这次放宽的是协议版本，不是传输。

## 日期

2026-10-23（PDT）

## 谁拍板

owner。

## 背景

仍在旧规范上的真实服务（例如 Robinhood `https://agent.robinhood.com/mcp/trading`）连不上，界面只显示笼统的“检查服务地址和凭据”。SDK 本来就会回退，是 TOFI 自己的发送中间件拦掉了 `initialize`。

## 影响

替代“严格 2026-07-28”的早先立场（仅协议版本部分）。`requiredMCPProtocolVersion` 常量保留，只作缓存键盐值。

## 相关代码与文档

- `internal/extensions/mcp.go`（`prepareMCPSession`）、`mcp_protocol.go`（`isMCPProtocolFailure`）、`inspection_diagnostic.go`
- 测试：`internal/extensions/mcp_protocol_test.go`
- [DISCOVERY.md](../../internal/extensions/DISCOVERY.md)
