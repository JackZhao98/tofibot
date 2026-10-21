# 0031 设计系统迁移的体积方案：方案 A，库全部懒加载，入口预算不动

> 编号 D31，索引见 [README](README.md)。回答 [`docs/design/migration.md`](../design/migration.md) §7 的 Q1。

## 决定

- 走方案 A：迁移引入的每一个库（Radix 的菜单、弹出卡片、对话框，以及第 4、5 步实验里可能用到的 Motion、dnd-kit）都只出现在懒加载的块里，不进入口包。
- 入口包预算不动：`test:shell-budget` 的上限仍是 315,805 B（gzip），即 `ui/scripts/test-shell-budget.mjs:29` 的 `Math.floor(394_757 * 0.8)`。
- 懒加载桩和弹层宿主在入口包里的字节，用"把现有弹层代码搬出入口包"省下来的字节支付（第 2、3 步）。
- 不采用方案 B（把预算提高约 31 KB、直接静态导入 Radix）。

## 日期

2026-10-21（协调者转述的日期）。落地提交的时间是 2026-10-20 晚上（PDT），哪个为准待确认，同 [README](README.md) 对 D24–D29 的说明。

## 谁拍板

owner（jackzhao）。原话由协调者转述给执行者，仓库内没有原始对话记录。

## 背景

- 方案写的时候（`748c774`），入口包实测 315,064 B，离预算只剩 741 B（`migration.md` §2.2）。
- 实测的候选库没有一个能进入口包：最小的 `motion/mini` 也要 +2,987 B；每个 `import()` 桩在入口包里只要 47–93 B（`migration.md` §2.3、§2.4）。

## 理由

owner 没有另写理由。方案里给的理由是：入口包变大约 10% 会拖慢首屏解析和执行，要重新量（[D02](0002-measure-interaction-cost.md)）；而懒加载边界本身很便宜（`migration.md` §2.4 第 2、5 条）。

## 影响

- 实施状态：生效。第 1 步（层级刻度和 portal 根节点，分支 `ds/step1-layers`）不引入任何库，入口包从 315,064 B 变成 315,113 B（+49 B，余量 692 B），见 `migration.md` 第 1 步。六步合在集成分支 `ds/integrate` 之后入口包是 314,194 B（余量 1,611 B）：四个 Radix 包和右滑返回引擎都只在懒加载块里，第 3 步把 `ReassignPrimaryDialog`、`PrimaryAssistantSetting` 搬出入口，付掉了第 2、4 步的桩（`migration.md`"集成"一节）。预算没有动。
- 第 2、3 步的每一次合并前都要报入口包字节数，超预算不合并（`migration.md` §4 第 7 条）。
- 每个新库的懒加载块在空闲时预取（`migration.md` §4 第 3 条，做法同 `ui/src/useSidebarDnd.ts:33-35`）。

## 相关代码与文档

- `ui/scripts/test-shell-budget.mjs:29`（预算）、`:32-46`（入口包的量法）
- [`docs/design/migration.md`](../design/migration.md) §2、§4、§7
- 相关决定：[D02](0002-measure-interaction-cost.md)、[D28](0028-headless-design-system-migration.md)
