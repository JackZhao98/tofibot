# 新页面怎么做

给要在 `ui/src/` 里加一个页面、面板、设置页、对话框或组件的人（或 AI 会话）。按顺序走，每一步都指向已有的文档章节和代码；这里不立新规矩，只把散在 `AGENTS.md`、`tokens.md`、`components.md`、`principles.md`、`process.md` 里的现行规则串成一条线。

两条出发点：

- owner："按照 Radix 来，用户体验尽量不自创"。能用已有的积木和已装的 Radix 原语就不自己写交互。
- 以 Anthropic 的产品为对标：文案、交互先看它们怎么做（[P5](principles.md)，PR 说明里写一句"主流产品怎么做"）。

写之前先读 [`principles.md`](principles.md) 的 P1–P8，再读 [`components.md`](components.md) 里最像的那个组件的一节，并在组件目录页（`cd ui && npm run dev`，打开 http://localhost:5173/catalog.html）里看它现在长什么样。

## 1. 选容器

| 要做的 | 用什么 | 依据 |
|---|---|---|
| 设置里的一个页 | 在 `SettingsShell.tsx` 加一个 `SettingsTab`（`:13`）和 `groups` 里的一项（`:24-29`），在 `settings/SettingsPages.tsx` 的 `switch` 里渲染；页面内容用 `SettingsSection` / `SettingsCard` / `SettingsRow`（`settings/components.tsx`）。页名和说明是 `settings:shell.page.<id>.name/description` 两个键 | `components.md` §17 |
| 设置页里的一行开关或按钮 | `SettingsRow`；开关沿用 `<input type="checkbox" role="switch">` + `.debug-toggle`（见第 2 节） | `ui/src/ExtensionPanel.tsx`（内置 Skill 开关） |
| 按需打开的面板（设置、Bot 电脑、任务板） | 单独的懒加载块，在 `ui/src/lazyPanels.tsx` 的 `loaders` 里登记，空闲时预热；入口包不许带它 | `AGENTS.md` "Web timeline performance"、`test:shell-budget` |
| 模态对话框、确认框、底部抽屉 | `ModalDialog` / `ConfirmDialog`（`ui/src/overlays/ModalDialog.tsx`），只从懒加载块里 import；显式选 `closeOnEscape`、`closeOnOutside`、`locked`、`initialFocus`、`returnFocus` | `AGENTS.md` "Web UI dialogs"、`components.md` §8 |
| 菜单、弹出卡片 | `OverlayMenu` / `OverlayPopover`（`ui/src/overlays/OverlayHost.tsx`），触发器仍是自己的 `<button>`，带 `aria-haspopup`、`aria-expanded`、`onKeyDown={arrowOpens(setOpen)}` | `AGENTS.md` "Web UI menus and popovers"、`components.md` §9–§11 |
| 悬停提示 | `data-hint`（由 `ActionHints` 读），不用 Radix Tooltip | 同上 |
| 通知、横幅 | `components.md` §18 现有的 toast 和 `Banner`，不另造 | |

不要新开的东西：第二套问人机制（所有 Bot 向人提问走 `internal/app/questions.go` 那一个，`AGENTS.md` "Asking a human"）；原生 `<dialog>`（只有图片灯箱和文件预览保留，理由在 "Web UI dialogs" 末尾）；右键菜单用 Radix ContextMenu（侧栏拖动引擎占着长按）。

## 2. 什么时候用 Radix，怎么保持懒加载

- 仓库里只装了四个 Radix 包，版本精确钉死：`react-alert-dialog`、`react-dialog`、`react-dropdown-menu`、`react-popover`（`ui/package.json`）。**对话框、菜单、弹出卡片这三类交互必须走它们**（经上面的 `ModalDialog` / `OverlayMenu` / `OverlayPopover`），不手写焦点陷阱、Escape 栈和外点关闭。
- 其余控件（开关、分段选择 `Segmented`、折叠 `Disclosure`）现在是自己的原生元素加 CSS，没有 Radix 版本。想引入新的 `@radix-ui/*` 包是架构改动：先问 owner，四个包要保持 npm 去重到同一份底层（"Web UI dialogs" 末尾），并跑 `test:dialogs`、`test:overlays`、`test:shell-budget`。别因为"按 Radix 来"就顺手加依赖。
- 保持懒加载的三条硬规则（都有测试）：
  1. Radix 只在懒加载块里：对话框 import `ModalDialog` 的文件本身必须是懒加载块（`lazyPanels.tsx` 的 `loaders`，或它的子模块）；入口包里不能出现（`test:dialogs` 会爬构建产物，`test:shell-budget` 守入口体积）。
  2. 菜单和弹出卡片不要自己包 `React.lazy` + `Suspense`（React 19 会把已显示的边界多留约 300 ms，"Web UI menus and popovers"）；用 `OverlayMenu` / `OverlayPopover`，它们自己在空闲时取块。
  3. 菜单和弹出卡片由拥有者"只在打开（或正在离开）时渲染"，现有 class 当 `className`，现有标记当子节点；大组件只设置不渲染的状态放 `OverlaySlot` + `SlotView`，免得打开菜单重绘整个 Workspace。

