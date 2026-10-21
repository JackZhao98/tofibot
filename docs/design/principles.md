# TOFI 界面设计原则

这份文件把 owner 这几天对界面的决定和反馈提炼成规则。每条规则写清楚：从哪条决定来、现在代码里怎么做到的、**怎么检查**、以及现在已知违反它的地方（编号指向 [`components.md`](components.md) 的问题 C1–C43）。

- 改界面之前读一遍；提 PR 时按文末的"评审清单"逐条自查，在 PR 说明里写结果。
- 规则之间冲突时，按第 9 节的顺序取舍。
- 规则改了（owner 新的决定），要在同一个提交里改这份文件，并在 [`../decisions/`](../decisions/README.md) 加一条决定记录（`docs/process.md`）。
- 基线 `748c774`（2026-10-20）。令牌数值见 [`tokens.md`](tokens.md)。

| # | 原则 | 主要来源 |
|---|---|---|
| P1 | 控件要低调 | D16、D17、D20、D23 |
| P2 | 任何东西都不遮住内容和按钮 | D17、D18 |
| P3 | 操作要立刻有反馈，失败要说出来 | D16、D17 |
| P4 | 动效要有目的 | D19、D20、`AGENTS.md` "Web UI motion" |
| P5 | 说业界通行的话，用业界通行的做法 | D11、D16、D20；文案语气规则 |
| P6 | 网页和桌面客户端长得一样，模糊除外 | D18；`AGENTS.md` "Core source boundaries" |
| P7 | 量交互开销，预算写成会失败的测试 | D02、D22、D30 |
| P8 | 键盘、触屏、读屏同样能用 | 现有代码约定（不是 owner 拍板，见该节） |

---

## P1 控件要低调

**规则**

1. 静止时不显示操作控件。按钮在指针悬停、键盘聚焦或轻点之后才出现；只有"必须不问就看得到"的状态常驻（发送中、发送失败）。
2. 行内控件没有容器：不加外框、底色和阴影，用 `--muted` 色的线条图标，悬停时变深到 `--ink`。
3. 同一件事只在一个地方表示。例：谁在干活由输入框上的猫表示，顶栏不放第二只。
4. 等待画面不用转圈，用模糊骨架和缓慢呼吸的灰色 logo。

**来源**

- D17：消息操作行放在消息下方，没有容器，owner 形容为 "Trivial"（[0017](../decisions/0017-quiet-message-action-row.md)）。
- D16：已送达、已读后来改成只在悬停、聚焦、轻点时出现，放进操作行（[0016](../decisions/0016-optimistic-send-receipts.md)，`1d2b322`）。
- D23：顶栏不再放第二只猫（[0023](../decisions/0023-no-second-cat-in-header.md)）。
- D20：启动画面是模糊骨架加呼吸的灰色 logo（[0020](../decisions/0020-phone-push-and-boot-screen.md)）。

**现在怎么做到的**

- 操作行只给当前那一条消息挂载（`ui/src/messageActionsStore.ts:3-6`），按钮 `background:transparent; color:var(--muted)`，悬停 `color:var(--ink)`（`ui/src/v2-app.css:594-599`）。
- 已送达、已读不占自己的一行（`ui/src/SendReceipt.tsx:16-20` 注释）。
- 启动画面 logo `grayscale(1)`、透明度 0.7，呼吸周期 2.4 s（`ui/src/boot-screen.css`，`.boot-logo`）。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 静止时没有新常驻控件 | 在 `npm run dev:remote` 预览（D22）里打开一个长对话，鼠标移出消息区截一张图，和 `main` 的同一位置对比：除了内容本身，不应多出按钮、徽章或边框 |
| 测试 | `npm --prefix ui run test:send-receipts`（"desktop: delivered and read are not on show and own no row"，`ui/scripts/test-send-receipts.mjs:251`）、`test:chat-bubbles`（触屏下"actions are hidden until a tap"，`ui/scripts/test-chat-bubbles.mjs:421`） |
| 颜色 | 新的行内控件在 DevTools 里看 computed `color`：静止时等于 `--muted`，`background-color` 为 `transparent`，`box-shadow` 为 `none` |
| 新增常驻元素 | 要在 PR 说明里写明为什么不能等到悬停再出现，并请 owner 在预览里看过 |

