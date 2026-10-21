# 0016 乐观发送与送达/已读回执

> 编号 D16，索引见 [README](README.md)。

## 决定

- 人发出的消息按回车后立即出现在对话里（乐观发送），请求在后台进行。
- 回执分四种状态：
  - 发送中：气泡变淡，加时钟图标；
  - 已送达：一个勾 ✓；
  - 已读：两个勾 ✓✓，含义是某个 Bot 的 run 已经开始处理这条消息；
  - 发送失败：显示“未发送 · 重试”。
- 群聊里，已读的提示框列出哪些成员已读。
- 后续补充（同日）：已送达和已读只在悬停、聚焦或轻点时出现，放在消息下方的操作行里，不单独占一行。发送中和失败两种状态要让人不用操作就能看到，所以仍然直接显示在气泡上。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

改之前，消息要等 POST 返回才出现，人看不出消息有没有发出去、Bot 有没有开始处理。首版回执在每条自己的消息下面固定占一行 14 px，owner 随后要求把它收进操作行（见 `1d2b322` 提交说明）。

## 理由

- 操作要立刻有反馈：回车后当帧就出现气泡，输入框同时清空（`97323b9` 提交说明）。
- “已读”以 run 真正开始为准，不看消息是否已入库。只有真正开始执行的 run 才能报告的失败码（`STARTED_FAILURES`）也算已读；run 在开始前就失败了不算（`ui/src/sendReceipts.ts`）。
- 控件要低调：回执常驻一行会拉大消息间距，所以收进悬停才出现的操作行（`1d2b322`）。
- owner 原话里关于取舍的其他理由：待确认。

## 影响

- 实施状态：已实施：首版 `97323b9`（合并 `a063c2c`），回执移入操作行 `1d2b322`
- 首个仍存在的含此改动的标签：v0.1.4-rc.26（`97323b9`）；回执移入操作行在 v0.1.4-rc.28（`1d2b322`）（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 前端：判定回执状态只在 `receiptFor()` 一处（`ui/src/sendReceipts.ts`）。失败的消息和发送中的消息存在对话快照里，切换对话后仍在；重新连上时不会自动重发（`97323b9`）。
- 服务端：消息 JSON 带上 `client_message_id`，POST 按 (conversation, client_message_id) 幂等，重试不会产生重复消息（`internal/app/app.go`，`internal/app/http_acceptance_test.go` 中的 `TestHTTPAcceptanceMessageEchoesClientIDAndRetryAddsNothing`）。
- 文案：`ui/src/locales/*/chat.json` 的 `receipt.*` 键，中文版见 `ui/src/locales/zh-CN/chat.json:487`。
- 和 D17 的操作行共用一行。

## 相关代码与文档

- `ui/src/SendReceipt.tsx`（`SendReceipt` 显示发送中和失败，`SendReceiptMark` 显示已送达和已读的勾）
- `ui/src/sendReceipts.ts`、`ui/src/send-receipt.css`
- 测试：`npm --prefix ui run test:send-receipts`（`ui/scripts/test-send-receipts.mjs`）、`test:chat-motion`（断言悬停时页面不移动）
- 相关决定：[D17](0017-quiet-message-action-row.md)
