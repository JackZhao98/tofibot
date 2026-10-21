# 0002 界面改动要量交互开销，预算由测试守住

> 编号 D02，索引见 [README](README.md)。

## 决定

改界面时要实际量交互的开销（每次按键、每一帧花多少脚本时间、触发多少次排版），并把预算写成会失败的测试，例如 `npm --prefix ui run test:composer-typing`。高频变化的状态放在组件局部，不放进整个工作区的状态里。

## 日期

2026-10-15（Pacific）

## 谁拍板

owner（jackzhao）。起因是 owner 当天反馈"打字特别卡"（`docs/agent-plan/wave-notes.md:94`）。

## 背景

- 生产环境实测：在一个群聊里每按一个键，脚本耗时 37.5 ms，排版 66 次（`docs/agent-plan/wave-notes.md:94`）。
- 原因：草稿文字存在 App 的状态里，每次按键都让整个工作区（包括消息列表）重新渲染；轮询和头像动画又额外强制排版（提交 `ef6a51c` 说明）。
- 这类问题在开发机上不明显，只有在长对话、真实数据上才暴露；不量就不知道。

## 理由

- 卡顿是用户能直接感觉到的缺陷，必须用数字说话，不能凭感觉说"应该好了"。
- 写成测试后，以后的改动一旦让开销回升，测试会失败，不靠人记得。

## 影响

- 修复：输入框自己保存正在输入的文字，停手 400 ms 后（以及失焦、发送、切会话、页面隐藏时）再同步给 App；轮询结果不变时保留旧对象；头像眼睛的包围盒改为计算，不再每帧调 `getBBox`（提交 `ef6a51c`）。
- 结果：150 条消息的群里，减少动画模式下每键 68.6 ms 降到 0.44 ms，有动画时 72.5 ms 降到 2.07 ms（`docs/agent-plan/wave-notes.md:94`）。
- 预算（`ui/scripts/test-composer-typing.mjs:156-166`）：每键脚本时间不超过 4 ms，每键排版不超过 2 次，按键不得让消息列表发生变动。
- 同一做法推广到其他界面：时间线、切会话、外壳体积、侧栏拖拽、头像帧、到达动画都各有预算脚本（见下表）。新的界面改动应先找对应的预算测试，没有就补一个。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 打字预算测试 | `ui/scripts/test-composer-typing.mjs`（`npm --prefix ui run test:composer-typing`，`ui/package.json:46`） |
| 实现提交 | `ef6a51c` ui: make typing in the composer independent of conversation length（10-15 21:19）；合并 `9920ae4` fix/composer-typing-lag |
| 其他预算脚本（`ui/package.json`） | `test:stream-cost`、`test:switch-budget`、`test:chat-switch`、`test:timeline-rows`、`test:perf-structure`、`test:shell-budget`（提交 `3e2de40`）、`test:sidebar-pins`、`test:avatar-frame-budget`、`test:arrival-glide` |
| 规则说明 | `AGENTS.md` 的 "Web timeline performance"、"Sidebar order and pins"、"Web UI motion" 三节列出了各项预算数值 |
| 记录 | `docs/agent-plan/wave-notes.md:94`（Next wave 第 8 条） |
