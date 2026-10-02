# Motion Lab → 生产 UI 接入交接（给 Codex）

## 2026-09-29 邮件能力更新

Web 已接入只读来信显示、从来信请求 Bot 起草回复、`prepare_email` 持久草稿、可编辑信纸、逐封审批和 read-send Gmail 发送。`demo:true` 生成不能联系 Gmail 的演示卡。`#approval` 现展示同一生产邮件卡，可切换私信／群聊并操作演示审批：私信无头像姓名且填满消息列，群聊显示身份。确认发送后信纸三折、入信封、封口、贴猫邮票、盖戳飞出，最后收成完成行；本地 Lab 已检查中间帧和结果。源码和 App/Runner 配套镜像已于 2026-09-30 发布；真实个人 Gmail OAuth 未完成，不能声称真实收发验收。

仍有功能缺口：收件箱摘要、归档、稍后提醒、标签和 Gmail 原线程回复尚无完整数据与动作接口。下面的旧交接表是当时快照，邮件状态以本节为准。

Motion Lab 是一个独立的展示页：`ui/motion-lab.html`，源码在 `ui/src/motion-lab/`。它复用生产环境的 `design-tokens.css`、`lib/tofi-avatar`（`mountCat`）、`icons`（`TofiIcon`）和 `GazeAvatar`；新增的 `ApprovalCard`、`ScheduledRun` 直接与 Web 共用组件。本地运行 `npm --prefix ui run dev`，打开 `/motion-lab.html` 就能看到。构建时它由 `ui/vite.motion-lab.config.ts` 单独打包。

## 2026-09-29 新增组件与能力

以用户补充的 ApprovalCard、ScheduledRun 截图为准；本轮 ZIP 的组件源码与已入库版本相同，不用旧 token JSON 覆盖当前主题。

| 组件 | Web 实现 | Lab 展示 | 功能缺口 |
|---|---|---|---|
| Question Card / Other Answer | 工具可设 `allow_other:true`；单选、是／否可用手动回答，多选可组合选项与手动回答；提交失败保留输入，历史回显文本 | `#question` 有“允许其他回答”开关、原生输入和回答收起效果 | 无新增字段缺口；真实模型选择何时开启仍需日常观察 |
| ApprovalCard | 普通是／否保留“需要你回答”；独立 `request_approval` 卡持久化动作／对象／影响、自定义按钮和同会话草稿预览链接；MCP 工具审批可附带有上限的完整参数纯文本，默认折叠、主动展开；答复后收起 | `#approval` 共用组件，展示邮件、重启和长参数三种审批信息以及拒绝、确认、查看草稿 | 审批卡只记录决定并恢复 Bot，不自动执行操作；完整参数超过上限或含明确私密字段时拒绝创建卡片；真实模型发起和继续执行仍需上线观察 |
| ScheduledRun | 蜂蜜色时间票根、虚线接缝、展开任务原文、真实本轮状态／计时／重试反馈；新计划可存独立标题和创建来源，触发时保存频率、时区与执行序号 | `#scheduled-run` 共用组件，展示完成、运行中、失败三态 | 旧触发记录没有快照时不编造；仍需真实计划触发后的浏览器验收 |

`ask_user_question` 的 Other 默认为关闭，只有选择型问题允许开启。提交形状为 `{values:[],other_text:"…"}`；多选可保留真实选项 ID，Other 算一项。文本必须非空且不超过 4,000 个 Unicode 字符，服务器也检查开关、数量和 ID。普通回答的 boolean/string/array 格式保留。手动回答不能自动解释成同意。

定时任务按 exact occurrence 取计划时间和状态；只有确认结果写入当前对话才显示完成绿点。当前计划的频率／创建时间只读取一次会话级列表，缺失时退回触发记录；不拿别轮或委派根运行的时间冒充完成时间。Lab 数据明确标记为模拟，不发送邮件或重启服务。

## 必须遵守的规则（用户明确提过）

1. **按用户当前授权区分视觉与功能。** 原始交接是视觉范围；用户后来允许配合 UI 完善功能，并明确授权 Other Answer。本轮允许对应接口与存储扩展，其他缺失能力列入 To Do。用户明确修改优先于设计文件中的旧范围。
2. **发送消息改用表演版。** 用户于 2026-09-29 明确撤回旧的克制版要求：确认发送后，气泡从输入框沿弧线飞向对话右侧，约 640ms；Lab 和 Web 默认都用此版本。克制版仅保留为 Lab 对照。减少动态效果时直接显示已发送消息。
3. **输入框保持原生。** 不要用透明文字加叠加层去替换原生的圆点、光标和选区。之前做的爪印版密码框因为选区对不上，已经删掉了。
4. **减少动态效果。** CSS 部分由 `interaction-system.css` 的全局规则兜底；用 JS 或 WAAPI 驱动的动画必须自己判断 `prefers-reduced-motion`，参考 `lib/hooks.ts` 里的 `prefersReducedMotion`。

## 可复用的底层（从 lab 移到 `ui/src/` 的共享位置即可）

- `lib/spring.ts`：弹簧求解器，`springEasing()` 能把弹簧烘焙成 CSS `linear()` 缓动；有测试 `npm run test:motion-lab`。
- `lib/flip.ts`：列表 FLIP，`flipList(container, update, …)`，靠 `data-flip` 匹配元素。
- `lib/CopyFeedbackIcon.tsx`、`lib/LoadingCat.tsx`、`lib/WakeableCat.tsx`，样式在 `lib/primitives.css`。

