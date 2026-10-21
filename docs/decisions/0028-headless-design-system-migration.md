# 0028 设计系统：改用 headless 组件库，保留 TOFI 的外观，先出方案再分步迁移

> 编号 D28，索引见 [README](README.md)。

## 决定

- Web UI 改用 headless 组件库：只负责交互逻辑，不带外观。外观仍然是 TOFI 自己的。
- 候选库：
  - Radix：弹层、菜单、对话框等；
  - Motion：动效；
  - dnd-kit：拖动。
- 先写设计系统规范和迁移方案，再一步一步迁移。每一步都要给 owner 在预览里看过（预览方式见 D22）。
- 按协调者的计划，第一步是统一层级刻度（z-index）和弹层。

## 日期

上下文记为 2026-10-21；按 PDT 计实际日期待确认

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

现在弹层、菜单、拖动、动效都是手写的：

- z-index 从 1 到 1200 随手写（最高的是 `ui/src/onboarding.css:5` 的 `z-index:1200`），例如 `ui/src/styles.css:249` 的 `z-index:50`、`:448` 的 `z-index:80`、`ui/src/progressive-blur.css:51` 的 `z-index:6`；
- 键盘、焦点、关闭方式各个组件各自实现。

## 理由

- 交互的正确性（焦点管理、键盘操作、关闭方式、弹层定位）交给成熟的库来保证。
- 视觉仍然由 TOFI 的设计 token 控制（`ui/src/design-tokens.css`、`ui/src/motion.ts`）。
- 分步迁移，每一步都能单独预览、单独回退，风险可控。

## 影响

- 实施状态：已定方向，未开始迁移。方案写在 [`docs/design/migration.md`](../design/migration.md)。owner 看过方案、点头之后才动第一步
- 每个库会让网页包大多少：要先实际安装一遍量出来，再写进方案。实测数字（gzip 增量字节）见 [`docs/design/migration.md`](../design/migration.md) 第 2 节"体积实测"。
- 现有的性能预算测试必须继续通过：
  - `test:shell-budget`（入口包体积）
  - `test:sidebar-pins`（拖动帧预算）
  - `test:motion-tokens`（动效 token 守卫）
  - `test:switch-budget` 等
- 换掉 `ui/src/sidebarDndEngine.ts` 这类已经过测量的引擎之前，要证明新库能达到同样的预算（见 D21）。

## 相关代码与文档

- `ui/src/design-tokens.css`、`ui/src/motion.ts`、`ui/src/sidebarDndEngine.ts`
- `docs/design/`（规范、组件清单、迁移方案、设计原则）
- `AGENTS.md` “Web UI motion”、“Sidebar order and pins”
- 相关决定：[D21](0021-sidebar-reorder-and-pins.md)、[D22](0022-preview-via-dev-remote.md)
