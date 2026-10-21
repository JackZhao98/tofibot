# Web UI 迁移到 headless 组件库：分步方案

> 状态：**owner 已点头，按本文分步迁移**（2026-10-21；§7 的 Q1–Q4 已回答，见 [D31](../decisions/0031-entry-budget-plan-a.md)–[D34](../decisions/0034-component-catalog-dev-only.md)）。**第 1、2、3 步已完成**（分支 `ds/step1-layers`、`ds/step2-menus`、`ds/step3-dialogs`，在集成分支 `ds/integrate` 上合在一起，等合并进 `main` 和 owner 预览）；第 4–6 步见各节。每一步都要预览（[D28](../decisions/0028-headless-design-system-migration.md)）。
> 快照：提交 `748c774`（分支 `docs/system-opus`），体积实测日期 2026-10-20（PDT）。
> 相关文档：层级刻度和每一处 z-index 归到哪一层，以 [`tokens.md` §9](tokens.md) 为准，本文不另定一套；本文只写"怎么迁"。
> 相关决定：[D02 量交互开销](../decisions/0002-measure-interaction-cost.md)、[D18 渐变模糊范围](../decisions/0018-progressive-blur-scope.md)、[D19 到达级联](../decisions/0019-arrival-spring-cascade.md)、[D20 手机推入](../decisions/0020-phone-push-and-boot-screen.md)、[D21 侧栏拖动和置顶](../decisions/0021-sidebar-reorder-and-pins.md)、[D22 用 dev:remote 预览](../decisions/0022-preview-via-dev-remote.md)、[D30 常设规则](../decisions/0030-standing-operating-rules.md)。

## 0. 结论

| 领域 | 选什么 | 一句话理由 | 能不能进入口包 |
|---|---|---|---|
| 层级与 portal | 自己做：`--z-*` 令牌 + 一个 portal 根节点 | 不需要库，[`tokens.md` §9.3](tokens.md) 的刻度落地即可（**第 1 步已完成**） | 实测 +49 B |
| 菜单、弹出卡片 | **Radix**：`DropdownMenu`、`Popover` | 不带样式，`asChild` 可以把现有元素和类名原样留住，外观不变；比 React Aria 小 17–21 KB | 不能，懒加载（**第 2 步已完成**：懒加载块 30,699 B，入口 +5 B） |
| 悬停提示 | **保留自研的 `ActionHints`**，不用 Radix Tooltip | 现在是整页只有一个委托监听器，行里没有任何组件；Radix Tooltip 要在每个图标外套一层组件，懒加载块 19 KB | — |
| 对话框 | **Radix**：`Dialog`、`AlertDialog` | 焦点圈定、Esc 只关最上一层、关闭后焦点回到原处，原来每个对话框各写一遍（**第 3 步已完成**，`ui/src/overlays/ModalDialog.tsx`） | 不能，要懒加载（实测 Radix 块 13,981 B，入口反而 −1,148 B） |
| 动效 | **暂不引入 Motion**，第 4 步只做一次对照实验 | 到达级联已经是预先算好的弹簧关键帧交给 WAAPI 在合成线程跑；Motion 的 React 组件版本最少 30.8 KB，而且必须进入口包 | 不能 |
| 拖动 | **保留自研引擎**，第 5 步只做一次对照实验 | 引擎懒加载块 3,480 B，拖动过程中 React 不渲染；dnd-kit 15.3 KB 起，且 `useSortable` 让每一行在拖动中重新渲染 | 不能 |
| 组件目录页 | 自己做，仿照 Motion Lab 单独打包 | 不占应用的任何一个包 | 不占 |

最硬的约束是体积：**写方案时入口包离预算只剩 741 字节**（§2；第 1 步之后是 692 字节，第 2 步之后是 687 字节）。所以本方案里任何一个库都只能在懒加载的块里出现，而且第 2、3 步要把现有的弹层代码一起搬出入口包，用省下来的字节支付懒加载的入口开销。owner 选了这条路（方案 A，[D31](../decisions/0031-entry-budget-plan-a.md)），不放宽预算（§2.4 的方案 B 不采用）。

## 1. 现状：要迁的东西在哪

### 1.1 弹层和对话框清单

"在哪个包"一栏来自对 `748c774` 的生产构建按类名 grep 产物（方法见附录 A）；`index` 就是入口包。

| 组件 | 代码 | 现在怎么做的 | 在哪个包 | 钉住它的测试 |
|---|---|---|---|---|
| 悬停提示 `.action-hint` | `ui/src/InteractionSystem.tsx:51-84` | 整页一个委托监听器读 `data-hint`，portal 到 `document.body`（第 1 步起是 `#tofi-layers`） | index | `test:chat-motion`（`scripts/test-chat-motion.mjs:358`） |
| 发送回执提示 `.send-receipt-tip` | `ui/src/SendReceipt.tsx:46` | 行内 `role="tooltip"`，z 5（第 1 步起 `--z-tooltip`）。宽屏下并不被所在行封顶：对话窗格不是层叠上下文，它直接和侧栏、浮卡在根层叠上下文里比。**第 2 步起**是已读标记上的 `data-hint`（`data-hint-tone="quiet"`），由 `ActionHints` portal 进 `#tofi-layers` | index | `test:send-receipts`、`test:overlays` |
| 侧栏右键菜单 `.sidebar-context-menu` | `ui/src/App.tsx:2462` | `position:fixed` 坐标定位；右键和拖动引擎的长按都会打开（`ui/src/sidebarDndEngine.ts:308`、`:351`）。**第 2 步起** Radix DropdownMenu，锚在 (x, y)，状态在 `OverlaySlot` | index | `test:sidebar-pins`（按 `menuitem` 角色断言，`scripts/test-sidebar-pins.mjs:266-312`、`:458-464`） |
| 新建菜单、顶栏菜单 | `ui/src/App.tsx:610-611`（`useSurfacePresence`）、渲染在 `:2447`、`:2477` | 绝对定位，跟着父级层叠上下文。**第 2 步起** Radix DropdownMenu | index | 待确认（未找到按类名断言的测试）；第 2 步起 `test:overlays` |
| 账户菜单 `.sidebar-account-menu` | `ui/src/OwnerSession.tsx:182` | 绝对定位 z 150（第 1 步起 `--z-menu`），实际被侧栏封顶（第 1 步起侧栏是 `calc(var(--z-chrome) + 1)`，`tokens.md` Z3）。**第 2 步起** Radix DropdownMenu，不再封顶 | index | 无；第 2 步起 `test:overlays` |
| 输入框附件菜单 `.composer-add-menu` | `ui/src/App.tsx:3538` | 绝对定位，被输入框封顶（Z3）。**第 2 步起** Radix DropdownMenu，不再封顶 | index | 待确认；第 2 步起 `test:overlays` |
| 表情反应菜单 `.reaction-context-menu` | `ui/src/MessageReactions.tsx:77` | portal，`role="dialog"`，表情选择器懒加载（`:7`）。**第 2 步起** Radix Popover | index | `test:chat-motion`（`:399-402`） |
| 身份卡 `.bot-identity-card` | `ui/src/BotIdentityCard.tsx:47` | 悬停/长按出现，portal，退场靠 `useSurfacePresence(position, 180)`（`:19`）。**第 2 步起** Radix Popover；界面上没有入口（第 2 步"事实更正"） | index | 无；第 2 步起 `test:overlays`（单独的开发页） |
| MCP 更多菜单 `.mcp-menu` | `ui/src/MCPSettings.tsx:232-233` | 绝对定位。**第 2 步起**走同一个宿主（Radix DropdownMenu），层级 `calc(var(--z-dialog) + 1)` | SettingsPanel（懒） | `test:settings-shell`、`test:overlays` |
| 删除对话框、重新指派主助理 | `ui/src/DialogShell.tsx:4-14`，用在 `DeleteConversationDialog.tsx:41`、`PrimaryAssistant.tsx:97` | 自写 Tab 圈定和 Esc；`role="alertdialog"` | DeleteConversationDialog 懒（1,027 B）；`PrimaryAssistant` 在 index（`App.tsx:43` 静态导入） | `test:primary-followups` |
| 表单 sheet `.sheet-overlay` | `ui/src/AdminSheets.tsx:35`、`ui/src/settings/SkillAccess.tsx:82` | portal，点遮罩关闭 | AdminSheets、SettingsPanel（懒） | `test:settings-polish` |
| 全屏只读会话 | `ui/src/ViewOnlyChat.tsx:79` | portal，`aria-modal` 只在非卡片形态 | ViewOnlyChat（懒） | 无 |
| VM 授权 | `ui/src/VMOAuthDialog.tsx:107` | portal | VMOAuthDialog（懒） | 待确认 |
| 交接（接管屏幕） | `ui/src/TakeoverDialog.tsx:68` | portal，里面是 `RemoteDesktopControl`，键盘要转发给远端 | QuestionCard（懒） | `test:handover-cards`、`scripts/test-remote-desktop-control.mjs` |
| 引导 | `ui/src/Onboarding.tsx:82` | `role="dialog" aria-modal`，自写圈定 | Onboarding（懒） | `test:onboarding` |
| 终端内确认 | `ui/src/TerminalPanel.tsx:49-50` | 面板内的 `alertdialog`，局部层 | TerminalPanel（懒） | 待确认 |
| 原生 `<dialog>` ×4 | `MessageAttachment.tsx:25,76`、`FileViewer.tsx:124`、`SettingsShell.tsx:101`、`ComputerResources.tsx:16` | `showModal()`，进浏览器顶层 | 各自所在包 | `test:file-preview`（FileViewer） |
| 权限提示 | `ui/src/PermissionCoach.tsx:95` | 非模态 `aside role="dialog"`，右下角 | PermissionCoach（懒） | 无 |

另有一处依赖现有写法的代码：`ui/src/App.tsx:768` 用 `document.querySelector('[role="dialog"][aria-modal="true"]')` 判断"有模态开着就不响应桌面客户端菜单快捷键"。Radix Dialog 只设 `role="dialog"`，**不设 `aria-modal`**（在 `@radix-ui/react-dialog@1.3.1` 的 `dist/index.mjs` 里 grep `aria-modal` 为 0），迁移时必须显式加上，否则这道闸会失效。第 3 步：`ModalDialog` 给每个对话框加 `aria-modal="true"`，闸改认 `[aria-modal="true"]`（原来的选择器漏掉了 `role="alertdialog"` 的删除确认），`test:dialogs` 模拟 `tofiDesktop.onCommand("toggle-sidebar")` 守着。

### 1.2 层级

现状和问题（Z1–Z7）见 [`tokens.md` §9.1-9.2](tokens.md)。写方案时：产品 CSS 89 处 z-index、31 个不同的值，最大 1200（`onboarding.css:5`），TS 里还有 2147483000（`sendFlight.ts:19`）。第 1 步之后：46 处全局层全部走 `--z-*`，43 处组件内部的局部值，TS 里只剩 `"var(--z-drag)"` 一处；Z1、Z2、Z5、Z6 已修。第 2 步之后：全局层 45 处（去掉顶栏抬高和行内回执提示，加了模态里菜单的 `data-layer="dialog"`），Z3、Z4 已修。第 3 步加了两个确认框的遮罩（全局层 47 处），Z7 已修。

### 1.3 动效

| 动效 | 代码 | 做法 | 守着它的测试 |
|---|---|---|---|
| 新消息到达级联（D19） | `ui/src/arrivalCascade.ts:79-98`，参数 `ui/src/motion.ts:18-30` | 每条消息的欠阻尼弹簧曲线预先采样成 26 帧关键帧（`cascadeKeyframes`，`:37-48`），用 WAAPI 动 `translate`，布局和 `scrollTop` 全程不变 | `test:arrival-glide` |
| 手机列表↔会话推入（D20） | `ui/src/styles.css:317-325` | CSS transition，280 ms 进 / 220 ms 退（令牌 panel-in / base） | `test:shell-budget` 第 3 节（`scripts/test-shell-budget.mjs:223-290`，断言 `transitionend` 的 `elapsedTime` 正好 280 / 220） |
| 侧栏行进出、换位 | `ui/src/App.tsx:491-494` | WAAPI FLIP | `test:sidebar-pins` |
| 回答高度、移动 | `ui/src/App.tsx:2678`、`:2853`、`:3051` | WAAPI | `test:chat-motion` 等 |
| 弹层退场 | `ui/src/InteractionSystem.tsx:33-41` `useSurfacePresence` | 用定时器把关掉的内容多留 N 毫秒 | — |
| 动效词汇 | `ui/src/design-tokens.css`、`ui/src/motion.ts:4-11` | 所有时长、曲线来自令牌 | `test:motion-tokens` |