**已知违反**：C22（悬停提示用强调色底，是界面里最显眼的东西，待 owner 定）。

---

## P2 任何东西都不遮住内容和按钮

**规则**

1. 出现的东西给自己留位置，不压在别的内容上。能放进文档流就放进文档流；要浮起来，就先为它预留空间。
2. 有几样东西必须永远点得到：发送按钮、"新消息"跳转胶囊、手机上的返回按钮、正在回答的问题卡的按钮。任何浮层都不能盖住它们。
3. 浮层的位置用**实测尺寸**夹在视口里，不用写死的宽高估算。
4. 层级按 `tokens.md` 第 9.3 节的刻度放，不随手写数字。

**来源**

- D17：消息间距加大，给操作行预留位置，保证它出现时不压住任何内容（[0017](../decisions/0017-quiet-message-action-row.md)）。之前的悬浮胶囊会挡住上一条消息的最后一行（`bfab118`）。
- D18：任何东西都不能盖住"新消息"跳转胶囊（[0018](../decisions/0018-progressive-blur-scope.md)）。

**现在怎么做到的**

- 悬停设备上操作行的 28 px 始终预留，显示或隐藏都不让页面跳（`ui/src/v2-app.css:580-584` 注释、`:601-606`）。
- 触屏上操作行插进文档流，把下面的内容推开，而不是盖在相邻消息上（`v2-app.css:607-610`）。
- 胶囊 `--z-pill`，高于模糊带 `--z-edge-blur` 和输入框 `--z-chrome`（`ui/src/styles.css:153`；迁移第 1 步之前是 `progressive-blur.css:51` 的 `z-index:6`）。
- 电脑小窗在视口 ≥ 1024 px 时让消息区右侧让出它的宽度（`ui/src/FloatingDesktop.tsx:50-60`）。
- 表情菜单按实测尺寸夹在视口里（`ui/src/MessageReactions.tsx:43-52`）。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 自动测试 | `npm --prefix ui run test:live-fixes`：胶囊中心和四角用 `elementFromPoint` 取到的都是胶囊本身，且完全在视口内（`ui/scripts/test-live-fixes.mjs:60-80`）。新的"必须点得到"的控件照这个写法加断言 |
| 层级 | `npm --prefix ui run test:layers`：z-index 只能用 `tokens.md` 9.3 的 `--z-*`（组件内部用 -1..3），弹层只进 `#tofi-layers`，叠在一点上的弹层按刻度顺序画（`ui/scripts/test-layers.mjs`） |
| 间距 | `test:chat-bubbles` 检查同一发送者消息间距 28–36 px（`ui/scripts/test-chat-bubbles.mjs:198-201`） |
| 手动：遮挡扫描 | 在预览里打开新浮层后，在 DevTools Console 运行下面的片段，四个尺寸各跑一次：390×844、768×1024、1024×768、1280×800。任何一行是 `false` 就是违反 |
| 手动：留位 | 让浮层出现、消失各一次，用 Performance 面板的 Layout Shift 轨道确认没有内容被推动（触屏操作行这类有意推开的除外，要在 PR 里说明） |

```js
// 必须点得到的控件：最上层元素是不是它自己
[".send-button", ".jump-latest", ".back-button", ".composer textarea", ".question-card.is-pending button"]
  .flatMap(sel => [...document.querySelectorAll(sel)])
  .filter(el => el.getClientRects().length)
  .map(el => { const r = el.getBoundingClientRect(); const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return [el.className || el.tagName, Boolean(top && el.contains(top))]; });
```

