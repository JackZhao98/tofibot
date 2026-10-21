# 0032 接受第 1 步层级刻度带来的先后顺序变化，逐条列出给 owner 预览

> 编号 D32，索引见 [README](README.md)。回答 [`docs/design/migration.md`](../design/migration.md) §7 的 Q2。

## 决定

- 第 1 步（`--z-*` 层级刻度和 portal 根节点）会有意改变几处弹层的先后顺序，owner 接受。
- 每一处变化都要列出来，让 owner 在预览里逐条看（预览方式见 [D22](0022-preview-via-dev-remote.md)）。清单在 `migration.md` 第 1 步的"层级变化清单"和"owner 预览看哪里"。

## 日期

2026-10-21（协调者转述的日期）。落地提交的时间是 2026-10-20 晚上（PDT），哪个为准待确认。

## 谁拍板

owner（jackzhao）。原话由协调者转述给执行者，仓库内没有原始对话记录。

## 背景

方案里点名的三处是：toast 升到模态之上（`tokens.md` Z1）、设置窗从 30 升到 `--z-dialog`（Z5）、启动画面从 60 升到 `--z-cover` 但仍低于引导（`migration.md` 第 1 步"风险"①）。落地时用一个逐对比较的审计（主干快照对分支，9 种视口和状态，每对元素谁画在上面）找出了全部变化，除了这三处还有十几处是刻度本身带来的，例如同一层的模态改为按打开顺序叠放、电脑小窗排到浮卡和抽屉下面。全部列在 `migration.md` 第 1 步。

## 理由

owner 没有另写理由。方案的理由是：这几处现在的先后顺序本身就是问题（toast 在对话框开着时看不见；设置窗是模态，却被启动画面、菜单、身份卡盖住），统一刻度之后它们按"这一层放什么"排，不再按谁写的数字大。

## 影响

- 实施状态：已在分支 `ds/step1-layers` 落地，合进集成分支 `ds/integrate`，等 owner 预览。L4、L9 后来分别被第 3 步（引导进 `#tofi-layers`）和第 2 步（顶栏回到 `--z-chrome`）改写；合并后的预览清单在 `migration.md`"集成"一节，第 1 步的变化排在最前。
- 审计里发现、刻度不打算改变的三处先后顺序，用 `calc(var(--z-*) ± 1)` 保住了原样：侧栏列仍在顶栏和输入框之上（收起侧栏时账户菜单会伸进对话区，`v2-app.css` `.contact-pane`）；macOS 桌面客户端的拖动条仍在侧栏、抽屉和抽屉遮罩之上（`desktop-system.css` `.native-titlebar`）；≥1100 px 时顶栏仍在上下文浮卡之上，直到第 2 步把顶栏菜单改成 portal（`context-card.css`）。
- `test:layers`（`ui/scripts/test-layers.mjs`）守住刻度：叠在同一点的弹层必须按刻度的顺序画。

## 相关代码与文档

- `ui/src/design-tokens.css`（`--z-*`）、`ui/src/layers.ts`、`ui/index.html`（`#tofi-layers`）
- [`docs/design/tokens.md`](../design/tokens.md) §9、[`docs/design/migration.md`](../design/migration.md) 第 1 步
- 相关决定：[D22](0022-preview-via-dev-remote.md)、[D28](0028-headless-design-system-migration.md)