Motion Lab 自己也有一个弹簧积分器，会把弹簧"预烘焙进 `linear()` 缓动"（`ui/src/motion-lab/lib/spring.ts:1-2`），这和 Motion 库对弹簧做硬件加速的思路是同一类。

### 1.4 拖动

`ui/src/useSidebarDnd.ts`（38 行）只持有跨渲染的状态，在空闲时（`requestIdleCallback`，`:33-35`）加载 `ui/src/sidebarDndEngine.ts`（395 行）。引擎直接操作 DOM：拿起时量一次所有位置，移动中每帧一个 transform，不读布局，放下时 React 只渲染一次（文件头注释 `:5-16`；AGENTS.md "Sidebar order and pins"）。长按 380 ms（`:39`），Alt+方向键移动，两个区域（置顶格子 dock 和列表），读屏播报。`test:sidebar-pins` 守着它（60 个会话的帧预算、390 px 触摸、键盘、减弱动效）。

## 2. 体积实测

### 2.1 方法

- 在工作区**外面**复制一份 `ui/`（`748c774`），`npm ci` 后装候选库，构建方式和 `ui/scripts/test-shell-budget.mjs:29-46` 完全一样：`configFile:false`、只用 `@vitejs/plugin-react`，入口包 = `index.html` 里 `src`/`href` 引用的所有 `.js`，每个文件用 Node `zlib` level 9 压缩后求和。
- 每个候选写一个探针模块，真正渲染 TOFI 会用到的那几个原语（不渲染会被摇树摇掉）。探针入口先 `import "../main"`，再：
  - **进入口**：静态导入探针，挂到 `window.__probe` 上防止被摇掉；
  - **懒加载**：`window.__probe = () => import("./probe")`。
- "懒加载块"= 全部 JS 的 gzip 总和的增量减去入口增量。懒加载一栏的"入口 +"里包含探针自己那一行 `import()` 桩代码，所以是上限。
- 工具链：vite 8.3.3、rolldown 1.2.14、react 19.3.0、@vitejs/plugin-react 6.1.1。复现步骤见附录 A；探针目录已经删掉。
- 事实核查复测（2026-10-20，同一提交、同一方法）：`748c774` 入口包 315,064 B（6 个文件逐个相同）、探针基线 315,062 B、`sidebarDndEngine` 等 4 个懒加载块字节数都与本文一致；抽查两个候选的"进入口"增量：Radix Dialog +12,508 B（表中 12,592）、`motion/mini` +2,969 B（表中 2,987），差几十字节来自探针写法不同，不影响 §2.4 的结论。其余候选的数字没有复测。Radix 的两条源码事实（`@radix-ui/react-dialog@1.3.1` 的 `dist/index.mjs` 不含 `aria-modal`；`@radix-ui/react-context-menu@2.4.1` 的 `dist/index.mjs:104` 是 700 ms 长按）已对 npm 包核对。

### 2.2 基线

| 项 | 字节（gzip） | 出处 |
|---|---|---|
| 预算 | 315,805 | `test-shell-budget.mjs:29`：`Math.floor(394_757 * 0.8)` |
| `748c774` 入口包实测 | **315,064**（6 个文件：`index` 226,799 + `tofi-avatar` 43,182 + `i18n` 39,745 + `jsx-runtime` 3,384 + `react-dom` 1,394 + `format` 560） | 附录 A |
| 余量 | **741** | — |

协调者给的"约 313 KB"已经过时，以上面的实测为准。

### 2.3 候选库的代价

基线（探针构建）为 315,062 B。所有数字为增量字节（gzip）。

| 候选（版本） | 探针里用到的原语 | 进入口 +B | 懒加载：入口 +B | 懒加载块 B |
|---|---|---|---|---|
| Radix Tooltip（`@radix-ui/react-tooltip` 1.4.1） | Provider/Root/Trigger/Portal/Content | 18,352 | 82 | 19,060 |
| Radix Popover（1.3.1） | Root/Anchor/Trigger/Portal/Content/Close | 22,440 | 78 | 23,177 |
| Radix DropdownMenu（2.2.1）+ ContextMenu（2.4.1） | Root/Trigger/Portal/Content/Item/Separator | 28,746 | 73 | 29,333 |
| **Radix DropdownMenu + Popover（第 2 步推荐组合）** | 受控 `open`、隐形锚点、`modal={false}` | 29,134 | 85 | **29,723** |
| Radix Tooltip + Popover + DropdownMenu + ContextMenu | 四件套 | 32,599 | 89 | 33,312 |
| Radix Dialog（1.3.1） | Root/Portal/Overlay/Content/Title/Description/Close | 12,592 | 76 | 13,237 |
| Radix Dialog + AlertDialog（1.2.1） | 两种对话框 | 12,926 | 93 | 13,685 |
| **第 2 步组合 + Dialog + AlertDialog（第 2、3 步合计）** | — | 30,255 | 84 | **30,903** |
| Radix 全部（四件套 + 两种对话框） | — | 33,987 | 78 | 34,527 |
| React Aria Components 1.22.1 | TooltipTrigger/Tooltip、DialogTrigger/Popover/Dialog、MenuTrigger/Menu/MenuItem、ModalOverlay/Modal | 50,948 | 288 | 51,574 |
| `@floating-ui/react-dom` 2.1.9（只定位） | useFloating + offset/flip/shift/autoUpdate | 6,857 | 70 | 7,473 |
| Motion 14.4.0 `motion/react` 完整 | `motion.div layout`、AnimatePresence、LayoutGroup、spring | 45,042 | 75 | 45,287 |
| Motion `motion/react` 精简 | LazyMotion + `m` + domAnimation | 30,827 | 73 | 31,144 |
| Motion `motion/react-mini` | `useAnimate`（WAAPI 包装） | 3,063 | 67 | 3,591 |
| Motion `motion`（命令式，无 React） | `animate()` + `spring` | 22,440 | 47 | 22,824 |
| Motion `motion/mini` | `animate()` | 2,987 | 49 | 3,540 |
| `@dnd-kit/core` 6.3.1 + `sortable` 10.0.0 + `utilities` 3.2.2 | DndContext、Pointer/Touch/Keyboard 传感器、SortableContext、useSortable、DragOverlay | 15,309 | 75 | 16,037 |
| `@dnd-kit/react` 0.5.1 | DragDropProvider + `useSortable` | 36,323 | 72 | 36,519 |

对照：现有懒加载块 `sidebarDndEngine` 3,480 B、`DeleteConversationDialog` 1,027 B、`AdminSheets` 1,339 B、`ViewOnlyChat` 2,877 B（同一构建，Node zlib level 9）。

### 2.4 读法

1. **没有任何一个候选能进入口包**，连最小的 `motion/mini`（+2,987 B）都超出 741 B 余量。
2. 懒加载边界本身很便宜：每个 `import()` 桩在入口包里 47–93 B（上限）。
3. Radix 的几个原语共用一大块底层（定位、外部点击、焦点、滚动锁），所以单个 Tooltip 就要 18 KB，而第 2、3 步全部加起来也只有 30.9 KB；Dialog 叠在菜单上只多约 1.2 KB（30,903 − 29,723）。
4. React Aria 比同等的 Radix 组合大 17–21 KB（51,574 对 34,527 和 30,903）。原因之一：它把 34 种语言的内置文案打进了包（在探针产物里能 grep 到 `"de-DE"`、`"ja-JP"`、`"zh-CN"` 等 34 个语言键和 `dismiss:"Schließen"` 这样的字符串），而 TOFI 已经有自己的 i18next 文案（`ui/src/locales/`）。
5. 所以走**方案 A**（owner 已定，[D31](../decisions/0031-entry-budget-plan-a.md)）：库全在懒加载块里，第 2、3 步把现有弹层代码搬出入口包来支付入口开销，每一步合并前入口包都不超预算。不采用的**方案 B**：把 `test-shell-budget.mjs:29` 的预算提高约 31 KB，直接静态导入 Radix，代码更简单，但入口包变大约 10%，首屏解析和执行时间要重新量（D02）。

## 3. 选型理由

### 3.1 Radix 还是 React Aria

| 维度 | Radix | React Aria Components | 对 TOFI 意味着什么 |
|---|---|---|---|
| 体积（§2.3） | 第 2、3 步合计 30.9 KB | 同类组合 51.6 KB | Radix 更小 |
| 保留外观 | 不带样式；`asChild` 把行为合并到 TOFI 现有元素上，类名照旧 | 也不带样式，但要用它的组件（`Button`、`Popover`）替换元素，状态走 render props / `data-*` | Radix 改动更小；现有测试按类名选元素（§1.1），类名必须留住 |
| 状态属性 | `data-state="open|closed"`、`data-side` | `data-entering`、`data-exiting`、`data-placement` | 都能接上现有的 `menu-enter`、`modal-enter` 关键帧（`ui/src/interaction-system.css:91`、`:103`） |
| 退场动画 | `Presence` 等 CSS 动画结束再卸载 | 同样支持 | 可以替掉 `useSurfacePresence` 的定时器 |
| 文案 | 没有内置文案 | 内置 34 种语言 | TOFI 用自己的 i18n，React Aria 的文案是多余的 |
| 触屏 | 标准指针事件 | `usePress` 对触屏的处理更细 | TOFI 的长按归拖动引擎管（§3.3），这项优势用不上 |
| 菜单可以锚定任意点 | `Popover.Anchor`；DropdownMenu 要借一个隐形 Trigger | `Popover` 有 `triggerRef` | 两者都能做，Radix 要做一次小实验（第 2 步风险 1） |

**选 Radix。** 用单个 `@radix-ui/react-*` 包，不用总包 `radix-ui`，依赖和许可证一目了然。新增的许可证文本放进 `ui/public/licenses/`（`ui/THIRD_PARTY_NOTICES.md:5` 说明随包依赖的声明放在那里）。

### 3.2 Motion

Motion 擅长三件事：带速度衔接、可中途打断的弹簧；自动 FLIP 的布局动画；`AnimatePresence` 退场。TOFI 的情况：

- 到达级联已经是预先算好的弹簧关键帧交给 WAAPI 动 `translate`（§1.3），在合成线程跑，主线程不参与。Motion 做的也是这种事，换过去拿不到更好的结果。
- 手机推入是 CSS transition，测试断言的是 `transitionend` 的时长（§1.3）。Motion 真正比它强的只有一种情况：**手指拖着走的边缘返回手势**，松手后按速度用弹簧收尾。这个手势 TOFI 现在没有（在 `ui/src` 里 grep `swipe` 没有结果，motion-lab 除外）。owner 定了要做（[D33](../decisions/0033-phone-swipe-back-experiment.md)）：作为有门槛的实验，自研和 Motion 各做一版，留好的那个。
- 用 React 组件的 Motion（`motion.div`）要出现在渲染树里，所以必须进入口包（最少 30.8 KB）。能懒加载的只有命令式的 `animate()`（22.8 KB），或者 `motion/mini`（3.5 KB，只是 WAAPI 的包装，`motion.ts` + `Element.animate` 已经在做同样的事）。

所以 Motion 只放在第 4 步做对照实验，有门槛，不达标就不引入。

### 3.3 dnd-kit 还是自研引擎

| 维度 | 自研引擎 | `@dnd-kit/core` + `sortable` |
|---|---|---|
| 体积 | 懒加载块 3,480 B | 15.3 KB；`useSortable` 写在每一行里，所以代码要进入口包，或者把整份列表包进懒加载 |
| 拖动中 React 渲染 | 0 次，放下时 1 次（AGENTS.md） | 每一行都调用 `useSortable`，悬停目标变了这些行就跟着重新渲染（库的设计如此）；60 个会话、每行一只猫，正好是 AGENTS.md 警告过的情况（"restyles every cat; measured +60 ms at 4x CPU"） |
| 拖动中读布局 | 不读，拿起时量一次 | 自带测量策略，要另行配置 |
| 长按、键盘、播报、两个区域 | 都有（§1.4） | 都能配，但长按时长、"长按不动就开菜单"、置顶落区要重写 |
| 维护 | 395 行自己维护 | 社区维护，新 API `@dnd-kit/react` 还是 0.x |