**已知违反**：C3（操作行的悬停提示压在消息上）、C9（猫可能压住最后一条消息，待实测）、C15（权限提示盖住发送按钮）、C20（右键菜单写死尺寸）、C23（到不了原生 dialog 之上）、C26（层级，第 1 步后待 owner 定是否关闭）、C32（toast 压住顶栏标题）、**C36（窄于 1024 px 时电脑小窗盖住消息，高）**、C40、C42（刚加载就打开电脑小窗时停在视口外，待确认）。

---

## P3 操作要立刻有反馈，失败要说出来

**规则**

1. 点了就变：本地能决定的结果当帧显示（乐观更新），不等服务器。例：发送后气泡立刻出现，表情立刻加上，复制后图标立刻变勾。
2. 要等服务器的，当帧给出"进行中"的状态（按钮变"提交中"、禁用、`aria-busy`）；加载动画只在超过 300 ms 时才出现，快的请求什么都不闪。
3. 失败要在原地说出来：写明发生了什么，给一个重试。静默失败是缺陷。
4. 状态的每一步都要能看出区别（发送中、已送达、已读、失败）。
5. 保存是否已经生效，要让人一眼知道。

**来源**

- D16：乐观发送，发送中（变淡 + 时钟）、✓ 已送达、✓✓ 已读、失败"未发送 · 重试"（[0016](../decisions/0016-optimistic-send-receipts.md)）。
- D17 的提交里同时做了"悬停即出现，不淡入等待"（`bfab118`）；之后 `0b66420` 给它加了 80 ms 透明度淡入（`--m-instant`，无延迟，见 `components.md` C4）。
- 表情乐观更新（`25ca5ae` "optimistic reactions"）。

**现在怎么做到的**

- 乐观气泡和回执状态由 `receiptFor` 一处决定（`ui/src/sendReceipts.ts:36-42`）；失败行"未发送 · 重试"（`ui/src/SendReceipt.tsx:23-25`）。
- 表情：先在本地改，请求失败再回滚并在表情旁显示错误（`ui/src/MessageReactions.tsx:58-67`）。
- 复制：成功后图标变勾 1.2 s；局域网 http 没有异步剪贴板时退回 `execCommand`（`ui/src/App.tsx:296-305`、`:315-318`）。
- 慢请求才显示加载：`DelayedFeedback`（300 ms）、`useGraceFlag`（`ui/src/DelayedFeedback.tsx:3-10`、`ui/src/useGraceFlag.ts:3-11`）；启动画面前 300 ms 不画（`ui/src/BootScreen.tsx:16`）。
- 对话框出错时主按钮变"重试"（`ui/src/DeleteConversationDialog.tsx:48`）。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 当帧变化 | 在 Playwright 测试里点击后不等网络，下一帧（`page.evaluate(() => new Promise(requestAnimationFrame))`）就断言界面已变。参考 `test:send-receipts` 里"sending, delivered, read, in place, with one bubble all the way"（`ui/scripts/test-send-receipts.mjs:218`） |
| 慢网络 | 测试里把 mock 延迟设到 3 s（`test-send-receipts.mjs` 的 `mock.readDelayMs = 3000` 写法），确认进行中状态立刻出现、加载动画在 300 ms 后才出现 |
| 失败路径 | 每个新操作都要有一个失败用例：mock 返回 500，断言出现 `role="alert"` 或具名的失败文字，并且有重试入口 |
| 静默失败 | 评审时看新代码里每个事件处理里的 `catch`：要么设置可见的错误，要么有注释说明为什么可以不说（例：`App.tsx:297` 的剪贴板降级） |
| 状态可辨 | 每个状态截图放进 PR，不靠颜色单独区分时要写明靠什么区分 |

**已知违反**：C5（已读和已送达同色，测试抓不到）、C19（http 下"复制 ID"静默失败）、C30（设置里两种保存方式并存）、C31（对话框开着时 toast 看不到）、C43（刷新失败的 toast 被一次成功的配置读取清掉，待确认）。

---

## P4 动效要有目的

