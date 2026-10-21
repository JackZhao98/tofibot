# Web UI 迁移到 headless 组件库：分步方案

> 状态：**方案，等 owner 点头**。点头之前不按本文改代码（[D28](../decisions/0028-headless-design-system-migration.md)：先出方案，再一步一步迁移，每一步都要预览）。
> 快照：提交 `748c774`（分支 `docs/system-opus`），体积实测日期 2026-10-20（PDT）。
> 相关文档：层级刻度和每一处 z-index 归到哪一层，以 [`tokens.md` §9](tokens.md) 为准，本文不另定一套；本文只写"怎么迁"。
> 相关决定：[D02 量交互开销](../decisions/0002-measure-interaction-cost.md)、[D18 渐变模糊范围](../decisions/0018-progressive-blur-scope.md)、[D19 到达级联](../decisions/0019-arrival-spring-cascade.md)、[D20 手机推入](../decisions/0020-phone-push-and-boot-screen.md)、[D21 侧栏拖动和置顶](../decisions/0021-sidebar-reorder-and-pins.md)、[D22 用 dev:remote 预览](../decisions/0022-preview-via-dev-remote.md)、[D30 常设规则](../decisions/0030-standing-operating-rules.md)。

## 0. 结论

| 领域 | 选什么 | 一句话理由 | 能不能进入口包 |
|---|---|---|---|
| 层级与 portal | 自己做：`--z-*` 令牌 + 一个 portal 根节点 | 不需要库，[`tokens.md` §9.3](tokens.md) 的刻度落地即可 | 只有几十字节 |
| 菜单、弹出卡片 | **Radix**：`DropdownMenu`、`Popover` | 不带样式，`asChild` 可以把现有元素和类名原样留住，外观不变；比 React Aria 小 17–21 KB | 不能，要懒加载（见 §2） |
| 悬停提示 | **保留自研的 `ActionHints`**，不用 Radix Tooltip | 现在是整页只有一个委托监听器，行里没有任何组件；Radix Tooltip 要在每个图标外套一层组件，懒加载块 19 KB | — |
| 对话框 | **Radix**：`Dialog`、`AlertDialog` | 焦点圈定、Esc 只关最上一层、关闭后焦点回到原处，现在每个对话框各写一遍 | 不能，要懒加载 |
| 动效 | **暂不引入 Motion**，第 4 步只做一次对照实验 | 到达级联已经是预先算好的弹簧关键帧交给 WAAPI 在合成线程跑；Motion 的 React 组件版本最少 30.8 KB，而且必须进入口包 | 不能 |
| 拖动 | **保留自研引擎**，第 5 步只做一次对照实验 | 引擎懒加载块 3,480 B，拖动过程中 React 不渲染；dnd-kit 15.3 KB 起，且 `useSortable` 让每一行在拖动中重新渲染 | 不能 |
| 组件目录页 | 自己做，仿照 Motion Lab 单独打包 | 不占应用的任何一个包 | 不占 |

最硬的约束是体积：**入口包现在离预算只剩 741 字节**（§2）。所以本方案里任何一个库都只能在懒加载的块里出现，而且第 2、3 步要把现有的弹层代码一起搬出入口包，用省下来的字节支付懒加载的入口开销。如果 owner 宁愿放宽预算换更简单的代码，见 §2.4 的方案 B，这需要 owner 拍板。

## 1. 现状：要迁的东西在哪

### 1.1 弹层和对话框清单

"在哪个包"一栏来自对 `748c774` 的生产构建按类名 grep 产物（方法见附录 A）；`index` 就是入口包。

