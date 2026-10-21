# TOFI 网页界面组件清单

这份清单列出网页界面（`ui/src/`）里每一个组件和交互模式：它是什么、在哪个文件、有哪些状态、行为契约（焦点、键盘、关闭方式、位置、层级、动效、触屏），以及已知的问题。目的是让后来的人和 AI 会话改界面之前，先知道现在的东西是怎么工作的、哪里已经不稳。

- 基线：`748c774`（branch `docs/system-opus`，2026-10-20）。下文的 `file:line` 都按这个提交写，代码改了就要跟着改这份文件（`docs/process.md`，"改了设计规范或组件"一行）。
- 颜色、字体、间距、阴影、模糊、动效令牌和 z-index 的完整清单在同目录的 [`tokens.md`](tokens.md)。本文只写每个组件用到的层级数值，层级问题的编号（Z1–Z7）引用 `tokens.md` 第 9.2 节。
- **层级已在迁移第 1 步换成 `--z-*` 刻度**（分支 `ds/step1-layers`）：本文各节"层级"一行和层级相关的问题（C15、C16、C21、C23、C26、C31、C35、C40）已按该分支更新，括号里是迁移前的数值；其余 `file:line` 仍按 `748c774`。
- **菜单和弹出卡片已在迁移第 2 步换成 Radix**（分支 `ds/step2-menus`，`ui/src/overlays/`）：第 3、4（C6）、9、10、11、12 节和问题 C6、C18、C20、C21、C24、C25 已按该分支更新，`file:line` 是该分支的。
- **对话框已在迁移第 3 步换成 Radix**（分支 `ds/step3-dialogs`，`ui/src/overlays/ModalDialog.tsx`）：第 8 节和相关问题（C12、C13、C14、C17、C23）已按该分支更新。
- **每个组件的每种状态都能在组件目录页里直接看**（迁移第 6 步，分支 `ds/step6-catalog`，只在开发时可用，[D34](../decisions/0034-component-catalog-dev-only.md)）：`cd ui && npm run dev`，打开 http://localhost:5173/catalog.html 。目录页按本文 §1–§23 分节（`test:catalog` 断言一一对应）；本文加了组件或状态，目录页 `ui/src/catalog/sections/` 要跟着加，画不出来的写进该节的 `gaps`。
- **六步已合在集成分支 `ds/integrate`**（还没进 `main`）：上面按各步骤分支写的 `file:line`，在 `ds/integrate` 上大文件（尤其 `App.tsx`）的行号会因为其它几步的改动偏几行，以符号名为准。第 4 步的手机右滑返回见 [`migration.md`](migration.md) 第 4 步和 `AGENTS.md`"Web UI motion"。
- 设计原则和检查方法在 [`principles.md`](principles.md)。本文每条已知问题都标了它违反的原则（P1–P8）。
- 桌面客户端（Electron）消费同一份网页 UI 构建产物（`AGENTS.md` "Core source boundaries"），用 `html[data-desktop]` 和 `isDesktop`（`ui/src/desktop.ts:45`）分支。本文说"网页端"指 `html:not([data-desktop])`。

问题严重度：**高** = 用户会直接撞上的错误行为或违反 owner 明确决定；**中** = 键盘、读屏、特定尺寸下才出现，或会让以后的改动出错；**低** = 代码卫生、死代码、文案不一致。

---

## 0. 所有组件共用的约定

这些是现有代码已经统一做到的，新组件必须照做。

| 约定 | 现在怎么做 | 证据 |
|---|---|---|
| 焦点环只给键盘 | `<html data-input>` 在按导航键时变 `keyboard`，任何指针按下变回 `pointer`；`focus-ring.css` 在 `pointer` 下隐藏所有焦点环。Escape 和修饰键不切换，所以用鼠标打开的对话框关掉后焦点安静地回去 | `ui/src/focusModality.ts:1-20`；共用审计 `ui/scripts/focus-audit.mjs`（被 settings-polish、chat-bubbles、file-preview、onboarding 四个测试调用） |
| 关掉的东西退出 Tab 顺序 | 折叠或退场中的内容挂 `inert`，不靠 `display:none` | `Disclosure`（`ui/src/InteractionSystem.tsx` 中 `inert={!open}`）、`create-menu`/`more-menu` 的 `inert={!open}`、`ApprovalCard` 的折叠区 |
| 退场有动画 | `useSurfacePresence(value, duration)` 在值变空后把旧值保留 `duration` 毫秒（减少动效时为 0），让关闭和打开一样连续 | `ui/src/InteractionSystem.tsx`（`useSurfacePresence`） |
| 动效令牌 | 时长 `--m-instant` 80 / `--m-press` 120 / `--m-quick` 140 / `--m-base` 220 / `--m-panel-in` 280 / `--m-panel-out` 180 / `--m-flight` 640 ms，曲线五种，JS 镜像在 `motion.ts`；`test:motion-tokens` 拦字面量 | `AGENTS.md` "Web UI motion"，`ui/src/motion.ts:4-11` |
| 减少动效 | 每个动画都有 `prefers-reduced-motion: reduce` 分支：要么瞬间到位，要么只剩淡入淡出 | 例：`ui/src/sidebar-pins.css`（`@media (prefers-reduced-motion:reduce)`）、`ui/src/styles.css:324-328` |
| 慢请求才显示加载 | `DelayedFeedback`（默认 300 ms 后才挂载）和 `useGraceFlag`（短于宽限期的状态什么都不显示） | `ui/src/DelayedFeedback.tsx:3-10`，`ui/src/useGraceFlag.ts:3-11` |
| 文案本地化 | 所有用户可见文字在 `ui/src/locales/<lang>/<namespace>.json`；语气规则在 `ui/src/locales/README.md` "Tone" | `npm --prefix ui run test:i18n` |

还**没有**统一的（这正是 D28 迁移要解决的）：焦点陷阱原有 9 份各自手写的实现，第 3 步之后迁过去的对话框共用 `ModalDialog`（Radix），只剩交接窗和 App 的详情窗格（设置窗等）两份自写（见第 8 节表格"焦点陷阱"列）；菜单的方向键、关闭后焦点回到哪里原来各写各的（第 9 节，**第 2 步已统一**到 `ui/src/overlays/`）；z-index 原有 31 个不同的值，第 1 步已统一到 13 层刻度，由 `test:layers` 守着（`tokens.md` 第 9 节）。

---

## 1. 消息气泡（MessageBubble / MessageRow）

**是什么**：时间线里的一行。`MessageRow` 是外层（memo，按 `rowModelEqual` 比较），`MessageBubble` 画气泡本身。一条消息按 `kind` 走不同分支：普通消息、`ui_card`（展示卡片）、`scheduled_task`（定时任务行）、`message_ref`（相关对话链接）、`notice`（系统提示，含续跑提示 `resume`）、委派（handoff/forward 成一行"交给了谁"，简报折叠在 `<details>` 里）。

**文件**：`ui/src/App.tsx:2661-2778`（`MessageBubble`）、`:2780-2808`（`MessageRow`）、`ui/src/timelineRow.ts`（`rowModelEqual`）、`ui/src/draftStream.ts` + `useLiveDraft`（流式文字）、`ui/src/arrivalCascade.ts`（到达动效）、`ui/src/sender-groups.css`、`ui/src/v2-app.css`。

**状态**

| 状态 | 表现 | 证据 |
|---|---|---|
| 发送中（自己的） | 内容透明度 0.72，读屏读"发送中" | `ui/src/send-receipt.css`（`.is-sending .message-content{opacity:.72}`），`ui/src/SendReceipt.tsx:26` |
| 发送失败 | 气泡下一行"未发送 · 重试" | 见第 3 节 |
| 流式输出中 | 文字从 `draftStream` 直接进这一行，不经过 Workspace 状态；行内光标 | `AGENTS.md` "Web timeline performance"，`App.tsx:2781-2784` |
| 工具运行中 / 结束 | 网页端答案上方有工具行；工具行收起时答案用 `MOTION.panelIn` 滑到新位置（减少动效时不动） | `App.tsx:2667-2681` |
| 新到达 | `.new-arrival` / `.was-streamed`；整条回复到达时弹簧链（D19） | `App.tsx:2792`，第 22 节 |
| 跳转目标 | 滚到中间并闪一次 `is-jump-target`，1.7 s 后去掉，不留常驻框 | `App.tsx:352-362`（`jumpToMessage`） |
| 群聊 / 单聊 | 群聊显示小头像和名字（同一发送者连续消息合并）；单聊 Bot 消息没有容器 | `ui/src/sender-groups.css:38-39`，`App.tsx:2735` |

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 整个列表只占一个 Tab 停靠点（最新一条，`tabIndex={m.isLast ? 0 : -1}`，`App.tsx:2792`）；上下方向键、Home、End 在消息间移动；Tab 进入当前消息的操作行，Escape 回到消息（`App.tsx:2232-2244`） |
| 键盘 | 同上。消息本身没有快捷键 |
| 关闭 | 不适用 |
| 位置 | 文字列居中；用户消息靠右。底部为输入框预留 `--composer-height + 44px`，单聊有猫时 `+64px`（`ui/src/sender-groups.css:43-44`）；软键盘弹起时缩到 `+8px`（`:47-49`） |
| 层级 | 文档流，无 z-index |
| 动效 | 到达：弹簧链（第 22 节）；工具行收起后的答案滑动；全部有减少动效分支 |
| 触屏 | 轻点气泡正文打开操作行（只在 `(hover: none)` 下，选中文字、点链接/按钮/代码块不算，`App.tsx:2391-2398`）；桌面客户端右键打开反应菜单（`App.tsx:2792` 的 `onContextMenu`） |

**测试**：`test:chat-bubbles`、`test:timeline-rows`、`test:stream-cost`、`test:chat-switch`、`test:switch-budget`、`test:chat-motion`、`test:arrival-glide`、`test:message-markdown`、`test:perf-structure`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C1 | 低 | 死代码：`mention` 只在 `assignment` 为真时有值，但 `assignment` 为真时 `:2726` 已经提前 `return`，所以 `inlineMention` 永远是 `undefined`，正文里那个 `BotIdentityCard` 不会出现 | `App.tsx:2726-2732` | — |
| C2 | 中 | 一个组件里有 7 种 `kind` 分支和网页/桌面两套工具行（`WebToolActivityRun` / `DesktopToolActivityRun`），任何样式改动都要分别验证 | `App.tsx:2682-2778`，`:3010`、`:3091` | P6 |

---

## 2. 消息操作行（MessageHoverActions）

