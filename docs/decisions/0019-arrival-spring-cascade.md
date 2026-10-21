# 0019 新消息到达：弹簧链式联动

> 编号 D19，索引见 [README](README.md)。

## 决定

- 一条完整的回复到达时，屏幕上的消息像弹簧链或牛顿摆一样依次动起来：
  - 最下面的新消息先动，往上每条比下面那条晚约 15 ms 起步；
  - 每条越过自己的位置 2–4 px，再弹回原位；
  - 整个过程约 400 ms。
- 流式输出期间不做联动，只轻柔地跟随到底部。
- 系统开了“减少动态效果”时，新消息直接淡入。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

owner 反馈新消息到达时看不到滑动效果。原因有两个（`a7814b3` 提交说明）：

- 读者滚动过一次后，窗格就永久脱离“跟随到底部”，只有精确回到最底才恢复；
- 跳转按钮在离底部 48 px 时就出现，所以只要读者离底部稍远一点，就不再跟随。

## 理由

- 操作要立刻有反馈：消息到达要有可感知的动作。
- 不能移动版面：滚动位置在当帧直接落到正确的地方。读者看到的只是加在可见消息上的合成层 `translate` 偏移。layout、`scrollTop`、`scrollHeight` 全程不变，React state 也不变（`ui/src/arrivalCascade.ts` 文件头注释）。

## 影响

- 实施状态：已实施：`a7814b3`（之前的跟随滚动见 `9e17e9e`、`d810657`）
- 首个仍存在的含此改动的标签：v0.1.4-rc.28（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 参数集中在 `ui/src/motion.ts` 的 `CASCADE`：`staggerMs: 15`、`overshootPx: 3.8`、`maxLinks: 12`、`maxShiftPx: 130` 等。在浏览器控制台给 `window.__TOFI_CASCADE` 赋值可以临时覆盖这些参数。
- 消息块太高、联动带不完时，剩下的位移交给原来的滑动（`GLIDE_MS`，见 `AGENTS.md` “Web UI motion”）。
- 跟随规则：读者离底部在窗格高度的五分之一以内（96–200 px），并且最近 600 ms 内没有主动往上滚，就带着读者走；离得更远的读者不移动（`ui/src/useConversationScroll.ts`）。

## 相关代码与文档

- `ui/src/arrivalCascade.ts`、`ui/src/motion.ts`、`ui/src/useConversationScroll.ts`
- 测试：`npm --prefix ui run test:arrival-glide`（`ui/scripts/test-arrival-glide.mjs`）
- `AGENTS.md` “Web UI motion”
