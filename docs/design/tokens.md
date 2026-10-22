# TOFI 设计令牌（Design Tokens）现状与整理方案

> 范围：Web UI（`ui/src`）。快照基于提交 `748c774`（分支 `docs/system-opus`，2026-10-20 抽取）。
> 本文分两部分：**第一部分是"现在真实在用的值"**（每条带 `file:line`），**第二部分是"提议"**（层级刻度、合并方案）。提议部分在 owner 点头前不是规范，不得据此改代码（D28：先出方案，再逐步迁移并预览）。
> **例外：第 9 节的层级刻度已经落地**（迁移第 1 步，分支 `ds/step1-layers`，owner 的回答见 D31–D34），是规范；第 9 节的 `file:line` 按该分支写。其余各节仍按 `748c774`。
>
> 统计口径：产品样式 = `ui/src` 下除 `ui/src/motion-lab/`（独立页面 `ui/motion-lab.html`，18 个 CSS）和 `ui/src/lib/tofi-avatar/acceptance/`（头像验收页）以外的全部 `.css`，共 67 个文件（`ui/src/*.css` 63 个 + `ui/src/settings/` 3 个 + `ui/src/oauth/` 1 个）。数字由对这 67 个文件的正则扫描得出，复现方法见文末。

---

## 0. 令牌从哪里来、谁在守

| 东西 | 位置 | 说明 |
|---|---|---|
| 唯一令牌源 | `ui/src/design-tokens.css:3-105`（浅色 `:root`，其中 `:99-104` 是层级刻度 `--z-*`）、`:106-123`（深色 `:root[data-theme="dark"]`） | 由 `ui/src/interaction-system.css:1` 的 `@import "./design-tokens.css"` 引入。共定义 201 个自定义属性（`748c774` 是 188 个，第 1 步加了 13 个 `--z-*`）。 |
| 动效 JS 镜像 | `ui/src/motion.ts:4-11`（`MOTION`、`EASE`），`:18-30`（`CASCADE` 到达级联参数） | 与 CSS 的 `--m-*`、`--ease-*` 必须一致。 |
| 主题切换 | `ui/src/InteractionSystem.tsx:7-30`（`useAppearance`） | 偏好存 `localStorage["tofi:appearance"]`（system/light/dark），写到 `<html data-theme>`；`system` 跟随 `prefers-color-scheme`。启动画面在 React 前先设一次：`ui/src/BootScreen.tsx:23-26`。**CSS 里没有任何 `@media (prefers-color-scheme)`**，深色只由 `data-theme="dark"` 触发。 |
| 颜色守卫 | `npm --prefix ui run test:final-v2-tokens`（`ui/scripts/test-final-v2-tokens.mjs`） | 对照 `ui/test-fixtures/design/tokens/colors.css` 与 `tokens.json`：67 个颜色令牌两种模式都要有；36 对"`*-text` 对表面"对比度 ≥ 4.5:1；12 对"`--on-block` 对填充色"≥ 4.5:1；禁止把填充色（`--clay` 等）当前景 `color:` 用。2026-10-20 在本快照运行退出码 0。 |
| 动效守卫 | `npm --prefix ui run test:motion-tokens`（`ui/scripts/test-motion-tokens.mjs`） | 禁止 800 ms 以下的字面时长、`cubic-bezier()`、裸 `ease`（延迟值除外，见脚本 `:67`）；每个 `backdrop-filter` 必须有 `-webkit-` 孪生。遗留上限在 `ui/scripts/motion-tokens-baseline.json`：目前只剩 `src/mailSendFlight.ts` 9 处。2026-10-20 运行退出码 0。 |
| 层级守卫 | `npm --prefix ui run test:layers`（`ui/scripts/test-layers.mjs`） | z-index 只能是 `var(--z-*)`、`calc(var(--z-*) ± 1)` 或 -1..3 的局部值；旧的更大局部值的上限在 `ui/scripts/layers-baseline.json`（5 个文件 14 处）；TS 里不许写数字 z-index；弹层只进 `#tofi-layers`；浏览器里按刻度检查叠放顺序。见第 9.5 节。 |
| 焦点环规则 | `ui/src/focus-ring.css:1-7` | 全产品一种键盘焦点环；组件只能改 offset，不改宽度和颜色。鼠标/触摸时不画（`focusModality.ts` 设 `html[data-input="pointer"]`，`focus-ring.css:14-19`）。 |

**圆角、间距、阴影、模糊没有任何测试守着**，也基本没走令牌（见下文各节的"令牌使用率"）。z-index 从迁移第 1 步起由 `test:layers` 守着（第 9 节）。

---

## 1. 颜色

### 1.1 核心调色板（语义名 → 浅色 / 深色）

定义：浅色 `design-tokens.css:7-15`，深色 `design-tokens.css:102-107`。

| 令牌 | 角色 | 浅色 | 深色 | 产品 CSS 中 `var()` 次数（不含 `design-tokens.css` 自身） |
|---|---|---|---|---|
| `--bg` | 页面底 | `#f1efeb` | `#14120f` | 31 |
| `--surface` | 卡片、弹层、输入聚焦底 | `#fcfbf9` | `#1f1c18` | 194 |
| `--surface-2` | 次级面、悬停、选中 | `#f5f3ef` | `#2a2621` | 135 |
| `--sunk` | 下沉面（输入框、侧栏） | `#e9e6e1` | `#181613` | 57 |
| `--line` | 粗墨线（主按钮/卡片描边） | `#2a2019` | `#f1e6d6` | 180 |
| `--line-soft` | 细分隔线 | `#dcd8d1` | `#3a352e` | 238 |
| `--ink` | 正文 | `#241b14` | `#f3eadb` | 303 |
| `--ink-soft` | 次级文字 | `#54473a` | `#c9bca8` | 155 |
| `--muted` | 元信息、占位符 | `#6f6252` | `#a3957f` | 355 |
| `--focus` | 焦点环色（经 `--focus-ring`） | `#2f6db0` | `#8cc3f0` | 2（`--focus-ring` 61） |
| `--on-block` | 彩色块上的字 | `#1a140f` | 同左（未覆盖） | 44 |

### 1.2 四个品牌色（"填充 / 可读前景"成对）

每个色相有一个填充值和一个用于文字的 `*-text` 值（`design-tokens.css:9-11`，深色 `:104-105`）。填充色不得用作 `color:`（守卫见第 0 节）。

| 令牌（填充 / 文字） | 用途（来自 `:28-29` 的状态映射） | 浅色 | 深色 |
|---|---|---|---|
| `--clay` / `--clay-text` | 主强调（主按钮、发送键、光标） | `#e07f55` / `#a8502a` | `#ec9a74` / `#f2a887` |
| `--iris` / `--iris-text` | "需要你决定"、提及、选区 | `#a58ee6` / `#6a4fbf` | `#c0acf0` / `#c9b8f4` |
| `--lagoon` / `--lagoon-text` | 工作中 | `#4fb3a0` / `#1d7466` | `#72cdbc` / `#7fd6c6` |
| `--honey` / `--honey-text` | 未读 | `#e0a82a` / `#8a6106` | `#f3c75a` / `#f5cf6e` |
| `--green` / `--green-text` | 完成、成功 | `#6fae7f` / `#37744a` | `#93cfa1` / `#9dd6ab` |
| `--danger` / `--danger-text` | 错误、删除 | `#d9573f` / `#b0381f` | `#f4917f` / `#f79d8c` |
| `--wait` | 等待 | `#6f6252` | `#c9bca8` |

### 1.3 别名层（同一颜色的第二、第三个名字）

`design-tokens.css` 自己注明这些是"临时别名"（`:1-2`、`:5-6`、`:83`）。实际仍在大量使用：

| 别名组 | 定义 | 解析到 | 使用次数 |
|---|---|---|---|
| 旧色名 `--yellow --pink --blue --orange`（及 `*-text`） | `:12-13` | `--clay --iris --lagoon --honey` | 17 / 10 / 17 / 13（`*-text` 另有 0 / 0 / 3 / 3） |
| 语义面 `--surface-page/-card/-raised/-sunk` | `:23` | `--bg --surface --surface-2 --sunk` | 1 / 1 / 0 / 1 |
| 语义字 `--text-body/-secondary/-meta/-on-block` | `:24` | `--ink --ink-soft --muted --on-block` | 12 / 24 / 1 / 2 |
| 语义字 `--text-process` | `:24` | `--muted`（Bot 过程行的字：安静、不抢正文；浅/深色对比度都不低于 AA，见 `components.md` 第 1 节"过程行"） | 7 |
| 语义边/强调 `--border-edge/-soft`、`--accent-primary/-decide(-text)` | `:25-26` | `--line --line-soft --clay --iris` | 2 / 7 / 1（`-text` 1）/ 2（`-text` 0） |
| 状态 `--status-working/-unread/-done/-error/-wait(-text)` | `:28-29` | 对应色相 | 大多为 0（见 1.6） |
| 兼容名 `--text --text-soft --accent --surface-soft --surface-hover --sidebar-bg --selected-bg --line-strong --success --on-accent --bubble-user` | `:84-88` | 见右 | 43 / 39 / 7 / 18 / 19 / 3 / 5 / 4 / 3 / 1 / 1 |