**保留引擎。** dnd-kit 只放在第 5 步做对照实验，有门槛。

## 4. 共用的做法（第 1–3 步都要遵守）

1. **portal 根节点不是层叠上下文。** `#tofi-layers` 是 `<body>` 里排在 `#root` 后面的一个普通 `div`，不设 `z-index`、`isolation`、`transform`、`filter`。它里面每个弹层自己带 `--z-*`，和 `#root` 里的启动画面（`--z-cover`）、顶栏（`--z-chrome`）在同一个根层叠上下文里比大小。如果把它做成层叠上下文，`tokens.md` 的刻度就会被整体打包成一层，启动画面就盖不住菜单了。
2. **portal 出去的内容会丢掉祖先选择器。** 有的样式写成 `.workspace .sidebar-account-menu`（`ui/src/v2-app.css:115`），portal 到 `.workspace` 外面后就不生效。搬之前先把这类选择器改成不依赖祖先的写法；主题变量在 `<html>` 上（`InteractionSystem.tsx:22`），portal 出去没问题。组件目录页同理，要套 `.workspace` 才有正确样式。
3. **懒加载的弹层宿主。** 入口包里只放一个很小的 `OverlayHost`（第 2 步新建；实际做法见第 2 步"和原方案的不同"第 1、2 条：没有集中状态，不用 `React.lazy` + `Suspense`），它持有"现在开着什么、锚在哪"的状态，开的时候用 `React.lazy` 渲染真正的 Radix 实现；空闲时预取那个块，做法和 `useSidebarDnd.ts:33-35` 一样，所以用户第一次点的时候块通常已经在了。触发按钮仍是 TOFI 现有的普通 `<button>`，行里不增加任何组件（守住 `test:timeline-rows`、`test:stream-cost` 的每行开销）。
4. **类名不变。** Radix 的 `Content` 用现有类名（如 `className="sidebar-context-menu"`），现有测试的选择器和样式都能继续用。
5. **动画走令牌。** 进出场用 Radix 的 `data-state` 接现有关键帧和 `--m-*`；`test:motion-tokens` 不能新增例外。
6. **每一步一个合并**，同一个提交里更新本文的进度和 [`tokens.md`](tokens.md) 的归层表。合并前跑完该步列出的测试，**以退出码为准**（D30）；浏览器测试需要 `PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs`。
7. **每一步都要报入口包字节数**（迁移前、迁移后），超预算不合并。

## 5. 分步

每一步的"owner 预览"都用 D22 的方式：`cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`，在 Chrome 打开 http://localhost:5173 。

### 第 1 步：层级刻度和 portal 根节点 — **已完成**（分支 `ds/step1-layers`，等合并和 owner 预览）

**目标**：把 [`tokens.md` §9.3](tokens.md) 的 13 个 `--z-*` 令牌落地，89 处 z-index 按 §9.4 归层，修掉 Z1、Z2、Z5、Z6。这一步不引入任何库。

**做了什么**（提交：分支 `ds/step1-layers` 上的 "Design system step 1" 一提交；合并提交号由协调者合并时补进 [`../product/roadmap.md`](../product/roadmap.md)）

| 项 | 内容 |
|---|---|
| 令牌和镜像 | `ui/src/design-tokens.css:99-104` 加 13 个 `--z-*`；新建 `ui/src/layers.ts`：`Z`（JS 镜像，做法同 `motion.ts`）和 `layerRoot()` |
| portal 根节点 | `ui/index.html` 在 `#root` 后面加 `<div id="tofi-layers"></div>`；`layerRoot()` 找到它，页面里没有就创建（测试夹具和 Motion Lab 的页面没有它）。原计划的 11 处 `createPortal(..., document.body)` 全部改成 `layerRoot()`；另外 4 处往 `document.body` 追加的弹层元素也改了：`App.tsx` 里拖文件的残影、@提及残影，`sendFlight.ts`，`mailSendFlight.ts` |
| 归层 | `tokens.md` §9.4 表里的全部 CSS；`progressive-blur.css` 里覆盖新消息胶囊的那条规则删掉，合并进 `styles.css:153`；`sendFlight.ts:20` 改成 `"var(--z-drag)"`（这样 `Z` 对象不进入口包）；`mailSendFlight.ts` 删掉行内的 `zIndex:"120"`，信封翻盖改为加 `.is-front` 类（`mail-send-flight.css:15`） |
| 审计后保住原样的三处 | 逐对审计（见下）发现刻度会意外改变三处先后顺序，用 `± 1` 保住：侧栏列 `calc(var(--z-chrome) + 1)`（`v2-app.css:20`，否则顶栏和输入框靠 DOM 顺序压住伸出侧栏的账户菜单）；macOS 拖动条 `calc(var(--z-panel) + 1)`（`desktop-system.css:9`，保持在侧栏、抽屉和抽屉遮罩之上）；≥1100 px 顶栏 `calc(var(--z-panel) + 1)`（`context-card.css:49`，Z4 的抬高，第 2 步改回 `--z-chrome`）。这三处和 §9.4 原表不同，`tokens.md` 已改 |
| 新测试 | `test:layers`（`ui/scripts/test-layers.mjs`；本文原计划的名字是 `test:layer-tokens`）和上限文件 `ui/scripts/layers-baseline.json`。检查内容见 [`tokens.md` §9.5](tokens.md)。和原计划的差别：局部值允许 -1..3（原计划写 0–3，`tokens.md` §9.5 写 -1..3，现有代码有 4 处 -1）；"局部值的父级要有 `isolation:isolate`"改在浏览器里查（1280、1000、390 px 下没有局部值落在根层叠上下文里），因为静态地把一条 CSS 规则对应到它的组件根做不到 |
| 不在这一步 | 被父级封顶的三处（账户菜单、附件菜单、@菜单，Z3）和顶栏抬高（Z4）要靠 portal 才能修，放第 2 步（这一步只把它们的数值换成令牌，仍被封顶）；原生 `<dialog>`（Z7）放第 3 步 |

**事实更正**：`.workspace .chat-pane` 的 `container-type:inline-size` 不建立层叠上下文（Chromium、WebKit 实测），所以宽屏下对话窗格里的顶栏、输入框、新消息胶囊、回执提示都和侧栏、抽屉、浮卡、电脑小窗在同一个根层叠上下文里比大小；只有手机宽度下 `.chat-pane` 有 `z-index:7`，才把它们包住。按"会建立"去推，会得出"对话窗格里的层级都被封顶"这个错误结论。

**怎么找出全部变化**：在工作区外面导出 `main`（`5f4396f`）的 `ui/` 做"迁移前"，和本分支各打一份生产构建，用同一个审计脚本打开 9 种状态（1280 / 1180 / 1000 / 390 px，设置窗开着，电脑放大，侧栏收起，macOS 桌面客户端 1000 / 1280 px），把每个弹层的真实类名插到它真实的父节点下，按层叠上下文规则算出每一对元素谁画在上面，逐对比较两边。脚本是一次性的，没有进仓库；它找出的变化全部列在下面。

**层级变化清单**（owner 已接受，[D32](../decisions/0032-step1-stacking-changes-accepted.md)；预览时逐条看）

| # | 变化 | 迁移前 → 现在 | 来源 |
|---|---|---|---|
| L1 | 错误 toast 在所有模态之上（设置窗、删除确认和重新指派主助理、全屏只读会话、交接、VM 授权、表单 sheet、引导），也在侧栏右键菜单、表情反应菜单、身份卡之上 | 80 → `--z-toast` 90 | 方案点名（Z1） |
| L2 | 设置窗在启动画面、身份卡、表情反应菜单、侧栏右键菜单、相关对话卡片之上；设置窗里的 MCP 菜单、权限提示跟着设置窗走 | 30 → `--z-dialog` 80 | 方案点名（Z5） |
| L3 | 启动画面在身份卡、表情反应菜单、侧栏右键菜单之上，在设置窗和放大的电脑窗之下；引导仍在它上面 | 60 → `--z-cover` 70 | 方案点名 |
| L4 | 同一层的模态按 DOM 顺序叠：portal 出去的（删除确认、交接、VM 授权、全屏只读、表单 sheet）都在设置窗和引导之上，它们之间后打开的在上；表单 sheet 始终比同层高一格。以前是固定的数字顺序：引导 1200 > 删除确认 1000 > 表单 sheet 210 > 交接、VM 授权 200 > 全屏只读 110 > 设置窗 30。所以现在引导在这些对话框之下，删除确认在后打开的交接、VM 授权、表单、全屏只读之下 | 各种 → `--z-dialog` | 刻度 |
| L5 | 悬停提示在引导和删除确认之上（以前引导盖住提示；删除确认和提示同为 1000，看 DOM 顺序） | 1000 → `--z-tooltip` 100 | 刻度（修掉 C23 的前一半） |
| L6 | 拖动层在一切之上：拖文件的遮罩和残影、@提及残影现在也在引导之上；寄信飞行以前是 120，低于表情反应菜单 150 和所有 200 以上的模态，现在都在它们之上 | 1000 / 120 / 2147483000 → `--z-drag` 110 | 刻度（Z6） |
| L7 | 放大的电脑窗（网页端大窗的遮罩）在身份卡、表情反应菜单、侧栏右键菜单、启动画面、相关对话卡片之上，和引导同层（DOM 在后，所以在引导之上） | 40 → `--z-dialog` | 刻度 |
| L8 | 电脑小窗在上下文浮卡、详情抽屉及其遮罩、放大的电脑详情之下，宽屏下也在顶栏和顶栏菜单（更多、成员）之下；以前它是 25，在这些之上。抽屉开着时点小窗会点到遮罩、关掉抽屉 | 25 → `--z-pill` 30（低于 `--z-panel` 40） | 刻度 |
| L9 | ≥1100 px 网页端顶栏和它的菜单在相关对话卡片之上；以前只在上下文浮卡之上，在相关对话卡片之下。**第 2 步撤销**：顶栏回到 `--z-chrome`，它的菜单 portal 出去后在卡片之上（第 2 步 M5） | 14 → `calc(var(--z-panel) + 1)` | 刻度 + Z4 的保留 |
| L10 | <1100 px 顶栏和它的菜单在底部模糊条之上（以前模糊条 3 > 顶栏 2；两者一上一下，不重叠） | 2 → `--z-chrome` | 刻度 |
| L11 | 群聊已读名单（回执提示，仍在消息行内）在宽屏下高于周围的一切：浮卡、抽屉和遮罩、电脑小窗、顶栏和它的菜单、身份卡、菜单、启动画面、toast。只有悬停或聚焦已读标记时出现；模态开着时背后的工作区是 `inert`，碰不到它。手机宽度下在对话窗格里，不变 | 5 → `--z-tooltip` | 刻度 |
| L12 | 顶栏"更多"菜单在成员面板之上（以前同为 8，看 DOM 顺序）；两者不会同时打开 | 8 → `--z-menu` / `--z-popover` | 刻度 |
| L13 | 侧栏"新建"菜单在置顶落区之上（落区只在拖动时出现，那时菜单是关的） | 8 → `--z-menu` | 刻度 |
| L14 | macOS 桌面客户端：拖动条在电脑小窗之上（以前在下面）。拖动条在左上角的侧栏上，小窗在右下角，正常不重叠 | 20 → `calc(var(--z-panel) + 1)` | 刻度 + 保留 |

测试保证不会变的：新消息胶囊仍在底部模糊条和输入框之上（`test:live-fixes`），手机推入的方向、时长和无空帧（`test:shell-budget` 第 3 节，Chromium 和 WebKit）。

**撤回验证**（D30，2026-10-20 PDT，测试文件保留、只动产品代码，见 `docs/process.md` 3.2）