| 组件 | 代码 | 现在怎么做的 | 在哪个包 | 钉住它的测试 |
|---|---|---|---|---|
| 悬停提示 `.action-hint` | `ui/src/InteractionSystem.tsx:51-84` | 整页一个委托监听器读 `data-hint`，portal 到 `document.body` | index | `test:chat-motion`（`scripts/test-chat-motion.mjs:358`） |
| 发送回执提示 `.send-receipt-tip` | `ui/src/SendReceipt.tsx:46` | 行内 `role="tooltip"`，z 5，被所在行封顶 | index | `test:send-receipts` |
| 侧栏右键菜单 `.sidebar-context-menu` | `ui/src/App.tsx:2462` | `position:fixed` 坐标定位；右键和拖动引擎的长按都会打开（`ui/src/sidebarDndEngine.ts:308`、`:351`） | index | `test:sidebar-pins`（按 `menuitem` 角色断言，`scripts/test-sidebar-pins.mjs:266-312`、`:458-464`） |
| 新建菜单、顶栏菜单 | `ui/src/App.tsx:610-611`（`useSurfacePresence`）、渲染在 `:2447`、`:2477` | 绝对定位，跟着父级层叠上下文 | index | 待确认（未找到按类名断言的测试） |
| 账户菜单 `.sidebar-account-menu` | `ui/src/OwnerSession.tsx:182` | 绝对定位 z 150，实际被侧栏 z 5 封顶（`tokens.md` Z3） | index | 无 |
| 输入框附件菜单 `.composer-add-menu` | `ui/src/App.tsx:3538` | 绝对定位，被输入框封顶（Z3） | index | 待确认 |
| 表情反应菜单 `.reaction-context-menu` | `ui/src/MessageReactions.tsx:77` | portal，`role="dialog"`，表情选择器懒加载（`:7`） | index | `test:chat-motion`（`:399-402`） |
| 身份卡 `.bot-identity-card` | `ui/src/BotIdentityCard.tsx:47` | 悬停/长按出现，portal，退场靠 `useSurfacePresence(position, 180)`（`:19`） | index | 无 |
| MCP 更多菜单 `.mcp-menu` | `ui/src/MCPSettings.tsx:232-233` | 绝对定位 | SettingsPanel（懒） | `test:settings-shell` |
| 删除对话框、重新指派主助理 | `ui/src/DialogShell.tsx:4-14`，用在 `DeleteConversationDialog.tsx:41`、`PrimaryAssistant.tsx:97` | 自写 Tab 圈定和 Esc；`role="alertdialog"` | DeleteConversationDialog 懒（1,027 B）；`PrimaryAssistant` 在 index（`App.tsx:43` 静态导入） | `test:primary-followups` |
| 表单 sheet `.sheet-overlay` | `ui/src/AdminSheets.tsx:35`、`ui/src/settings/SkillAccess.tsx:82` | portal，点遮罩关闭 | AdminSheets、SettingsPanel（懒） | `test:settings-polish` |
| 全屏只读会话 | `ui/src/ViewOnlyChat.tsx:79` | portal，`aria-modal` 只在非卡片形态 | ViewOnlyChat（懒） | 无 |
| VM 授权 | `ui/src/VMOAuthDialog.tsx:107` | portal | VMOAuthDialog（懒） | 待确认 |
| 交接（接管屏幕） | `ui/src/TakeoverDialog.tsx:68` | portal，里面是 `RemoteDesktopControl`，键盘要转发给远端 | QuestionCard（懒） | `test:handover-cards`、`scripts/test-remote-desktop-control.mjs` |
| 引导 | `ui/src/Onboarding.tsx:82` | `role="dialog" aria-modal`，自写圈定 | Onboarding（懒） | `test:onboarding` |
| 终端内确认 | `ui/src/TerminalPanel.tsx:49-50` | 面板内的 `alertdialog`，局部层 | TerminalPanel（懒） | 待确认 |
| 原生 `<dialog>` ×4 | `MessageAttachment.tsx:25,76`、`FileViewer.tsx:124`、`SettingsShell.tsx:101`、`ComputerResources.tsx:16` | `showModal()`，进浏览器顶层 | 各自所在包 | `test:file-preview`（FileViewer） |
| 权限提示 | `ui/src/PermissionCoach.tsx:95` | 非模态 `aside role="dialog"`，右下角 | PermissionCoach（懒） | 无 |