### 1.4 专用色组（固定美术或专用场景）

| 组 | 定义 | 深色是否覆盖 | 用途 |
|---|---|---|---|
| 信纸 `--paper --paper-ink --paper-rule --paper-muted --sticky --airmail-red --airmail-blue` | `:18-19` | 只覆盖 `--paper`（`:108`） | 显示卡片、邮件草稿 |
| 邮件 `--mail-*`（13 个） | `:64-68` | 只覆盖 `--mail-paper`（`:116`） | 邮件阅读/寄出动画 |
| 代码 `--code-inline-* --code-block-*` | `:21-22` | 全部覆盖（`:109-110`） | Markdown 代码 |
| 共享电脑外壳 `--desktop-controls-* --desktop-bezel* --desktop-switch-* --desktop-pointer-*` | `:40-56` | 不覆盖（刻意两种主题相同，`:39`、`:49` 注释） | 远程桌面预览 |
| 主题色板预览 `--swatch-light-* --swatch-dark-*` | `:57-62` | 不覆盖（刻意） | 设置里的外观选项，`interaction-system.css:114-116` |
| 灯箱 `--lightbox-scrim --lightbox-ink` | `:70-71` | 不覆盖（刻意，`:69`） | 图片放大 |
| 文件类型 `--file-xlsx --file-pdf` | `:20` | 不覆盖 | **未被使用**，见 1.6 |

### 1.5 令牌之外的颜色

- 产品 CSS 中字面十六进制色只有：`#000`/`#fff` 用于遮罩渐变（如 `progressive-blur.css:22-23`），以及 `file-preview.css:2-12` 的 10 个文件类型色、`extensions-system.css:14` 的 `#fff`、`extensions-system.css:131` 的 `#c98a1b`（作 `--amber` 的回退值）。
- 透明度派生全部靠 `color-mix(in srgb, var(--x) N%, transparent)`，共 152 处，百分比各不相同（例如遮罩层，见 1.6 第 8 条）。
- TSX 中字面色：`ui/src/ServiceMark.tsx` 6 处（第三方服务标志色）。头像库 `ui/src/lib/tofi-avatar/` 的调色板未纳入本次统计（待确认是否属于设计令牌范围）。
- `ui/index.html:6` 的 `<meta name="theme-color" content="#f4ecdd">` 不等于任何令牌；运行时 `InteractionSystem.tsx:25-26` 会改写成 `--bg`，所以只影响首帧前的浏览器栏颜色。

### 1.6 颜色问题与合并提议

| # | 问题 | 证据 | 提议 |
|---|---|---|---|
| C1 | **色名与色相不符**：`--yellow` 实为橙色 clay、`--pink` 实为紫色 iris、`--blue` 实为青色 lagoon、`--orange` 实为金色 honey | `design-tokens.css:12-13` | 63 处调用（含 `*-text` 6 处）改成 `--clay/--iris/--lagoon/--honey` 后删掉旧名 |
| C2 | **名字骗人的兼容别名**：`--line-strong` 解析为 `--line-soft`；`--surface-hover` 解析为 `--surface`（与卡片底同色，放在卡片上悬停没有变化） | `:85`、`:84` | 调用点改为真实令牌后删除 |
| C3 | 同色重复定义：`--paper`=`--mail-paper`=`#fffdf7`；`--paper-ink`=`--mail-ink`=浅色 `--line`=`#2a2019`；`--paper-rule`=`--mail-rule`=`#e3d6bf`；`--sticky`=`--mail-note`=`#f7e3a8`；`--airmail-red`=`--mail-post-red`=`#d9562a`；`--airmail-blue`=`--mail-post-blue`=`#4f789d`；`--lagoon-text-light`=浅色 `--lagoon-text`；`--desktop-controls-ink`=`--lightbox-ink`=深色 `--ink`=`#f3eadb` | `:18-19`、`:64-67`、`:17`、`:43`、`:71` | 信纸与邮件合成一组 `--paper-*`；`--mail-*` 留作别名一个版本后删除 |
| C4 | 近似色：`--danger #d9573f` 与 `--airmail-red #d9562a`；`--muted #6f6252`、`--mail-muted #766751`、`--paper-muted #8a7a66`；`--sunk #e9e6e1` 与 `--code-inline-bg #eae3d7` | `:15`、`:19`、`:8`、`:64`、`:18`、`:21` | 信纸美术色保留（刻意暖色），`--mail-muted` 并入 `--paper-muted`；其余待 owner 在预览里判断是否可合 |
| C5 | 定义了但从未使用（26 个）：`--accent-decide-text --desktop-controls-grip --field-inset-shadow --file-pdf --file-xlsx --lagoon-text-light --mail-muted --mail-note --mail-note-ink --mail-note-muted --mail-note-shadow --mail-rule --pink-text --settings-rail --sidebar-collapsed --space-1 --space-5 --status-done-text --status-unread --status-unread-text --status-wait --status-working --status-working-text --surface-raised --type-hero --yellow-text` | 扫描结果（`var(--x)` 在产品 CSS/TS 中出现 0 次） | 先决定语义层（1.3）要不要真正启用；不启用就删 |
| C6 | `--file-xlsx`/`--file-pdf` 有令牌却没人用，`file-preview.css:4-5` 用同值字面色，另外 8 个文件类型色只有字面值 | `design-tokens.css:20`，`file-preview.css:2-12` | 建一组 `--file-*`（10 个），`file-preview.css` 全改 `var()` |
| C7 | 引用了不存在的变量：`--hard-ink`（`mail-send-flight.css:2,6`，无回退。它只定义在 motion-lab 页 `motion-lab/motion-lab.css:4-5`）；`--amber`（`extensions-system.css:131`，有回退）；`--panel`（`work-panel.css:296`，有回退）；`--gate-clear`（`desktop-system.css:27`，有回退，可能由桌面客户端注入，待确认） | 扫描 | `--hard-ink` 未定义会让整条 `filter: drop-shadow(...)` 在计算时失效，寄信动画的硬阴影因此不显示（推断，待在预览确认）；改用 `--line`/`--hard` 的颜色。`--amber` 改 `--honey` |
| C8 | 遮罩层（scrim）有 8 种写法，深色模式下用 `--ink` 的那几种会变成**奶白色**蒙层（深色 `--ink` 是 `#f3eadb`） | `--ink` 16%（`styles.css:318`）、19%（`settings-system.css:58`）、22%（`interaction-system.css:99`）、24%（`interaction-system.css:43`）、30%（`desktop-presence.css:106`）、48%（`settings-system.css:70`）；`--bg` 48%（`web-file-drop.css:3`）、58%（`terminal.css:11`、`computer-resources.css:7`）、62%（`onboarding.css:6`）、82%（`message-attachment.css:131`）；`--desktop-controls-solid` 50%（`handover.css:1`、`extensions-system.css:101`、`settings/settings-components.css:144`）；`--lightbox-scrim`（`message-attachment.css:149`、`file-preview.css:16`） | 定两个：`--scrim`（模态，统一一个颜色和不透明度，两种主题都偏暗）和 `--scrim-soft`（拖放、页面内确认这类"页面变浅"的）。需要 owner 在预览里看深色效果 |
| C9 | 深色模式下主按钮悬停时硬阴影从黑变奶白：静止用 `--hard-sm`（深色是 `#000`），悬停写死 `3px 4px 0 var(--line)`（深色 `--line` 是 `#f1e6d6`） | `design-tokens.css:111`；`v2-foundations.css:32,45,159` | 悬停改用新令牌 `--hard-hover`（深色同为 `#000`）。是否刻意，待 owner 确认 |
| C10 | 浅色 `--hard` 写死 `#2a2019` 而不是 `var(--line)` | `design-tokens.css:34` | 改为引用，避免改墨色时漏改 |

---

## 2. 字体与排版

### 2.1 字族

| 令牌 | 值 | 定义 | 实际加载 |
|---|---|---|---|
| `--display`（=`--font-display`） | `"Fredoka","PingFang SC","HarmonyOS Sans SC",system-ui,sans-serif` | `design-tokens.css:74,77` | Fredoka 自托管，字重区间 500–700（`fonts.css:4-13`） |
| `--body`（=`--font-body`） | `"PingFang SC","Inter",system-ui,sans-serif` | `:75` | **Inter 没有打包**（`ui/src/assets/fonts/` 只有 Fredoka 与 JetBrains Mono），非苹果系统落到 system-ui |
| `--mono`（=`--font-mono`） | `"JetBrains Mono",ui-monospace,monospace` | `:76` | 只打包 400、600 两个字重（`fonts.css:14-21`） |

