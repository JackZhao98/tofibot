# TOFI 设计系统

网页界面（`ui/src/`）的设计规范。桌面客户端（Electron）用同一份网页 UI 构建产物，所以这里的规则两端都适用，模糊效果除外（[D18](../decisions/0018-progressive-blur-scope.md)）。

- 基线：`748c774`（= `v0.1.4-rc.29`，2026-10-20）。四份文件里的 `file:line` 都按这个提交写，代码改了要在同一个提交里跟着改（[`process.md` 第 10 节](../process.md)）。
- **现状 vs 提议**：owner 已对 `migration.md` 点头（Q1–Q4 的回答是 [D31](../decisions/0031-entry-budget-plan-a.md)–[D34](../decisions/0034-component-catalog-dev-only.md)），按它分步迁移。`tokens.md` 第 9 节的层级刻度已在第 1 步落地，是规范；菜单和弹出卡片已在第 2 步换成 Radix（`ui/src/overlays/`，`components.md` 第 9–12 节）；对话框在第 3 步统一到 `ui/src/overlays/ModalDialog.tsx`；手机右滑返回在第 4 步上线（`ui/src/phoneSwipe*.ts`）；`file:line` 按集成分支 `ds/integrate`；`tokens.md` 第二部分的其余合并方案仍是提议。其余内容是对现有代码的描述，可以当依据。
- 已经在跑的硬约束仍以 [`AGENTS.md`](../../AGENTS.md) 为准：动效令牌（"Web UI motion"）、侧栏拖动（"Sidebar order and pins"）、时间线性能（"Web timeline performance"）、预览方式（"Fast UI preview against a running server"）。

## 阅读顺序

| 顺序 | 文件 | 管什么 | 什么时候读 |
|---|---|---|---|
| 1 | [`principles.md`](principles.md) | 八条设计原则 P1–P8，每条有来源决定、检查方法、已知违反项；§9 冲突时的取舍顺序；§10 PR 评审清单 | 动任何界面之前 |
| 2 | [`tokens.md`](tokens.md) | 颜色、字体、间距、圆角、描边、阴影、模糊、动效的真实取值（第一部分，带 `file:line`）；§9 z-index 现状、问题 Z1–Z7、提议的 13 层刻度和每一处的归层 | 写样式、加颜色或加层级之前 |
| 3 | [`components.md`](components.md) | 每个组件的状态、焦点、键盘、关闭方式、位置、层级、动效、触屏；已知问题 C1–C43（§24 按严重度汇总）；§25 待确认 | 改某个组件之前，先读它那一节 |
| 4 | [`migration.md`](migration.md) | 迁到 headless 库的分步方案：选型结论（§0）、体积实测（§2）、六步各改哪些文件、风险、owner 在预览里看哪里（§5）、owner 对 Q1–Q4 的回答（§7） | 参与迁移时；当前状态：**六步都已完成**（第 4 步不引入 Motion、右滑返回上线自研版；第 5 步 dnd-kit 不通过、保留自研拖动引擎），合在集成分支 `ds/integrate` 上，等 owner 预览和合并进 `main`；合并后的预览清单在"集成"一节 |
| — | [`review-2026-10.md`](review-2026-10.md) | 2026-10 设计评审：13 个分区的问题（带截图与 `file:line`）、"纸与猫"改版提案、令牌改动、备选方向、分阶段实施与预算、owner 待定问题；HTML 稿在 [`proposals/index.html`](proposals/index.html)，截图在 `review-2026-10/` | 提案，未实施；owner 在第 6 节选完再动代码 |
| — | [`ui-issues.md`](ui-issues.md) | owner 陆续报的界面问题清单（截图、初步判断、状态） | 进行中 |
| — | [`style-notebook-brutal.md`](style-notebook-brutal.md) | **已定（[D51](../decisions/0051-notebook-brutal-style.md)）**：把现有风格（新粗野墨线与硬影 + 笔记本拟物 + 毛玻璃）写下来并拧紧——层级梯 L3→L0、按压物理令牌、深色墨线、工作行；对上一轮"纸与猫"的保留与撤回（§8）；配色想法单独放 §9 讨论；HTML 稿在 [`proposals-notebook/index.html`](proposals-notebook/index.html)（含按压实验台） | 已定，第一波实施中 |
| — | [`new-page.md`](new-page.md) | 新页面、面板、设置页、对话框、组件的做法：选容器、Radix 与懒加载、令牌、7 种语言文案、目录页状态、无障碍、预算与测试、评审清单、要更新的文档；末节记录用两个现有组件走查时代码与步骤不一致处 | 开始做任何新界面时，和 `principles.md` 一起读 |
| — | 组件目录页 `ui/catalog.html`（只在开发时可用，[D34](../decisions/0034-component-catalog-dev-only.md)） | `components.md` §1–§23 的每个组件、每种状态，浅色/深色、1280/390 px、7 种语言，真组件 + 假数据；`npm --prefix ui run dev` 后打开 http://localhost:5173/catalog.html | 改组件之前先看它现在长什么样；改完对照 |