## 演示 → 生产代码对照（逐条接入）

| Lab 演示 | 文件 | 接入位置 |
|---|---|---|
| 状态位变形 | `stations/StatusMorph.tsx` | `App.tsx` 的 `conversationRow`（`.unread-badge` / `.conversation-working-indicator`） |
| 工具步骤折叠 | `stations/ToolSteps.tsx` | `App.tsx` 的 `MessageBubble` 工具行、`toolTimeline.ts` |
| 发送（默认表演版弧线飞行） | `stations/SendFlight.tsx` | `App.tsx` 的 `Composer`、`sendFlight.ts` 和消息列表 |
| 问题卡 | `demos/chat/QuestionDemo.tsx` | `QuestionCard.tsx`、`question-card.css` |
| 审批卡 | `demos/chat/ApprovalDemo.tsx` | 共享 `ApprovalCard.tsx`；普通二选一保留问答语义 |
| @ 菜单 | `demos/chat/MentionDemo.tsx` | `App.tsx` 里 `Composer` 的 mention 逻辑 |
| 信纸 / 寄信 | `stations/LetterSeal.tsx` | `DisplayCard.tsx`（邮件卡片）；拟稿确认组件在生产环境还不存在，先问 |
| 复制成功 | `demos/chat/CopyDemo.tsx` | `DisplayCard`、`MessageAttachment`、`BotInspector`、`MCPSettings`、`ComputerCredentials` |
| 拖文件 | `demos/chat/DropDemo.tsx` | `Composer` 附件区域；目前没有拖放功能，属于新功能，先问 |
| 图片放大 | `demos/chat/ZoomDemo.tsx` | `MessageAttachment.tsx` 的 lightbox `<dialog>` |
| 私密输入 | `demos/chat/SecretDemo.tsx` | `SecretInputCard.tsx` |
| 定时任务票根 | `demos/chat/ScheduleRowDemo.tsx` | `ScheduledTaskRow.tsx` → 共享 `ScheduledRun.tsx` |
| 录音波形 | `demos/chat/MicDemo.tsx` | `useDictation.ts` / `DictationControls` |
| 侧栏重排 / 折叠 / 新 Bot / 连接状态 | `demos/sidebar/SidebarDemo.tsx` | `App.tsx` 侧栏、`sidebarCollapsed`、`SidebarAccount` |
| 头像 → 面板大猫、换毛色、换身形 | `demos/bot/BotPanelDemo.tsx` | `App.tsx` 的 `BotPanel`、`BotAvatarPicker.tsx` |
| 按住删除 | `demos/bot/HoldDeleteDemo.tsx` | `DeleteConversationDialog.tsx`（交互方式会变，先确认） |
| ticket 接力 | `stations/HandoffRelay.tsx` | `TeamBoard.tsx` / `WorkPanel.tsx`（设计稿里的未来提案） |
| 悬浮电脑开机、指针、接管 | `demos/computer/DesktopDemo.tsx` | `FloatingDesktop.tsx`、`desktop-presence.css`、`RemoteDesktopControl.tsx` |
| 设备配对 | `demos/computer/PairingDemo.tsx` | Web `ComputerPanel.tsx` 查询一次性配对记录，确认该码关联的设备后才显示实线与勾；真实 Mac 配对验收待做 |
| 权限引导 | `demos/computer/PermissionDemo.tsx` | `PermissionCoach.tsx` |
| MCP 连接测试 | `demos/settings/McpDemo.tsx` | `MCPSettings.tsx` |
| 账户授权 | `demos/settings/OAuthDemo.tsx` | `VMOAuthDialog.tsx`、`ModelSettings.tsx`（Codex device flow） |
| 保存栏 | `demos/settings/SaveBarDemo.tsx` | `SettingsShell.tsx` |
| 用量 | `demos/settings/UsageDemo.tsx` | `UsagePanel.tsx` |
| 空状态 / 加载 | `demos/global/EmptyDemo.tsx`、`LoadingDemo.tsx` | `App.tsx` 的 `empty-workspace`、`conversation-empty`、`.spinner` + `DelayedFeedback` |
| 主题圆形揭幕 | `ThemeToggle.tsx` | 应用里切换主题的地方 |
| Hero 玩具台、图标波浪 | `hero/`、`stations/IconRipple.tsx` | 只用于展示，不接入 |

## 未解决的问题：主题揭幕中途还是会停一下

- 现状：View Transition 加上 `::view-transition-new(root)` 的 `clip-path` 圆形动画，`ease-out`，500ms。切换期间会给根元素加 `data-theme-switching`，暂停所有 CSS 过渡和动画（`motion-lab.css`）。实测揭幕期间的 CSS 过渡已经从 402 个降到 0 个，主线程每帧稳定在 17ms，但用户仍然看到中途有停顿。
- 下一步排查方向：
  1. 在 Chrome DevTools 的 Performance 面板里录一次，看 GPU 和光栅化线程。
  2. 页面上还有 JS 驱动的猫（`mountCat`）和 canvas 在动，它们会让实时快照每帧都重画。
  3. `clip-path` 动画在 Chromium 里可能没有走合成器。可以改成一个纯色圆形遮罩层，只用 `transform: scale` 动画，缩放完成后再切换主题并淡出遮罩，这一步完全在合成器上执行。

## 验证

`npm --prefix ui run build`、`npm --prefix ui run test:motion-lab`，然后在浏览器里分别检查浅色、深色、375px 宽度下的效果，并用 DevTools 模拟「减少动态效果」。