**是什么**：每条消息下方一行低调的线条图标：复制、表情反应、回复、查看过程（"View activity"），最后是相对时间，自己的消息末尾再加送达/已读标记。没有外框、底色和阴影（D17）。只给网页端；桌面客户端用右键菜单和原生 `title`。

**文件**：`ui/src/App.tsx:308-345`（组件）、`:347-350`（`MessageActionsSlot`）、`:2203-2235`（悬停/焦点/轻点的委托处理）、`ui/src/messageActionsStore.ts`（当前哪条消息显示操作行）、`ui/src/v2-app.css:580-610`。决定：[D17](../decisions/0017-quiet-message-action-row.md)。

**状态**：隐藏 / 悬停显示 / 键盘焦点显示 / 轻点显示；复制后图标变勾 1.2 s（`App.tsx:315-318`）；"查看过程"按钮有 `aria-pressed`。

**行为契约**

| 项 | 契约 |
|---|---|
| 挂载 | 只有当前那一条消息挂载操作行（`useMessageActionsOpen`，三个来源 hover/focus/tap 任一命中）；悬停、焦点移动或轻点只改两个小插槽，不重渲染对话（`messageActionsStore.ts:3-6`） |
| 焦点 | `role="toolbar"`，整行一个 Tab 停靠点（第一个按钮），左右方向键、Home、End 在行内移动（`App.tsx:320-331`）；只有键盘焦点（`:focus-visible`）会打开操作行，鼠标按下聚焦不会（`App.tsx:2229-2230`） |
| 键盘 | Escape 从操作行回到消息（`App.tsx:2235`） |
| 关闭 | 指针离开消息或进入消息间空隙后 120 ms 宽限再收（`App.tsx:2216-2228`）；触屏点别处就收（`:2203-2210`） |
| 位置 | 消息正下方、文档流内；悬停设备上这 28 px 的行高**始终预留**，所以显示与否页面都不动；同一发送者消息间距 28–36 px，换人再加 14 px（`v2-app.css:601-606`，`test:chat-bubbles` 检查 28–36） |
| 层级 | 文档流，无 z-index |
| 动效 | 只做透明度，`--m-instant` 80 ms，无位移（`v2-app.css:591-592`） |
| 触屏 | 按钮 44 px；轻点打开时在文档流里插入一行，**会把下面的内容往下推**（有意为之，换来不盖住相邻消息，`v2-app.css:607-610`） |

**测试**：`test:chat-bubbles`（间距、触屏轻点，`ui/scripts/test-chat-bubbles.mjs:198-201`、`:418-435`）；`ui/scripts/test-message-spacing-browser.mjs`（没有对应的 npm script，只能直接 `node` 运行）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C3 | 中 | 操作行按钮的悬停提示（第 10 节 ActionHints）默认出现在按钮**上方**（按钮距视口顶部 > 62 px 时），正好压在这条消息的最后一行上 | `ui/src/InteractionSystem.tsx:70`（`y: rect.top > 62 ? rect.top - 8 : …`） | P2 |
| C4 | 低 | D17 决定记录写"悬停移入时没有淡入"（出自 `bfab118` 提交说明），代码里其实有 80 ms 透明度淡入（`message-bar-in`，`0b66420` 加入） | `docs/decisions/0017-quiet-message-action-row.md`"影响"，对照 `v2-app.css:591-592` | — |

---

## 3. 送达回执（SendReceipt / SendReceiptMark）

**是什么**：自己发出的消息的状态。发送中 = 气泡变淡 + 读屏文字；已送达 = 一个勾；已读 = 两个勾（某个 Bot 的运行已经开始处理这条消息）；失败 = 气泡下"未发送 · 重试"。已送达和已读只在操作行末尾出现（D16 的"以后"一条已实施，`1d2b322`）。群聊里已读标记的提示写出是谁读了。

**文件**：`ui/src/SendReceipt.tsx`、`ui/src/sendReceipts.ts`（`receiptFor` 是唯一判定处，`:36-42`）、`ui/src/send-receipt.css`。决定：[D16](../decisions/0016-optimistic-send-receipts.md)。

**状态**：`sending` / `delivered` / `read` / `failed`（`sendReceipts.ts:3`）。已读的判定：该消息触发的运行进入 running/waiting/done，或在开始执行后才失败（`sendReceipts.ts:9-13`）；历史分页里看不到运行时，以后面有 Bot 回复推断为已读（`:40`）。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 已读且是群聊时，标记加入操作行的方向键序列（`data-roving`，`tabIndex=-1`），聚焦 150 ms 后显示读者名单（标记的 `data-hint`，`SendReceipt.tsx`） |
| 键盘 | 随操作行 |
| 关闭 | 提示随焦点/悬停离开、任意按下、按键、滚动消失（`ActionHints` 的规则） |
| 位置 | 标记在操作行末尾、时间之后；失败的重试行在气泡下方 14 px 高 |
| 层级 | 读者名单是 `ActionHints` 的提示（`--z-tooltip`，portal 进 `#tofi-layers`），`data-hint-tone="quiet"` 让它保持原来的白底外观（`.action-hint.is-quiet`，`interaction-system.css:97`）；名单在提示开着时变了，文字跟着变（`InteractionSystem.tsx:60` 的 `MutationObserver`）。迁移前是行内的 `.send-receipt-tip`，z 5 |
| 动效 | 状态变化时勾"落定"一次（`--m-panel-in`，`send-receipt.css` `send-receipt-settle`）；减少动效下不动 |
| 触屏 | 重试按钮视觉 14 px，触屏下用 `::after` 把点击区扩到约 44 px（`send-receipt.css`，`@media (pointer:coarse)`） |

**测试**：`test:send-receipts`（`ui/scripts/test-send-receipts.mjs`：发送中→已送达→已读位置不变、失败重试、切走再回来、读者名单）、`test:overlays`（名单在 `#tofi-layers`）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C5 | 中 | **已读的强调色不再生效**。`.send-receipt[data-state="read"] .send-receipt-mark` 要求标记在 `.send-receipt` 里面，但 `1d2b322` 之后标记渲染在操作行里（`App.tsx:339`），自己带 `data-state`。结果已读和已送达颜色一样（都是 `--muted`），只能靠一个勾和两个勾区分。测试只断言颜色"不是空字符串"，抓不到 | `ui/src/send-receipt.css:6-7`；`ui/scripts/test-send-receipts.mjs:240-242`。是否有意让已读也变低调：待确认（测试名写的是"accent colour resolved"，看起来本意是强调色） | P3 |

---

## 4. 输入框（Composer）

**是什么**：底部的输入区：文本框、@提及菜单、附件按钮和菜单（图片、文件、截屏）、附件小卡片、回复引用条、听写控件、停止按钮、发送按钮、"连接模型"提示。

**文件**：`ui/src/App.tsx:3184-3542`（`Composer`），`:3127`（`DictationControls`），`ui/src/composerDraft.ts`，`ui/src/useDictation.ts`，`ui/src/conversation-workspace.css`，`ui/src/v2-app.css:280-370`，`ui/src/web-mention.css`。决定：[D02](../decisions/0002-measure-interaction-cost.md)。

**状态**：空（发送按钮变灰，`is-send-empty`）/ 有字 / 发送中 / Bot 运行中（显示停止按钮）/ 只读（归档会话）/ 没配模型（`is-no-model`，上方出提示）/ 拖文件进来（`is-dragging`）/ 上传中（`web-upload-ring`）/ 听写录音中 / 转写中 / 有回复引用 / @菜单打开。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 回复某条消息后焦点回到文本框（`App.tsx:2404`） |
| 键盘 | Enter 发送，Shift+Enter 换行，输入法组字中（`isComposing` 或 `keyCode 229`）不发送（`App.tsx:3426-3433`）；@菜单打开时上下键选、Enter 选中、Escape 关菜单；⌘⇧I 选图片、⌘⇧F 选文件（`:3354-3360`） |
| 关闭 | 附件菜单：点外面或 Escape 关（`:3339-3352`） |
| 位置 | 固定在消息窗格底部；`--composer-height` 由 ResizeObserver 测量，给消息区留白用 |
| 层级 | 输入框 `--z-chrome`（迁移前 4）且 `isolation:isolate`（`conversation-workspace.css:12`），所以它里面的附件菜单（`--z-menu`）、@菜单（`--z-popover`）、权限提示（`--z-toast`）都被封顶在输入框这一层（`tokens.md` Z3，第 2 步改 portal） |
| 动效 | @提及选中时名字飞到文本框里（`web-mention-ghost`）；发送飞行（`sendFlight.ts`） |
| 触屏 | 网页端输入字号 16 px，避免 iOS 放大（`34c7749`）；软键盘弹起时猫隐藏、底部留白缩小（`sender-groups.css:45-49`） |
| 交互开销 | 打字只重渲染输入框：每键脚本 ≤ 4 ms、排版 ≤ 2 次（`ui/scripts/test-composer-typing.mjs:158-165`）；草稿停手 400 ms 后才同步给 App（D02） |

**测试**：`test:composer-typing`、`test:composer-draft`、`test:reply-quote`、`test:reply-ack`；软键盘相关的 `ui/scripts/test-visual-viewport.mjs` 没有 npm script。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C6 | 中 | ~~附件菜单是 `role="menu"`，但打开时焦点不进菜单、没有方向键、Escape 关掉后焦点不回到触发按钮~~ **第 2 步已修**（Radix 菜单，第 9 节） | `App.tsx` `Composer`（`composer-add-menu`） | P8 |
| C7 | 中 | 快捷键只认 `metaKey`，菜单里也写死 `⌘⇧I`、`⌘⇧F`。在 Windows/Linux 浏览器上 Ctrl 不触发，显示的 ⌘ 也不存在 | `App.tsx:3354-3360`，`:3538`（`<kbd>⌘⇧I</kbd>`） | P5 |
| C8 | 中 | @提及菜单是 `role="listbox"`，但文本框没有 `aria-controls`/`aria-activedescendant`，读屏听不到当前选中项；Tab 不能选中 | `App.tsx:3538`（`mention-menu`），全文件 grep `aria-activedescendant` 为 0 | P8 |

---

## 5. 输入框上的猫（ComposerPerch）

**是什么**：单聊时蹲在输入框右上沿的那只 Bot 的猫，表示谁在干活：发送时"看"、运行中"工作"、收到回复后"开心"。D23 定了顶栏不再放第二只猫。