另有一处依赖现有写法的代码：`ui/src/App.tsx:768` 用 `document.querySelector('[role="dialog"][aria-modal="true"]')` 判断"有模态开着就不响应桌面客户端菜单快捷键"。Radix Dialog 只设 `role="dialog"`，**不设 `aria-modal`**（在 `@radix-ui/react-dialog@1.3.1` 的 `dist/index.mjs` 里 grep `aria-modal` 为 0），迁移时必须显式加上，否则这道闸会失效。

### 1.2 层级

现状和问题（Z1–Z7）见 [`tokens.md` §9.1-9.2](tokens.md)：产品 CSS 89 处 z-index、31 个不同的值，最大 1200（`onboarding.css:5`），TS 里还有 2147483000（`sendFlight.ts:19`）。

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
5. 所以默认走**方案 A**：库全在懒加载块里，第 2、3 步把现有弹层代码搬出入口包来支付入口开销，每一步合并前入口包都不超预算。**方案 B**（需要 owner 拍板）：把 `test-shell-budget.mjs:29` 的预算提高约 31 KB，直接静态导入 Radix，代码更简单，但入口包变大约 10%，首屏解析和执行时间要重新量（D02）。

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
- 手机推入是 CSS transition，测试断言的是 `transitionend` 的时长（§1.3）。Motion 真正比它强的只有一种情况：**手指拖着走的边缘返回手势**，松手后按速度用弹簧收尾。这个手势 TOFI 现在没有（在 `ui/src` 里 grep `swipe` 没有结果，motion-lab 除外），要不要做是产品决定，不是迁移本身。
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
3. **懒加载的弹层宿主。** 入口包里只放一个很小的 `OverlayHost`（第 2 步新建），它持有"现在开着什么、锚在哪"的状态，开的时候用 `React.lazy` 渲染真正的 Radix 实现；空闲时预取那个块，做法和 `useSidebarDnd.ts:33-35` 一样，所以用户第一次点的时候块通常已经在了。触发按钮仍是 TOFI 现有的普通 `<button>`，行里不增加任何组件（守住 `test:timeline-rows`、`test:stream-cost` 的每行开销）。
4. **类名不变。** Radix 的 `Content` 用现有类名（如 `className="sidebar-context-menu"`），现有测试的选择器和样式都能继续用。
5. **动画走令牌。** 进出场用 Radix 的 `data-state` 接现有关键帧和 `--m-*`；`test:motion-tokens` 不能新增例外。
6. **每一步一个合并**，同一个提交里更新本文的进度和 [`tokens.md`](tokens.md) 的归层表。合并前跑完该步列出的测试，**以退出码为准**（D30）；浏览器测试需要 `PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs`。
7. **每一步都要报入口包字节数**（迁移前、迁移后），超预算不合并。

## 5. 分步

每一步的"owner 预览"都用 D22 的方式：`cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`，在 Chrome 打开 http://localhost:5173 。

### 第 1 步：层级刻度和 portal 根节点

**目标**：把 [`tokens.md` §9.3](tokens.md) 的 13 个 `--z-*` 令牌落地，89 处 z-index 按 §9.4 归层，修掉 Z1、Z2、Z5、Z6。这一步不引入任何库。