## 四份文件怎么互相引用

| 编号 | 定义在 | 被谁引用 |
|---|---|---|
| P1–P8（原则） | `principles.md` | `components.md` 每条问题标它违反的原则 |
| C1–C43（组件问题） | `components.md` | `principles.md` 每条原则下的"已知违反" |
| Z1–Z7（层级问题）、`--z-*` 刻度 | `tokens.md` §9.2、§9.3 | `components.md` 各组件的层级、`migration.md` 第 1–3 步 |
| D01–D34（owner 决定） | [`../decisions/`](../decisions/README.md) | 全部四份 |

## 和设计有关的决定

| 决定 | 内容 |
|---|---|
| [D02](../decisions/0002-measure-interaction-cost.md) | 界面改动要量交互开销，预算写成会失败的测试 |
| [D16](../decisions/0016-optimistic-send-receipts.md) | 乐观发送和送达/已读回执 |
| [D17](../decisions/0017-quiet-message-action-row.md) | 消息下方安静的操作行 |
| [D18](../decisions/0018-progressive-blur-scope.md) | 渐变模糊的使用范围；新消息胶囊不能被盖住 |
| [D19](../decisions/0019-arrival-spring-cascade.md) | 新消息到达的弹簧级联 |
| [D20](../decisions/0020-phone-push-and-boot-screen.md) | 手机列表↔对话推入、启动画面 |
| [D21](../decisions/0021-sidebar-reorder-and-pins.md) | 侧栏拖动排序和置顶 |
| [D22](../decisions/0022-preview-via-dev-remote.md) | 用 `npm run dev:remote` 预览，不为小改动发 rc |
| [D23](../decisions/0023-no-second-cat-in-header.md) | 顶栏不放第二只猫 |
| [D28](../decisions/0028-headless-design-system-migration.md) | 采用 headless 库、保留 TOFI 外观；先出方案，再逐步迁移并预览 |
| [D31](../decisions/0031-entry-budget-plan-a.md) | 迁移的库全部懒加载，入口预算不动 |
| [D32](../decisions/0032-step1-stacking-changes-accepted.md) | 接受第 1 步的层级变化，逐条预览 |
| [D33](../decisions/0033-phone-swipe-back-experiment.md) | 手机右滑返回：自研和 Motion 各做一版对照 |
| [D34](../decisions/0034-component-catalog-dev-only.md) | 组件目录页只在开发时可用 |

## 改设计规范时

- owner 有新的界面决定：在 [`../decisions/`](../decisions/README.md) 加一条，同一提交里改 `principles.md`（必要时改 `tokens.md`、`components.md`）。
- 改了组件行为：同一提交里更新 `components.md` 对应一节和 §24 汇总。
- 迁移每做完一步：在 `migration.md` 里标完成并写提交号，同时更新 [`../product/roadmap.md`](../product/roadmap.md)。