**文件**：`ui/src/App.tsx:3156-3172`、`ui/src/CatStage.tsx`、`ui/src/v2-app.css:372-374`、`ui/src/sender-groups.css:44-47`。决定：[D23](../decisions/0023-no-second-cat-in-header.md)。

**状态**：idle / look（刚发送）/ work（`activeRun.status === "running"`）/ happy（发送后收到新回复）。闲置 20 s 无输入或有过渡动画时猫停下（`39a1822`）。

**行为契约**

| 项 | 契约 |
|---|---|
| 出现条件 | 只在网页端、非只读、单聊（`conversation.kind === "dm"`）时出现；群聊没有（`App.tsx:3538` `ComposerPerch` 的条件） |
| 焦点 / 键盘 | `aria-hidden`、`pointer-events:none`，不可聚焦、不挡点击 |
| 位置 | 输入框内 `right:84px; top:-74px`，84×84 px；没配模型提示出现时再往上抬 `--banner-lift` |
| 层级 | 输入框内部 `z-index:2` |
| 触屏 | 软键盘弹起时隐藏 |

**测试**：`test:avatar-frame-budget`（猫的逐帧开销）、`test:composer-typing`（猫不能让打字变卡）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C9 | 中 | 几何上可能压住最后一条消息：猫比输入框顶边高出 74 px，而有猫时底部留白只比没猫多 20 px（共 `--composer-height + 64px`）。靠右的用户气泡在 390 px 宽时是否被猫盖住：待确认（要在预览里实测） | `v2-app.css:372`，`sender-groups.css:43-44` | P2 |

---

## 6. 侧栏会话行（conversationRow）

**是什么**：侧栏里的一行：头像（猫或群标）、名字、主助理徽章、最近一条预览、时间、未读数或任务状态。可拖动排序（D21），右键或长按出菜单（第 9 节）。

**文件**：`ui/src/App.tsx:2272-2297`（`conversationRow`）、`:2314`（隐藏 Bot 行）、`ui/src/sidebarOrder.ts`（纯排序）、`ui/src/sidebarDndEngine.ts`（拖动引擎，空闲时加载的独立 chunk）、`ui/src/useSidebarDnd.ts`、`ui/src/web-sidebar-list.css`、`ui/src/sidebar-pins.css`。决定：[D21](../decisions/0021-sidebar-reorder-and-pins.md)；服务端契约见 `AGENTS.md` "Sidebar order and pins"。

**状态**：普通 / 当前（`active`）/ 未读（数字徽章，>99 显示 99）/ 运行中（呼吸圆点）/ 任务状态（`?` 等待或需要处理、`!` 失败、`×` 取消，`App.tsx:2278`）/ 拖动中（`is-dnd-dragging`）/ 落定中（`is-dnd-settling`）/ 侧栏收起（只剩头像，名字进悬停提示）。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 每行一个 `<button>`，`aria-label` 拼上名字、任务状态、未读数（`App.tsx:2280`） |
| 键盘 | Alt+↑/↓ 移动当前行（`aria-keyshortcuts`）；Menu 键或 Shift+F10 打开行菜单（`sidebarDndEngine.ts:345-352`）；拖动中 Escape 取消（`:132`）；每次移动用 `role="status"` 播报（`App.tsx:2453`） |
| 关闭 | 不适用 |
| 位置 | 侧栏滚动区；排序在服务端（`PUT /api/conversations/order`） |
| 层级 | 拖动中的行 `z-index:40`（`sidebar-pins.css:6-10`），在侧栏层叠上下文内 |
| 动效 | 拖动只用 transform，矩形在拿起时量一次，React 只在放下时渲染一次（迁移第 5 步实测：一次拖动中侧栏行渲染 0 次，放下时每行 1 次；dnd-kit 版是拖动中每行 11–13 次，见 `migration.md` 第 5 步）；邻居让位 `--m-quick`；落定 `--m-panel-in` + `--ease-settle`；拿起放大 1.02（`sidebarDndEngine.ts:42`）；减少动效下无滑动 |
| 触屏 | 长按 380 ms 才拿起，先移动的手指算滚动（`sidebarDndEngine.ts:13-14`、`:39-41`）；长按不动松开 = 打开行菜单；拿起时震动 8 ms（`:211`） |
| 可用范围 | 只有默认"消息"区可拖；有搜索词或侧栏收起时关闭（`App.tsx:1283`）；自定义分区和隐藏 Bot 行不可拖（`:2451`、`:2314`） |
| 交互开销 | 60 个会话时拖动 p95 帧 ≤ 20 ms（4 倍降速 ≤ 34 ms），拖动中每帧排版比静止时多不超过 1.5 次（`ui/scripts/test-sidebar-pins.mjs:415-421`）。`AGENTS.md` 禁止在拿起时加依赖列表祖先属性的样式（实测 +60 ms） |

**测试**：`test:sidebar-pins`、`test:conversation-route`、`test:conversation-route-browser`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C10 | 低 | 鼠标悬停 80 ms 预取会话、按下立刻预取，侧栏快速扫过会发出一串预取请求（有 80 ms 去抖但无上限） | `App.tsx:2283-2285` | P7 |

---

## 7. 置顶格（PinTile / PinDock / PinDropZone）

**是什么**：置顶的会话以大号猫格子显示在侧栏顶部，名字小字在下（D21）。拖一行到置顶区就置顶：置顶区本身就是落点，一个框（`.pin-frame`）框住所有大头像；没有置顶时同一个框空着出现在原处。框是列表的第一块，紧贴搜索框下面，占文档流。

**文件**：`ui/src/PinnedDock.tsx`、`ui/src/App.tsx:2299-2309`（`pinTile`）、`ui/src/sidebar-pins.css`。

**状态**：普通 / 当前（底色 + 描边 + 小硬阴影）/ 未读（橙色数字）/ 运行中（蓝色呼吸点）/ 侧栏收起（单列、44 px、无名字）。落点：隐藏 / 拖动行时出现 / 指针在落点上（橙色）。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 每格一个按钮，`aria-current` 标当前；名字进 `data-hint` |
| 键盘 | Alt+←/→ 在格子间移动（`PinnedDock.tsx:28`） |
| 位置 | 格子网格 `minmax(72px,1fr)`；`.pin-frame` 框住整个 dock（`PinDropZone` 包着 `PinDock`），是列表滚动区的第一块，紧贴搜索框下面。框的边框一直在（透明），所以拖行时大头像不动；拖行时边框变虚线、框里出现提示（"Drop to pin"），其下的行整体下移，由引擎的 `glideFrom` 滑过去。没有置顶：框空着，高 112 px（一排大头像），提示在中间。指针在框上（含搜索框一带）时变橙色。拖起一行时列表先回到顶部，保证整个框在屏幕上；框不是 sticky，所以没有任何东西盖在它上面。不占指针事件、不进 Tab 顺序 |
| 层级 | 框没有 z-index，也没有定位元素压在上面（`test:sidebar-pins` 在顶部/中间/底部三种滚动位置、0 和 3 个置顶下，检查没有元素和框或大头像相交，且框和每个大头像中心都是最上层）；拖动中的行和落下去的飞行动画是 `--z-drag`（飞行期间 `.is-dnd-landing`） |
| 动效 | 提示出现 `--m-quick`（淡入下滑），其余内容让位/收回 `--m-base`；落下去 `--m-panel-in` + `--ease-settle`（排序：从松手处飞到新位；置顶：从松手处缩放飞进大格；取消置顶同理）；运行中圆点呼吸 2.4 s（减少动效下不呼吸） |
| 触屏 | 同第 6 节；`-webkit-touch-callout:none` 防 iOS 长按弹系统菜单 |

**测试**：`test:sidebar-pins`（含 390 px 触屏、键盘、减少动效）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C11 | 低 | 拖动行的阴影写死 `rgba(0,0,0,.2)`，不走阴影令牌，深色模式下不调整 | `sidebar-pins.css:8` | — |

---

## 8. 对话框

协调者给的数是 12。按实现逐个数，实际有 16 个。**第 3 步之后**分三类机制：`ModalDialog` / `ConfirmDialog`（`ui/src/overlays/ModalDialog.tsx`，Radix `Dialog` / `AlertDialog`，懒加载块，9 个）；仍自己写遮罩和焦点陷阱的（交接窗、放大的电脑、App 详情窗格，3 个）；原生 `<dialog>.showModal()`（灯箱和文件预览，2 个，进浏览器顶层）；另有两个非模态的（终端内确认、权限提示）。

`ModalDialog` 的共同契约：`aria-modal="true"`、`data-tofi-modal`；Tab / Shift+Tab 不出框；Esc 只关最上面一层（Radix 的层栈；新对话框首帧里的 Esc 被丢掉，不会关到下面那层），并 `preventDefault`，App 自己的 Esc 处理都看 `defaultPrevented`，所以不会连带关掉面板或设置窗；关闭后焦点回到打开它的元素（只在焦点随对话框丢失时）；其余页面 `aria-hidden`（`aria-live` 区域和它们的祖先除外）；Radix 关掉 `<body>` 的指针事件，工作区 toast 例外（可点，点它不关对话框）。`test:dialogs` 守着。