| 撤回什么 | `test:layers` 退出码 | 失败的检查 |
|---|---|---|
| 第 1 步对 `ui/src`、`ui/index.html` 的全部改动（`git show <第 1 步提交> -- ui/src ui/index.html \| git apply -R`） | 1 | 13 个令牌缺失、`layers.ts` 缺失、各文件的字面 z-index 超出上限（如 `interaction-system.css` 的 1000、1000、90） |
| 只把 toast 改成 `var(--z-menu)`（合法令牌，层放错） | 1 | 叠放顺序：toast 跑到启动画面下面 |
| 只给 `#tofi-layers` 加 `isolation:isolate` | 1 | 根节点是层叠上下文；提示不再是最上层；整组弹层被压到 `#root` 下面 |
| 只把新消息胶囊改回 `z-index:3`（静态检查允许的局部值） | 1 | 1280 和 1000 px：`button.jump-latest (3)` 落在根层叠上下文里；390 px 通过（手机宽度下它在对话窗格里） |
| 只把悬停提示改回 `createPortal(..., document.body)` | 1 | `InteractionSystem.tsx` 往 `document.body` portal |

恢复后 `test:layers` 退出码 0，没有 SKIP。

**owner 预览看哪里**（`dev:remote`，Chrome，[D22](../decisions/0022-preview-via-dev-remote.md)）

1. （L1）打开设置窗，然后在 DevTools › Network 里切到 Offline，等侧栏刷新失败：顶部出现"暂时无法刷新工作区"的 toast，盖在设置窗上面。再切回 Online、点重试。
2. （L2、L3）刷新页面看启动画面：骨架和呼吸的猫在最上面；新账户的引导盖在启动画面上（L3）。
3. （L4）设置窗开着时打开一个表单 sheet（例如技能的访问设置），sheet 在设置窗上面；在归档列表里删除一个会话，确认框在最上面。L5（提示在引导和删除确认之上）在界面里没有现成的入口，由 `test:layers` 覆盖。
4. （L6）发一条消息：发送飞行在最上面；寄出一封邮件草稿：信封动画在所有东西上面。拖一个文件进窗口：遮罩盖住一切。
5. （L8）窗口宽度 1180 px 左右，打开 Bot 的电脑小窗，再打开 Bot 设置浮卡：两者重叠的地方浮卡在上面（以前小窗在上面）。窗口窄于 1120 px 时打开一个抽屉面板：小窗变暗在遮罩下面，点它会关掉抽屉。**这一条是看得见的变化，不喜欢就说，改成小窗在 `--z-panel + 1`。**
6. （L9）≥1100 px，点消息里的"来自……"打开相关对话卡片，再点顶栏的"⋯"：菜单在卡片上面。
7. （L11）群聊里，≥1100 px 打开上下文浮卡，悬停自己消息上的 ✓✓：读者名单在浮卡上面。
8. （保留）收起侧栏，打开左下角账户菜单：菜单仍盖在输入框和对话上面（和以前一样）。
9. （保留）手机宽度（390 px）下列表↔会话推入和以前一样。
10. （L14，桌面客户端，要用这次构建的 UI 包，`dev:remote` 看不到）窗口窄于 1280 px 时打开一个抽屉面板，按住侧栏顶部空白处拖动窗口：仍能拖。

**必须保持绿**（全部按退出码，2026-10-20 PDT 在本分支实测，见本步"实测"）：`typecheck`、`test:i18n`、`build`、`test:shell-budget`（含 `SHELL_BUDGET_ENGINE=webkit`）、`test:sidebar-pins`、`test:chat-motion`、`test:send-receipts`、`test:settings-shell`、`test:settings-polish`、`test:onboarding`、`test:handover-cards`、`test:primary-followups`、`test:motion-tokens`、`test:layers`，以及协调者给的完整清单（`test:chat-bubbles`、`test:chat-switch` 含 WebKit、`test:chat-safari`、`test:composer-typing`、`test:timeline`、`test:arrival-glide`、`test:live-fixes`、`test:standing-allowance`、`test:user-form`、`test:approval-facts`、`test:preloader`、`test:stream-cost`、`test:switch-budget`、`test:avatar-frame-budget`、`go build ./...`）。

**实测**（同一台机器，迁移前 = 本分支起点 `5f4396f`）

| 预算 | 迁移前 | 现在 | 上限 |
|---|---|---|---|
| 入口包（gzip，`test:shell-budget`） | 315,064 B | 315,113 B（+49 B，余量 692 B） | 315,805 B |
| 设置窗首帧（Chromium 1x / 4x 中位数，多次运行；WebKit 1x） | 1x 18.5、25.3、23.2、25.2 ms；4x 41.9–43.2 ms；WebKit 17.0 ms | 1x 23.5、19.9、24.5、24.5、20.4 ms；4x 41.6–44.4 ms；WebKit 17.0、18.0、17.0 ms | 32 / 100 ms |
| 手机推入 4x CPU 超过 25 ms 的帧 | 1 | 0 | ≤ 1 |
| 流式每块（1x / 4x） | 2.14 / 8.53 ms | 2.18 / 8.01 ms | 4 / 16 ms |
| 切换会话 p95（1x / 4x） | 22.1 / 70.0 ms | 22.8 / 67.5 ms | 60 / 100 ms |
| 打字每键脚本（减弱动效 / 动画开着） | 0.36 / 0.59 ms | 0.33 / 0.62 ms | ≤ 4 ms |
| 猫头像帧率（暂停前 / 恢复后） | 61.2 / 59.7 fps | 64.4 / 59.8 fps | 见 `test:avatar-frame-budget` |

入口包的估计是 +100 到 +300 B，实测 +49 B：`layers.ts` 和几行 import 并进了已有的共享块（这个 6 个文件之一的块从 `react-dom` 改名成 `layers`，1,394 → 1,474 B），`Z` 对象因为没有入口代码用它而被摇掉。设置窗首帧的 1x 中位数每次运行在 18–25 ms 之间跳，迁移前后的范围相同（迁移前 4 次、现在 5 次，同一台机器交替跑），这一步没有改设置窗的代码。

### 第 2 步：菜单和弹出卡片 — **已完成**（分支 `ds/step2-menus`，从 `ds/step1-layers` 开出，等合并和 owner 预览）

**目标**：所有菜单和弹出卡片走同一个宿主、同一套键盘和关闭规则，用 Radix 实现；顺带修掉 Z3、Z4。

**做了什么**（提交：分支 `ds/step2-menus` 上的 "Design system step 2" 一提交）

| 项 | 内容 |
|---|---|
| 依赖 | `@radix-ui/react-dropdown-menu` 2.2.1、`@radix-ui/react-popover` 1.3.1（`ui/package.json` 固定版本）。许可证：`ui/public/licenses/radix-ui.txt`、`floating-ui.txt`、`react-remove-scroll.txt`、`tslib.txt`，清单在 `ui/THIRD_PARTY_NOTICES.md` 末节（打进懒加载块的全部第三方包，按构建的 sourcemap 列出） |
| 入口包里的宿主 | `ui/src/overlays/OverlayHost.tsx`：`OverlayMenu`、`OverlayPopover` 两个薄包装（没加载完时什么都不画，加载完在同一次提交里渲染），`arrowOpens`（在菜单按钮上按 ↓ 打开），`OverlaySlot` / `SlotView`（见"交互开销"），以及空闲时预取懒加载块（`requestIdleCallback`，超时 1.5 s，做法同 `useSidebarDnd.ts`） |
| 懒加载块 | `ui/src/overlays/RadixOverlays.tsx`：`Menu`（Radix `DropdownMenu`，`modal={false}`）和 `Popover`（Radix `Popover`），都 portal 进 `layerRoot()`。菜单的子元素原样是 TOFI 的标记：其中每个 `<button>`（任意深度、含 fragment 里的）用 `DropdownMenu.Item asChild` 变成菜单项，元素和类名不变 |
| 一套规则 | ↑↓、Home/End、首字母跳转；Enter/空格选中；Esc 关闭并把焦点还给触发按钮；Tab 关闭并从触发按钮往后走（在设置窗这类模态里，只在模态里走并回绕）；点外面关闭且不吞掉这次点击；再按一次触发按钮是关闭；键盘打开焦点在第一项，鼠标打开焦点在菜单本身（不画焦点环，方向键照样能用）；选中一项后焦点无处可去时回到触发按钮；只是焦点移走（没有按下、Esc、Tab）不关闭，和迁移前一样 |
| 迁了哪些 | 菜单（DropdownMenu）：新建（`App.tsx`）、顶栏"⋯"（`App.tsx`，锚在顶栏 `chatHeaderRef`）、侧栏行右键/长按（`App.tsx:2401`，锚在 (x, y) 的点）、输入框附件（`App.tsx` `Composer`）、账户（`OwnerSession.tsx:176`）、MCP 行"⋯"（`MCPSettings.tsx:227`，也走同一个宿主，没在 SettingsPanel 里静态导入 Radix）。弹出卡片（Popover）：表情反应（`MessageReactions.tsx:47`）、Bot 身份卡（`BotIdentityCard.tsx`）。删掉了它们各自手写的外部点击、Esc、方向键、焦点归还、视口夹取代码，以及新建/顶栏菜单的 `inert` 退场 |
| 悬停提示 | 不用 Radix Tooltip。`ActionHints` 不变（第 1 步已挂到 `layerRoot()`、`--z-tooltip`）；群聊读者名单从行内的 `.send-receipt-tip` 改成已读标记上的 `data-hint`（`SendReceipt.tsx`），`data-hint-tone="quiet"` 让提示保持原来的白底外观（`.action-hint.is-quiet`，`interaction-system.css:97`）；`ActionHints` 加了一个只在提示显示时工作的 `MutationObserver`（`InteractionSystem.tsx:60`），提示开着时名单变了，文字跟着变 |
| @菜单 | 它是 `role="listbox"`、焦点留在输入框里，不是菜单，没有换成 Radix；只是 `createPortal` 进 `#tofi-layers`，按视口坐标放（`App.tsx` 里计算 `mentionAnchor` 的 layout effect），`.mention-menu` 改为 `position:fixed`。这样它不再被输入框封顶（Z3 的第三处） |
| CSS | 菜单的 `position:absolute/fixed` + `top/left/right/bottom` 换成 `position:relative`（位置交给 Radix Popper 的外层，Popper 把内容的 `--z-*` 复制到外层）；原来写在偏移里的位置改成 `side/align/sideOffset/alignOffset`（收起侧栏时新建菜单在右侧 12 px、账户菜单在右侧 10 px 底对齐）；`.workspace` 开头的菜单选择器改成 `:is(.workspace,[id="tofi-layers"])`（特异性不变，`v2-app.css`、`web-mention.css` 共 47 处）；`[data-open="false"]` 退场改成 Radix 的 `[data-state="closed"]`，退场动画由 Radix `Presence` 等完再卸载；新建/顶栏菜单的 `transform-origin` 用 `var(--radix-dropdown-menu-content-transform-origin)`；删掉 `context-card.css` 的顶栏抬高（Z4）；新增 `#tofi-layers [data-layer="dialog"]{z-index:calc(var(--z-dialog) + 1)}`（`interaction-system.css:91`，模态里打开的菜单盖在模态上）；账户菜单的主题三选一改成 `role="menuitemradio"` + `aria-checked`（选了不关菜单），CSS 从 `[aria-pressed]` 跟着改 |
| 测试 | 新增 `test:overlays`（`ui/scripts/test-overlays.mjs`，夹具 `ui/scripts/lib/overlay-fixture.mjs`，Chromium，`OVERLAYS_ENGINE=webkit` 跑 WebKit）。改了两个现有测试：`test:send-receipts` 的读者名单改为找 `#tofi-layers` 里的 `.action-hint.is-quiet`；`test:layers` 的叠放探针里右键菜单的父节点改为 `#tofi-layers`（它现在就在那里） |

**和原方案的不同**

