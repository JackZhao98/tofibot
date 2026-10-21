# 0018 渐变模糊的使用范围

> 编号 D18，索引见 [README](README.md)。

## 决定

- “渐变模糊”（progressive blur，越靠近边缘越模糊）原则上用在 Electron 桌面端和 iOS 端。网页端保留现有的毛玻璃效果。
- 同日 owner 定了一个例外，只适用于网页端的消息窗格：
  - 消息窗格底部（输入框上方）用真正的渐变模糊；
  - 顶栏改成透明的毛玻璃，消息可以在顶栏下面滚过；
  - 任何东西都不能盖住“新消息”跳转按钮（`jump-latest`）。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

改之前，输入框上方是一层纯色 `var(--bg)` 遮罩，顶栏下面是一个硬切的 mask（`ui/src/chat-header.css` 在 `a5866aa` 中删掉的 `mask-image`）。

## 理由

- 不遮住内容和按钮：跳转按钮必须始终在最上层。
- 性能：每多一层 `backdrop-filter` 都会超出悬停延迟预算，所以模糊只叠两层（4 px + 12 px），输入框本身不再单独加背景模糊（`a5866aa` 提交说明，`ui/src/progressive-blur.css` 文件头注释）。
- 为什么原则上只给原生客户端用：owner 原话里的理由待确认。

## 影响

- 实施状态：网页端例外已实施：`a5866aa`。Electron 和 iOS 两端：待确认（客户端源码不在本仓库，见 `AGENTS.md` “Core source boundaries”）
- 首个仍存在的含此改动的标签：v0.1.4-rc.28（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 只对网页端生效：选择器都带 `html:not([data-desktop])`。桌面客户端和网页上的其他区域都保留原来的毛玻璃（`ui/src/progressive-blur.css:1-5`）。
- 跳转按钮设了 `z-index:6`（`ui/src/progressive-blur.css:51`），高于模糊带和输入框；有测试检查屏幕中心点最上层的元素就是这个按钮。
- 每个 `backdrop-filter` 都配 `-webkit-` 前缀版本。系统开了“减少透明度”时，退回一层普通遮罩。
- 消息窗格通过 `--chat-header-height` 延伸到顶栏下面（`ui/src/App.tsx` 中 Composer 的测量代码）。
- 设计系统规范（D28）已收录这条范围规则：[`docs/design/principles.md`](../design/principles.md)（跳转胶囊不被遮挡、渐变模糊的平台范围）。

## 相关代码与文档

- `ui/src/progressive-blur.css`、`ui/src/chat-header.css`、`ui/src/App.tsx`（`.edge-blur.is-bottom`）
- 测试：`npm --prefix ui run test:live-fixes`（`ui/scripts/test-live-fixes.mjs`：测量模糊渐变、按钮在最上层、滚动开销）；`6894317`（像素级模糊检查只在 Chromium 上跑）
- 守卫：`npm --prefix ui run test:motion-tokens`（`backdrop-filter` 缺 `-webkit-` 版本时失败，见 `AGENTS.md` “Web UI motion”）