| # | 对话框 | 文件 | 模态 | Escape | 点遮罩关 | 焦点陷阱 | 关后焦点回去 | 层级 |
|---|---|---|---|---|---|---|---|---|
| 1 | 删除 Bot/群确认 | `ui/src/DeleteConversationDialog.tsx`（用 `DialogShell`） | 是，`alertdialog`（Radix `Dialog`，不是 `AlertDialog`：要保留点遮罩关闭） | 是（忙时不关） | 是（忙时不关） | `ModalDialog` | 是，回到打开它的控件（App 在工作区变 `inert` 之前记下，`rememberDialogOpener`；以前这里还不回去）；从侧栏行菜单打开时回到那一行（菜单项随菜单卸载，`OverlaySlot` 的监听器在菜单打开时记下当时的焦点；`ds/integrate` 起，`test:overlays` 守着） | `--z-dialog` |
| 2 | 隐藏主助理时选接任者 | `ui/src/ReassignPrimaryDialog.tsx:28`（`DialogShell`，第 3 步起懒加载） | 是 | 是 | 是 | `ModalDialog` | 同上 | `--z-dialog` |
| 3 | 管理员表单 sheet、已记住的审批 | `ui/src/AdminSheets.tsx:12`，`ui/src/RememberedApprovals.tsx:59` | 是 | 是（`locked` 时不关） | 是（`locked` 时不关） | `ModalDialog`；打开时焦点在 sheet 本身 | 是 | `--z-dialog` + 1 |
| 4 | 技能访问 sheet | `ui/src/settings/SkillAccess.tsx:65` | 是 | 是（保存中也关） | 是（忙时不关） | `ModalDialog` | 是 | `--z-dialog` + 1 |
| 5 | 接管屏幕（handover） | `ui/src/TakeoverDialog.tsx:48-69` | 是 | **否**（按键要给远程电脑） | 否 | 自写（第 3 步没迁，风险 ③） | 是 | `--z-dialog` |
| 6 | VM 里授权（MCP OAuth） | `ui/src/VMOAuthDialog.tsx:93` | 是 | **否**（按键给远程电脑；Esc 和 Tab 在远程键盘里直接转发，`RemoteDesktopControl` 自己停止冒泡） | 否 | `ModalDialog` | 是 | `--z-dialog` |
| 7 | 相关对话只读窗（窄屏/桌面客户端时是模态） | `ui/src/ViewOnlyChat.tsx:59` | 宽屏网页是卡片（`ViewOnlyCard`，非模态），否则模态（`ModalDialog`） | 是 | — | 模态时 `ModalDialog`（卡片形态不陷） | 是（`returnFocus`） | `--z-dialog`；卡片 `--z-panel` |
| 8 | 首次引导 | `ui/src/Onboarding.tsx:75` | 是（第 3 步起 portal 到 `#tofi-layers`） | 否 | 否 | `ModalDialog`；每一步聚焦 `[data-autofocus]` | 打开前的焦点（若还在） | `--z-dialog` |
| 9 | 放大的电脑窗 / 窄屏全屏电脑 | `ui/src/FloatingDesktop.tsx:45-102`，`:135` | 放大时是 | 是：大→小，只有全屏形态直接关（`:45`） | 网页端大窗点遮罩变回小窗（`BotDesktopPanel.tsx:822`）；全屏形态无遮罩 | 自写 | 是 | 小窗 `--z-pill`；放大 `--z-dialog` |
| 10 | 详情窗格当模态（设置总是；其他面板在窄屏） | `ui/src/App.tsx`（`modalPanelOpen`、详情窗格的 Tab/Esc 圈定，有 `[data-tofi-modal]` 时让开） | 是 | 是（设置走 `tofi-settings-close` 经离开确认；电脑放大或面板忙时不关） | 是（`.detail-backdrop`） | 自写 | 是 | 设置 `--z-dialog`，其他 `--z-panel`，遮罩 `--z-panel` − 1 |
| 11 | 终端：关闭 / 中断确认（两个） | `ui/src/TerminalPanel.tsx:44-50` | `alertdialog` | 是 | 否 | 无 | 待确认 | 2（终端内） |
| 12 | 权限提示（仅桌面客户端，听写要麦克风时） | `ui/src/PermissionCoach.tsx:37`、`:95`；`App.tsx:3538` | **非模态** `role="dialog"` | **否** | — | 无 | 无 | `--z-toast`，但在输入框或设置窗里，被它们封顶 |
| 13 | 图片灯箱 | `ui/src/MessageAttachment.tsx:25`、`:76`、`:85` | 原生 | 原生 `cancel` | 是 | 原生 | 原生 | 顶层（开着时提示和 toast 渲染进它里面） |
| 14 | 文件预览 | `ui/src/FileViewer.tsx:120-130` | 原生 | 原生 | 是 | 原生 | 原生 | 顶层（同上） |
| 15 | 设置：离开前保存确认 | `ui/src/SettingsShell.tsx:153` | `ConfirmDialog`（Radix `AlertDialog`，`alertdialog`；原来是原生） | 是（保存中不关） | 否 | `ConfirmDialog`；焦点在"继续编辑" | 是 | `--z-dialog`（原来顶层） |
| 16 | 电脑重启确认 | `ui/src/ComputerResources.tsx:17` | `ConfirmDialog`（原来是原生） | 是 | 否 | `ConfirmDialog`；焦点在"取消" | 是 | `--z-dialog`（原来顶层） |

同一层（`--z-dialog`）的模态按 DOM 顺序叠：portal 到 `#tofi-layers` 的对话框在设置窗（`#root` 里）之上，它们之间（第 3 步起也包括引导）后打开的在上；迁移前按数字大小排（`migration.md` 第 1 步 L4、第 3 步 S1）。

另有一种"就地确认"：`ConfirmAction`（`ui/src/InteractionSystem.tsx`，危险操作在原位变成"确定？/取消"，焦点进取消，Escape 退回）。设置里的危险操作用它，不弹窗。

**状态（各对话框共有）**：打开 / 忙（`aria-busy`，按钮禁用，不响应关闭）/ 出错（`role="alert"`，主按钮文字变"重试"，例 `DeleteConversationDialog.tsx:46-48`）/ 退场。

**动效**：遮罩淡入 `--m-base`（`interaction-system.css:43` `delete-dialog-arrive`）；原生灯箱在网页端从缩略图放大（`MessageAttachment.tsx:25`，桌面客户端和减少动效下不做）。

**触屏**：sheet 在窄屏全屏；`DialogShell` 卡片 `max-height: calc(100dvh - 48px)` 内部滚动（`interaction-system.css:44`）。

**测试**：`test:dialogs`（`ModalDialog` 的契约，见上）、`test:primary-followups`（删除/接任对话框）、`test:handover-cards`（接管）、`test:mcp-oauth`、`test:onboarding`、`test:file-preview`、`test:settings-shell`、`test:computer-window`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C12 | 中 | **第 3 步已修**（迁过去的 9 个共用 `ModalDialog`；交接窗和详情窗格仍自写）：9 份手写焦点陷阱，规则各不一样：`DialogShell` 只收 `button`、`select`（以后加输入框或链接就漏掉）；`TakeoverDialog` 只收 `button`、`textarea`；App 详情窗格的版本最完整（过滤隐藏元素） | `DialogShell.tsx:7-11`，`TakeoverDialog.tsx:54`，`App.tsx:1116-1150` | P8 |
| C13 | 中 | **第 3 步已修**（Radix 在 document 上处理 Esc；`test:dialogs` 点卡片空白后再按 Esc）：`DialogShell` 的 Escape 和 Tab 处理挂在遮罩的 `onKeyDown` 上：点一下卡片上的空白处，焦点落到 `body`，之后 Escape 和 Tab 都不再经过它 | `DialogShell.tsx:5-6` | P8 |
| C14 | 中 | **第 3 步已修**（工作区 `inert`，背景 `aria-hidden`）：接任者对话框（#2）打开时工作区没设 `inert`：`inert` 条件只看 `deleteTarget` 和只读窗，不含 `hidePrimaryTarget`。读屏的虚拟光标能走到背景 | `App.tsx:2441`，对照 `:2611` | P8 |
| C15 | 中 | 权限提示（#12）固定在右下角 `right:24px; bottom:24px`，宽到 420 px，盖住发送按钮所在的角；没有 Escape。更正：它渲染在输入框里（`App.tsx` 的 `Composer`），被输入框的 `isolation:isolate` 封顶，所以并不盖住"新消息"胶囊（胶囊 `--z-pill` 高于输入框 `--z-chrome`，迁移前 6 也高于 4） | `ui/src/permission-coach.css:1`，`PermissionCoach.tsx:95` | P2 |
| C16 | 中 | **第 1 步已修**：设置窗是带遮罩的模态，层级却只有 30，启动画面、toast、身份卡、右键菜单都会浮在它上面。现在是 `--z-dialog`，只有 toast、提示和拖动层在它上面 | `tokens.md` Z5，`settings-system.css:2` | P2 |
| C17 | 低 | 第 3 步后：Esc / 点遮罩的规则写在 `ModalDialog` 的 `closeOnEscape`、`closeOnOutside`、`locked` 里，每个对话框显式选择（见上表）。原生 `<dialog>` 只剩灯箱和文件预览。原问题：三套模态机制（自写、原生 `<dialog>`、`ConfirmAction`），Escape 是否关闭、点遮罩是否关闭没有统一规则（见上表"待确认"格） | 本节表格 | P6 |

---

## 9. 菜单和右键菜单

迁移第 2 步起，6 个菜单都是 Radix `DropdownMenu`（`modal={false}`），通过入口包里的 `OverlayMenu`（`ui/src/overlays/OverlayHost.tsx`）懒加载 `ui/src/overlays/RadixOverlays.tsx`。菜单的标记和类名不变，其中每个 `<button>` 变成一个菜单项（`role="menuitem"`）；触发按钮仍是 TOFI 自己的 `<button>`，带 `aria-haspopup="menu"`、`aria-expanded`，按 ↓ 也能打开（`arrowOpens`）。

**所有菜单共用的行为**（`RadixOverlays.tsx` `Menu`）

| 项 | 契约 |
|---|---|
| 打开 | 点触发按钮；Enter、空格、↓；右键菜单另有右键、长按 380 ms（拖动引擎）、Menu 键、Shift+F10 |
| 焦点进菜单 | 键盘打开（`<html data-input="keyboard">`）：第一项；鼠标打开：菜单本身（不画焦点环，方向键照样能用） |
| 键盘 | ↑↓ 循环，Home/End，首字母跳转，Enter/空格选中 |
| Escape | 关闭，焦点回触发按钮（右键菜单回打开前聚焦的行） |
| Tab | 关闭，焦点从触发按钮往后走；在模态（设置窗）里只在模态里走并回绕；Shift+Tab 回到触发按钮 |
| 点外面 | 关闭，不吞掉这次点击（例如点另一个会话照样打开它）；再按触发按钮是关闭 |
| 焦点移走 | 只是焦点移走（没有按下、Esc、Tab）不关闭，和迁移前一样（手机长按打开右键菜单后，触摸的兼容鼠标事件会把焦点移到行上） |
| 选中后 | 焦点无处可去时回到触发按钮；不是在本项按下的抬起不选中任何项 |
| 位置 | Radix Popper：`side/align/sideOffset`，离视口边缘不足时翻转、平移，留 8 px |
| 层级 | 菜单内容自带 `--z-menu`，Radix 把它复制到 `position:fixed` 的外层，外层在 `#tofi-layers`；从 `aria-modal="true"` 的祖先里打开的是 `calc(var(--z-dialog) + 1)`（`data-layer="dialog"`） |
| 动效 | 进场仍是各自的 CSS（新建、更多、右键：`menu-enter`，`--m-quick`）；新建、更多的退场是 `[data-state="closed"]` 上的 `menu-leave`，Radix 等动画结束再卸载；减弱动效下立即出现、立即消失 |
| 开销 | 关着的菜单不渲染任何东西；右键菜单的状态在 `OverlaySlot`，打开关闭不重渲染工作区；打开时猫停 320 ms 呼吸（`holdAvatarLife`） |