1. **没有集中的"现在开着什么"状态。** 每个菜单的开关状态仍在它的触发按钮旁边；宿主只是两个薄包装，关着的菜单不渲染任何东西。方案 §4 第 3 条写的集中状态没有带来好处，反而要让所有菜单的父组件订阅它。
2. **不用 `React.lazy` + `Suspense`。** React 19 显示一个挂起过的 Suspense 边界时会压住约 300 ms：实测"点下到菜单出现"是 340 ms。改成模块缓存（`useRadix`）后是 40–50 ms，和迁移前一样（撤回验证 P6）。**第 3 步的对话框也不要用 Suspense 包懒加载的 Radix**。
3. **锚点。** Popover 用 `Popover.Anchor virtualRef` 直接锚到元素或一个虚拟矩形。DropdownMenu 没有公开的 Anchor，用一个隐形的 `DropdownMenu.Trigger`：按钮菜单是放在按钮旁边、复制锚框位置的 `span`（和锚一起滚动）；右键菜单是 portal 进 `#tofi-layers` 的 0×0 `position:fixed` 点。方案的风险 ①（隐形锚点下键盘打开时焦点能否到第一项）的结论：Radix 自己的"键盘打开"判断在菜单挂载之前的按键上拿不到，改为读 `<html data-input="keyboard">`（`focusModality.ts`），菜单获得焦点时把焦点交给第一项。
4. **长按的尾巴。** 手机上长按打开行菜单后，那次触摸的兼容鼠标事件会把焦点移到行上；Radix 默认"焦点移出就关闭"，菜单会一闪而过（`test:sidebar-pins` 抓到）。所以焦点移出不关闭（`keepOnFocusMove`）。另外菜单项上不是在本项按下的抬起（Radix 的"按住拖到某项松开即选中"）不选中任何项。
5. **设置窗里的菜单要盖在设置窗上。** 迁移前 MCP 菜单在设置窗内部；portal 之后它的 `--z-menu`（60）低于设置窗的 `--z-dialog`（80），会被盖住。从带 `aria-modal="true"` 的祖先里打开的菜单和卡片加 `data-layer="dialog"`，层级是 `calc(var(--z-dialog) + 1)`。
6. **交互开销（D02）。** 右键菜单的状态从 Workspace 的 `useState` 挪到 `OverlaySlot`，打开和关闭只重渲染 `SlotView`，整个工作区不渲染（`test:overlays` 用一个 React DevTools 钩子数工作区的渲染次数，撤回验证 P15）。菜单或卡片打开时让猫的呼吸停 320 ms（`holdAvatarLife`，和面板打开同一个机制，P16）。试过"空闲时预渲染一次 Radix"（首开 4x CPU 下脚本时间少约 10–15 ms），但它本身是一次 12 ms（1x）/ 约 50 ms（4x）的空闲任务、可能撞上手机推入，而且效果在本机的噪声里证明不了，没有保留。

**事实更正**

- **身份卡在界面上没有入口。** `App.tsx` 只在"指派"通知里构造 `BotIdentityCard`（`inlineMention`），但指派通知在那之前已经 `return`，`mention` 永远是 `undefined`。`components.md` 第 11 节"群聊发送者头像上"是错的，已改。组件照方案迁到 Popover，`test:overlays` 在一个单独的开发页里挂它来测。要不要在界面上恢复它，待 owner 定。
- `.member-panel`（`styles.css:72`）没有任何组件使用，是死 CSS（未删）。
- 顶栏"⋯"菜单的定位基准是 `.chat-header`（`position:relative`），不是 `.header-more`：右边距 14 px 是相对整个顶栏的。

**看得见的变化**（预览时逐条看）

| # | 变化 | 迁移前 → 现在 |
|---|---|---|
| M1 | 菜单贴近视口边缘时翻转或平移进视口（菜单留 8 px，卡片留 12 px）；正常位置下 7 个菜单的位置逐像素不变（`test:overlays` 按旧 CSS 的偏移逐个核对） | 右键菜单用写死的 248×360 夹视口（C20），其余菜单不处理边缘 |
| M2 | 群聊读者名单：悬停 500 ms（键盘聚焦 150 ms）后出现，在已读标记正上方居中，和图标提示同一套延迟和关闭规则；白底外观不变；手机上点按已读标记会显示 | 悬停立即淡入，右对齐在标记上方，在消息行里 |
| M3 | 键盘：所有菜单都能 ↑↓/Home/End/首字母，Enter/空格/↓ 打开时焦点在第一项，Esc 回到按钮，Tab 关闭并往后走；账户菜单的主题三选一读屏读成单选菜单项 | 账户、附件菜单声明了 `role="menu"` 却只有 Esc（C18）；新建、顶栏菜单只能 Tab |
| M4 | 鼠标打开的菜单焦点在菜单本身 | 新建、顶栏菜单把焦点放在第一项（鼠标模式下不画焦点环，看不出差别） |
| M5 | 收起侧栏时的账户菜单、输入框附件菜单、@菜单都在 `#tofi-layers` 里，盖在新消息胶囊、电脑小窗、浮卡之上（Z3）；≥1100 px 顶栏回到 `--z-chrome`，"⋯"菜单仍在上下文浮卡之上（Z4）。第 1 步的 L9（顶栏在相关对话卡片之上）随之撤销：顶栏本身回到卡片之下，它的菜单在卡片之上 | 被侧栏 / 输入框封顶；顶栏抬到 `calc(var(--z-panel) + 1)` |
| M6 | 设置 › 连接里 MCP 行的"⋯"菜单在 `calc(var(--z-dialog) + 1)` | 在设置窗内部 |
| M7 | 选中一项后，焦点无处可去时回到触发按钮；菜单或卡片打开时猫停 320 ms 呼吸 | 留在 `<body>`；猫不停 |
| M8 | 表情反应菜单放不下时翻到按钮上方（不再压住按钮） | 夹在视口内，可能盖住按钮 |

**撤回验证**（D30，2026-10-21 PDT，测试保留、只动产品代码）

| # | 撤回什么 | 测试 | 退出码 | 失败的检查 |
|---|---|---|---|---|
| P1 | 本步对 `ui/src` 的全部改动（`git diff ds/step1-layers -- ui/src \| git apply -R`） | `test:overlays`；`test:send-receipts` | 1；1 | 22 项里 20 项失败（没有 Radix 块、键盘、位置、层级、读者名单、渲染次数……；长按和表情菜单焦点两项迁移前就满足）；16 项里读者名单那一项 |
| P2 | 只去掉"焦点移出不关闭" | `test:sidebar-pins` | 1 | 390 px 长按后菜单不出现 |
| P3 | 只去掉 Tab 处理 | `test:overlays`（新建菜单） | 1 | Tab 之后菜单不关 |
| P4 | 只删 `data-layer="dialog"` 那条 CSS | `test:overlays` | 1 | MCP 菜单中心处最上层是设置窗里的按钮 |
| P5 | 把顶栏抬高加回 `context-card.css` | `test:overlays` | 1 | 1280 px 顶栏不是 `--z-chrome` |
| P6 | `OverlayMenu` 改回 `React.lazy` + `Suspense` | `test:overlays`（开销） | 1 | 点下到出现 348.7 ms > 100 |
| P7 | @菜单不 portal | `test:overlays` | 1 | @菜单不在 `#tofi-layers` |
| P8 | 去掉菜单项的 `onPointerUp` 拦截 | `test:overlays` | 1 | 不是在本项按下的抬起选中了该项 |
| P10 | 去掉 `ActionHints` 的 `MutationObserver` | `test:send-receipts` | 1 | 名单变了提示没跟着变 |
| P11 | 去掉主题按钮的 `data-keep-open` | `test:overlays` | 1 | 选主题后菜单关了 |
| P12 | 去掉 `App.tsx` 模态 Tab 圈定里的 `defaultPrevented` 判断 | `test:overlays`（MCP 菜单） | 1 | Tab 之后焦点回绕到触发按钮之前 |
| P13 | 顶栏"⋯"菜单锚回按钮 | `test:overlays` | 1 | 位置偏 −20 / −14 px |
| P14 | 去掉新建按钮的 `arrowOpens` | `test:overlays` | 1 | ↓ 打不开菜单 |
| P15 | 右键菜单状态改回 Workspace 的 `useState` | `test:overlays` | 1 | 工作区渲染了 2 次 |
| P16 | 去掉打开时的 `holdAvatarLife` | `test:overlays` | 1 | 打开后 250 ms 内猫写了 590 次属性 |

每一项恢复后对应测试退出码 0，没有 SKIP。

**owner 预览看哪里**（`dev:remote`，Chrome，[D22](../decisions/0022-preview-via-dev-remote.md)；M1–M8 见上表）

1. （M1）右键侧栏里的会话：菜单在鼠标处出现，动画和以前一样；把窗口拉矮、在最下面的会话上右键：菜单翻到上面，不出界。手机宽度（390 px）长按会话：菜单出现，松手不会误选。
2. 新建（侧栏 +）、顶栏"⋯"、账户（左下角）、输入框 +、设置 › 连接里 MCP 行的"⋯"：位置和外观和以前一样；收起侧栏后新建和账户菜单在侧栏右边。
3. （M5）收起侧栏、打开左下角账户菜单：菜单伸到对话区，盖在新消息胶囊、电脑小窗上面。输入框打 `@`：名单盖在新消息胶囊上面。≥1100 px 打开 Bot 设置浮卡，再点顶栏"⋯"：菜单在浮卡上面。
4. （M6）设置 › 连接，点某个 MCP 服务的"⋯"：菜单在设置窗上面；按 Esc 只关菜单，设置窗还在。
5. （M3）只用键盘：Tab 到侧栏的 +，按 ↓，上下键选，Esc 回到 +；在会话行上按 Shift+F10；账户菜单里用 ↑↓ 走到主题，回车换主题，菜单不关。
6. （M2）群聊里悬停自己消息上的 ✓✓：半秒后出现"已读：……"，白底；手机上点 ✓✓。
7. （M8）对一条消息开表情反应：焦点在第一个表情，Esc 关闭后焦点回到笑脸按钮。
8. 身份卡在界面上看不到（见"事实更正"），无需预览。

**必须保持绿**（全部按退出码，2026-10-21 PDT 在本分支最终提交上实测，没有 SKIP）：`typecheck`、`test:i18n`、`build`、`go build ./...`、`test:overlays`（Chromium 和 `OVERLAYS_ENGINE=webkit`；WebKit 下长按和身份卡两项只在 Chromium 跑，会打印说明）、`test:layers`、`test:motion-tokens`、`test:chat-bubbles`、`test:chat-switch`（含 `CHAT_SWITCH_ENGINE=webkit`）、`test:chat-safari`、`test:composer-typing`、`test:timeline`、`test:chat-motion`、`test:arrival-glide`、`test:live-fixes`、`test:send-receipts`、`test:sidebar-pins`、`test:handover-cards`、`test:standing-allowance`、`test:user-form`、`test:approval-facts`、`test:settings-polish`、`test:settings-shell`、`test:preloader`、`test:onboarding`、`test:primary-followups`、`test:shell-budget`（含 `SHELL_BUDGET_ENGINE=webkit`）、`test:stream-cost`、`test:switch-budget`、`test:avatar-frame-budget`、`test:timeline-rows`、`test:perf-structure`。

**实测**（同一台机器；迁移前 = 本分支起点 `ds/step1-layers` 的 `97ac43d`）

| 预算 | 迁移前 | 现在 | 上限 |
|---|---|---|---|
| 入口包（gzip，`test:shell-budget`） | 315,113 B | 315,118 B（+5 B，余量 687 B） | 315,805 B |
| 懒加载块 `RadixOverlays` | — | 30,699 B（方案估计 29,723 B，多出来的是 TOFI 的包装代码） | 不在入口包 |
| 新建菜单点下到出现（块已加载） | 35–48 ms（4 次） | 38–69 ms（10 次，负载不同） | `test:overlays` ≤ 100 ms |
| 同上，块在页面加载后被压住 800 ms | — | 84–255 ms（块到了就出现，多次运行） | ≤ 1,400 ms |
| 最大的菜单（侧栏右键，9 项）首次打开，前后交替 7 次的中位数，1x CPU | 最长帧 17 ms；右键到下一帧 40 ms；事件处理 + 渲染 3 ms | 最长帧 33 ms；右键到下一帧 40 ms；事件处理 + 渲染 8 ms | `test:overlays` 中位数最长帧 ≤ 3 帧（50 ms） |
| 同上，4x CPU | 最长帧 33 ms；右键到下一帧 64 ms；事件处理 + 渲染 11 ms | 最长帧 50 ms；右键到下一帧 88 ms；事件处理 + 渲染 37 ms | ≤ 6 帧（100 ms） |
| 设置窗首帧（1x / 4x 中位数；WebKit 1x） | 1x 23.5、19.9、24.5 ms；4x 41.6–44.4 ms；WebKit 17 ms（第 1 步实测） | 1x 22.1–24.7 ms；4x 46.3–47.1 ms；WebKit 11.0 ms | 32 / 100 ms |
| 手机推入 4x CPU 超过 25 ms 的帧 | 0（第 1 步实测）；今天和本步的构建交替测 4 次，首测 1、3、0、2，过了 2 次 | 1（最终运行）；交替测 4 次，首测 2、2、1、1，过了 3 次（`attempt` 不过会重测一次） | ≤ 1 |
| 流式每块（1x / 4x） | 2.18 / 8.01 ms | 2.21 / 9.46 ms | 4 / 16 ms |
| 切换会话 p95（1x / 4x） | 22.8 / 67.5 ms | 19.4 / 72.1 ms | 60 / 100 ms |
| 打字每键脚本（减弱动效 / 动画开着） | 0.33 / 0.62 ms（第 1 步实测）；今天和本步交替测 2 次：0.39–0.43 / 0.65–0.66 ms | 交替测 2 次：0.43–0.44 / 0.67–0.73 ms | ≤ 4 ms |