| 项 | 内容 |
|---|---|
| 改哪些文件 | `ui/src/design-tokens.css`（加 `--z-*`）；新建 `ui/src/layers.ts`（`layerRoot()`：找到或创建 `#tofi-layers`；导出 `Z` 常量给 TS 用，做法同 `motion.ts`）；`tokens.md` §9.4 表里列出的全部 CSS 文件；`ui/src/sendFlight.ts:19`、`ui/src/mailSendFlight.ts:16`（删掉 TS 里的魔法数字）；现有的 `createPortal(..., document.body)` 改成 `layerRoot()`（`InteractionSystem.tsx:83`、`BotIdentityCard.tsx:47`、`MessageReactions.tsx:77`、`DeleteConversationDialog.tsx:41`、`PrimaryAssistant.tsx:97`、`AdminSheets.tsx:35`、`SkillAccess.tsx:82`、`ViewOnlyChat.tsx:79`、`VMOAuthDialog.tsx:107`、`TakeoverDialog.tsx:68`、`WebFileDropOverlay.tsx:70`） |
| 不在这一步 | 被父级封顶的三处（账户菜单、附件菜单、@菜单，Z3）和顶栏抬高（Z4）要靠 portal 才能修，放第 2 步；原生 `<dialog>`（Z7）放第 3 步 |
| 风险 | ① 归层会**有意**改变几处先后顺序：设置窗从 30 升到 `--z-dialog`（Z5），toast 升到模态之上（Z1），启动画面从 60 升到 `--z-cover` 但仍低于引导。每一处都是看得见的变化，列在 PR 描述里给 owner 逐条看。② 局部层要求父级有 `isolation:isolate`，漏加会让局部的小整数漏到全局去比。③ 移动端推入那一对（`styles.css:317-322`）的 6/7/2 是局部的，不能改成全局令牌 |
| 必须保持绿 | `typecheck`、`build`、`test:shell-budget`（含 WebKit：`SHELL_BUDGET_ENGINE=webkit`）、`test:sidebar-pins`、`test:chat-motion`、`test:send-receipts`、`test:settings-shell`、`test:settings-polish`、`test:onboarding`、`test:handover-cards`、`test:primary-followups`、`test:motion-tokens`、`test:i18n` |
| 新测试 | `test:layer-tokens`（新建 `ui/scripts/test-layer-tokens.mjs`，仿 `test-motion-tokens.mjs`）：静态检查——`z-index` 只能是 `var(--z-*)`、`calc(var(--z-*) ± 1)` 或 0–3 的局部小整数；局部值所在规则的组件根必须有 `isolation:isolate`；TS 里不许出现字面 `zIndex`；`layers.ts` 的 `Z` 和 CSS 令牌一致。浏览器检查——同时打开设置窗、toast、悬停提示，用 `document.elementFromPoint` 在重叠处取到的元素符合刻度顺序。改回旧值时测试要失败（D30） |
| owner 预览看哪里 | ① 打开设置窗，再触发一个错误 toast：toast 在设置窗上面。② 设置窗开着时悬停侧栏图标：提示不会盖在设置窗之外的错误位置。③ 刷新页面看启动画面：骨架和猫在最上面，之后引导（新账户）盖在启动画面上。④ 手机宽度下列表↔会话推入和以前一样。⑤ 发送飞行和寄信飞行动画仍在最上面 |
| 体积 | 入口包 +100 到 +300 B（`layers.ts` 和令牌，估计值，合并前实测）。余量 741 B 够用，但要报数 |

### 第 2 步：菜单和弹出卡片（Tooltip / Popover / DropdownMenu / ContextMenu）

**目标**：所有菜单和弹出卡片走同一个宿主、同一套键盘和关闭规则，用 Radix 实现；顺带修掉 Z3、Z4。

各原语的处理：

| 原语 | 用在哪 | 做法 |
|---|---|---|
| Tooltip | `ActionHints`、发送回执提示 | **不用 Radix Tooltip**。`ActionHints` 保留委托监听，只把它挂到 `layerRoot()`、用 `--z-tooltip`。发送回执提示改走同一套 `data-hint` 委托，从行内挪到 portal（`tokens.md` 归层表要求），群聊里"谁读了"的文字作为 hint 文本（D16 后续还要改成只在悬停/聚焦/点按时出现，界面细节待确认） |
| DropdownMenu | 新建菜单、顶栏菜单、账户菜单、输入框附件菜单、MCP 更多菜单 | Radix `DropdownMenu`，`modal={false}`（不锁整页指针和滚动，控件保持低调）。前四个在入口包里，走懒加载宿主；MCP 菜单本来就在懒加载的 SettingsPanel 里，可以直接静态导入 Radix |
| ContextMenu | 侧栏右键菜单 | **不用 Radix ContextMenu**：它的 Trigger 要包住每一行，而且自带 700 ms 触屏长按（`@radix-ui/react-context-menu` `dist/index.mjs:104`），会和引擎的 380 ms 长按（`sidebarDndEngine.ts:39`）抢。改为引擎照旧调用 `onMenu(id, x, y)`，宿主在 (x, y) 放一个 1×1 的隐形锚点，打开同一个 `DropdownMenu` |
| Popover | 身份卡、表情反应菜单 | Radix `Popover`，`Popover.Anchor` 锚到触发元素的位置；身份卡的悬停延时和长按逻辑保留在 `BotIdentityCard.tsx`，只把卡片本身交给 Popover |