| 菜单 | 文件 | 位置（和迁移前的 CSS 偏移逐像素相同） |
|---|---|---|
| 侧栏行右键菜单（置顶、移到分区、标为未读、改名、设为主助理、复制链接、复制 ID、隐藏、删除） | `ui/src/App.tsx:2401`（`SlotView`） | 指针处（右下展开），贴边时翻转 |
| 新建菜单（新 Bot、新群、导入） | `App.tsx`，`create-menu` | 按钮下 8 px 右对齐；收起侧栏时在侧栏右侧 12 px 顶对齐 |
| 顶栏"⋯"菜单 | `App.tsx`，`more-menu`，锚在 `.chat-header` | 顶栏下 8 px，右边距顶栏右缘 14 px |
| 附件菜单（图片、文件、截屏） | `App.tsx` `Composer`，`composer-add-menu` | 按钮上方 12 px 左对齐 |
| 账户菜单（设置、用量、主题、连接、归档、退出） | `ui/src/OwnerSession.tsx:176` | 账户区上方 10 px 左对齐；收起侧栏时在右侧 10 px 底对齐。主题三选一是 `role="menuitemradio"` + `aria-checked`，选了不关菜单 |
| MCP 服务器行"⋯" | `ui/src/MCPSettings.tsx:227` | 按钮下 7 px 右对齐；在设置窗之上 |

**触屏**：侧栏行长按 380 ms 出菜单（第 6 节），不用 Radix ContextMenu（它自带 700 ms 长按）。

**测试**：`test:overlays`（每个菜单的鼠标、键盘、Esc、Tab、点外面、再按触发按钮、位置；右键菜单的指针位置、Shift+F10、贴边、长按、工作区不重渲染）、`test:sidebar-pins`（右键菜单、长按）、`test:chat-motion`（表情菜单）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C18 | 中 | ~~四个菜单四种键盘行为~~ **第 2 步已修**：一套规则，见上表 | `test:overlays` | P8 |
| C19 | 中 | "复制 ID"直接调 `navigator.clipboard?.writeText`，在局域网 http 下（没有异步剪贴板）静默失败，没有任何提示；同文件里已有带降级的 `writeClipboard` | `App.tsx` 右键菜单"复制 ID"一项（第 2 步未改） | P3 |
| C20 | 低 | ~~右键菜单用写死的 248×360 估尺寸夹视口~~ **第 2 步已修**：Radix 按实测尺寸翻转、平移 | `test:overlays` 贴边检查（390、1280 px） | P2 |
| C21 | 低 | ~~账户菜单和附件菜单的 z-index 被父级封顶（Z3）~~ **第 2 步已修**：portal 进 `#tofi-layers` | `tokens.md` Z3 | — |

---

## 10. 悬停提示（2 个）

| 提示 | 文件 | 触发 | 延迟 | 位置 | 层级 |
|---|---|---|---|---|---|
| 图标按钮提示 `ActionHints`（全局一个，委托监听带 `data-hint` 的元素） | `ui/src/InteractionSystem.tsx:51-84`，`interaction-system.css:93` | 鼠标悬停（触屏和笔不触发）、键盘聚焦 | 悬停 500 ms，聚焦 150 ms；从一个图标移到相邻图标 400 ms 内不再等（`:71`） | 按钮上方 8 px（离顶 ≤ 62 px 时改到下方），水平夹在 [100, 视口宽 − 100]，最宽 200 px | `--z-tooltip`（1000），portal 到 `#tofi-layers`（迁移前是 `body`） |
| 群聊已读名单（第 2 步起就是上一行的提示，`data-hint-tone="quiet"` 白底） | `ui/src/SendReceipt.tsx`（标记的 `data-hint`），`interaction-system.css:97` | 同上（悬停、聚焦；手机点按标记） | 同上 | 同上 | `--z-tooltip`，portal 到 `#tofi-layers`（迁移前 5，在行内） |

**关闭**：`ActionHints` 在指针移出、失焦、任意指针按下、任意按键、任意滚动时消失（`InteractionSystem.tsx:74-80`）。

**第三种（非组件）**：原生 `title` 属性，`ui/src` 的 tsx 里有 104 处（`grep -o ' title={'`），包括桌面客户端的消息精确时间（`App.tsx:2735`）、表情计数的名单（`MessageReactions.tsx:73`）。原生 title 不能样式化、触屏不可见。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C22 | 中 | 提示是强调色底（`--accent`），在一套"控件要低调"的界面里是最显眼的东西 | `interaction-system.css:93` | P1（是否改为中性色：待 owner 定） |
| C23 | 中 | 层级 1000 低于引导遮罩 1200，也到不了原生 `<dialog>` 顶层；目前那些地方没有 `data-hint`，暂无故障。**前一半第 1 步已修**：`--z-tooltip` 在所有模态之上；**原生 `<dialog>` 那一半第 3 步已修**：两个确认框改成 Radix，剩下的灯箱和文件预览开着时提示渲染进 dialog 里面 | `tokens.md` Z7 | P2 |
| C24 | 低 | ~~两套提示~~ + 原生 title：第 2 步起读者名单也走 `ActionHints`，只剩提示和原生 title 两种机制 | 本节 | P6 |

---

## 11. 浮层（popover）

| 浮层 | 文件 | 打开 | 关闭 | 位置 | 层级 |
|---|---|---|---|---|---|
| Bot 身份卡（**界面上没有入口**：`App.tsx` 只给指派通知构造它，而指派通知在那之前已经 return，`inlineMention` 永远为空；迁移前写的"群聊发送者头像上"是错的） | `ui/src/BotIdentityCard.tsx`（第 2 步起 Radix Popover，`role="dialog"`） | 鼠标悬停 400 ms；聚焦；触屏松手；长按 500 ms 直接 @ 这个 Bot | 离开 180 ms 后；滚动、改窗口大小、Escape、点外面立即关；不抢焦点 | 锚点下 8 px 左对齐，贴边翻转、留 12 px | `--z-popover`（90） |
| 表情反应菜单（第 2 步起 Radix Popover） | `ui/src/MessageReactions.tsx:47` | 操作行笑脸按钮；桌面客户端右键消息 | Escape、点外面、外面滚动；焦点回到笑脸按钮 | 按钮下 6 px（放不下时翻到按钮上方），留 12 px | `--z-menu`（150） |
| 完整表情选择器 | `ui/src/ReactionEmojiPicker.tsx`（懒加载） | 表情菜单里点 + | 随表情菜单 | 表情菜单内 | 随表情菜单 `--z-menu`（150） |
| 相关对话只读卡片（宽屏网页） | `ui/src/ViewOnlyChat.tsx`，`context-card.css:30` | 点消息里的相关对话链接 | Escape、关闭按钮 | 右上卡片位置，与上下文面板同位 | `--z-panel`（40） |
| 上下文浮卡（≥1100 px 的 Bot 面板等） | `ui/src/context-card.css:13`、`:49` | 顶栏按钮 | 待确认 | 右上，`inset: var(--card-top) 16px auto auto` | `--z-panel`（12） |

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C25 | 低 | ~~身份卡没有 `role`；用写死尺寸夹视口~~ **第 2 步已修**（Popover 给 `role="dialog"`，按实测尺寸翻转）。另：身份卡在界面上没有入口，要不要恢复待 owner 定 | `BotIdentityCard.tsx` | P8 |
| C26 | 中 | 身份卡 90 低于只读窗 110、右键菜单 120；同一视图里叠开时被盖。第 1 步后按刻度：身份卡 `--z-popover` 低于菜单和模态是有意的顺序；身份卡只在主对话里出现，模态开着时背后是 `inert`，碰不到它。第 2 步发现身份卡在界面上根本没有入口（见上表）。是否关闭这一条，等 owner 定 | `tokens.md` 9.4 | P2 |

---

## 12. 列表框（2 种）

| 列表框 | 文件 | 键盘 | 读屏 | 位置 |
|---|---|---|---|---|
| @提及菜单（自写 `role="listbox"`，唯一一个） | `App.tsx` `Composer`，`ui/src/web-mention.css` | 上下键、Enter 选、Escape 关；鼠标悬停改选中，按下即选 | 选项有 `aria-selected`，但文本框不关联（见 C8） | 网页端跟着光标位置，桌面客户端固定在输入框上方 8 px；第 2 步起 portal 进 `#tofi-layers`、`position:fixed`，`mentionAnchor` 按视口坐标算，不再被输入框封顶（Z3） |
| 原生 `<select>` | 设置各页、引导、工作面板等 17 个文件共 36 处（`grep -o "<select" ui/src/*.tsx`） | 浏览器原生 | 浏览器原生 | 浏览器原生（深色模式下的下拉样式由系统决定） |

---

## 13. 问题卡（QuestionCard）

**是什么**：Bot 问人的唯一出口（D10，`AGENTS.md` "Asking a human"）。三种形态：二选一/审批（`BinaryQuestionCard`，用 `ApprovalCard` 画）、网页表单（`UserFormCard`，`ask_user_form` 和 `request_secret_input`）、旧版选项（`LegacyQuestionCard`）。

**文件**：`ui/src/QuestionCard.tsx:35-343`、`ui/src/questionTimeline.ts`、`ui/src/useQuestions.ts`、`ui/src/question-card.css`、`ui/src/userForm.ts`。QuestionCard 懒加载以守住入口体积（`5e0cd3b`）。

**状态**：`pending`（可答）/ `answered`（谁答的：人、自动审核 `auto-review`、已记住的审批、常驻授权）/ `expired`（默认 30 分钟）/ `cancelled` / `run_done`（运行已结束）/ 审核中（`reviewing`）/ 归档会话（不可答，显示说明）/ 出错（`role="alert"`）。网页端答完后答案折叠（`is-folding`，`QuestionCard.tsx:288`）。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 提交后焦点回到卡片记录（`:58`）；表单出错时焦点到第一个出错的控件（`:184`） |
| 键盘 | 表单是真 `<form>`，Enter 提交 |
| 冲突 | 服务器回 404/409 时重新拉状态，不覆盖别人的答复（`:107`、`:201`、`:313`） |
| 位置 | 时间线内，文档流 |
| 动效 | 答完折叠 |
| 密码 | 密码字段只进保险库（`AnswerUserForm`），表单结果摘要不显示密码（`AGENTS.md`） |