**规则**

1. 每个动画都要回答"它在解释什么"：东西从哪来、到哪去、哪个是新的。只为好看的动画不加。
2. 只用动效令牌里的时长和曲线；离开比进入短。
3. 弹簧（`--ease-spring`）是唯一会回弹的曲线，只用在整条消息到达这类"有冲量"的时刻，不用于布局。
4. 动画只动 `transform` 和 `opacity`（以及 `translate`），不动会触发排版的属性。
5. 每个动画都有减少动效分支：瞬间到位，或者只剩淡入淡出。
6. JS 里的计时器时长和对应 CSS 动画用同一个令牌，不各写一个数。

**来源**

- D19：新消息到达是弹簧链（牛顿摆），最下面的先动，约 15 ms 错开，越过 2–4 px，共约 400 ms；流式输出时轻跟随；减少动效时只淡入（[0019](../decisions/0019-arrival-spring-cascade.md)）。
- D20：手机列表和对话之间是 iOS 式推入（[0020](../decisions/0020-phone-push-and-boot-screen.md)）。
- 动效词汇和守卫：`AGENTS.md` "Web UI motion"（`0b66420`、`36c6e0c`）。

**现在怎么做到的**

- 令牌：`--m-instant` 80 / `--m-press` 120 / `--m-quick` 140 / `--m-base` 220 / `--m-panel-in` 280 / `--m-panel-out` 180 / `--m-flight` 640 ms；JS 镜像 `ui/src/motion.ts:4`。
- 弹簧链参数集中在 `CASCADE`（`ui/src/motion.ts:18-30`），整个过程不改布局和 `scrollTop`，只加视觉偏移（`ui/src/arrivalCascade.ts:3-12`）。
- 手机推入：进入 280 ms、返回 220 ms，两个窗格同时落地，减少动效时直接切换（`ui/src/styles.css:313-328`）。
- 侧栏拖动只用 transform，React 只在放下时渲染一次（`ui/src/sidebarDndEngine.ts:5-11`）。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 令牌守卫 | `npm --prefix ui run test:motion-tokens`：800 ms 以下的字面时长、`cubic-bezier()`、裸 `ease`、缺 `-webkit-` 孪生的 `backdrop-filter` 都会失败；旧的遗留在 `ui/scripts/motion-tokens-baseline.json`，只许减少 |
| 目的 | PR 说明里每个新动画写一句"它解释什么"，以及用的令牌、减少动效下的表现 |
| 不排版 | 在 Performance 面板录一次动画，动画期间 Layout 事件应为 0（或在测试里用 CDP `Performance.getMetrics` 的 `LayoutCount` 前后对比，写法见 `ui/scripts/test-composer-typing.mjs`） |
| 减少动效 | 用 `page.emulateMedia({ reducedMotion: "reduce" })` 跑一遍，断言动画时长为 0 或只有透明度。参考 `test:sidebar-pins` 的减少动效用例（`ui/scripts/test-sidebar-pins.mjs:356`）、`test:shell-budget` 第 3 部分 |
| 已有预算 | `test:arrival-glide`（跟随滚动）、`test:chat-motion`、`test:shell-budget`（手机推入） |

**已知违反**：C34（启动画面溶解 JS 320 ms 对 CSS 280 ms）、C41（胶囊按下不走按压令牌）；遗留的字面量见 `motion-tokens-baseline.json`。

---

## P5 说业界通行的话，用业界通行的做法

**规则**

1. 文案用用户在主流产品里已经见过的说法，短、直接，说发生了什么、下一步做什么。不用助手腔（"看起来……""很抱歉""请注意"），不用自己发明的词。
2. 状态的说法和主流聊天软件一致：发送中、已送达、已读、未发送 · 重试。
3. 交互照主流做法：手机上像 iOS 一样推入；快捷键按平台写（Mac 用 ⌘，其他平台用 Ctrl）；Enter 发送、Shift+Enter 换行、输入法组字时不发送。
4. 做新功能前先看对标产品怎么做（D11 点名了 Meta Muse、OpenAI Dots、xAI Grok Bot）。