中文不打包字体，靠系统 PingFang/HarmonyOS（`fonts.css:2-3`）。

使用量（`font:` 简写里的字族）：`--body` 215、`--mono` 118、`--display` 89；另有 `--font-body/-mono/-display` 别名 6 次，字面 `"Fredoka"`/`"JetBrains Mono"`/`ui-monospace`/`system-ui` 共 13 次。

### 2.2 排版刻度令牌

`design-tokens.css:78-82`：

| 令牌 | 值 | 使用次数 |
|---|---|---|
| `--type-hero` | `700 40px/1.05 display` | 0 |
| `--type-title` | `700 20px/1.2 display` | 3 |
| `--type-button` | `600 14px/1 display` | 4 |
| `--type-body` | `400 15px/1.6 body` | 9 |
| `--type-row-title` | `600 14px/1.4 body` | 10 |
| `--type-meta` | `400 13px/1.5 body` | 50 |
| `--type-label` | `600 10px/1.2 mono`，配 `--label-tracking: .14em` | 1 |
| `--type-status` | `600 11px/1.4 mono` | 20 |
| `--type-status-regular` | `400 11px/1.4 mono`（T2 的落地；过程行里步骤状态用） | 2 |
| `--type-code` | `400 13px/1.6 mono` | 7 |

### 2.3 实际在用的值（`font:` 简写 + `font-size`/`font-weight`/`line-height` 单写）

- **字号**（`font:` 简写里）：12px ×101、13px ×85、11px ×61、14px ×52、10px ×35、15px ×22、**12.5px ×21**、16px ×14、18px ×8、20px ×7、17px ×5、9px ×4、**13.5px ×3**、22/25/26px 各 3，另有 38、21、23、40、24、30、**14.5、10.5**px 各 1。再加单写 `font-size` 约 330 处，分布相同（12px 148、13px 63、11px 35……）。共 24 个不同字号。
- **字重**：400 约 230、600 约 207、700 约 26、500 约 20、**550 ×9、650 ×3**、800 ×1。550/650 只对可变字体有效；PingFang 与 JetBrains Mono（只有 400/600）会就近取整，结果在不同系统上不一致。
- **行高**：1.4 ×105、1.5 ×102、1.3 ×68、1 ×66、1.2 ×44、1.6 ×45、1.45 ×19、1.55 ×16、1.35 ×11、1.65 ×10、1.25、1.7、1.15、1.08、1.12、1.42 各少量，另有 16/18/20/21/24/32px 等像素行高。共 20 余种。
- 令牌化程度：`font:` 简写共约 550 处，其中 104 处用 `--type-*`（约 19%），另有 13 处 `font:inherit`。

### 2.4 排版问题与合并提议

| # | 问题 | 证据 | 提议 |
|---|---|---|---|
| T1 | 与令牌完全相同的字面值：`13px/1.5 var(--body)` 出现 19 次，正好等于 `--type-meta` | 如 `display-card.css:105`、`work-panel.css:248` | 直接替换 |
| T2 | 与令牌只差字重：`11px/1.4 var(--mono)`（400）12 次，`--type-status` 是 600 | `conversation-workspace.css:91`、`display-card.css:44` | 已新增 `--type-status-regular`（过程行先用上了），其余 12 处待换（需预览） |
| T3 | 半像素字号 12.5 / 13.5 / 14.5 / 10.5px | `onboarding.css:35,38`、`empty-states.css:23` 等 | 归到 12 / 13 / 14 / 10 或 11 |
| T4 | 自定义字重 550 / 650 | `extensions-system.css:12` 等 | 归到 500 / 600 |
| T5 | 等宽 700 只有合成粗体（只加载了 400/600） | `file-preview.css:2`（`700 9px/1 var(--mono)`） | 改 600 |
| T6 | `--body` 写了 Inter 却不打包 | `design-tokens.css:75`、`fonts.css` | 二选一：打包 Inter，或从字族里删掉，承认用 system-ui |
| T7 | 实际刻度应收敛为 | — | 提议 8 档：10 / 11 / 12 / 13 / 14 / 15 / 18 / 20，加 hero 28–40 仅用于引导页；行高收敛为 1（图标/按钮）、1.3（标题）、1.4（UI）、1.5（说明）、1.6（正文） |

---

## 3. 间距

- 令牌：`--space-1..5` = 4 / 8 / 12 / 16 / **22**px（`design-tokens.css:30`）。注意第 5 档是 22，不是 20 或 24。
- 令牌使用：`--space-2` 20 次、`--space-3` 9 次、`--space-4` 4 次，共 33 次，32 次在 `task-issue-card.css`，`styles.css:352` 用了一次 `--space-4`；`--space-1`、`--space-5` 0 次。
- 实际值（`padding`/`margin`/`gap` 中的全部 px 值，共 2459 个）：

| 值 | 次数 | 值 | 次数 | 值 | 次数 |
|---|---|---|---|---|---|
| 8 | 325 | 4 | 186 | 9 | 89 |
| 12 | 286 | 16 | 156 | 18 | 86 |
| 10 | 271 | 14 | 142 | 20 | 49 |
| 6 | 216 | 5 | 108 | 24 | 48 |
| 2 | 97 | 7 | 96 | 3 | 71 |

另有 1、11、13、15、17、19、21、22、25、26、28、30、32、34……共 50 多个不同值。只有 46% 落在 4px 网格上；奇数值（3/5/7/9/11/13）合计约 400 次。

**提议**：用 2px 半步的 4px 网格，即 2 / 4 / 6 / 8 / 10 / 12 / 16 / 20 / 24 / 32 / 48，令牌名 `--space-0_5`(2) `--space-1`(4) `--space-1_5`(6) `--space-2`(8) `--space-2_5`(10) `--space-3`(12) `--space-4`(16) `--space-5`(20) `--space-6`(24) `--space-8`(32) `--space-12`(48)。现有 `--space-5:22px` 改为 20px（目前没人用，无影响）。奇数值按"就近、向偶数"归并，按组件分批改，每批在预览里看。

布局尺寸令牌（`design-tokens.css:98`）：`--chat-max 760px`、`--sidebar-width 272px`、`--sidebar-collapsed 80px`（未用）、`--settings-rail 216px`（未用）。问题：`styles.css:24` 与 `context-card.css:14` 的回退值写的是 `260px`，与令牌 272 不一致（令牌存在时回退不生效，只是误导）。

断点（无令牌）：max-width 700 ×26、767 ×19、560 ×11、min-width 768 ×8、1100 ×5、520 ×4、420 ×4、640 ×2、540 ×2，以及 1360、1280、1279、1121、1120、1099、1023、1000、800、680、480 各 1。提议收敛为 4 个：420（小手机）、767/768（手机/平板分界）、1100（宽窗口浮卡）、1360。

---

## 4. 圆角

令牌（`design-tokens.css:31,33`）：`--radius-sm 9`、`--radius-md 12`、`--radius-lg 16`、`--radius-xl 20`、`--radius-2xl 22`、`--radius-pill 999`；另有旧名 `--r 14`、`--r-lg 20`（与 `--radius-xl` 重复）。

令牌使用 36 次；字面值约 476 次：

| 值 | 次数 | 值 | 次数 | 值 | 次数 |
|---|---|---|---|---|---|
| 12px | 68 | 16px | 30 | 18px | 16 |
| 50% | 54 | 8px | 28 | 22px | 11 |
| 10px | 46 | 6px | 26 | 11px | 9 |
| 14px | 40 | 9px | 23 | 7px | 8 |
| 999px | 31 | 0 | 32 | 4/13/15/20/28px | 4–7 |

还有 2、3、5、30%、99px 和若干四角不等的写法（如 `conversation-workspace.css:93` 的气泡 `16px 5px 16px 16px`）。

**问题**：`task-issue-card.css:39` 写 `var(--radius-sm,10px)`，回退值与令牌 9 不一致；`--r-lg` 与 `--radius-xl` 重复；10/11/13/15 与 9/12/14/16 混用，肉眼几乎无差别。

**提议** 6 档：`--radius-xs 6`（徽标、小块）、`--radius-sm 10`（行内按钮、菜单项）、`--radius-md 12`（按钮、输入框）、`--radius-lg 16`（卡片、菜单）、`--radius-xl 22`（大面板、设置窗）、`--radius-pill 999`；`50%` 只用于正圆。归并：7/8→6，9/11→10，13/14→12，15/18→16，20/28→22。删 `--r`、`--r-lg`、`--radius-2xl`。