**测试**：`test:question-poll`、`ui/scripts/test-question-timeline.mjs`（无 npm script）、`test:user-form`、`test:approval-facts`。

**已知问题**：没有找到当前打开的缺陷。

---

## 14. 审批卡（ApprovalCard）

**是什么**：需要人点"批准/拒绝"的卡片外观，由问题卡和 MCP 审批共用。服务端根据确切参数生成的事实（`verified`）排在 Bot 自己写的说明之前（`2befb5b`）。

**文件**：`ui/src/ApprovalCard.tsx`、`ui/src/approval-card.css`。

**状态**：待批（头部、事实、参数、按钮都展开）/ 已决（头部和内容折叠，`inert` + `aria-hidden`，只剩标题和结果行）/ 已拒（`is-declined`）/ 忙（`aria-busy`，按钮文字变"提交中"）/ 出错。

**行为契约**

| 项 | 契约 |
|---|---|
| 按钮 | 拒绝、批准、可选"总是允许"（常驻授权，第 16 节）、可选"批准并在本对话记住" |
| 参数 | 完整参数只按需展开（`<details>`），以惰性文本显示，可聚焦滚动（`ApprovalCard.tsx:60`） |
| 结果 | 只有调用方确认后的结果能让卡片变成已决（`:38` 注释） |
| 动效 | 折叠区收起 |

**测试**：`test:approval-facts`、`test:tool-tiers-e2e`、`test:auto-review`、`ui/scripts/test-approval-expiry-browser.mjs`（无 npm script）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C27 | 低 | 结果行不是 live region，读屏用户提交后听不到"已批准" | `ApprovalCard.tsx:75-78`（对照表单卡结果用了 `role="status"`，`QuestionCard.tsx:256`） | P8 |

---

## 15. 接管卡和连接卡（HandoverCards）

**是什么**：Bot 请人接管屏幕（登录、两步验证、验证码）或连接一个服务，都在对话里发起（D11）。走同一个问题集合点。

**文件**：`ui/src/HandoverCards.tsx`（`TakeoverCard` `:67`、`ConnectionCard` `:175`、`useConnect` `:98`）、`ui/src/TakeoverDialog.tsx`（接管时的大窗，第 8 节 #5）、`ui/src/handover.css`。决定：[D11](../decisions/0011-handover-cards.md)。

**状态**：接管卡 `pending` / 已完成（"done"）/ 做不了（"cannot"）/ 过期 / 取消 / 运行已结束 / 归档。连接卡 `pending` / 连接中（弹窗已开）/ 已连接 / 已拒绝 / 失败 / 被浏览器拦了弹窗。

**行为契约**

| 项 | 契约 |
|---|---|
| 接管 | 打开 `TakeoverDialog`，实时画面 + 远程控制；"完成"或"做不了"之前先把屏幕控制交还，让恢复的运行不排在这次控制后面（`TakeoverDialog.tsx:64-66`）；画面断流时每 1.5 s 截一张静态图（`:43`） |
| 连接 | https 下开新窗口走 OAuth，必须在点击事件内同步 `window.open`（浏览器拦截 await 后的弹窗，`HandoverCards.tsx:156-159`）；http 下用共享电脑；弹窗开着时每 2 s 查一次是否已连上（`:132`），窗口重新获得焦点时也查（`:126`） |
| 关闭 | 接管大窗没有 Escape（键要给远程电脑），用"关闭"按钮 |

**测试**：`test:handover-cards`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C28 | 低 | 连接卡的 2 s 轮询在弹窗被用户手动关掉后是否停止：待确认 | `HandoverCards.tsx:129-133` | P7 |

---

## 16. 常驻授权（"总是允许"）

**是什么**：对一个 Bot 的一个写入级 MCP 工具点"总是允许"，以后同样的调用不再问人（D24）。界面有三处：审批卡上的按钮和说明、已被授权决定的卡片结果行、设置 › 自动行为里的列表。

**文件**：`ui/src/ApprovalCard.tsx:64`、`:71`（按钮和说明）、`ui/src/QuestionCard.tsx:87-89`、`:101`、`:126`、`:130`（提供条件与结果文案 `question.resolution.standing`）、`ui/src/StandingAllowances.tsx`（设置列表）。服务端契约见 `AGENTS.md`（"Always allow"一段）。决定：[D24](../decisions/0024-standing-tool-allowance.md)。

**状态**：卡片上：可提供（服务端给了 `approval.standing`）/ 不提供。设置列表：加载中 / 空 / 有效 / 失效（`tool_changed`、`server_changed`、`tier_changed`，显示原因）/ 撤销中 / 撤销失败。

**行为契约**

| 项 | 契约 |
|---|---|
| 提供条件 | 只在服务端标了 `standing_eligible` 时出现：写入级、有人看得到的对话，从不对高风险或只读工具（`QuestionCard.tsx:88-89` 注释，`AGENTS.md`） |
| 说明 | 卡片上写明这个授权在无人值守时也生效（`alwaysAllowHint`） |
| 撤销 | 设置里每行一个"撤销"按钮，直接生效、无确认（收紧权限的方向）；撤销后进行中的运行在下一次调用时就被拦（`AGENTS.md`） |
| 同步 | 授权或撤销后广播 `tofi:standing-allowances-changed` 事件，让其他视图刷新（`QuestionCard.tsx:101`） |

**测试**：`test:standing-allowance`（`ui/scripts/test-standing-allowance-card.mjs`）。

**已知问题**：没有找到当前打开的缺陷。

---

## 17. 设置行和设置页组件

**是什么**：设置页的积木：`SettingsSection`、`SettingsCard`、`SettingsRow`（左文字右控件，手机上换行，`inline` 时保持同行）、`StatusBadge`、`Banner`、`DangerZone`、`Segmented`（单选按钮组）、`SettingsDetails`（"详情"折叠）、`FilePicker`；外加 `ConfirmAction`（就地确认）、`Disclosure`、未保存修改条。

**文件**：`ui/src/settings/components.tsx`、`ui/src/settings/settings-components.css`、`ui/src/settings/settings-polish.css`、`ui/src/SettingsShell.tsx`（外壳、12 个 tab、未保存条 `:151`、离开确认 `:153`）、`ui/src/settingsDraft.ts`（`useSettingsDraft`）。

**两种保存方式**（现状，未统一）

| 方式 | 用在哪 | 行为 |
|---|---|---|
| 草稿 + 保存条 | 模型、时区、听写、电脑资源、自动审核、电脑凭据（`useSettingsDraft` 的 6 个调用方） | 改动登记为草稿，底部出现"有未保存的修改 · 放弃 · 保存"；切 tab 或关设置时用原生 `<dialog>` 问 |
| 立即生效 | 外观、语言、自动行为页的开关等 | 点了就存 |

**行为契约**

| 项 | 契约 |
|---|---|
| `Segmented` | `role="radiogroup"` + 每项 `role="radio"`、`aria-checked`（`components.tsx:60-64`）；方向键：待确认（没有看到 `onKeyDown`） |
| `Banner` | `tone="error"` 时 `role="alert"`，其余无 live region |
| `ConfirmAction` | 打开时焦点进"取消"，关掉焦点回触发按钮，Escape 取消（`InteractionSystem.tsx` `ConfirmAction`） |
| `Disclosure` | 内容一直挂载，关时 `inert` + `aria-hidden` |
| 打开速度 | 设置窗第一帧 ≤ 32 ms（4 倍降速 ≤ 100 ms），之后 200 ms 内无超过 50 ms 的帧（`ui/scripts/test-shell-budget.mjs:4-5`） |

**测试**：`test:settings-shell`、`test:settings-polish`、`test:automation-settings`、`test:shell-budget`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C29 | 中 | `Segmented` 声明了 radio 语义，但 radio 组的标准键盘（方向键切换、组内一个 Tab 停靠点）是否实现：待确认；代码里每个选项都是独立 Tab 停靠点 | `settings/components.tsx:61-62` | P8 |
| C30 | 低 | 同一个设置窗里两种保存方式并存，用户要猜哪个改动已经生效 | 上表 | P3 |

---

## 18. 通知、toast 和横幅

| 名称 | 文件 | 何时出现 | 位置 | 层级 | 关闭 |
|---|---|---|---|---|---|
| 工作区刷新失败 toast | `App.tsx:2443`，`styles.css:447-448` | 列表刷新失败 | `position:fixed` 顶部居中 10 px | `--z-toast`（80） | 无关闭按钮，只有"重试" |
| 共享电脑状态条 | `App.tsx:2444` | 电脑启动、恢复、出错、无响应 | 工作区顶部文档流 | — | 自动消失；出错时带操作按钮 |
| 新版本横幅 | `ui/src/UpdateNotice.tsx:58-79`，`ui/src/UpdateNotice.css` | 管理员且有新版本 | 工作区最上方文档流（会把内容往下推） | — | 关闭按钮，按版本记住（localStorage） |
| 对话内系统提示 | `styles.css:449-450`（`.error-banner.system-notice`） | 失败提示 | 消息列表内 | — | — |
| 面板内错误条 | `.error-banner`，如 `ui/src/WorkPanel.tsx:96-97` | 操作失败 | 面板内 | — | 带"重试/重新加载" |
| 设置页横幅 | `settings/components.tsx:40-52`（`Banner`） | 设置页警告/错误/信息 | 设置页内 | — | 可带一个操作 |
| "连接模型"提示 | `App.tsx:3538`（`config-warning`） | 没配模型 | 输入框上方 | — | 点了去设置 |

**动效**：横幅和 toast 直接出现（待确认有无进场动画）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C31 | 中 | **第 1 步已修**：toast 层级 80，被所有模态、菜单盖住，对话框开着时出错用户看不到（Z1）。现在是 `--z-toast`，在所有模态和菜单之上；原生 `<dialog>` 仍盖住它（Z7） | `tokens.md` Z1 | P3 |
| C32 | 中 | toast 固定在顶部正中，正好压在顶栏标题上；没有关闭按钮，要么重试成功要么一直在 | `styles.css:448` | P2 |
| C43 | 低 | 刷新失败的 toast 会被随后一次成功的配置读取清掉：`refreshConfig` 成功时 `setWorkspaceError("")`，不管错误是不是它造成的。只有会话列表失败、配置正常时（服务器部分故障），toast 出现约 0.75 秒就消失（组件目录页实测）。是否有意：待确认 | `App.tsx:919`（`refreshConfig`），对照 `:905` | P3 |
| C33 | 低 | 没有统一的 toast/通知组件：7 种写法，样式、`role`、关闭方式各不相同；toast 阴影写死 `rgb(0 0 0 / .18)` | 本节表格 | P6 |