| 项 | 内容 |
|---|---|
| 改哪些文件 | 新建 `ui/src/overlays/OverlayHost.tsx`（入口包，很小：状态、`openMenu`/`openPopover`、空闲预取）和 `ui/src/overlays/RadixOverlays.tsx`（懒加载块：Radix 实现、隐形锚点、焦点归还）；改 `ui/src/App.tsx`（新建菜单、顶栏菜单、附件菜单、侧栏右键菜单）、`ui/src/OwnerSession.tsx:182`、`ui/src/MessageReactions.tsx`、`ui/src/BotIdentityCard.tsx`、`ui/src/MCPSettings.tsx:232-233`、`ui/src/InteractionSystem.tsx`（`ActionHints`）、`ui/src/SendReceipt.tsx`；对应 CSS（`styles.css:30,71,141`、`v2-app.css:115-120,307`、`message-reactions.css:6`、`interaction-system.css:93,175`、`extensions-system.css:61`、`context-card.css:49` 去掉顶栏抬高）；`ui/package.json`、`ui/package-lock.json`；`ui/public/licenses/` 加 Radix 的 MIT 文本；`ui/THIRD_PARTY_NOTICES.md` |
| 风险 | ① **隐形锚点要先做小实验**：用键盘在真按钮上按 ↓ 打开时，Radix 是否把焦点放到第一项（这个行为原本由 Radix 自己的 Trigger 触发）；关闭后用 `onCloseAutoFocus` 阻止默认并把焦点还给真按钮；真按钮要自己带 `aria-haspopup`、`aria-expanded`、`aria-controls`。实验不通过就退到方案 B 或者让 Trigger 留在入口包。② 第一次打开时块还没到（网络慢）：宿主先什么都不画，块到了再开，要量"点下到出现"的时间。③ 菜单从行内挪进 portal 后丢失 `.workspace` 祖先选择器（§4 第 2 条）。④ `modal={false}` 时外部点击要关闭菜单，同时不能吞掉那次点击要做的事（例如点另一个会话） |
| 必须保持绿 | 第 1 步那组，加 `test:timeline-rows`、`test:stream-cost`、`test:composer-typing`、`test:switch-budget`、`test:chat-switch`（含 `CHAT_SWITCH_ENGINE=webkit`）、`test:perf-structure`、`test:chat-safari`、`test:layer-tokens` |
| 新测试 | `test:overlays`（新建）：对每个菜单——鼠标打开、键盘打开（Enter / 空格 / ↓）后焦点在第一项、上下键移动、Home/End、Esc 关闭且焦点回到触发按钮、点外部关闭、Tab 离开时关闭；菜单在视口边缘时翻转不出界（390 px 和 1280 px）；侧栏长按 380 ms 打开右键菜单而不是 700 ms；菜单之上悬停提示可见。体积：入口包不超预算、`RadixOverlays` 不在入口包里（仿 `test-shell-budget.mjs:41-44` 的写法）。性能：打开最重的菜单时没有超过 50 ms 的帧 |
| owner 预览看哪里 | ① 侧栏右键、手机长按会话：菜单出现的位置、动画和以前一样。② 侧栏底部账户菜单：不再被侧栏边界切掉（Z3）。③ 输入框的附件菜单和 @菜单：不再被输入框切掉。④ ≥1100 px 打开上下文浮卡，再开顶栏菜单：菜单在浮卡上面（Z4）。⑤ 悬停 @提及的 Bot 名字出身份卡；对一条消息开表情反应。⑥ 用键盘：Tab 到"新建"按钮，按 ↓，上下键选，Esc |
| 体积 | 入口包：`OverlayHost` + 若干 `import()` 桩，约 +0.5 到 +1 KB（估计），减去搬出入口包的菜单代码（待实测），目标是净值 ≤ 0。懒加载块：约 29.7 KB（§2.3 实测"第 2 步推荐组合"），空闲时预取 |