结论：入口包基本不变；菜单的打开比迁移前贵——最大的菜单在 1x 下多约 5 ms 脚本、4x 下多约 26 ms，最长帧多一帧，仍在 50 ms 以内。主要是 Radix 每一项都是一串组件（项、集合、漫游焦点、Slot），以及首次打开时这些代码第一次执行。已经做的：右键菜单不重渲染工作区、打开时猫停呼吸。还能做但没做的：空闲时预渲染一次 Radix（见"和原方案的不同"第 6 条），要不要换这 10–15 ms（4x）由 owner 定。手机推入、流式、切换、打字的数字和迁移前在同一噪声范围内；本机测试期间有别的会话在跑，负载在 3–21 之间，手机推入 4x 那一项迁移前的构建也会偶尔超（上表）。

**原方案留档**（各原语怎么处理、改哪些文件、风险）：Tooltip 不用 Radix（见上"悬停提示"）；DropdownMenu 用于新建、顶栏、账户、附件、MCP 菜单；ContextMenu 不用 Radix ContextMenu（它的 Trigger 要包住每一行，自带 700 ms 触屏长按，会和引擎的 380 ms 长按抢；`test:overlays` 断言 450 ms 的长按打开菜单）；Popover 用于身份卡、表情反应菜单，身份卡的悬停延时和长按逻辑留在 `BotIdentityCard.tsx`。风险 ②（第一次打开时块还没到）实测见上表；风险 ③（portal 后丢掉 `.workspace` 祖先选择器）用 `:is(.workspace,[id="tofi-layers"])` 解决，并用一次性脚本逐元素比较了迁移前后 14 种打开状态的计算样式和位置：除 MCP 菜单外壳的 `font-size`（13 → 15 px，外壳里没有直接的文字，子元素各自定了字号，看不出）和有意改的 MCP 菜单层级，全部相同；风险 ④（点外面不吞点击）由 `test:overlays` 的"右键菜单开着时点另一个会话，菜单关、会话照样打开"守住。

### 第 3 步：对话框 — **已完成**（分支 `ds/step3-dialogs`，从 `ds/step1-layers` 分出，和第 2 步并行；等合并和 owner 预览）

**目标**：所有模态对话框用 Radix `Dialog` / `AlertDialog`：焦点圈定、Esc 只关最上面一层、关闭后焦点回到打开它的元素、背景对读屏隐藏，全部统一。

**做了什么**（提交：分支 `ds/step3-dialogs` 上的 "Design system step 3" 一提交）

| 项 | 内容 |
|---|---|
| 唯一实现 | 新建 `ui/src/overlays/ModalDialog.tsx`：`ModalDialog`（Radix `Dialog`）和 `ConfirmDialog`（Radix `AlertDialog`）。调用方把对话框元素本身交给它（`asChild`，类名、`role`、标签都不变），有遮罩的写 `overlayClassName`，内容仍渲染在遮罩里面（CSS 原来就这么写）。它统一做：`aria-modal="true"` 和 `data-tofi-modal`（Radix 不设 `aria-modal`）；`locked`（忙时 Esc 和点遮罩都不关）、`closeOnEscape`、`closeOnOutside`；打开时焦点给 `initialFocus`，没有就给对话框本身（不画焦点环）；关闭时只在焦点随对话框丢失时才还回去，还给 `returnFocus` 或打开时的焦点元素 |
| 只进懒加载块 | 只有懒加载块导入 `ModalDialog.tsx`（D31）。Radix 在一个共用的懒加载块 `ModalDialog-*.js`（13,981 B gzip），随设置、引导、删除确认等块在空闲时预取 |
| 迁了哪些 | `DialogShell.tsx`（同名组件改成包 `ModalDialog`，`role="alertdialog"`；删除会话、重新指派主助理跟着走）；`AdminSheets.tsx` 的 `AdminSheet`（管理员表单、已记住的审批）；`SkillAccess.tsx`；`ViewOnlyChat.tsx` 的全屏形态（≥1100 px 网页卡片形态仍是非模态面板，没迁）；`VMOAuthDialog.tsx`；`Onboarding.tsx`；原生 `<dialog>` 的两个确认框 `SettingsShell.tsx`（离开前保存）和 `ComputerResources.tsx`（重启）改成 `ConfirmDialog` |
| 没迁（按计划） | `TakeoverDialog.tsx`（风险 ③）、`TerminalPanel.tsx` 面板内确认、`PermissionCoach`、图片灯箱和文件预览（仍是原生 `<dialog>`） |
| 搬出入口包 | `ReassignPrimaryDialog` 搬进自己的懒加载块（`ReassignPrimaryDialog.tsx`，`lazyPanels.tsx` 的 `reassignPrimary`）；`PrimaryAssistantSetting`（只有设置页用）搬进 `PrimaryAssistantSetting.tsx`。后一条不在原计划里：只搬前一条时 rolldown 会把 `InteractionSystem` 拆成单独的入口块，入口反而 +442 B；两条一起搬，`settings/components` 也离开入口，入口 −1,148 B |
| Z7 | 还留在原生 `<dialog>`（浏览器顶层）里的两个：悬停提示在显示时挂进最上层打开的模态 `<dialog>`（`layers.ts` 的 `topLayerDialog()`）；工作区 toast 从 App.tsx 抽成 `WorkspaceToast.tsx`，包在 `InTopLayer` 里：有模态 `<dialog>` 开着时渲染进它里面，关掉（或被卸载）后回到原处；只在 toast 显示时监听 DOM |
| App.tsx | 桌面菜单快捷键的闸改认 `[aria-modal="true"]`（原来只认 `role="dialog"`，删除确认是 `alertdialog`，闸对它一直无效）；详情窗格自写的 Tab/Esc 圈定在有 `[data-tofi-modal]` 时让开；删除确认和重新指派打开时记下焦点（`rememberDialogOpener`），关掉还回去（工作区同一提交变成 `inert`，焦点在对话框渲染前就掉到 `<body>` 了，原来的代码也因此还不回去）；重新指派打开时工作区也 `inert`（C14） |
| 样式 | 两个原生确认框换成普通元素后，`::backdrop` 改成遮罩类 `.settings-leave-overlay`、`.resource-restart-overlay`（`--z-dialog`），并补上原生模态 `<dialog>` 的尺寸规则；它们原来在设置页里面，继承了设置页的标题、按钮和字体样式，portal 出去以后在 `computer-resources.css` 和 `settings-polish.css` 里显式写回。工作区 toast 加 `pointer-events:auto`（Radix 模态开着时 `<body>` 关掉了指针事件） |

**和计划不同的几处，以及原因**

1. **删除确认用 `Dialog` + `role="alertdialog"`，不用 `AlertDialog`**：Radix `AlertDialog` 永远不让点遮罩关闭，而删除确认现在点遮罩会关（components.md §8 #1）。保持现有行为。真正"只能用按钮或 Esc 回答"的两个原生确认框用了 `AlertDialog`。
2. **不用 Radix `Overlay`**：它带的滚动锁（`react-remove-scroll`）每次打开都改 `<body>` 的样式，而 TOFI 的页面在对话框后面本来就不滚动（遮罩盖满、自己不可滚）。遮罩是普通元素，`pointer-events:auto`。实测在 4x CPU 下打开删除确认快了几毫秒，也消掉了风险 ④（关掉后消息区滚动位置），`test:chat-safari` 照旧通过。
3. **两处补丁 Radix 的空隙**（都在 `ModalDialog.tsx`，都有测试）：Radix 在新对话框首帧之后才把它登记成最上层，这一帧里按 Esc 会关掉下面那一层（连带新开的）——现在这个键被丢掉；Radix 把逃出对话框的焦点送回"上次聚焦的元素"，那个元素被替换掉时焦点会停在它插在 `<body>` 两端的隐形 span 上，Tab 画不出焦点环（`test:settings-polish` 在引导里抓到）——现在送回对话框的第一个（或最后一个）控件。
4. **新测试叫 `test:dialogs`，不扩展 `test:overlays`**：第 2 步在并行建 `test:overlays`，合并后可以并在一起。"对话框里再开菜单，按一次 Esc 只关菜单"现在还测不了（迁过来的对话框里没有菜单）；用"对话框里再开对话框"测了同一条规则，菜单走 Radix 以后共享同一个层栈。
5. **全屏只读会话跨过 1100 px 时**：卡片和模态是两套外壳，窗口宽度跨过断点时历史会重新挂载（重新取一次）。原来是同一个元素换属性。

**层级和行为变化清单**（预览时逐条看；D32 接受的是第 1 步，这几条是第 3 步新的）

| # | 变化 | 迁移前 → 现在 |
|---|---|---|
| S1 | 引导从 `#root` 搬进 `#tofi-layers`：现在在设置窗之上（以前设置窗在它之上，DOM 在后）；和其它 portal 出去的对话框按打开顺序叠（以前它们总在引导之上，第 1 步 L4） | `.workspace-grid` 里 → `#tofi-layers` |
| S2 | 离开前保存、电脑重启两个确认框不再在浏览器顶层：toast、悬停提示、拖动层现在在它们之上（Z7 修好的一半）；它们仍在设置窗之上 | 顶层 → `--z-dialog` |
| S3 | 引导、VM 授权：Esc 和点遮罩现在明确什么都不做，也不再传给后面的工作区（以前引导打开时 Esc 会落到 App 自己的处理上） | — |
| S4 | 任何模态开着时，`<body>` 上的指针事件关掉（Radix）：后面的东西点不到，点遮罩外的地方等于点遮罩；工作区 toast 例外，仍可点，点它不会关掉对话框 | — |
| S5 | 读屏：模态开着时页面其余部分 `aria-hidden`（Radix；`aria-live` 区域和它们的祖先除外），重新指派主助理时工作区也 `inert`（C14） | 原来只有删除确认和只读会话让工作区 `inert` |
| S6 | 桌面客户端：删除确认、重新指派、两个原生确认框改成的对话框开着时，菜单快捷键（新建、搜索、收起侧栏等，设置除外）不再生效 | 闸以前只认 `role="dialog"` |

**撤回验证**（D30，测试文件保留，只动产品代码，见 `docs/process.md` 3.2）

在工作区外的一份副本上逐项改回，测试文件保留（`docs/process.md` 3.2）：

| 撤回什么 | 跑的测试 | 退出码 | 失败的检查 |
|---|---|---|---|
| 第 3 步对 `ui/src` 的全部改动（换回第 1 步的 `ui/src`） | `test:dialogs` | 1 | 14 项里 13 项挂：没有任何块带 Radix；删除确认缺 `data-tofi-modal`；忙时 Esc 之后关不掉（C13）；桌面快捷键在删除确认后面照样生效；测试页导不进 `ModalDialog`。"Esc 只关最上一层（对话框和下面的非模态面板）"一项原来的代码也通过 |
| 去掉 `ModalDialog` 的 `aria-modal` | `test:dialogs` | 1 | 删除确认 `aria-modal` 为空；`toggle-sidebar` 在删除确认后面生效 |
| App.tsx 的快捷键闸改回只认 `[role="dialog"][aria-modal="true"]` | `test:dialogs` | 1 | `toggle-sidebar` 在删除确认（`alertdialog`）后面生效 |
| 删除确认不传 `returnFocus`（去掉 `rememberDialogOpener` 的作用） | `test:dialogs` | 1 | 关掉后焦点在 `<body>`，没回到"⋯" |
| `ReassignPrimaryDialog` 改回 App.tsx 静态导入 | `test:dialogs` | 1 | Radix 进了入口包（`ModalDialog-*.js` 成了入口文件） |
| 去掉"新对话框首帧里的 Esc 丢掉" | `test:dialogs` | 1 | 首帧的 Esc 关掉了下面的 sheet（`onClose` 只有 `sheet`） |
| 悬停提示改回总挂在 `layerRoot()` | `test:dialogs` | 1 | 原生 `<dialog>` 开着时提示不在 dialog 里、也不在最上面 |
| `WorkspaceToast` 去掉 `InTopLayer` | `test:dialogs` | 1 | 原生 `<dialog>` 开着时 toast 不在 dialog 里 |
| `InTopLayer` 只看 `open` 属性、不看节点删除 | `test:dialogs` | 1 | dialog 被卸载后 toast 没回到页面 |
| 两个确认框不再写回设置页的标题和按钮样式 | `test:dialogs` | 1 | 重启确认的标题是 Fredoka 20px/700，设置页是 15px/600 |
| toast 不再 `pointer-events:auto`、点它会关掉对话框 | `test:dialogs` | 1 | sheet 开着时点不到 toast 的"重试" |
| 去掉焦点落在 Radix 隐形 span 上时的补救 | `test:settings-polish` | 1 | 390 px 的引导里 Tab 落在不画焦点环的 `.onb-sheet` 上（3 处） |
| 什么都不撤（对照） | `test:dialogs` | 0 | — |