---

## 5. 描边、焦点与轮廓

- 线宽令牌：`--edge-soft 1px`、`--edge 2px`、`--edge-bold 3px`（`design-tokens.css:32-33`）。使用 2 / 18 / 9 次；字面写法占绝大多数：`1px solid var(--line-soft)` ×171、`1px solid var(--line)` ×53、`2px solid var(--line)` ×34、`3px solid var(--line)` ×5，另有 `1.5px` 线宽 13 次（`v2-foundations.css:63,102` 等）。
- 风格约定（`v2-foundations.css:11`、`:179`）：只有主操作用粗墨线 + 偏移硬阴影；普通卡片是平的或发丝线。
- 焦点环：`--focus-ring`（=`--focus`）、`--focus-ring-width 2px`、`--focus-ring-offset 3px`（`design-tokens.css:87`），按规范写法 `var(--focus-ring-width) solid var(--focus-ring)` 出现 57 次。offset 用了 3px ×30、2px ×19、4px ×6、-2px ×4、-3px ×1。
- 偏离：`interaction-system.css:11` 给 `.search-box` 画 outline，`v2-foundations.css:122` 又给同一元素画 `box-shadow` 光晕，同一个焦点有两种表现；`v2-app.css:233` 用 `2px solid var(--lagoon)` 标"跳转目标消息"，这是高亮不是焦点，应改名为单独的 `--highlight` 令牌，免得与焦点混淆。

**提议**：线宽只留 1 / 1.5 / 2 / 3，其中 1.5 只给"未选中的勾选框和次级图标按钮"；用 `--border-hairline: 1px solid var(--line-soft)` 这类复合令牌替代 171 处重复写法。

---

## 6. 阴影

| 令牌 | 浅色 | 深色 | 次数 | 用途 |
|---|---|---|---|---|
| `--hard` | `3px 4px 0 #2a2019` | `3px 4px 0 #000` | 29 | 主卡片、对话框、输入框（`design-tokens.css:34,111`） |
| `--hard-sm` | `2px 3px 0 #2a2019` | `2px 3px 0 #000` | 48 | 按钮、菜单 |
| `--float-shadow` | `= --hard` | 同 | 8 | 浮层（与 `--hard` 完全相同，`:89,115`） |
| `--sheen` | `inset 0 1px 0 rgba(255,255,255,.7)` | `… .08` | 7 | 玻璃上沿高光 |
| `--menu-shadow` | `0 12px 32px rgb(0 0 0/.18)` | 不覆盖 | 1 | 表情菜单 |
| `--desktop-bezel-shadow` | `0 12px 32px rgba(0,0,0,.2)` | 不覆盖 | 1 | 电脑外壳 |
| `--field-inset-shadow` | `inset 0 1px 2px rgba(0,0,0,.08)` | 不覆盖 | 0 | — |
| `--mail-note-shadow` | `2px 3px 0 rgba(0,0,0,.18)` | 不覆盖 | 0 | — |

令牌外的阴影：
- 硬阴影悬停态 `3px 4px 0 var(--line)` ×3（见 C9）、`1px 2px 0 var(--line)` ×2（`owner-setup.css:7`、`v2-foundations.css:108`）。
- 模糊投影 5 种互不相同：`0 6px 24px rgb(0 0 0/.18)`（toast，`styles.css:448`）、`0 4px 14px rgb(0 0 0/14%)`（读者名单提示，第 2 步起是 `.action-hint.is-quiet`，`interaction-system.css:97`）、`0 12px 26px rgba(0,0,0,.2)`（拖动中的会话，`sidebar-pins.css:8`）、`--menu-shadow`、`--desktop-bezel-shadow`；还有手机推入面板的侧影 `-14px 0 28px -16px`（`styles.css:322`）。
- 状态光晕 `0 0 0 3px color-mix(… N%)`：N 取 11、13、13、15、28、32（`styles.css:80,262,263`、`web-tool-steps.css:5`、`onboarding.css:76`、`v2-foundations.css:122`）。

**提议**：`--hard` / `--hard-sm` / `--hard-hover`（新，见 C9）三档硬阴影；一个 `--shadow-float`（合并 5 种模糊投影，建议 `0 10px 28px rgb(0 0 0/.18)`，深色加深）；一个 `--halo: 0 0 0 3px` 配统一的 14% 混色。删 `--float-shadow`（与 `--hard` 相同）、`--field-inset-shadow`、`--mail-note-shadow`。

---

## 7. 模糊与玻璃

### 7.1 令牌

| 令牌 | 浅色 | 深色 | 次数 |
|---|---|---|---|
| `--glass` | `rgba(252,251,249,.58)` | `rgba(31,28,24,.52)` | 7 |
| `--glass-strong` | `rgba(252,251,249,.85)` | `rgba(31,28,24,.8)` | 4 |
| `--glass-edge` | `rgba(42,32,25,.14)` | `rgba(241,230,214,.2)` | 6 |
| `--blur` | `blur(40px) saturate(1.8)` | 不覆盖 | 13 |
| `--dots`（点阵底纹，不是模糊） | `#2a201914` 1px 点 / 16px | `#ffffff14` | 11 |

定义：`design-tokens.css:35-38`、`:112-114`。

### 7.2 实际在用的模糊强度（`backdrop-filter`，每处都有 `-webkit-` 孪生）

| 强度 | 位置 | 角色 |
|---|---|---|
| `blur(2px)` | `interaction-system.css:99`（`.detail-backdrop`）、`file-preview.css:56`（文件预览 `::backdrop`） | 抽屉遮罩、文件预览遮罩 |
| `blur(3px)` | `settings-system.css:70`（离开确认 `::backdrop`） | 模态遮罩 |
| `blur(4px)` | `terminal.css:11`（终端面板内确认）、`computer-resources.css:5`（电脑重启确认 `::backdrop`） | 面板内确认遮罩、原生 dialog 遮罩 |
| `blur(6px)` | `interaction-system.css:43`（删除对话框）、`desktop-presence.css:107`（放大电脑） | 模态遮罩 |
| `blur(10px)` | `onboarding.css:6`、`settings-system.css:58`（设置窗遮罩） | 模态遮罩 |
| `blur(12px)` | `handover.css:1`、`extensions-system.css:101`、`settings/settings-components.css:144`、`web-file-drop.css:4`；`boot-screen.css:25,41`（加 `saturate(.85)`） | 模态遮罩、启动画面 |
| `blur(20px) saturate(1.6)` | `desktop-presence.css:197` | 电脑预览状态胶囊 |
| `blur(24px) saturate(1.08/1.2/1.25)` | `desktop-system.css:18-22` | 仅桌面客户端（`html[data-desktop]`）的菜单与只读会话 |
| `blur(32px) saturate(1.4)` | `v2-app.css:307`（附件菜单）、`v2-app.css:541`（全部菜单） | 菜单 |
| `--blur` = `blur(40px) saturate(1.8)` | 顶栏 `v2-app.css:168`、输入框 `v2-app.css:284`、只读会话头尾 `conversation-workspace.css:84,94`、MCP 菜单 `extensions-system.css:61`、账户菜单 `v2-app.css:119`、附件图注 `message-attachment.css:74-75,176`、电脑控件 `desktop-presence.css:14,37,222` | 玻璃 chrome |
| 渐进模糊 `--pb-blur-1 4px` + `--pb-blur-2 12px`（底部），`--pbt-blur-1 3px` + `--pbt-blur-2 9px`（顶栏） | `progressive-blur.css:9,37` | Web 消息区上下边缘（D18） |

另有非 backdrop 的 `filter: blur(1.5px)`（启动 logo，`boot-screen.css:26,28`）。

降级：`prefers-reduced-transparency: reduce` 共 11 处处理（如 `v2-foundations.css:164-168`、`progressive-blur.css:53-59`），不支持 backdrop-filter 时 `progressive-blur.css:60-65`、`v2-app.css:551` 改用实色。

### 7.3 玻璃问题与提议