**来源**

- D16 的定稿文案"未发送 · 重试"，以及 ✓ / ✓✓ 的约定（[0016](../decisions/0016-optimistic-send-receipts.md)）。
- D20 的 iOS 式推入（[0020](../decisions/0020-phone-push-and-boot-screen.md)）。
- D11 的对标产品（[0011](../decisions/0011-handover-cards.md)）。
- "用业界术语，别用直白白话描述状态，那读起来像 AI 写的"：owner 的长期反馈，仓库内没有原话，由协调者转述；它在 TOFI 上的具体适用范围：待确认。

**现在怎么做到的**

- 语气规则：`ui/src/locales/README.md` "Tone"（简洁、直接；不用 hedging；英文按钮用句首大写；错误写成 "Couldn't load your timezone. Try again."）。
- 回执文案：`ui/src/locales/zh-CN/chat.json`（`sending` 发送中、`delivered` 已送达、`read` 已读、`failed` 未发送、`retry` 重试）。
- 输入框 Enter / Shift+Enter / 输入法判断：`ui/src/App.tsx:3426-3433`。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 文案进本地化 | `npm --prefix ui run test:i18n`（新硬编码文字会失败） |
| 语气 | 新文案逐条对照 `locales/README.md` "Tone"；把中文读出来，像不像微信、iMessage、Slack 里会出现的话。出现"已成功完成""请稍候片刻""您的请求"这类说法就改 |
| 对标 | PR 说明里写一句"主流产品怎么做"，附产品名；和主流做法不同时写原因 |
| 平台惯例 | 快捷键和手势在 macOS、Windows、iOS 各试一次；显示的快捷键符号要和平台一致 |

**已知违反**：C7（快捷键只认 ⌘，Windows/Linux 不可用）、C38（手机没有边缘右滑返回）。

---

## P6 网页和桌面客户端长得一样，模糊除外

**规则**

1. 网页源码是唯一的界面源码；桌面客户端用同一份构建产物。样式和行为默认两端一致。
2. 允许不同的只有：渐变模糊的用法（D18）、操作系统本来就提供的东西（原生标题栏、系统权限、右键菜单习惯），以及能说清楚原因的平台限制。
3. 每加一个 `html[data-desktop]`、`html:not([data-desktop])` 选择器或 `isDesktop` 分支，都要在代码旁或 PR 里写明为什么两端不同。

**来源**

- D18：渐变模糊原则上给 Electron 和 iOS；网页保留毛玻璃，例外是网页消息窗格底部的真渐变模糊和透明顶栏（[0018](../decisions/0018-progressive-blur-scope.md)）。
- `AGENTS.md` "Core source boundaries"：网页源码是规范来源，桌面客户端消费带版本的 UI 产物。
- "除模糊外两端一样"是协调者对 D18 的概括；owner 是否这样说过：待确认。

**现状**（基线 `748c774`，`ui/src` 下）

| 计数 | 数量 | 怎么数 |
|---|---|---|
| 只给网页的 CSS 选择器 | 348 | `grep -o 'html:not(\[data-desktop\])' ui/src/*.css ui/src/settings/*.css \| wc -l` |
| 只给桌面客户端的 CSS 选择器 | 56 | `grep -o 'html\[data-desktop' …  \| wc -l` |
| tsx 里的 `isDesktop` 引用 | 123（其中 `App.tsx` 56 处，分布在 33 行） | `grep -o 'isDesktop' ui/src/*.tsx ui/src/settings/*.tsx \| wc -l` |