## 3. 令牌

只用令牌，不写字面量。完整取值见 [`tokens.md`](tokens.md)。

| 方面 | 怎么用 | 守卫 |
|---|---|---|
| 颜色 | 用 `ui/src/design-tokens.css` 里的语义令牌（浅色 `:root`，深色 `:root[data-theme="dark"]`）。填充色（`--clay` 等）不当前景 `color:`，要用成对的 `*-text` | `test:final-v2-tokens` |
| 间距、圆角、阴影 | 看 `tokens.md` §3–§6 的现状，贴近同类组件已有的值；这几类现在没有测试守，也不是全走令牌，**不要为你的页面新发明一档** | 无 |
| 动效 | 时长 `--m-instant/press/quick/base/panel-in/panel-out/flight`，曲线 `--ease-out/settle/spring/inout/in`；Web Animations 用 `ui/src/motion.ts` 的 `MOTION`、`EASE`，弹簧用 `SPRING`；离开比进入短；每个动画写清"它解释什么"，并有 `prefers-reduced-motion: reduce` 分支（瞬间到位或只剩淡入淡出） | `test:motion-tokens`（字面时长 < 800 ms、`cubic-bezier()`、裸 `ease`、字面 `stiffness`/`damping`/`mass`、缺 `-webkit-` 孪生的 `backdrop-filter` 都会挂；旧的遗留是 `motion-tokens-baseline.json`，只减不增） |
| 层级 | `z-index` 只写 `var(--z-*)`、`calc(var(--z-*) ± 1)` 或组件自己层叠上下文内的 -1..3；TS 里写 `"var(--z-*)"`，不写数字；浮层 portal 进 `layerRoot()`（`#tofi-layers`），不进 `document.body`；在 `aria-modal` 容器里打开的东西用 `calc(var(--z-dialog) + 1)`（`data-layer="dialog"`） | `test:layers` |
| 焦点环 | 不自己画，用 `focus-ring.css` 的那一种；组件只能改 offset | `ui/scripts/focus-audit.mjs` |
| 模糊 | 玻璃和渐变模糊的范围见 D18：渐变模糊只给 Electron 和 iOS 客户端，网页保留现有模糊 | `test:motion-tokens`（`-webkit-` 孪生） |

## 4. 文案和 7 种语言

规则全文在 `ui/src/locales/README.md`，这里只列走查要点。

1. 所有用户可见文字（文本、`aria-label`、`title`、`placeholder`、`data-hint`、`alt`、存进 state 的错误、toast）进 `ui/src/locales/<lang>/<namespace>.json`，用 `useTranslation` 从 `./i18n` 取（不直接 import `react-i18next`）。不在模块顶层调用 `t`。命名空间固定，不新增。
2. **7 种语言一次写全**：`en`、`zh-CN`、`zh-TW`、`ja`、`ko`、`de`、`fr` 都在 `COMPLETE_LANGUAGES`（`ui/src/i18n/languages.ts:15`），缺任何一个键 `test:i18n` 就挂。复数按各语言的 CLDR 类别（`_one`/`_other`，法语还有 `_many`，中日韩只有 `_other`）；一句话一个键，不拼句子；行内标记用 `<Trans>` 具名标签。
3. 语气：`locales/README.md` "Tone" 和 [`principles.md` P5](principles.md)——短、直接、说发生了什么和下一步，说主流产品里见过的话，不用助手腔和自己发明的词；英文按钮和标签用句首大写。"业界术语"的具体适用范围 owner 还没有定稿（principles.md P5 里标了"待确认"），拿不准的词按对标产品的原话来。
4. 服务端错误：显示 `errorText(cause, "<命名空间>", t("…兜底"))`（`ui/src/i18n/errors.ts`），它按稳定的 `error.code` 查 `<ns>:apiError.<code>`；新的错误码在后端给，前端补 `apiError.<code>` 键。不要直接显示 `cause.message`，也不要靠消息文字分支。
5. 日期、数字用 `ui/src/i18n/format.ts` 的帮手，时区用 `useUserTimezone()`。
6. 测试里要固定语言，用 `openUiModules({language})`，不要用 `tsc --ignoreConfig`。