| # | 问题 | 证据 | 提议 |
|---|---|---|---|
| G1 | 菜单底色是 `--surface` 的 **99.5%** 不透明，却仍做 `blur(32px) saturate(1.4)`：几乎看不到透出，却每帧付一次全量模糊的成本 | `v2-app.css:541`、`:307` | 二选一：菜单改实色并去掉模糊，或真用 `--glass-strong`。按 D18，Web 保留毛玻璃，需 owner 选 |
| G2 | 遮罩模糊 6 档（2/3/4/6/10/12px）没有规律 | 7.2 表 | 收为两档：`--scrim-blur: blur(8px)`（模态）、`--scrim-blur-soft: blur(3px)`（抽屉、面板内确认），与 C8 的 `--scrim` 一起改 |
| G3 | 玻璃强度 4 种（`--blur` 40px、32px、24px、20px），饱和度 1.08–1.8 | 7.2 表 | 定三级：`--glass-blur-chrome`（顶栏、输入框，=现 `--blur`）、`--glass-blur-menu`（24px saturate 1.4）、`--glass-blur-pill`（16px saturate 1.4）；桌面客户端的 24px 系列并入 menu 级 |
| G4 | 输入框的玻璃与"一个模糊叠一个模糊"的成本：Web 上 `.composer-row` 的 backdrop-filter 已被渐进模糊条取代 | `progressive-blur.css:28-30`（注释说明它曾单独打破悬停帧预算） | 保持；新增玻璃面时先查它下面是否已有模糊层 |
| G5 | `.composer-row` 的外观在 4 个文件里被依次覆盖：`conversation-workspace.css:14`（1px 线、28px 圆角）、`v2-foundations.css:125-130`（2px、20px）、`v2-app.css:279-285`（3px、`--r-lg`）、`progressive-blur.css:29` | 按 `main.tsx:28-46` 的导入顺序，`v2-app.css` 生效 | 合并为一处定义，迁移第一批里做 |

---

## 8. 动效令牌

### 8.1 时长与曲线

`design-tokens.css:95-97`，JS 镜像 `motion.ts:4-11`；用法说明见 `design-tokens.css:90-94` 与 `AGENTS.md` 的 "Web UI motion" 一节。

| 令牌 | 值 | JS | 用途（摘自 `design-tokens.css:91-94`） | 产品 CSS 引用 |
|---|---|---|---|---|
| `--m-instant` | 80ms | `MOTION.instant` | 悬停淡入淡出 | 2 |
| `--m-press` | 120ms | `MOTION.press` | 按下反馈 | 64 |
| `--m-quick` | 140ms | `MOTION.quick` | 开关、提示、淡入淡出 | 30 |
| `--m-base` | 220ms | `MOTION.base` | 到达、展开、滑动 | 58 |
| `--m-panel-in` | 280ms | `MOTION.panelIn` | 面板、抽屉、对话框、页面推入 | 77 |
| `--m-panel-out` | 180ms | `MOTION.panelOut` | 同上的离开（约短 35%） | 3 |
| `--m-flight` | 640ms | `MOTION.flight` | 仅发送飞行 | 9 |
| `--ease-out` | `cubic-bezier(.22,1,.36,1)` | `EASE.out` | 默认 | 180 |
| `--ease-settle` | `cubic-bezier(.2,.8,.2,1)` | `EASE.settle` | 离场 | 13 |
| `--ease-spring` | `cubic-bezier(.34,1.3,.64,1)` | `EASE.spring` | 唯一带回弹的，小物件用，不用于布局 | 12 |
| `--ease-inout` | `cubic-bezier(.45,0,.2,1)` | `EASE.inout` | 移动中的东西 | 19 |
| `--ease-in` | `cubic-bezier(.5,0,.75,0)` | `EASE.in` | 永久离开 | 1 |

### 8.2 其它动效参数

- 到达级联（D19）：`motion.ts:18-30` 的 `CASCADE`：错峰 15ms、单条 240ms、过冲 2.4–3.8px、最多 12 环、最大位移 130px；可在控制台用 `window.__TOFI_CASCADE` 临时调。
- 跟随底部滚动：`GLIDE_MS` 220、`GLIDE_STREAM_MS` 120，每帧最多 24px（来源 `AGENTS.md` "Web UI motion"；由 `test:arrival-glide` 守）。
- 弹簧：`ui/src/motion-lab/lib/spring.ts:7-14` 的 `SPRINGS`（toy 240/13、morph 320/22、settle 380/39），产品代码 `mailSendFlight.ts:2`、`BotAvatarPicker.tsx:7` 直接从 motion-lab 目录引用。
- 弹簧令牌（第 4 步新增，[D35](../decisions/0035-step4-motion-experiment-result.md)）：`ui/src/motion.ts` 的 `SPRING.push`（stiffness 900、damping 60、mass 1，临界阻尼），给手机右滑返回松手后的收尾用（`ui/src/phoneSwipeGesture.ts` 的 `springPath` 把它和手指的初速度一起采样成关键帧）。从一半处静止松手约 225 ms 到离终点 2 px 以内、约 280 ms 到 0.5 px 以内。只有 JS 版，没有 CSS 孪生（弹簧要带初速度，CSS 的时长和曲线带不了）。`test:motion-tokens` 守着：`SPRING.push` 必须存在且临界阻尼（`damping = 2·√(stiffness·mass)`，误差 0.5），`motion.ts` 以外的 TS 不许写 `stiffness`/`damping`/`mass` 数字字面值（`motion-lab/` 除外，和其他规则一样）。
- 右滑返回的手势常量（不是动效令牌，是手势规则）：`ui/src/phoneSwipeGesture.ts` 的 `SWIPE`：起手阈值 10 px、过半完成、快甩 0.5 px/ms、速度窗口 80 ms、橡皮筋最多 10% 宽度（常数 0.55）、列表位移 22%（和点按推入的 `styles.css` 一致）。
- 长循环动画（≥800ms，不受测试限制）：2.4s ×10（如 `v2-app.css:67`、`sidebar-pins.css:49`）、1.4s ×6、1.8s ×3、1.6s ×2、1s ×2、2.8s、2s、1.5s、1.3s、1.2s、1400ms、980ms、900ms ×2。
- 减弱动效：产品 CSS 中 `prefers-reduced-motion` 共 60 处。

### 8.3 动效问题与提议

| # | 问题 | 证据 | 提议 |
|---|---|---|---|
| M1 | `--ease-press` 名字是"曲线"，值却是时长 `var(--m-press)`，11 处都当时长用 | `design-tokens.css:32`；`terminal.css:4`、`computer-credentials.css:5`、`oauth/connection.css:33`、`owner-setup.css:23` | 调用点改 `--m-press`，删 `--ease-press` |
| M2 | `--m-panel-out` 只被引用 3 次，大多数面板离场没用更短的时长 | 8.1 表 | 迁移弹层时一起补上 |
| M3 | 产品依赖 `motion-lab/lib/` 下的 `spring.ts`、`hooks.ts`、`flip.ts`、`choreo.ts` 等 | `App.tsx:69-70`、`mailSendFlight.ts:1-2`、`TaskRunBlock.tsx:15`、`sendFlight.ts:1` | 把产品要用的移到 `ui/src/motion/`，`SPRINGS` 并入 `motion.ts`（第 4 步已在 `motion.ts` 建了 `SPRING`，新的弹簧放那里；旧的三个还没搬） |
| M4 | 遗留 9 处字面值都在 `mailSendFlight.ts` | `motion-tokens-baseline.json` | 改用 `MOTION`/`EASE` 后把上限降到 0 |
| M5 | 循环动画时长没有令牌 | 8.2 | 新增 `--m-loop-slow 2.4s`、`--m-loop 1.4s`（只给呼吸、脉冲这类循环用） |

---

## 9. z-index：层级刻度与每一处的归层

> 状态：**已落地**（迁移第 1 步，分支 `ds/step1-layers`；第 2 步菜单和弹出卡片改走 Radix、修 Z3/Z4，分支 `ds/step2-menus`；第 3 步对话框改走 Radix、修 Z7，分支 `ds/step3-dialogs`；三步在集成分支 `ds/integrate` 上合在一起；owner 的回答见 [D31](../decisions/0031-entry-budget-plan-a.md)–[D34](../decisions/0034-component-catalog-dev-only.md)）。本节的 `file:line` 按 `ds/integrate` 写；"迁移前"一列是 `748c774` 的值。守卫：`npm --prefix ui run test:layers`（`ui/scripts/test-layers.mjs`），菜单和卡片的层级另有 `test:overlays`，对话框另有 `test:dialogs`。

### 9.1 现状

产品 CSS 中 `z-index` 共 **90 处**：**47 处是全局层**，写成 `var(--z-*)` 或 `calc(var(--z-*) ± 1)`；**43 处是组件内部的局部值**（-1 到 12 的小整数）。第 1 步之后是 89 处（全局 46）；第 2 步删了顶栏抬高和行内的回执提示，加了"模态里打开的菜单"一条；第 3 步加了两个确认框的遮罩。TS 里只剩 1 处：发送飞行的行内样式写 `"var(--z-drag)"`（`ui/src/sendFlight.ts:20`）。motion-lab 页面另有 35 处，不在本表。

迁移前（`748c774`）是 89 处 CSS、3 处 TS，31 个不同的值（-1 到 1200，TS 里还有 2147483000）。