另外试过、后来删掉的一处：让每个对话框保留自己的 `tabIndex`（不用 Radix 给的 `-1`）。有了上面那条补救之后，撤掉它 `test:settings-polish` 仍是 0，测不出作用，所以没留。

**owner 预览看哪里**（`dev:remote`，Chrome，[D22](../decisions/0022-preview-via-dev-remote.md)）

1. 删除一个会话（顶栏"⋯" › 删除）：确认框样子和出现动画和以前一样；Esc 关；点遮罩关；点卡片空白处不关，之后 Esc 照样能关；关掉后焦点回到"⋯"（用键盘打开时能看到焦点环）。点"删除"后、请求还没回来时，Esc 和点遮罩都不关。
2. 设置 › 技能 › 某个技能的"更改访问"：表单 sheet 叠在设置窗上面；Tab 在 sheet 里转圈；Esc 只关 sheet，设置窗还在；再按 Esc 才关设置窗。
3. 隐藏主助理（侧栏右键主助理 › 隐藏）：选接任者的对话框；读屏（VoiceOver）里背景听不到（S5）。
4. 新账户引导（S1、S3）：样子不变；Esc 和点遮罩没有反应；Tab 在引导里转圈；连接服务时打开的 VM 授权窗叠在引导上面。
5. 设置里改一个时区不保存就切页：离开确认框样子不变（S2）；Esc 回到编辑。电脑页"重启并应用"：重启确认框样子不变；点遮罩不关，Esc 关。
6. 全屏只读会话（窗口窄于 1100 px，点消息里的"来自……"）：Tab 不出框，Esc 关，焦点回到那条消息上的入口。
7. Settings 开着时断网等 toast 出来，再打开一个表单 sheet：toast 在 sheet 上面，"重试"能点，点了 sheet 不关（S4）。
8. 打开一张图片（灯箱）时断网：toast 在灯箱上面（Z7）。
9. 桌面客户端（要用这次构建的 UI 包）：开着删除确认按 ⌘N、⌘F：不响应（S6）。
10. 全程只用键盘走一遍 1–6。

**必须保持绿**（全部按退出码，2026-10-21 PDT 在本分支实测）：`typecheck`、`test:i18n`、`build`、`go build ./...`、`test:chat-bubbles`、`test:chat-switch`（含 `CHAT_SWITCH_ENGINE=webkit`）、`test:chat-safari`、`test:composer-typing`、`test:timeline`、`test:chat-motion`、`test:arrival-glide`、`test:live-fixes`、`test:send-receipts`、`test:sidebar-pins`、`test:handover-cards`、`test:standing-allowance`、`test:user-form`、`test:approval-facts`、`test:settings-polish`、`test:settings-shell`、`test:preloader`、`test:shell-budget`（含 `SHELL_BUDGET_ENGINE=webkit`）、`test:stream-cost`、`test:switch-budget`、`test:motion-tokens`、`test:avatar-frame-budget`、`test:dialogs`、`test:layers`、`test:primary-followups`、`test:onboarding`、`test:file-preview`、`test:resilience`、`test:mcp-oauth`、`node scripts/test-remote-desktop-control.mjs`，全部退出码 0、没有 SKIP。机器上同时跑着另外三步的测试（负载 3–5）：第一轮里 `test:shell-budget` 的"手机推入 4x 超过 25 ms 的帧 ≤ 1"（3 帧）和 `test:stream-cost` 的"4x 下没有超过 34 ms 的帧"（1 帧）各挂了一次，单独重跑都是 0。和起点交替跑同一项（见下表），起点在同样的负载下也会挂（手机推入 6 次里挂 1 次，两边一样），所以记为负载造成的抖动，不是这一步的回归。

**实测**（同一台机器，迁移前 = 本分支起点 `97ac43d`，即第 1 步完成时）

| 预算 | 迁移前 | 现在 | 上限 |
|---|---|---|---|
| 入口包（gzip，`test:shell-budget`） | 315,113 B（6 个文件） | **313,965 B（−1,148 B，余量 1,840 B）**（5 个文件） | 315,805 B |
| Radix 块 `ModalDialog-*.js`（懒加载，随设置、引导、删除确认等块空闲预取） | — | 13,981 B | — |
| 全部 JS（gzip） | 934,559 B | 948,815 B（+14,256 B） | — |
| 打开删除确认：点下到可见（`test:dialogs`，块已预取，5 次中位数，两轮交替） | 1x 9.5 / 8.6 ms；4x 25.8 / 23.5 ms | 1x 8.6 / 11.0 ms；4x 36.0 / 35.0 ms；最长任务 0 ms | 32 / 100 ms（新） |
| 设置窗首帧 1x 中位数（`SHELL_BUDGET_ONLY=panel`，6 轮交替） | 20.9–25.8 ms（均值 23.6） | 24.4–29.0 ms（均值 26.2）；最后一次完整运行 20.4 | 32 ms |
| 设置窗首帧 4x | 42.7–44.9 ms | 43.1–46.6 ms | 100 ms |
| 手机推入 4x 超过 25 ms 的帧（6 轮交替，首次测量） | 3、2、3、2、0、0（第 4 轮两次都超，挂） | 2、2、1、1、1、2（第 1 轮两次都超，挂） | ≤ 1（测试会重测一次） |
| 流式每块 1x / 4x（4 轮交替） | 2.22–2.82 / 8.0–12.2 ms | 2.23–2.63 / 7.6–10.3 ms | 4 / 16 ms |
| 切换会话 p95 1x / 4x（2 轮交替） | 19.0、19.5 / 66.1、80.3 ms | 22.9、22.5 / 66.1、69.6 ms | 60 / 100 ms |
| 打字每键脚本（减弱动效 / 动画开着） | 0.33 / 0.61 ms | 0.36 / 0.59 ms | ≤ 4 ms |
| 猫头像帧率（暂停前 / 恢复后） | 61.7 / 59.5 fps | 64.8 / 59.9 fps | 见 `test:avatar-frame-budget` |

读法：入口包变小，是因为 `ReassignPrimaryDialog` 和只给设置页用的 `PrimaryAssistantSetting` 离开了入口（后者也把 `settings/components` 带出去了），比新增的 `import()` 桩和 `WorkspaceToast` 多。看得出的代价只有一处：**4x CPU 下打开删除确认慢了约 11 ms**（25 → 36 ms 中位数），1x 下看不出差别。拆开看（Chrome trace，4x，每次打开的平均）：样式重算多约 10 ms，事件处理多约 11 ms——Radix 在 `<body>` 上关掉指针事件（继承属性，整页重算样式）、给页面其余部分加 `aria-hidden`、挂焦点监听。不用 Radix `Overlay` 省掉了滚动锁对 `<body>` 的改写（试过，约省几毫秒）。这是一次性的点击，不是高频交互，留在新预算（4x ≤ 100 ms）里很远。设置窗首帧 1x 均值高约 2.6 ms、切换会话 1x p95 高约 3 ms，都在各自轮次的抖动范围内（两边单次都有 20 到 29 ms 的跨度），4x 下没有差别；设置窗打开时不渲染任何 Radix 组件。

外观：用同一套截图脚本给迁移前后各拍一份（删除确认、已记住的审批 sheet：1280 / 390 px × 浅色 / 深色；离开确认、重启确认；引导全部 81 张），逐像素比。删除确认、sheet、重启确认 0 像素不同；离开确认 47 个像素（字的抗锯齿）；引导的差异和迁移前自己跑两次之间的差异是同一批位置、同一量级（动画中的猫），所以记为相同。第一次比对抓到两个确认框离开设置页后标题和按钮样式变了（标题变成 Fredoka 20px），已写回，`test:dialogs` 守着。

**原计划**（保留作对照）

| 项 | 内容 |
|---|---|
| 迁哪些 | `DialogShell.tsx`（删掉，换成一个包了 `AlertDialog` 的同名组件，`DeleteConversationDialog`、`ReassignPrimaryDialog` 跟着走）；`AdminSheets.tsx:35`、`SkillAccess.tsx:82`（表单 sheet，`--z-dialog` + 1）；`ViewOnlyChat.tsx:79`（全屏形态用 Dialog，≥1100 px 的卡片形态是非模态面板，不迁）；`VMOAuthDialog.tsx:107`；`Onboarding.tsx:82`；原生 `<dialog>` 中的两个确认框 `SettingsShell.tsx:101`、`ComputerResources.tsx:16`。**暂不迁**：`TakeoverDialog.tsx`（风险 ③）、`TerminalPanel.tsx:49-50`（面板内局部确认）、`PermissionCoach`（非模态提示）、图片灯箱 `MessageAttachment.tsx` 和 `FileViewer.tsx`（各自有专门的缩放和焦点处理，`FileViewer.tsx:123` 注释）。对还留在原生 `<dialog>` 里的两个，`ActionHints` 和 toast 改为挂到最上层打开的 `dialog[open]` 里面，解决 Z7 |
| 改哪些文件 | 上面列的组件；`ui/src/lazyPanels.tsx`（把 `ReassignPrimaryDialog` 改成懒加载，它现在随 `PrimaryAssistant` 在入口包里，`App.tsx:43`）；`interaction-system.css:43`、`settings/settings-components.css:144`、`extensions-system.css:101`、`onboarding.css:5`、`styles.css:142`；`ui/src/App.tsx:768`（见风险 ①） |
| 风险 | ① Radix 不设 `aria-modal`（§1.1），`App.tsx:768` 那道桌面快捷键闸会失效：每个 `Dialog.Content` 显式写 `aria-modal="true"`，或者把闸改成认 `[data-tofi-modal]`，二选一，新测试覆盖。② `busy` 时不许关闭（现在 `DialogShell.tsx:5-6` 的行为）：在 `onEscapeKeyDown`、`onPointerDownOutside` 里 `preventDefault`。③ 交接对话框里的远程桌面要把 Tab 等按键转发给远端，Radix 的焦点圈定会抢 Tab，所以这一个先不迁，等单独验证。④ Radix 模态会锁页面滚动（`react-remove-scroll`），要确认关掉以后消息区的滚动位置原样不动，尤其在 WebKit（`test:chat-safari`）。⑤ 原生 `<dialog>` 换成 portal 以后不再在浏览器顶层，层级完全靠 `--z-dialog`，要和第 1 步的刻度一起看。⑥ Esc 先由 Radix 在 document 上处理，App 自己的 Esc 处理（关面板等）不能同时触发 |
| 必须保持绿 | 第 2 步那组，加 `test:primary-followups`、`test:settings-polish`、`test:settings-shell`、`test:onboarding`、`test:handover-cards`、`node scripts/test-remote-desktop-control.mjs`、`test:file-preview`、`test:user-form`、`test:resilience` |
| 新测试 | 扩展 `test:overlays`：对每个对话框——打开后焦点在约定的元素上；Tab / Shift+Tab 不出框；Esc 关闭（`busy` 时不关）；点遮罩关闭（表单 sheet 有未保存内容时不关，以现有行为为准）；关闭后焦点回到打开它的按钮；背景元素对读屏隐藏；对话框里再开菜单，按一次 Esc 只关菜单。桌面快捷键闸：模拟 `tofiDesktop.onCommand("toggle-sidebar")`，对话框开着时侧栏不变。焦点环：沿用 `scripts/focus-audit.mjs` 的规则（鼠标打开不画焦点环，Tab 才画） |
| owner 预览看哪里 | ① 删除一个会话：确认框的样子、出现动画、Esc、点外面。② 设置 › 技能权限 等表单 sheet：叠在设置窗上面。③ 重新指派主助理。④ 新账户引导。⑤ 设置有未保存修改时离开、电脑重启确认。⑥ 全程只用键盘走一遍 |
| 体积 | 入口包：`DialogShell` 和 `ReassignPrimaryDialog` 搬出去，预计净减少（待实测）。懒加载：Radix Dialog 部分和第 2 步的块共用底层，第 2、3 步合计约 30.9 KB（§2.3）；只开对话框、没开过菜单时约 13.7 KB |