## 5. 把状态加进组件目录

D34：`docs/design/components.md` 里每个组件的每种状态，在 `ui/src/catalog/sections/` 里都有一个真组件加合成数据的状态；画不出来的写进该节的 `gaps`。

1. 在 `components.md` 找到对应的编号小节（新种类的组件加新小节，目录页按小节编号一一对应，`test:catalog` 会断言）。写它的文件、状态、行为契约（焦点、键盘、关闭方式、层级、动效、触屏）、测试。
2. 在对应的 `ui/src/catalog/sections/*.tsx` 加状态：`inline`（原地渲染）、`overlay`（盖住页面的，给 `probe` 和 `keyboard` 好让测试查"在最上面、键盘能关"）或 `app`（需要整个 Workspace）。服务端用合成数据（`isolated({...})`），永远不碰 `/api`。把加载中、空、出错、忙、禁用各画一个。
3. 目录页不许被产品代码 import，也不许进 `dist/`（`test:catalog` 守）。

## 6. 无障碍

按 [`principles.md` P8](principles.md) 逐条走，并真的走一遍：

- **键盘**：只用键盘能到达、操作、关闭；一组同类控件一个 Tab 停靠点、组内方向键；打开的东西接住焦点，关闭后焦点回到打开它的地方；Escape 只关最上面那层（应用自己的 Escape 处理要检查 `defaultPrevented`）；关掉、折叠、在背后的东西用 `inert`。声明了 ARIA 角色就要有对应的键盘行为（`Segmented` 是现成的反例，C29）。
- **触屏**：点击区至少 44 px；只在悬停时出现的东西要有轻点入口；390 px 宽、`hasTouch: true` 跑一遍（写法见 `test:sidebar-pins`）。
- **读屏**：状态变化用 `role="status"`，错误用 `role="alert"`；开关带 `aria-label`；列表带名字。macOS VoiceOver 听一遍。
- 焦点环只给键盘；新浮层把 `focus-audit.mjs` 接进自己的测试。

## 7. 交互开销预算和要写的测试

D02：高频交互要量开销，预算写成会失败的测试。一次性的设置页通常不需要新预算，但要确认没拖慢已有的。

| 你做了什么 | 加或扩展哪个脚本 |
|---|---|
| 设置页、设置里的开关或列表 | 扩展 `test:settings-shell`（带合成 `/api` 的行为测试，内置 Skill 开关的断言在 `test-settings-shell.mjs:87,272,521-526`）；自动行为页扩展 `test:automation-settings`；外观和键盘走 `test:settings-polish` |
| 新对话框或抽屉 | `test:dialogs`（契约、Radix 不进入口包）；放在懒块里 |
| 新菜单或弹出卡片 | `test:overlays`（键盘、关闭、定位、层级、打开开销） |
| 新的卡片（问题、审批、交接、常驻授权） | `test:question-poll`、`test:handover-cards`、`test:standing-allowance` 里挑一个最近的扩展 |
| 消息列表、流式、滚动、输入框、侧栏 | 对应预算脚本，见 `process.md` 3.4 的"按改动范围挑套件"表 |
| 新页面或面板 | 至少让 `test:shell-budget`（入口体积、设置首帧）仍然通过；新的懒块要确认没进入口 |

测试写法的约定在 [`../conventions.md`](../conventions.md) 的"测试"一节：只用合成数据；修复要有"撤掉就红"的测试（`process.md` 3.2）；用退出码证明，汇报时写明没有 `SKIP`（3.1）。

CI 现在跑的 UI 守卫见 `process.md` 3.3。时间敏感的几项（`test:dialogs`、`test:overlays` 的打开耗时，`test:shell-budget` 的帧预算）在 CI 里用 `TOFI_SKIP_TIMING=1` 跳过，本地照跑；改了这些交互的 PR 要在本地跑完整版并汇报。

## 8. 评审清单

照抄 [`principles.md` §10](principles.md) 到 PR 说明里，每条写结果：