第 2 步起，菜单和弹出卡片（新建、顶栏"⋯"、侧栏右键、账户、附件、MCP、表情反应、身份卡）是 Radix 的内容：它们的 CSS 写 `position:relative` 和自己的 `--z-*`，Radix Popper 把这个 z-index 复制到包着它们的 `position:fixed` 外层（`[data-radix-popper-content-wrapper]`），外层 portal 在 `#tofi-layers`。从带 `aria-modal="true"` 的祖先（设置窗）里打开的，内容带 `data-layer="dialog"`，层级是 `calc(var(--z-dialog) + 1)`（`interaction-system.css:91`）。Radix 打开菜单时还会往 `<body>` 首尾各插一个不可见的焦点哨兵（`data-radix-focus-guard`，`position:fixed`、无 z-index），那是库的代码，不在 `test:layers` 静态检查的 `ui/src` 范围里。

局部值里有 14 处大于 3，是刻度出现之前的组件内部叠放，按文件记在 `ui/scripts/layers-baseline.json` 里，只降不升：`desktop-presence.css` 6、`mail-send-flight.css` 4、`styles.css` 2（手机推入那一对）、`v2-app.css` 1、`web-file-drop.css` 1。

所有弹层都 portal 到同一个根节点：`ui/index.html` 里 `#root` 后面的 `<div id="tofi-layers">`，由 `ui/src/layers.ts` 的 `layerRoot()` 取得（页面里没有就创建）。它是一个普通的 `div`，**不是层叠上下文**，所以里面每个弹层的 `--z-*` 和 `#root` 里的启动画面、顶栏、引导在同一个根层叠上下文里比大小。`createPortal(..., document.body)` 和往 `document.body` 里追加弹层元素都被 `test:layers` 拦下（追加离屏测量镜像、剪贴板降级输入框、下载链接这三处不是弹层，列在白名单里）。

还有一类**不受 z-index 管**的：原生 `<dialog>` 的 `showModal()` 进入浏览器顶层（top layer），盖在所有 z-index 之上。第 3 步之后剩 2 个：图片灯箱（`MessageAttachment.tsx:25,76`）、文件预览（`FileViewer.tsx:124`）。设置离开确认和电脑重启确认改成了 Radix `AlertDialog`（portal 到 `#tofi-layers`，`--z-dialog`）。原生 dialog 开着时，悬停提示和工作区 toast 渲染进最上层的那个 dialog 里面（`layers.ts` 的 `topLayerDialog()`、`InTopLayer`），所以仍在最上面。

### 9.2 层级问题

| # | 问题 | 状态 |
|---|---|---|
| Z1 | **toast 被模态盖住**：工作区错误 toast 是 80，低于只读会话 110、右键菜单 120、面板/表单 200–210、删除对话框 1000、引导 1200 | **第 1 步已修**：toast 是 `--z-toast` 90，在所有模态（80）、菜单（60）之上。原生 dialog 仍盖住它（Z7） |
| Z2 | 同值不同角色：1000 同时给了删除对话框、悬停提示、拖放遮罩、拖动残影、@提及拖动残影；200 给了两个模态，表单 sheet 只能用 210 压过它们 | **第 1 步已修**：每种角色一层；表单 sheet 是 `calc(var(--z-dialog) + 1)` |
| Z3 | 数值被父级层叠上下文封顶，写多大都没用：账户菜单在侧栏（≥768px）里面；输入框里的附件菜单、@菜单被输入框（`isolation:isolate`）封顶 | **第 2 步已修**：账户、附件菜单是 portal 进 `#tofi-layers` 的 Radix 菜单，@菜单 portal 进 `#tofi-layers`、`position:fixed`；`test:overlays` 检查它们上方只有自己那一层 |
| Z4 | 为了让顶栏菜单盖过浮卡，顶栏在 ≥1100px 被抬高：菜单没有走 portal，层级只能靠抬父级 | **第 2 步已修**：顶栏菜单 portal 出去，抬高删掉，顶栏回到 `--z-chrome`；`test:overlays` 检查 1280 px 下顶栏是 20、"⋯"菜单在浮卡之上 |
| Z5 | 设置窗（模态、带遮罩）只有 30，低于启动画面 60、toast 80、身份卡 90、右键菜单 120、权限提示 160 | **第 1 步已修**：设置窗是 `--z-dialog`（`settings-system.css:2`）。权限提示只出现在两处，都在别人的层叠上下文里：设置的电脑页（`ComputerPanel.tsx`，被设置窗包住）和输入框里（`App.tsx` 的 `Composer`，桌面客户端听写要麦克风时，被输入框的 `isolation:isolate` 包住） |
| Z6 | 魔法数字：发送飞行 2147483000；寄信飞行层在 CSS 和 TS 各写一次 120 | **第 1 步已修**：都是 `--z-drag`；寄信飞行的 TS 行内 z-index 删掉，信封翻盖改成加 `.is-front` 类（`mail-send-flight.css:15`） |
| Z7 | 原生 dialog 在顶层，提示和 toast 永远到不了它上面。目前这 4 个 dialog 里没有 `data-hint` 按钮，暂无可见故障 | **第 3 步已修**：两个确认框改成 Radix（`--z-dialog`，toast、提示、拖动层在它们之上）；剩下的灯箱和文件预览开着时，提示和 toast 渲染进 dialog 内部（`test:dialogs` 守着） |

### 9.3 层级刻度

原则：**全局层只给"脱离文档流、对整个视口定位"的东西**（`position:fixed` 或 portal 到 `#tofi-layers`）；组件内部的叠放用 `isolation:isolate`（或别的方式）建自己的层叠上下文，只用 -1..3 的小整数（下表记为"局部"），不占全局层。层与层间隔 10，留给同层内的特殊情况，写成 `calc(var(--z-*) ± 1)`。令牌在 `ui/src/design-tokens.css` 的 `:root`（`:99-104`），JS 镜像是 `ui/src/layers.ts` 的 `Z`。

| 令牌 | 值 | 放什么 | 说明 |
|---|---|---|---|
| `--z-base` | 0 | 普通文档流 | — |
| `--z-raised` | 1 | 表格粘性表头、登录页语言切换这类页面内略高一层的 | — |
| `--z-edge-blur` | 10 | 消息区底部渐进模糊条 | 必须在消息之上、输入框之下，所以排在 chrome **下面** |
| `--z-chrome` | 20 | 顶栏、输入框、侧栏列 | 侧栏列是 `+ 1`：收起侧栏时账户菜单会伸进对话区，必须在顶栏和输入框之上（`v2-app.css:20`） |
| `--z-pill` | 30 | 浮动胶囊：回到最新按钮、浮动电脑小窗 | D18：新消息胶囊不能被任何东西盖住，所以要高于 chrome 和模糊条 |
| `--z-panel` | 40 | 非模态面板：右侧详情抽屉、≥1100px 的上下文浮卡、相关对话卡片；抽屉遮罩是 `- 1` | macOS 桌面客户端的拖动条是 `+ 1`，见 9.4 |
| `--z-popover` | 50 | 锚定在某元素上的弹出内容：身份卡、@菜单（成员面板的 CSS 还在，已没有组件用它） | — |
| `--z-menu` | 60 | 菜单：更多、新建、右键、账户、表情反应、MCP、附件 | 在模态（设置窗）里打开的菜单和卡片是 `calc(var(--z-dialog) + 1)`（`data-layer="dialog"`） |
| `--z-cover` | 70 | 盖住整个应用但不拦模态的：启动画面 | 盖住 chrome、面板和菜单，模态（引导、设置窗）在它之上 |
| `--z-dialog` | 80 | 模态（遮罩 + 内容）：设置窗、全屏只读会话、删除确认、交接、VM 授权、表单 sheet（`+ 1`）、引导、离开确认、重启确认、放大的电脑 | 同层多个模态按 DOM 顺序叠：`#tofi-layers` 在 `#root` 后面，所以 portal 出去的对话框在设置窗之上；portal 出去的几个之间（第 3 步起也包括引导），后打开的在上 |
| `--z-toast` | 90 | toast、权限提示（右下角非阻塞通知） | 解决 Z1：出错提示永远在模态之上 |
| `--z-tooltip` | 100 | 悬停提示（第 2 步起群聊读者名单也是同一个提示） | 提示要在 toast 和模态之上才有用 |
| `--z-drag` | 110 | 拖动残影、拖放整屏遮罩、发送/寄信飞行动画 | 跟随指针或纯动画、`pointer-events:none`，必须在一切之上 |

原生 `<dialog>`（顶层）不在刻度里。第 3 步把两个确认框迁到 Radix（portal + `--z-dialog`）；剩下的灯箱和文件预览（各自有专门的缩放和焦点处理，没迁）开着时，提示和 toast 渲染在 dialog 内部（Z7）。