### 第 4 步：Motion（级联、手机推入、布局过渡）——只在比现有 WAAPI 更好时才做

**目标**：做一次有门槛的对照实验，结果写回本文。默认结论是"不引入"。

| 项 | 内容 |
|---|---|
| 实验对象 | ① 到达级联：用 Motion 的弹簧重做 `arrivalCascade.ts`，对照现有预算。② 手机推入：**owner 要"手指拖着返回"手势**（[D33](../decisions/0033-phone-swipe-back-experiment.md)），做两版对照：自研（Pointer Events + WAAPI / CSS）和 Motion 命令式 `animate()`（懒加载，22.8 KB），都做手势和松手后的弹簧收尾，留更好的那一版；CSS transition 保留给点按返回。③ 布局过渡：`AnimatePresence` 换掉 `useSurfacePresence`、`layout` 换掉 `App.tsx:491-494` 的 FLIP——这两项都要 React 组件版本，必须进入口包（≥30.8 KB），方案 A 下直接排除 |
| 做在哪 | Motion Lab（`ui/motion-lab.html`、`ui/src/motion-lab/`，单独打包，`ui/vite.motion-lab.config.ts`），不碰生产代码 |
| 通过门槛（全部满足才进入生产） | 同一台机器、同一组测试：帧预算不差于现有（`test:arrival-glide`、`test:shell-budget` 4x CPU 那一节"超过 25 ms 的帧 ≤ 1"）；中途打断（推入一半点返回、级联中又来一条消息）比现有更顺，有逐帧位置记录作证；减弱动效时立即到位；入口包不超预算；owner 在预览里看过并认可 |
| 改哪些文件（如果通过） | ① `ui/src/arrivalCascade.ts`、`ui/src/motion.ts`；② `ui/src/App.tsx` 手机推入部分、`ui/src/styles.css:317-325`；`ui/package.json`、`package-lock.json`、`ui/public/licenses/` |
| 风险 | 推入测试断言的是 `transitionend` 时长（`test-shell-budget.mjs:239-254`），换成 JS 驱动要重写这部分断言而且不能放松；`test:motion-tokens` 不认 Motion 的弹簧参数，要扩展词汇（弹簧参数也要成为令牌）；Motion 的弹簧是否对 `translate` 做合成线程加速，在 14.4.0 上待确认 |
| 必须保持绿 | `test:arrival-glide`、`test:shell-budget`（含 WebKit）、`test:chat-motion`、`test:motion-tokens`、`test:motion-lab`、`test:stream-cost`、`test:chat-safari` |
| 新测试（如果通过） | 中途打断：推入进行到一半点返回，逐帧记录位置，不许跳变（相邻两帧位移不超过正常速度的 2 倍）；拖动返回手势：390 px 触摸，拖过一半松手完成返回，不到一半弹回 |
| owner 预览看哪里 | 先在 Motion Lab 里并排看"现有"和"Motion"两个版本；通过后在手机（或 390 px 窗口）上看连发多条消息的级联、推入一半时点返回 |
| 体积 | 不通过：0。通过：手势用命令式 `animate()` 懒加载块约 22.8 KB，入口包只多一个桩（约 50 B） |

### 第 5 步：dnd-kit——只在比自研引擎更好时才做

**目标**：一次有门槛的对照实验，默认结论是"保留引擎"。

| 项 | 内容 |
|---|---|
| 实验 | 在单独的分支用 `@dnd-kit/core` + `sortable` 重做侧栏拖动和置顶落区，跑同一个 `test:sidebar-pins` |
| 通过门槛（全部满足） | `test:sidebar-pins` 全部通过，包括 60 个会话的帧预算、390 px 长按、键盘、减弱动效；拖动中 React 渲染次数不多于现有（用 React Profiler 或渲染计数记录）；入口包不超预算（dnd-kit 15.3 KB 进不了入口包，只能把整份列表放进懒加载，这本身就会让首屏列表变慢，要量）；带来引擎做不到的东西，例如拖进群组、更好的边缘自动滚动，并且 owner 想要 |
| 改哪些文件（如果通过） | `ui/src/useSidebarDnd.ts`、`ui/src/sidebarDndEngine.ts`（删除）、`ui/src/PinnedDock.tsx`、`ui/src/App.tsx` 侧栏部分、`ui/src/sidebar-pins.css`、AGENTS.md "Sidebar order and pins" 一节（D27：改实现要同步改 AGENTS.md） |
| 风险 | 每行重新渲染（§3.3）；长按 380 ms 和"长按不动就开菜单"要重写；AGENTS.md 明令禁止拿起时改变列表祖先的样式，dnd-kit 的 `DndContext` 状态变化可能触发这类重算 |
| 必须保持绿 | `test:sidebar-pins`、`test:shell-budget`、`test:switch-budget`、`test:chat-switch`、`test:perf-structure` |
| 新测试 | 如果通过：渲染计数测试（拖动一次，侧栏行渲染次数 ≤ 1 次/行）。如果不通过：不加 |
| owner 预览看哪里 | 通过时才需要：在 60 个以上会话的账户里拖动、拖进置顶区、手机长按拖动 |
| 体积 | 不通过：0。通过：懒加载 16.0 KB 起，入口包变化待实测 |

### 第 6 步：组件目录页

**目标**：一个页面列出每个组件的每种状态，owner 和以后的会话不用翻代码、不用造数据就能看。

| 项 | 内容 |
|---|---|
| 做法 | 新建 `ui/catalog.html` 和 `ui/src/catalog/`，**只在开发时可用**（[D34](../decisions/0034-component-catalog-dev-only.md)）：开发时 `npm run dev` / `dev:remote` 直接打开 `/catalog.html`；不加进 `build` 脚本，也不加进 `ui/vite.motion-lab.config.ts` 的 `input`（Motion Lab 是随 `dist/` 一起构建的，目录页不能照抄这一点）。用真组件、真 CSS、假数据，外面套 `.workspace`（§4 第 2 条）。每个组件列出：默认、悬停、聚焦（键盘）、按下、禁用、忙、出错、空；弹层列出打开/关闭、贴边翻转；浅色/深色、390 px/1280 px、减弱动效各一份 |
| 内容来源 | 组件和状态清单以 [`components.md`](components.md) 为准；本页是它的可视化 |
| 改哪些文件 | `ui/catalog.html`、`ui/src/catalog/**`、`ui/vite.config.ts` 不用改（开发服务器本来就提供 `ui/` 根目录下的 html；`ui/vite.config.ts:28` 的 `build` 没有设 `input`，默认只打 `index.html`，第 6 步动工时再核对一次）、`ui/package.json`（新测试脚本）、`deploy/open-source-allowlist.json`（`ui/motion-lab.html` 和 `ui/vite.motion-lab.config.ts` 是逐个列出的，`:92`、`:98`；新的 `ui/catalog.html` 也要加） |
| 风险 | 目录页和生产组件走样：只能导入生产组件，不许在目录页里另写一份；目录页不随正式版发布（D34），要有测试证明 `dist/` 里没有它 |
| 必须保持绿 | `build`、`test:shell-budget`（证明应用的包没变）、`test:motion-lab`、`test:i18n`（目录页上的说明文字也走 i18n，或者明确豁免，以 `ui/src/locales/README.md` 规则为准） |
| 新测试 | `test:catalog`（新建）：先断言正式构建的 `dist/` 里没有 `catalog.html` 和目录页的块（D34）；再在开发服务器里用 Playwright 逐个打开每一节，无页面错误；每个弹层打开后 `elementFromPoint` 在它中心取到的是它自己（没被盖住）；键盘打开/关闭；截图输出到 `CATALOG_SHOTS` 目录给 owner 看 |
| owner 预览看哪里 | http://localhost:5173/catalog.html ：逐节翻看，和生产界面对照 |
| 体积 | 应用入口包和懒加载块 0 字节（单独打包） |

## 6. 顺序和依赖

```
第 1 步 层级 + portal 根（完成）──► 第 2 步 菜单/卡片（完成）──► 第 3 步 对话框
                                  │
                                  └──► 第 6 步 目录页（第 2 步之后就能开始，随后续步骤补内容）
第 4 步 Motion 实验、第 5 步 dnd-kit 实验：互相独立，和第 1–3 步不冲突，可以并行；不通过就只在本文记结果
```

## 7. owner 的决定

2026-10-21 owner 回答了这四个问题（日期是协调者转述的；本分支的提交时间是 2026-10-20 晚上 PDT，哪个为准待确认）。

| # | 问题 | 推荐 | owner 的回答 | 决定 |
|---|---|---|---|---|
| Q1 | 入口预算：方案 A（库全懒加载，预算不动）还是方案 B（预算提高约 31 KB，直接静态导入） | A | **A**。每个库都懒加载，入口预算不动（`test:shell-budget` 入口 ≤ 315,805 B，写方案时余量约 741 B）；把现有弹层代码搬出入口包来支付懒加载桩 | [D31](../decisions/0031-entry-budget-plan-a.md) |
| Q2 | 第 1 步会有意改变几处先后顺序（设置窗、toast、启动画面），是否接受 | 接受，预览时逐条看 | **接受**，每一处都要列出来给 owner 在预览里看。清单是第 1 步的 L1–L14 | [D32](../decisions/0032-step1-stacking-changes-accepted.md) |
| Q3 | 是否要"手指拖着返回"的手机手势（决定第 4 步的实验②要不要做） | 待 owner 定 | **要**：手指把对话往右拖回到列表。做成有门槛的实验，自研一版、Motion 一版，留好的那个 | [D33](../decisions/0033-phone-swipe-back-experiment.md) |
| Q4 | 组件目录页是否随正式版发布 | 只在开发时可用 | **只在开发时可用**，不进正式版构建 | [D34](../decisions/0034-component-catalog-dev-only.md) |

## 附录 A：复现体积测量

在工作区外面做，不要在 `ui/` 里装依赖：

```sh
P=<scratch>/bundle-probe
rsync -a --exclude node_modules --exclude dist <repo>/ui/ $P/tofi/
cd $P/tofi && npm ci --ignore-scripts
npm install --ignore-scripts @radix-ui/react-tooltip @radix-ui/react-popover @radix-ui/react-dropdown-menu \
  @radix-ui/react-context-menu @radix-ui/react-dialog @radix-ui/react-alert-dialog react-aria-components@1.22.1 \
  motion@14.4.0 @dnd-kit/core@6.3.1 @dnd-kit/sortable @dnd-kit/utilities @dnd-kit/react@0.5.1 @floating-ui/react-dom
```

基线（与 `test-shell-budget.mjs:32-37` 相同）：

```js
await build({ configFile: false, root, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
const eager = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map(m => m[1]);
const gzip = eager.reduce((s, f) => s + zlib.gzipSync(readFileSync(join(outDir, f)), { level: 9 }).length, 0);
```

每个候选：在 `src/probe/<name>.tsx` 里渲染它的原语并导出 `Probe`；入口文件 `src/probe/entry-<name>-<mode>.tsx` 先 `import "../main"`，进入口模式写 `import { Probe } from "./<name>"; window.__probe = Probe;`，懒加载模式写 `window.__probe = () => import("./<name>");`；复制一份 `index.html`，把 `/src/main.tsx` 换成这个入口，作为 `rollupOptions.input` 构建，然后按上面的方法计算入口包；全部 JS 的 gzip 总和减去基线，再减去入口增量，就是懒加载块。注意：用 zsh 拼参数时 `$v:eager` 会被当成修饰符，要写成 `${v}:eager`。
