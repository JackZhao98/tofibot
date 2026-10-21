# 0021 侧边栏可以拖动排序，并有置顶区

> 编号 D21，索引见 [README](README.md)。

## 决定

- 侧边栏里的会话可以拖动排序，顺序保存在服务端。
- 有一个置顶放置区：把会话拖进去就置顶。
- 置顶的会话显示为大的猫咪方块，名字用小字。

## 日期

2026-10-20（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

改之前侧边栏只按最近活动时间排序，没办法手动排，也没有置顶。

## 理由

- 顺序存在服务端，换设备、重新打开都还在（测试 `TestSidebarLayoutSurvivesReopen`）。
- 性能：拖动只改 transform，元素尺寸只在拿起时量一次，React 只在放下时渲染一次。拖动引擎单独打包成一个 chunk，空闲时才加载，保证主界面的加载预算（`AGENTS.md` “Sidebar order and pins”）。
- 为什么置顶要用大猫咪方块：owner 原话里的理由待确认。

## 影响

- 实施状态：已实施：`9a92f4a`（合并 `d4d0fa4`）
- 首个仍存在的含此改动的标签：v0.1.4-rc.28（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 数据：`conversations` 表新增 `pin_order` 和 `sort_order` 两列，通过 `ensureColumn` 添加。
- 接口：`PUT /api/conversations/order {pinned, order}` 一次性原子替换两份顺序，并拒绝以下输入：重复 id、未知 id、同时出现在两份列表里的 id、已归档的置顶（`internal/app/sidebar_order.go:37` `SetSidebarLayout`，`:112` `routeSidebarOrder`）。
- 归档 Bot 或群会取消它的置顶（`internal/app/archive.go`）。
- 导出（`portable*`）刻意不带这两个字段（`TestPortableExportLeavesSidebarLayoutBehind`）。
- 交互：
  - 触屏长按开始拖动（按住不动则打开菜单）；
  - 键盘用 Alt+方向键移动，移动结果会读屏播报；
  - 开了“减少动态效果”时去掉滑动。
- 排序规则：在人手动排序之前按最近活动排。排过之后以人的顺序为准；之后新出现、服务端还没编号的会话插到最上面，最新的在前。

## 相关代码与文档

- 后端：`internal/app/sidebar_order.go`、`internal/app/sidebar_order_test.go`（`TestSidebarLayoutPersistsOrderAndPinsInListing`、`TestSidebarLayoutIsAtomicOnInvalidInput`、`TestArchivingUnpinsAndRestoreStaysUnpinned`、`TestSidebarColumnsMigrateIdempotently` 等）
- 前端：`ui/src/sidebarOrder.ts`、`ui/src/sidebarDndEngine.ts`、`ui/src/useSidebarDnd.ts`、`ui/src/PinnedDock.tsx`、`ui/src/sidebar-pins.css`
- 测试：`npm --prefix ui run test:sidebar-pins`（60 个会话下的帧预算、390 px 宽触屏、键盘、减少动态效果）
- `AGENTS.md` “Sidebar order and pins”
- D28 迁移方案里 dnd-kit 是候选库之一，替换这套拖动引擎之前要先过 `test:sidebar-pins`