两端差别已经很大：网页有消息操作行，桌面客户端用右键菜单和原生 `title`（`ui/src/App.tsx:307` 注释"Browser-mode message actions"、操作行 `:2804`、右键 `onContextMenu` `:2792`）；工具行有两套实现（`WebToolActivityRun`、`DesktopToolActivityRun`，`App.tsx:3010`、`:3091`）。这些差别哪些是有意的：待确认，需要 owner 逐项看。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 不新增无理由的分支 | `git diff main -- ui/src \| grep -E '^\+.*(data-desktop\|isDesktop)'`：每一行新增都要有原因 |
| 两端对照 | 同一个对话分别在浏览器预览和桌面客户端里截图对比；除模糊外不应有差别 |
| 计数只减不增 | 上表三个数字每次迁移后重数，写进 PR 说明 |

**已知违反**：C2（两套工具行）、C17（三套模态机制）、C24（三种提示机制）、C33（七种通知写法）。

---

## P7 量交互开销，预算写成会失败的测试

**规则**

1. 改界面要量：每次按键、每一帧花多少脚本时间、触发多少次排版。不量就不说"不卡了"。
2. 预算写成会失败的测试，并证明它会失败：把修复撤掉，测试必须红。
3. 高频变化的状态放在组件局部（例：正在输入的文字放在输入框里，流式文字不进 Workspace 状态），不放进整个工作区。
4. 在真实规模上量：长对话、几十个会话、4 倍 CPU 降速、WebKit。
5. 报"通过"以退出码为准。
6. 小改动用 `npm run dev:remote` 对着生产数据预览，不为每个小改动发 rc。

**来源**

- D02：输入框每键 37.5 ms、66 次排版的事故；之后要求量开销、预算进测试（[0002](../decisions/0002-measure-interaction-cost.md)，`docs/agent-plan/wave-notes.md:94`，`ef6a51c`）。
- D22：用 `dev:remote` 预览（[0022](../decisions/0022-preview-via-dev-remote.md)）。
- D30：以退出码证明通过；每个修复都要有撤掉修复就会失败的测试（[0030](../decisions/0030-standing-operating-rules.md)）。

**现有预算**

| 测试 | 预算 | 出处 |
|---|---|---|
| `test:composer-typing` | 每键脚本 ≤ 4 ms，排版 ≤ 2 次 | `ui/scripts/test-composer-typing.mjs:158-165` |
| `test:stream-cost` | 每个流式片段 ≤ 4 ms，4 倍降速 ≤ 16 ms，没有超过 34 ms 的帧 | `AGENTS.md` "Web timeline performance" |
| `test:switch-budget` | 已缓存会话切换 p95 < 60 ms，4 倍降速 < 100 ms，WebKit 也测 | 同上 |
| `test:sidebar-pins` | 60 个会话拖动 p95 帧 ≤ 20 ms（4 倍 ≤ 34 ms），每帧排版比静止时多不超过 1.5 次，每帧脚本 < 4 ms（4 倍 < 16 ms） | `ui/scripts/test-sidebar-pins.mjs:415-421` |
| `test:shell-budget` | 入口 JS gzip ≤ 315,805 字节（`Math.floor(394_757 * 0.8)`；脚本头注释写的 315,806 是未取整的说法）；设置窗第一帧 ≤ 32 ms（4 倍 ≤ 100 ms）；手机推入不出空帧 | `ui/scripts/test-shell-budget.mjs:1-8`、`:29` |
| `test:avatar-frame-budget` | 猫的逐帧开销 | `ui/package.json` |
| `test:arrival-glide` | 跟随滚动 220 ms、流式 120 ms、每帧最多 24 px | `AGENTS.md` "Web UI motion" |
| `test:timeline-rows`、`test:perf-structure`、`test:chat-switch` | 行的 memo 结构、切换 | `AGENTS.md` "Web timeline performance" |

**怎么检查**