---

## 19. 启动画面（BootScreen / BootDissolve）

**是什么**：工作区出现之前所有等待共用的一个画面：会话检查、第一次列表加载、启动失败。模糊的应用骨架 + 中间呼吸的灰色 logo，没有转圈（D20）。离开时骨架的复制品在真界面上溶解。

**文件**：`ui/src/BootScreen.tsx`、`ui/src/boot-screen.css`。决定：[D20](../decisions/0020-phone-push-and-boot-screen.md)。

**状态**：前 300 ms 什么都不画（快加载不闪，`BootScreen.tsx:16`、`:96-100`）/ 可见（骨架淡入 `--m-base`）/ 接力（从一个启动画面换到另一个时不重新淡入，`is-warm`）/ 失败（同背景上一张卡片，带消息和重试，`immediate` 跳过等待）/ 溶解（`BootDissolve`）。

**行为契约**

| 项 | 契约 |
|---|---|
| 读屏 | `aria-busy`，隐藏的 `role="status"` 读 `label` |
| 位置 | `position:fixed; inset:0`，内容在视口正中（`36c6e0c`，`test:live-fixes` 检查卡片中心偏差 ≤ 1 px，`ui/scripts/test-live-fixes.mjs:55-57`） |
| 层级 | `--z-cover`（迁移前 60，`boot-screen.css:2`）：盖住 chrome、面板、菜单，模态（设置窗、引导、放大的电脑窗）在它上面 |
| 主题 | 在 App 应用外观之前就读 localStorage 设好深浅色，深色用户不会先看到一闪白（`BootScreen.tsx:20-28`） |
| 动效 | logo 呼吸 2.4 s，多个启动画面共用同一相位（`--boot-phase`）；溶解 `--m-panel-in`；只有一层 `backdrop-filter`（`boot-screen.css` "Frost: the one blur layer"） |
| 骨架形态 | 按当前路由选列表视图或对话视图（`currentView`） |

**测试**：`test:preloader`、`test:live-fixes`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C34 | 低 | 溶解时长 JS 写 `DISSOLVE_MS = 320`（再加 30 ms 才卸载），CSS 动画是 `--m-panel-in` 280 ms，两处数字没有绑定 | `BootScreen.tsx:17`、`:51`，`boot-screen.css`（`.boot-dissolve`） | P4 |
| C35 | 低 | **第 1 步已修**：层级 60 低于设置窗以外的多数弹层。现在是 `--z-cover` 70 | `tokens.md` 9.3 | — |

---

## 20. Bot 的电脑面板（BotDesktopPanel / FloatingDesktop）

**是什么**：看 Bot 的共享电脑屏幕，必要时接过控制。三种形态：小窗（画中画，停在输入框和猫的上方、视口右侧）、大窗（模态）、窄屏全屏 sheet。只在用户点顶栏电脑按钮时打开。

**文件**：`ui/src/BotDesktopPanel.tsx`（836 行：连接、状态、控制开关）、`ui/src/FloatingDesktop.tsx`（外壳、尺寸、焦点）、`ui/src/RemoteDesktopControl.tsx`、`ui/src/DesktopVideo.tsx`、`ui/src/desktop-presence.css`、`ui/src/DesktopPlaceholder.tsx`。

**状态**：连接中 / 启动中（带阶段）/ 休眠中 / 已休眠 / 恢复中 / 就绪 / 没有桌面（只有浏览器）/ 连接断了 / 出错（`BotDesktopPanel.tsx:778-791`）。控制：Bot 在控制 / 你在控制（`role="switch"`，`:812`）。

**行为契约**

| 项 | 契约 |
|---|---|
| 焦点 | 放大时焦点进第一个按钮；关掉回到触发处，触发处没了回到电脑按钮（`FloatingDesktop.tsx:64-102`） |
| 键盘 | Escape 先大变小，只有全屏 sheet 直接关（`:45-47`）；远程控制时键盘事件给远程电脑，但 Escape 仍然关查看器（`:89-91`） |
| 位置 | 小窗 `right:16px`，底边在输入框可见最高处之上 64 px，随输入框尺寸变化（`:5-33`）；视口 ≥ 1024 px 时消息区右侧让出小窗宽度 + 32 px（`--desktop-reserve`，`:50-60`，`chat-header.css:56`） |
| 层级 | 小窗 `--z-pill`（迁移前 25），在浮卡、抽屉之下；放大后外壳、内部和 `.computer-detail.is-expanded` 都是 `--z-dialog`（迁移前 40、35、50）；电脑预览内部的局部值（最高 12）在 `layers-baseline.json` 的上限里 |
| 读屏 | 状态变化用 `DesktopStatusAnnouncement`（`role="status"`，`BotDesktopPanel.tsx:107-121`） |

**测试**：`test:computer-window`、`test:desktop-connect`；无 npm script 的 `ui/scripts/test-floating-desktop.mjs`、`test-remote-desktop-control.mjs`、`test-desktop-state.mjs`、`test-desktop-video-recovery.mjs`、`test-desktop-video-visibility.mjs`。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C36 | 高 | 视口窄于 1024 px（但大于手机全屏断点）时，小窗不让位，直接盖在消息上 | `FloatingDesktop.tsx:50-55`（`window.innerWidth >= 1024` 才设 `--desktop-reserve`） | P2 |
| C42 | 低 | 小窗打开时量一次输入框的位置（之后只在尺寸变化时重算，位置变化不重算）。工作区刚画出来的几百毫秒内打开，量到的位置不对，小窗停在视口外（组件目录页实测 `bottom: 938px`，1280×800）；等 1 秒再开就正常（`bottom: 202px`）。真实用户点得这么快不常见，但面板若在加载时自动恢复打开就会撞上：待确认 | `FloatingDesktop.tsx:6-33`（`useComposerAnchor`） | P2 |
| C37 | 低 | `floatingDesktopPosition.ts`（夹边、停靠算法）只被测试脚本引用，应用代码不用，测的是不上线的代码 | 文件是 `ui/src/floatingDesktopPosition.ts`；`grep -rl floatingDesktopPosition ui/src ui/scripts` 只得 `ui/scripts/test-floating-desktop.mjs` | — |

---

## 21. 手机推入（列表 ↔ 对话）

**是什么**：手机宽度（≤ 767 px）下，列表和对话之间像 iOS 一样推入：对话从右边滑进来盖住列表，列表同时左移 22% 并变暗；返回时反过来（D20）。返回也可以用手指：按住对话往右拖（右滑返回，D33、D35，迁移第 4 步新增）。

**文件**：`ui/src/styles.css:311-328`（媒体查询 `max-width:767px`），`App.tsx`（`mobileList` 状态，返回按钮 `:2477`）。右滑返回：`ui/src/phoneSwipeGesture.ts`（手势核心和规则 `SWIPE`）、`ui/src/phoneSwipeEngine.ts`（松手后的 WAAPI 收尾，懒加载块）、`ui/src/usePhoneSwipeBack.ts`（入口里的钩子，`App.tsx` 里 `usePhoneSwipeBack(() => flushSync(() => setMobileList(true)))`）、`styles.css` 手机段的 `touch-action` 和 `[data-swipe]` 规则。决定：[D20](../decisions/0020-phone-push-and-boot-screen.md)、[D33](../decisions/0033-phone-swipe-back-experiment.md)、[D35](../decisions/0035-step4-motion-experiment-result.md)。

**行为契约**

| 项 | 契约 |
|---|---|
| 时长 | 进入 `--m-panel-in` 280 ms，返回 `--m-base` 220 ms；每个窗格按它要去的状态取时长，两个窗格同时落地（`styles.css:313-317`） |
| 不出空帧 | 两个窗格整个过程都保持布局和绘制，`visibility` 只在结束时翻转；列表右缘始终在对话左缘之外（`:315-317`） |
| 层级 | 列表 6、对话 7；列表变暗层是它自己的 `::after`。这一对的父级不是层叠上下文，它们在根层叠上下文里，是 `test:layers` 唯一允许的例外（局部值，不能改成全局令牌） |
| 减少动效 | 直接切换（`:324-328`） |
| 返回 | 顶栏返回按钮；按下有 0.94 缩放反馈（`:330`）；或者右滑（下表） |
| 预算 | `test:shell-budget` 第 3 部分：方向、时长、无空帧（`ui/scripts/test-shell-budget.mjs:6-8`） |

**右滑返回的行为契约**

| 项 | 契约 |
|---|---|
| 什么时候有 | 只在手机布局（`(max-width: 767px)`）、只认触摸和触控笔（鼠标拖动不算）、只在打开着的对话（`.chat-pane.mobile-chat`）上。引擎块在窗口是手机宽度时空闲加载，宽窗口不加载 |
| 起手 | 手指先横向移动超过 10 px 才开始；先竖向就交给滚动。输入框、可编辑区、`[data-no-swipe]`、已经向左滚过的横向滚动块（如代码块）里不开始。`touch-action:pan-y pinch-zoom` 让浏览器只管竖向滚动和缩放 |
| 拖动中 | 对话 1:1 跟手；列表的位移和变暗跟着对话的进度走，画面和点按推入在同一进度时一样；任何一帧两个窗格都有绘制。拖动中 React 不渲染，只写 `transform` |
| 橡皮筋 | 拖到起点左边，对话受阻，最多让出宽度的 10% |
| 松手 | 过半完成；不到一半弹回；向右快于 0.5 px/ms 完成、向左快于 0.5 px/ms 取消（不论位置）；速度取松手前 80 ms。带着手指的速度走，临界阻尼弹簧 `SPRING.push`，不冲过边缘 |
| 接住 | 收尾途中手指再按上去，就在当前位置接住，继续跟手 |
| 提交 | 完成时调用和返回按钮同一个状态（`setMobileList(true)`，`flushSync`），提交那一帧 `data-swipe=done` 关掉过渡，不重放推入动画；结束后不留行内样式和动画，点按推入照旧 280 / 220 ms |
| 点击 | 从按钮上开始的拖动不会点到那个按钮；接住途中的那一下也不算点击 |
| 减少动效 | 仍然跟手（直接操作）；松手立即到位 |
| 预算 | `test:swipe-back`：4x CPU 下拖动和收尾超过 25 ms 的帧 ≤ 1，每次移动的页面脚本 ≤ 1.5 ms，一整次拖动的排版 ≤ 8 次；引擎块 ≤ 3,000 B（实测 2,079 B） |

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C38 | 中 | ~~没有从左边缘右滑返回的手势~~（第 4 步已加右滑返回，D35，分支 `ds/step4-motion`）。还剩：浏览器"后退"是否回到列表，待确认；Safari 非主屏模式下浏览器自己的左边缘返回手势会和它抢同一下滑动（浏览器抢到时页面收到 `pointercancel`，对话弹回），待真机确认 | `useConversationNavigation.ts:37` 只处理 `popstate` 路由；`ui/src/phoneSwipeGesture.ts` | P5 |
| C39 | 低 | 提交 `927a970` 的说明写"两个方向都用 `--m-base`"，但合并后的代码是进入 280 / 返回 220（与 `58da838` 一致）。以代码为准 | `git show 927a970`，`styles.css:318-323` | — |