```
- [ ] P1 静止时没有多出常驻控件；行内控件无容器、--muted 色
- [ ] P2 四个尺寸（390/768/1024/1280）跑过遮挡扫描片段，必须点得到的控件都是 true
- [ ] P2 浮层位置按实测尺寸夹视口；层级用 tokens.md 9.3 的刻度
- [ ] P3 点击当帧有变化；慢请求 300 ms 后才出加载；有失败用例且失败可见
- [ ] P4 每个新动画写了"它解释什么"、用的令牌、减少动效下的表现；test:motion-tokens 退出码 0
- [ ] P5 新文案在 locales 里，test:i18n 退出码 0；写了对标产品怎么做
- [ ] P6 新增的 data-desktop / isDesktop 分支都写了原因
- [ ] P7 高频交互有预算测试；撤掉修复时测试会失败；列出跑过的测试和退出码
- [ ] P8 只用键盘走过一遍；触屏 390 px 跑过；焦点回到原处
- [ ] 改了组件行为的，同一提交里更新 docs/design/components.md
```

再加本文新增的三项：

```
- [ ] 目录页有这个组件每种状态（或 gaps 里写明原因），test:catalog 退出码 0
- [ ] 对话框、菜单走 ModalDialog / OverlayMenu，Radix 只在懒块里（test:dialogs、test:overlays、test:shell-budget）
- [ ] 7 种语言都有键；服务端错误走 errorText 而不是 cause.message
```

## 9. 要同步更新的文档

同一个提交里（`process.md` 第 10 节，D27）：

| 改了什么 | 更新 |
|---|---|
| 新组件、新状态、组件行为变了 | `docs/design/components.md` 对应小节；`ui/src/catalog/sections/` |
| 新令牌或令牌取值变了 | `docs/design/tokens.md` |
| 设计原则或 owner 的新决定 | `docs/design/principles.md` + `docs/decisions/` 新文件 |
| 实现约定变了（新的 AGENTS.md 级约束） | `AGENTS.md` 对应一节 |
| 新测试脚本或 CI 守卫 | `ui/package.json`、`docs/process.md` 3.3/3.4 |
| 合并 | `docs/product/roadmap.md` |

## 10. 用最近的两个组件对一遍

这一节记录按本文走查现有组件时，真实代码和上面步骤不一致的地方，供后来的人判断是改代码还是改文档。核对基线：`25930a5`。

**设置 › Skills 的内置开关**（`ui/src/ExtensionPanel.tsx:66`；决定 [D41](../decisions/0041-builtin-deliverable-skills.md)）

| 步骤 | 实际 | 与本文是否一致 |
|---|---|---|
| 容器 | 设置页 `skills` tab 里的 `SettingsRow inline` | 一致 |
| Radix | 没有用：原生 `<input type="checkbox" role="switch">`，样式是 `.debug-toggle` / `.auto-switch`（`settings-system.css:62`、`settings/automation.css:9`）。仓库没有 Radix Switch 依赖 | 与"按 Radix 来"字面不同，但与现状一致：本文第 2 节只要求对话框、菜单、弹出卡片走 Radix |
| 文案 | `extensions:skills.builtin.*`，`aria-label` 带名字 | 一致 |
| 错误 | `act()` 的失败路径没有核对 | 待确认 |
| 目录页 | `components.md` 和目录页都没有这个开关和"内置"标签的状态（只有 §8 的"技能访问 sheet"） | **不一致**：第 5 步缺 |
| 触屏 | 开关 38×23 px（`settings-system.css:62`），整行是否有 44 px 点击区没有量过 | 待确认，第 6 步 |
| 测试 | `test:settings-shell` 覆盖开关写请求（`test-settings-shell.mjs:87,272,521-526`） | 一致 |

**常驻授权列表**（`ui/src/StandingAllowances.tsx`；决定 [D24](../decisions/0024-standing-tool-allowance.md)）

| 步骤 | 实际 | 与本文是否一致 |
|---|---|---|
| 容器 | 自动行为页底部，`SettingsSection` + `SettingsCard`（`AutomationSettings.tsx:169`） | 一致 |
| 数据 | 事件驱动刷新（`tofi:standing-allowances-changed`），`AbortController`，没有轮询 | 一致 |
| 文案 | `settings:automation.standing.*` | 一致 |
| 错误 | 直接显示 `cause.message`（`:51`、`:73`），没有走 `errorText` | **不一致**：第 4 步第 4 点。同样的写法在 `AutomationSettings.tsx` 的保存失败路径也有 |
| 时间格式 | 自己 `new Intl.DateTimeFormat(intlLocale(), …)`，没有用 `format.ts` 的 `formatDateTime` | 小偏差，第 4 步第 5 点 |
| 目录页 | `catalog/sections/cards.tsx:108` 有列表、空、加载失败三个状态；`components.md` §16 | 一致 |
| 测试 | `test:standing-allowance`、`test:automation-settings` | 一致 |

这两处"不一致"不是本文要强制改的：本文只记录，要不要改是单独的修复任务。