| 检查 | 方法 |
|---|---|
| 新的高频交互（打字、悬停、拖动、滚动、流式） | 加或扩一个预算测试：生产构建（开发版 React 慢几倍，量的是错的东西，见 `test-composer-typing.mjs:17`），用 CDP `Performance.getMetrics` 量脚本时间和 `LayoutCount`，1 倍和 4 倍 CPU 各一次 |
| 证明会失败 | 撤掉修复跑一次，测试应失败；在 PR 里写明 |
| 退出码 | `npm --prefix ui run test:<name>; echo $?`，以 `0` 为准，不数输出里的 ok 行 |
| 悬停和合成层 | 加 `backdrop-filter` 或大面积 `will-change` 前，先在悬停预算下量；`progressive-blur.css:26-30` 记录过一层模糊就超预算的例子 |
| 预览 | `cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`，用 Chrome 打开 `http://localhost:5173`（`AGENTS.md` "Fast UI preview"） |

**已知违反**：C10（侧栏扫过时的预取没有上限）、C28（连接卡轮询待确认）。另外，`ui/scripts/` 的 83 个 `test-*.mjs` 里有 26 个没有对应的 npm script（例 `test-message-spacing-browser.mjs`、`test-floating-desktop.mjs`），是否进 CI：待确认。

---

## P8 键盘、触屏、读屏同样能用

**来源说明**：这一条不是 owner 拍板的决定，是现有代码已经统一遵守的工程约定（证据如下），写在这里是为了让迁移时不丢。owner 若有不同意见，以 owner 为准。

**规则**

1. 焦点环只在键盘导航时出现，点击和轻点不出现。
2. 一组同类控件（工具栏、消息列表）只占一个 Tab 停靠点，组内用方向键。
3. 打开的东西接住焦点，关掉后焦点回到打开它的地方；Escape 关最上面那一层。
4. 关掉、折叠、在背后的东西用 `inert` 退出 Tab 顺序和读屏。
5. 触屏点击区至少 44 px；悬停才有的东西在触屏上要有轻点的入口。
6. 状态变化对读屏可见（`role="status"` / `role="alert"`），声明的 ARIA 角色要有对应的键盘行为。

**现在怎么做到的**：`ui/src/focusModality.ts:1-20`；操作行 `role="toolbar"` 和方向键（`ui/src/App.tsx:320-331`）；消息列表一个停靠点（`App.tsx:2232-2244`）；`Disclosure` 和菜单的 `inert`；触屏操作行按钮 44 px（`ui/src/v2-app.css:609`）；侧栏移动的 `role="status"` 播报（`App.tsx:2453`）。

**怎么检查**

| 检查 | 方法 |
|---|---|
| 焦点环 | `ui/scripts/focus-audit.mjs`（被 `test:settings-polish`、`test:chat-bubbles`、`test:file-preview`、`test:onboarding` 调用）。新的浮层把它接进自己的测试 |
| 只用键盘走一遍 | 拔掉鼠标：Tab 进入、方向键移动、Enter 执行、Escape 关闭并确认焦点回到原处 |
| 触屏 | Playwright `hasTouch: true`、390 px 宽跑一遍（写法见 `test:sidebar-pins`、`test-chat-bubbles.mjs:418`） |
| 读屏 | macOS VoiceOver 打开新组件，听一遍角色和状态播报 |

**已知违反**：C6、C8、C12、C13、C14、C18、C25、C27、C29。

---

## 9. 冲突时怎么取舍

从上到下，上面的优先：

1. **P2 不遮挡**。盖住按钮就是功能坏了。
2. **P3 反馈**。用户必须知道点了有没有用。
3. **P8 可用性**。键盘、触屏、读屏用户也得能完成同样的事。
4. **P7 开销**。超出预算的设计要换做法，不能放宽预算（放宽要 owner 同意）。
5. **P1 低调**。
6. **P4 动效**。
7. **P5 业界惯例**、**P6 两端一致**。

例子：触屏上的操作行如果像悬停设备那样始终预留空间，消息间距会大到难看（P1）；浮起来又会盖住相邻消息（P2）。现在的做法是插进文档流、把下面的内容推开（`v2-app.css:607-610`），P2 优先于"不让页面动"。

## 10. 评审清单（改界面的 PR 照抄进说明）

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