### 9.4 每一处 z-index 的归层

"局部"表示留在组件自己的层叠上下文里，不映射到全局层。`test:layers` 在 1280、1000、390 px 下检查：除了手机推入那一对，没有任何局部值落在根层叠上下文里。

**全局 / 视口级**（47 处 CSS + 1 处 TS；第 2 步删掉的两行留在表里，标"已删"）

| 位置 | 选择器 | 迁移前 | 定位 | 现在 |
|---|---|---|---|---|
| `file-preview.css:48` | `.fp-table th`（粘性表头） | 1 | sticky | `--z-raised` |
| `owner-setup.css:3` | `.auth-language`（登录页语言） | 2 | absolute | `--z-raised` |
| `progressive-blur.css:10` | `.edge-blur` | 3 | absolute | `--z-edge-blur` |
| `styles.css:57` | `.chat-header` | 2 | relative | `--z-chrome` |
| ~~`context-card.css:49`~~ | `.chat-header`（≥1100px，网页端） | 14 | relative | **第 2 步已删**：顶栏菜单走 portal，顶栏就是 `styles.css:57` 的 `--z-chrome`（Z4） |
| `styles.css:158` | `.composer` | 4 | relative | `--z-chrome` |
| `conversation-workspace.css:12` | `.workspace .composer` | 4 | absolute | `--z-chrome` |
| `v2-app.css:20` | `.contact-pane`（≥768px，侧栏列） | 5 | relative | `calc(var(--z-chrome) + 1)`：同层时顶栏和输入框在 DOM 里靠后，会压住伸出侧栏的账户菜单 |
| `desktop-system.css:9` | `.native-titlebar`（仅 macOS 桌面客户端） | 20 | fixed | `calc(var(--z-panel) + 1)`：拖动条原本在侧栏、抽屉和抽屉遮罩之上，保持不变 |
| `styles.css:153` | `.jump-latest` | 3（`.workspace` 下被 `progressive-blur.css:51` 覆盖成 6） | absolute | `--z-pill`（两处合并成这一处） |
| `desktop-presence.css:2` | `.desktop-floating-shell` | 25 | fixed | `--z-pill` |
| `styles.css:179` | `.detail-backdrop`（抽屉遮罩） | 9 | fixed | `calc(var(--z-panel) - 1)` |
| `styles.css:307` | `.detail-pane`（≤1120px 抽屉） | 10 | fixed | `--z-panel` |
| `styles.css:352` | `.detail-pane`（768–1279px 抽屉） | 10 | fixed | `--z-panel` |
| `context-card.css:13` | `.detail-pane.context-panel`、`.view-only-chat.is-card`（≥1100px 浮卡） | 12 | absolute | `--z-panel` |
| `context-card.css:30` | `.view-only-chat.is-card` | 40 | fixed | `--z-panel` |
| `styles.css:72` | `.member-panel` | 8 | absolute（在顶栏里） | `--z-popover` |
| `styles.css:167` | `.mention-menu` | 5 | 第 2 步起 fixed，portal 到 `#tofi-layers` | `--z-popover` |
| `interaction-system.css:177` | `.bot-identity-card` | 90 | 第 2 步起 Radix Popover（relative，外层 fixed），portal | `--z-popover` |
| `styles.css:30` | `.create-menu` | 8 | 第 2 步起 Radix 菜单（relative，外层 fixed），portal | `--z-menu` |
| `styles.css:71` | `.more-menu` | 8 | 同上 | `--z-menu` |
| `styles.css:141` | `.sidebar-context-menu` | 120 | 同上 | `--z-menu` |
| `v2-app.css:117` | `.sidebar-account-menu` | 150 | 同上（第 2 步以前被侧栏封顶，Z3） | `--z-menu` |
| `v2-app.css:307` | `.composer-add-menu` | 10 | 同上（第 2 步以前被输入框封顶，Z3） | `--z-menu` |
| `extensions-system.css:61` | `.mcp-menu` | 30 | 同上；在设置窗里打开，所以由下一行提到模态之上 | `--z-menu` |
| `interaction-system.css:91` | `#tofi-layers [data-layer="dialog"]`（第 2 步新增：从模态里打开的菜单和卡片） | — | Radix 外层 | `calc(var(--z-dialog) + 1)` |
| `message-reactions.css:6` | `.reaction-context-menu` | 150 | 第 2 步起 Radix Popover（relative，外层 fixed），portal | `--z-menu` |
| `boot-screen.css:2` | `.boot` | 60 | fixed | `--z-cover` |
| `settings-system.css:2` | `.detail-pane:has(.settings-tabs)`（设置窗） | 30 | fixed | `--z-dialog`（Z5） |
| `styles.css:142` | `.view-only-chat`（全屏） | 110 | fixed，portal | `--z-dialog` |
| `styles.css:249` | `.computer-detail.is-expanded` | 50 | fixed | `--z-dialog` |
| `styles.css:393` | `.detail-pane:has(.computer-detail.is-expanded)` | 20 | fixed | `--z-dialog` |
| `desktop-presence.css:28` | 放大的 `.computer-detail.is-expanded` | 35 | fixed（在电脑外壳里） | `--z-dialog` |
| `desktop-presence.css:104` | `.desktop-floating-shell.is-expanded`（Web 放大遮罩） | 40 | fixed | `--z-dialog` |
| `handover.css:1` | `.handover-overlay` | 200 | fixed，portal | `--z-dialog` |
| `extensions-system.css:101` | `.mcp-oauth-overlay` | 200 | fixed，portal | `--z-dialog` |
| `settings/settings-components.css:144` | `.sheet-overlay`（设置里的表单） | 210 | fixed，portal | `calc(var(--z-dialog) + 1)` |
| `interaction-system.css:43` | `.delete-dialog-overlay`（删除确认、重新指派主助理） | 1000 | fixed，portal | `--z-dialog` |
| `onboarding.css:5` | `.onb-scrim` | 1200 | fixed（第 3 步起 portal 到 `#tofi-layers`） | `--z-dialog` |
| `settings-system.css:70` | `.settings-leave-overlay`（离开前保存确认，第 3 步新增；原来是原生 dialog 的 `::backdrop`） | 顶层 | fixed，portal | `--z-dialog` |
| `computer-resources.css:7` | `.resource-restart-overlay`（电脑重启确认，同上） | 顶层 | fixed，portal | `--z-dialog` |
| `styles.css:448` | `.workspace-toast` | 80 | fixed | `--z-toast` |
| `permission-coach.css:1` | `.permission-coach` | 160 | fixed（在设置窗或输入框里，见 Z5） | `--z-toast` |
| ~~`send-receipt.css:10`~~ | `.send-receipt-tip` | 5 | absolute（消息行内） | **第 2 步已删**：名单是已读标记的 `data-hint`，由下一行的 `.action-hint` 显示 |
| `interaction-system.css:94` | `.action-hint` | 1000 | fixed，portal | `--z-tooltip` |
| `web-mention.css:10` | `.web-mention-ghost` | 1000 | fixed，追加到 `#tofi-layers` | `--z-drag` |
| `web-file-drop.css:2` | `.web-drop-overlay` | 1000 | fixed，portal | `--z-drag` |
| `web-file-drop.css:23` | `.web-drop-ghost` | 1000 | fixed，追加到 `#tofi-layers` | `--z-drag` |
| `mail-send-flight.css:1` | `.mail-flight-layer` | 120（`mailSendFlight.ts:16` 行内样式又写一次） | fixed，追加到 `#tofi-layers` | `--z-drag`（TS 里那份删掉） |
| `sendFlight.ts:20` | 发送飞行（行内样式） | 2147483000 | fixed，追加到 `#tofi-layers` | `"var(--z-drag)"` |

**局部（组件内部，保留小整数）**（43 处）