---

## 22. 边缘模糊、顶栏玻璃和"新消息"胶囊

**是什么**：网页端消息窗格底部、输入框上方一条渐变模糊带（越靠近输入框越模糊）；顶栏是透明毛玻璃，消息可以在它下面滚过；"新消息"跳转胶囊永远在最上面（D18）。桌面客户端和网页其他区域保留原来的毛玻璃。

**文件**：`ui/src/progressive-blur.css`（全文）、`App.tsx:2587-2588`（`edge-blur`、`jump-latest`）、`ui/src/v2-app.css:396-399`（胶囊样式）、`ui/src/useConversationScroll.ts`（`showJump`、`newCount`）。决定：[D18](../decisions/0018-progressive-blur-scope.md)、到达动效 [D19](../decisions/0019-arrival-spring-cascade.md)。

**构造**

| 部分 | 做法 | 证据 |
|---|---|---|
| 底部模糊带 | 高 `--composer-height + 76px`；两层 `backdrop-filter`（4 px、12 px），各用一段线性渐变遮罩显露，叠加后强度平滑上升；只有一点 `--bg` 色调 | `progressive-blur.css:1-24` |
| 输入框 | 不再自带背景模糊（每多一层都是整宽重模糊，单它就超过了悬停预算） | `:26-30` |
| 顶栏 | 消息区向上延伸到顶栏下（`margin-top: -var(--chat-header-height)`）；`::before` 3 px、`::after` 9 px 两层，各自遮罩；顶栏本身 `pointer-events:none`，只有子元素可点 | `:32-49` |
| 胶囊 | `--z-pill`（迁移前 6），高于模糊带 `--z-edge-blur` 和输入框 `--z-chrome` | `styles.css:153`（迁移前被 `progressive-blur.css:51` 覆盖，第 1 步合并） |
| 降级 | 系统"减少透明度"或浏览器不支持 `backdrop-filter` 时退回普通渐变遮罩 | `:53-65` |

**"新消息"胶囊**：只在读者离开底部时出现（`viewport.showJump`），有新消息时显示条数；点了滑到底并消失。按下有 2–3 px 的位移反馈（`v2-app.css:398-399`）。

**到达动效（弹簧链）**：整条回复到达且读者在底部附近时，滚动位置当帧就到正确位置，看到的是视觉偏移：新消息先从下面升起，往上每条晚 15 ms，每条都是略欠阻尼的弹簧，越过真实位置 2–4 px 再回到 0；12 条后共享最后一个延迟，最长约 405 ms；超过 130 px 的跳动交给普通滑动（`ui/src/motion.ts:13-30`，`ui/src/arrivalCascade.ts:3-12`）。流式输出时用 `useConversationScroll` 的跟随滑动（220 ms，流式 120 ms，每帧最多 24 px）。减少动效下只淡入（`arrivalCascade.ts:98`）。

**测试**：`test:live-fixes`（胶囊中心和四角都是最上层、完全在视口内、点了消失，`ui/scripts/test-live-fixes.mjs:60-80`）、`test:arrival-glide`、`test:chat-motion`、`test:motion-tokens`（拦没有 `-webkit-` 孪生的 `backdrop-filter`）。像素级模糊检查只在 Chromium 跑（`6894317`）。

**已知问题**

| # | 严重度 | 问题 | 证据 | 原则 |
|---|---|---|---|---|
| C40 | 中 | D18 要求"任何东西都不能盖住新消息胶囊"，但只有测试里的场景被验证；窄于 1024 px 的电脑小窗（C36）在同一个角落，和胶囊同在 `--z-pill`、DOM 在后，所以盖在胶囊上（迁移前 25 对 6，也盖着）。更正：权限提示（C15）在输入框里被封顶，并不高于胶囊 | `desktop-presence.css:2`、`styles.css:153` | P2 |
| C41 | 低 | 胶囊按下的位移反馈用 `:active` 的 `translate(2px,3px)`，不走动效令牌里的按压规范（`--m-press`） | `v2-app.css:398-399` | P4 |

---

## 23. 其他交互模式（简表）

| 模式 | 文件 | 要点 |
|---|---|---|
| 拖文件进窗口 | `ui/src/WebFileDropOverlay.tsx`，`web-file-drop.css:2`、`:23` | 整屏遮罩 + 跟随指针的残影，`--z-drag`（迁移前 1000），都进 `#tofi-layers`，`pointer-events:none` |
| 发送飞行 / 寄信飞行 | `ui/src/sendFlight.ts`，`ui/src/mailSendFlight.ts`，`mail-send-flight.css` | 纯动画，`--m-flight` 640 ms；`--z-drag`，追加到 `#tofi-layers`（迁移前是魔法数字 2147483000 和两处重复的 120，`tokens.md` Z6，第 1 步已修） |
| 定时任务行 | `ui/src/ScheduledTaskRow.tsx`，`scheduled-task-row.css` | 时间线里的定时任务，可重试 |
| 任务问题卡 | `ui/src/TaskIssueCard.tsx`，`taskIssuePresentation.ts` | 运行失败、需要处理时的说明卡 |
| 邮件草稿卡 | `ui/src/MailDraftCard.tsx` | `prepare_email` 走的独立卡片路径（`AGENTS.md`） |
| 空状态 | `ui/src/EmptyCat.tsx`，`empty-states.css` | 每种空状态一只固定姿势的猫；`test:empty-states` |
| 软键盘 | `ui/src/visualViewport.ts` | 键盘弹起时设 `keyboard-viewport-open`，用 `--viewport-height`（`App.tsx:2441`） |

---

## 24. 问题汇总

按严重度排列，方便排进迁移步骤。原则编号见 [`principles.md`](principles.md)。

| # | 严重度 | 组件 | 一句话 | 原则 |
|---|---|---|---|---|
| C36 | 高 | 电脑小窗 | 窄于 1024 px 时盖住消息 | P2 |
| C3 | 中 | 操作行 | 提示压在消息最后一行上 | P2 |
| C5 | 中 | 回执 | 已读强调色选择器失效，测试抓不到 | P3 |
| C6 | 中 | 附件菜单 | 声明了菜单却没有菜单键盘（**第 2 步已修**） | P8 |
| C7 | 中 | 输入框 | 快捷键只认 ⌘，Windows/Linux 不可用 | P5 |
| C8 | 中 | @菜单 | 读屏听不到选中项 | P8 |
| C9 | 中 | 输入框上的猫 | 可能压住最后一条消息（待实测） | P2 |
| C12 | 中 | 对话框 | 9 份焦点陷阱规则不一（**第 3 步已修**） | P8 |
| C13 | 中 | `DialogShell` | 焦点落到 body 后 Escape 失效（**第 3 步已修**） | P8 |
| C14 | 中 | 接任者对话框 | 背景没 `inert`（**第 3 步已修**） | P8 |
| C15 | 中 | 权限提示 | 盖住发送按钮（不盖胶囊，已更正） | P2 |
| C16 | 中 | 设置窗 | 模态层级 30，被很多东西盖（**第 1 步已修**） | P2 |
| C18 | 中 | 菜单 | 四个菜单四种键盘行为（**第 2 步已修**） | P8 |
| C19 | 中 | 右键菜单 | http 下"复制 ID"静默失败 | P3 |
| C22 | 中 | 悬停提示 | 强调色底，与"控件低调"冲突（待 owner 定） | P1 |
| C23 | 中 | 悬停提示 | 到不了原生 dialog 之上（引导那一半第 1 步已修，原生 dialog 那一半第 3 步已修） | P2 |
| C26 | 中 | 身份卡 | 层级低于只读窗和右键菜单（第 1 步后是刻度的有意顺序，待 owner 预览后定是否关闭） | P2 |
| C29 | 中 | `Segmented` | radio 语义的键盘行为待确认 | P8 |
| C31 | 中 | toast | 被模态盖住（**第 1 步已修**） | P3 |
| C32 | 中 | toast | 压住顶栏标题，无关闭 | P2 |
| C38 | 中 | 手机推入 | 右滑返回已加（第 4 步）；浏览器后退和 Safari 边缘手势待确认 | P5 |
| C40 | 中 | 新消息胶囊 | 同角落的电脑小窗盖在它上面 | P2 |
| C2 | 中 | 消息气泡 | 7 种分支 + 两套工具行 | P6 |
| C1、C4、C10、C11、C17、C20（第 2 步已修）、C21（第 2 步已修）、C24、C25（第 2 步已修）、C27、C28、C30、C33、C34、C35（第 1 步已修）、C37、C39、C41、C42、C43（组件目录页发现） | 低 | 各处 | 见各节 | — |

## 25. 待确认清单

1. C5：已读不再用强调色是不是有意的。
2. C9：390 px 宽时猫是否盖住靠右的用户气泡（在 `npm run dev:remote` 预览里实测）。
3. 第 8 节表格里终端确认（#11）关后焦点回到哪里。（引导 #8 第 3 步起由 `ModalDialog` 还给打开前的焦点元素；新账户多半是 `<body>`。）
4. C22：悬停提示是否改成中性色，要 owner 定。
5. C28：连接卡弹窗被手动关掉后轮询是否停止。
6. C29：`Segmented` 的方向键。
7. C38：浏览器后退在手机上是否回到列表；Safari 非主屏模式下浏览器的左边缘返回手势和右滑返回同时出现时，真机上是什么样子。
8. 第 11 节上下文浮卡的关闭方式；第 18 节横幅和 toast 有无进场动画。（第 9 节 MCP 菜单的焦点行为第 2 步起和其他菜单相同。）
9. 身份卡在界面上没有入口（第 11 节），要不要恢复。
10. C42：电脑面板加载时自动恢复打开的话，小窗会不会停在视口外。
11. C43：配置读取成功时清掉会话列表的刷新错误，是不是有意。