### 第 3 步：对话框

**目标**：所有模态对话框用 Radix `Dialog` / `AlertDialog`：焦点圈定、Esc 只关最上面一层、关闭后焦点回到打开它的元素、背景对读屏隐藏，全部统一。

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
| 实验对象 | ① 到达级联：用 Motion 的弹簧重做 `arrivalCascade.ts`，对照现有预算。② 手机推入：只在 owner 想要"手指拖着返回"手势时，用命令式 `animate()`（懒加载，22.8 KB）做手势和松手后的弹簧收尾；CSS transition 保留给点按返回。③ 布局过渡：`AnimatePresence` 换掉 `useSurfacePresence`、`layout` 换掉 `App.tsx:491-494` 的 FLIP——这两项都要 React 组件版本，必须进入口包（≥30.8 KB），方案 A 下直接排除 |
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
| 做法 | 仿照 Motion Lab：新建 `ui/catalog.html` 和 `ui/src/catalog/`，单独打包（加进 `ui/vite.motion-lab.config.ts` 的 `input`，或者单独一份配置），开发时 `npm run dev` / `dev:remote` 直接打开 `/catalog.html`。用真组件、真 CSS、假数据，外面套 `.workspace`（§4 第 2 条）。每个组件列出：默认、悬停、聚焦（键盘）、按下、禁用、忙、出错、空；弹层列出打开/关闭、贴边翻转；浅色/深色、390 px/1280 px、减弱动效各一份 |
| 内容来源 | 组件和状态清单以 [`components.md`](components.md) 为准；本页是它的可视化 |
| 改哪些文件 | `ui/catalog.html`、`ui/src/catalog/**`、`ui/vite.motion-lab.config.ts`（或新配置）、`ui/package.json`（`build` 脚本和新测试脚本）、`deploy/open-source-allowlist.json`（`ui/motion-lab.html` 和 `ui/vite.motion-lab.config.ts` 是逐个列出的，`:92`、`:98`；新的 `ui/catalog.html` 也要加） |
| 风险 | 目录页和生产组件走样：只能导入生产组件，不许在目录页里另写一份；目录页要不要随正式版发布（Motion Lab 现在是随 `dist/` 一起构建的）待 owner 定 |
| 必须保持绿 | `build`、`test:shell-budget`（证明应用的包没变）、`test:motion-lab`、`test:i18n`（目录页上的说明文字也走 i18n，或者明确豁免，以 `ui/src/locales/README.md` 规则为准） |
| 新测试 | `test:catalog`（新建）：构建目录页，用 Playwright 逐个打开每一节，无页面错误；每个弹层打开后 `elementFromPoint` 在它中心取到的是它自己（没被盖住）；键盘打开/关闭；截图输出到 `CATALOG_SHOTS` 目录给 owner 看 |
| owner 预览看哪里 | http://localhost:5173/catalog.html ：逐节翻看，和生产界面对照 |
| 体积 | 应用入口包和懒加载块 0 字节（单独打包） |

## 6. 顺序和依赖

```
第 1 步 层级 + portal 根 ──► 第 2 步 菜单/卡片 ──► 第 3 步 对话框
                                  │
                                  └──► 第 6 步 目录页（第 2 步之后就能开始，随后续步骤补内容）
第 4 步 Motion 实验、第 5 步 dnd-kit 实验：互相独立，和第 1–3 步不冲突，可以并行；不通过就只在本文记结果
```

## 7. 要 owner 定的事

| # | 问题 | 推荐 |
|---|---|---|
| Q1 | 入口预算：方案 A（库全懒加载，预算不动）还是方案 B（预算提高约 31 KB，直接静态导入） | A |
| Q2 | 第 1 步会有意改变几处先后顺序（设置窗、toast、启动画面），是否接受 | 接受，预览时逐条看 |
| Q3 | 是否要"手指拖着返回"的手机手势（决定第 4 步的实验②要不要做） | 待 owner 定 |
| Q4 | 组件目录页是否随正式版发布 | 只在开发时可用 |

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
