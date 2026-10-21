# 0020 手机端列表和聊天之间用 iOS 式推入；启动屏改为模糊骨架

> 编号 D20，索引见 [README](README.md)。

## 决定

- 手机端在会话列表和聊天之间切换时，用 iOS 式推入：
  - 进入聊天：聊天从右侧滑入，盖在列表上；列表同时向左移一段并变暗；
  - 返回：动作反过来。
- 启动屏：先显示一层模糊的界面骨架，中间一个灰色 logo 在缓慢“呼吸”，不再用转圈图标。
- 登录页和启动屏都在视口里居中。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 之前手机端的列表和聊天是直接切换的，没有过渡动画。
- 加载时显示的是转圈图标（`ea39209`）。
- 登录卡片偏下约 120 px：一个空的原生标题栏元素占了网格的一行，外边距也上下不对称（`36b2b12`）。

## 理由

- 和用户在手机上熟悉的导航方式一致。
- 推入过程中两个窗格都保持绘制，任何一帧都不会出现空白；开了“减少动态效果”时直接切换（`58da838`）。
- 启动屏：加载很快时什么都不显示；加载完成时模糊层在真实界面上方渐渐散开，过渡不会突兀（`ea39209`）。
- owner 原话里的其他理由：待确认。

## 影响

- 实施状态：已实施：推入 `58da838`、`927a970`；预算测试 `3e2de40`；启动屏 `ea39209`（合并 `477b41f`）；居中 `36b2b12`
- 首个仍存在的含此改动的标签：推入与居中 v0.1.4-rc.28；启动屏 v0.1.4-rc.26（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 时长：进入 280 ms，返回 220 ms，测试分别在 `ui/scripts/test-shell-budget.mjs:239` 和 `:252` 断言；推入要在 280 ms 内落定（`:241`）。`927a970` 把聊天窗格的过渡改为 `--m-base`。样式在 `ui/src/styles.css:312` 起。
- 启动屏同时用于三个场景：会话检查、首次加载工作区、启动失败。logo 用 `grayscale(1)` 滤镜做成灰色，按 2.4 s 一个周期呼吸（`ui/src/boot-screen.css:26`、`:42`）。
- 推入动画的性能由 `test:shell-budget` 守住。

## 相关代码与文档

- `ui/src/styles.css`（`.contact-pane`、`.chat-pane`）
- `ui/src/BootScreen.tsx`、`ui/src/boot-screen.css`、`ui/src/owner-setup.css`、`ui/src/desktop-system.css`
- 测试：`npm --prefix ui run test:shell-budget`（`SHELL_BUDGET_ENGINE=webkit` 时在 WebKit 上跑）、`test:preloader`、`test:live-fixes`（检查居中）
