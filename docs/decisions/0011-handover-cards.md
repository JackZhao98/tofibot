# 0011 接管卡片：Bot 在聊天里请人接手屏幕或连接服务

> 编号 D11，索引见 [README](README.md)。

## 决定

Bot 遇到需要人亲自操作的情况时，在聊天里发一张接管卡片：

- **接手屏幕**（`request_takeover`）：登录、两步验证、验证码这类只能人做的步骤，请人在 Bot 的电脑上接手完成。
- **连接服务**（`request_connection`）：请人在聊天里直接走一遍连接某个服务的授权流程。

对标的产品：Meta Muse、OpenAI Dots、xAI Grok Bot（owner 给出；仓库里没有对标分析文档，待确认）。

## 日期

2026-10-19（Pacific）拍板；合并于 10-20 12:03（`4afa77e`），首次进入 `v0.1.4-rc.28`。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 登录、两步验证、验证码只能由人完成；连接一个新服务原本要人自己去设置页操作。仓库里能确认的是：此前 Bot 没有从聊天里发起这两件事的工具（`request_takeover`、`request_connection` 均在 feat/handover-cards 分支上新增，最早见于 `9329cb7`），`manage_extensions` 返回 `oauth_required` 时也没有可指向的卡片。之前线上具体出了什么问题，仓库里没有记录（待确认）。
- 共享电脑的远程桌面（`RemoteDesktopControl`）和设置里的连接流程都已存在，缺的是"从聊天里发起、人做完后运行自动接着跑"的那一环。

## 理由

- 让人在当下、在原地完成那一步，Bot 的运行挂起等待，人做完后自动继续，不需要重新描述任务。
- 复用 [0010](0010-one-human-input-rendezvous.md) 的 questions 会合点，不另起机制：挂起、重启后仍在、过期、隐藏会话拒绝，全部沿用。

## 影响

- 新增问题类型 `takeover` 和 `connection`，以及一个存储列 `questions.handover_target`（`ensureColumn` 添加）（`AGENTS.md` "Asking a human" 一节第二段）。
- 回答只有一个枚举词：接手屏幕 `done` / `cannot`；连接 `connected` / `failed` / `declined`。返回给模型的结果（`handoverResult`，`internal/app/handover.go:85`）是状态词加一句固定的话。人在远程屏幕上输入、复制或看到的任何东西，都不进回答、事件、结果或表（按键和剪贴板只走浏览器 -> `/api/computers/firecracker/actions` -> 客机）。
- `request_takeover` 需要共享电脑；网站只存来源（origin）；窗口不超过 14 分钟（客机 15 分钟空闲会回收桌面）。卡片打开 `RemoteDesktopControl` 的弹窗，控制权绑定到这张卡，不会被 15 秒空闲释放抢走，卡片回答/取消/过期或运行开始时结束；运行挂在卡上期间保留它自己的浏览器标签页。
- `request_connection` 按客户端走设置里的连接流程（原生应用、https 弹窗，或纯 http 时走共享电脑，经 `mcpOAuthRoute`）；只有服务端确实看到令牌才接受 `connected`（否则 `connection_not_ready`）。
- 两种卡片在隐藏会话里都拒绝（`requireHumanPresent`）；在定时运行里会像 `ask_user_question` 一样挂起并过期。`manage_extensions` 返回 `oauth_required` 时，只在人能看到的会话里指向 `request_connection`。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 规则说明 | `AGENTS.md` "Asking a human (one rendezvous)" 第二段 |
| 服务端 | `internal/app/handover.go`（`takeoverTool` `:118`、`connectionTool` `:155`、`postHandoverCard` `:194`、`handoverResult` `:85`）、`internal/app/questions.go`、`internal/app/computer_control.go`、`internal/app/computer_hold.go`、`internal/app/extension_tools.go` |
| 界面 | `ui/src/HandoverCards.tsx`、`ui/src/TakeoverDialog.tsx`、`ui/src/RemoteDesktopControl.tsx`、`ui/src/handover.css`、`ui/src/locales/*/tasks.json` |
| 测试 | `internal/app/handover_test.go`（如 `TestTakeoverSuspendsSurvivesRestartAndIsNotStolenByIdleRelease`、`TestConnectionCardSuccessFailureAndExpiry`、`TestHandoverResultCarriesOnlyAStatusWord`、`TestOAuthRequiredPointsAtTheCardOnlyWhereAPersonCanAnswer`、`TestRunParkedOnTakeoverKeepsItsBrowserTabs`）；`ui/scripts/test-handover-cards.mjs`（`npm --prefix ui run test:handover-cards`） |
| 合并提交 | `4afa77e` feat/handover-cards（内含 `d3e4ff9`）；集成修复 `5e0cd3b`（`QuestionCard` 改为懒加载以守住外壳体积预算，接管遮罩补 `-webkit-` 前缀） |
| 待确认 | 对标产品的具体功能对照；接管卡片在生产上的真实对话验证记录 |