| 位置 | 选择器 | 值 | 所在组件 |
|---|---|---|---|
| `conversation-workspace.css:13` | `.composer::before` | -1 | 输入框 |
| `v2-app.css:280` | `.composer-row` | 3 | 输入框 |
| `v2-app.css:297` | `.composer-add-wrap` | 5 | 输入框 |
| `v2-app.css:360` | `.composer-rich-text` | 0 | 输入框（提及高亮层） |
| `v2-app.css:363` | `.has-mentions textarea` | 1 | 输入框 |
| `v2-app.css:366` | `.composer > .working-members` | 2 | 输入框 |
| `v2-app.css:372` | `.composer-perch`（输入框上方的猫） | 2 | 输入框 |
| `web-file-drop.css:30` | `.web-upload-ring` | 4 | 输入框 |
| `web-mention.css:3` | `.mention-menu > button[role=option]` | 1 | @菜单 |
| `web-mention.css:7` | `.web-mention-pill` | 0 | @菜单 |
| `progressive-blur.css:40` | `.chat-header::before/::after` | -1 | 顶栏（`isolation:isolate`，`progressive-blur.css:37`） |
| `styles.css:317` | `.contact-pane`（≤767px） | 6 | 手机推入：列表与会话一对。父级 `.workspace-grid` 不是层叠上下文，这一对在根层叠上下文里，`test:layers` 把它列为唯一的例外 |
| `styles.css:318` | `.contact-pane::after`（推入时的压暗） | 2 | 同上 |
| `styles.css:322` | `.chat-pane`（≤767px） | 7 | 同上 |
| `sidebar-pins.css` | 拖动中的会话行、落下去飞行中的行/格（`.is-dnd-landing`） | `--z-drag` | 侧栏列表（原来是 40，被侧栏里的落点和后面的行压住；现在写 `var(--z-drag)`，落点是 `--z-raised`，以前那条"保持 40"作废） |
| `sidebar-pins.css` | `.pin-frame`（置顶区的框，占文档流） | 无 | 侧栏列表（原来 `.pin-dropzone-anchor` 是盖在搜索区上的绝对定位，30；框不需要层级，上面不许有东西） |
| `settings-system.css:17` | `.settings-rail-indicator` | 0 | 设置导航 |
| `settings-system.css:21` | 设置导航按钮 | 1 | 设置导航 |
| `terminal.css:11` | `.terminal-confirm` | 2 | 终端面板内确认 |
| `team-board.css:47` | `.team-board-create/-detail` | 2 | 团队看板 |
| `bot-avatar-picker-motion.css:7` | `.web-avatar-paint` | 2 | 头像选择器 |
| `message-attachment.css:72` | 图片图注与操作 | 2 | 图片附件 |
| `desktop-presence.css:10` | `.desktop-preview-controls` | 8 | 电脑预览 |
| `desktop-presence.css:37` | `.desktop-presence-actor` | 4 | 电脑预览 |
| `desktop-presence.css:52` | `.desktop-pointer-overlay` | 3 | 电脑预览 |
| `desktop-presence.css:117` | `.computer-floating-heading` | 10 | 电脑预览 |
| `desktop-presence.css:166` | `.desktop-chin` | 3 | 电脑预览 |
| `desktop-presence.css:168` | `.desktop-drop` | -1 | 电脑预览 |
| `desktop-presence.css:187` | `.desktop-driver-side` | 1 | 电脑预览 |
| `desktop-presence.css:194` | `.desktop-expanded-scrim` | -1 | 电脑预览 |
| `desktop-presence.css:197` | 状态胶囊 | 6 | 电脑预览 |
| `desktop-presence.css:206` | `.desktop-frame-status` | 9 | 电脑预览 |
| `desktop-presence.css:222` | `.desktop-window-controls` | 12 | 电脑预览 |
| `mail-send-flight.css:4` | `.mail-flight-panel-0` | 3 | 寄信动画 |
| `mail-send-flight.css:5` | `.mail-flight-panel-2` | 2 | 寄信动画 |
| `mail-send-flight.css:7` | `.mail-flight-envelope-back` | 1 | 寄信动画 |
| `mail-send-flight.css:8` | `.mail-flight-envelope-insert` | 2 | 寄信动画 |
| `mail-send-flight.css:9` | `.mail-flight-envelope-pocket` | 3 | 寄信动画 |
| `mail-send-flight.css:13` | `.mail-flight-envelope-flap` | 0 | 寄信动画 |
| `mail-send-flight.css:15` | `.mail-flight-envelope-flap.is-front`（翻盖合上前加的类；原来是 `mailSendFlight.ts:62` 的行内样式） | 4 | 寄信动画 |
| `mail-send-flight.css:17` | `.mail-flight-envelope-stamp` | 5 | 寄信动画 |
| `mail-send-flight.css:20` | `.mail-flight-envelope-postmark` | 6 | 寄信动画 |
| `mail-send-flight.css:22` | `.mail-flight-envelope-address` | 5 | 寄信动画 |

合计：CSS 90 处（全局 47 + 局部 43）、TS 1 处（全局），全部已归层。

电脑预览内部的值（最高 12）可以压到 0–3，但这一块正在频繁改动（D11），建议排在后面单独做；寄信动画的信封有 8 层，压到 -1..3 要改结构。两处都在 `layers-baseline.json` 的上限里。

一个容易看错的事实：`.workspace .chat-pane` 的 `container-type:inline-size`（`conversation-workspace.css:64`）**不**建立层叠上下文（在 Chromium 和 WebKit 里实测，容器里 z-index 100 的子元素会盖过容器外 z-index 1 的兄弟），所以宽屏下顶栏、输入框、新消息胶囊、回执提示都和侧栏、抽屉、浮卡在同一个根层叠上下文里比大小。手机宽度下 `.chat-pane` 有 `z-index:7`，才是层叠上下文。

### 9.5 守卫：`test:layers`

`npm --prefix ui run test:layers`（`ui/scripts/test-layers.mjs`，浏览器部分要 `PLAYWRIGHT_MODULE`，没设时直接报错退出，不会 SKIP）：

1. **静态**：`design-tokens.css` 里 13 个 `--z-*` 各定义一次、值与 9.3 一致，`layers.ts` 的 `Z` 与之相同；产品 CSS 的 `z-index` 只能是 `var(--z-*)`、`calc(var(--z-*) ± 1)` 或 -1..3（更大的旧局部值按文件不超过 `layers-baseline.json`，只降不升，`-- --update-baseline` 收紧）；TS/TSX 里不许写数字 z-index；`index.html` 在 `#root` 后面有 `#tofi-layers`；没有 `createPortal(..., document.body)`，往 `document.body` 追加元素只允许白名单里的三处非弹层。
2. **浏览器**（Chromium、生产构建、合成数据）：`#tofi-layers` 紧跟 `#root` 且不是层叠上下文；真实的悬停提示 portal 进 `#tofi-layers`，并且在它中心处是最上层（下面垫一个 toast）；把各层的真实类名叠在输入框中心的同一点上，`elementsFromPoint` 的顺序必须是：拖动残影 > 悬停提示 > toast > 表单 sheet > （删除确认 / 设置窗 / 引导 / 离开确认 / 重启确认）> 启动画面 > 右键菜单 > 身份卡 > 相关对话卡片 > 电脑小窗 > 输入框 > 底部模糊条；1280 / 1000 / 390 px 下（新消息胶囊也在屏幕上时）没有局部值落在根层叠上下文里（手机推入那一对除外）。
3. 把第 1 步的产品改动撤掉，这个测试会失败（D30）；撤回时的输出见 `migration.md` 第 1 步。

每次改层级还要在预览里看一遍 `migration.md` 第 1 步"owner 预览看哪里"的清单（D22）。

---

## 10. 复现本文数字

在仓库根目录执行（只读）：

```bash
# 产品 CSS 文件清单（67 个）
find ui/src -name '*.css' -not -path '*/motion-lab/*' -not -path '*/acceptance/*' | sort
# 全部 z-index（第 3 步之后 CSS 90 处 + TS 1 处）
grep -rn 'z-index' ui/src --include='*.css' | grep -v '/motion-lab/'
grep -rn 'zIndex' ui/src --include='*.ts' --include='*.tsx' | grep -v '/motion-lab/'
# 某个令牌的使用处，例如 --glass
grep -rnE 'var\(--glass[,)]' ui/src --include='*.css' | grep -v '/motion-lab/'
# 守卫
npm --prefix ui run test:final-v2-tokens; echo "exit=$?"
npm --prefix ui run test:motion-tokens;   echo "exit=$?"
PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm --prefix ui run test:layers; echo "exit=$?"
```

字号、间距、圆角、阴影的频次表用的是同样的正则（按属性名匹配 `font:`、`font-size:`、`padding|margin|gap:`、`border-radius:`、`box-shadow:`、`backdrop-filter:` 后统计取值），复现时数字可能因格式写法差几个，量级与排序不变。

## 11. 待确认

- `--gate-clear` 是否由桌面客户端注入（`desktop-system.css:27`，仓库内找不到定义）。
- `--hard-ink` 未定义导致寄信动画丢失硬阴影：由 CSS 规则推断，未在浏览器里看过。
- C9（深色悬停阴影变奶白）、C8（深色遮罩变亮）是不是刻意的设计，需要 owner 在预览里看。
- 头像库 `ui/src/lib/tofi-avatar/` 的调色板与 `ServiceMark.tsx` 的品牌色要不要纳入令牌。
- 本文不覆盖 Electron 与 iOS 客户端的令牌（D18 的渐进模糊在那两端是默认），它们若有自己的令牌文件，应另行对照。
