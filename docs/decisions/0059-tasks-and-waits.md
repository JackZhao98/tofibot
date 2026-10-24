# 0059 任务和等待：一个任务对象、一套等待、一个收件箱

> 编号 D59，索引见 [README](README.md)。设计过程见 [collab-todo.md](../design/collab-todo.md) 的 C1、C2、C3 三节，实施方案见 [collab-implementation-plan.md](../design/collab-implementation-plan.md) 阶段 2。

## 决定

1. **任务就是 `work_items` 的一行（Q2）。** 看板条目加任务列：`assigner_kind`（`user`/`bot`/`self`）、`assigner_bot_id`、`root_task_id`、`parent_task_id`、`origin_run_id`、`assignee_run_id`、`work_conversation_id`、`spec_version`、`result_message_id`、`result_based_on_version`、`result_summary`、`depends_on`（存着，阶段 5b 才用）、`stall_count`、`message_count`、`closed_at`。状态 CHECK 加 `input_required`（持有人问了派活人）和 `failed`，照 `work_items.go` 原有写法在事务里重建表；旧条目在迁移里不记派活人（`assigner_kind` 留空，旧表没有记录创建者的列；留空按“人”处理，权限检查不把 Bot 当创建者）。人在看板上建的条目是 `user`。
2. **三套等待并成一张表 `waits`。** 种类 `bot_post`（等某成员下一次发言，原 `respond follow_up`）、`task_result`（等任务结束）、`human_answer`（等人回答，镜像问人卡片，卡片的续跑机制不变）、`command_exit`（表里有，阶段 3 才能登记）、`timer`。同一 `group_id` 的等待成组，模式 `any`（第一个到齐的成员就结束，其余作废）或 `all`（全部结束才算）。一个组只交付一次，交付就是一行收件箱 `wait_result`；全部被取消的组不交付。表 `run_waits` 保留不再写。
3. **等待是异步调用。** `wait_for` 立刻返回回执（`wait_id`、`group_id`、`ends_at`），Bot 说完该说的就结束这一轮；事件到了或到点了，作为结果叫醒它。**超时也是结果**：到点的等待状态是 `timed_out`（计时器是 `fulfilled`），结果正文写明超时，由 Bot 判断。本阶段叫醒一律是“结束这一轮，之后开新 run”（Q5 的“短等待挂起同一 run”属阶段 4）。
4. **默认期限按种类（Q19）**：等 Bot 发言 15 分钟、等任务结果 2 小时、等人回答 30 分钟（问人卡片自己的到期为准）、等命令 24 小时（必须配计时器，阶段 3）；`wait_for` 的 `seconds` 可改，计时器 5 秒到 7 天。每个 Bot 同时最多 16 个未结束的等待；一个 Bot 从自己的等待连续叫醒自己满 24 次，再登记就被拒（`wait_chain_limit`），防止空转。
5. **登记等待时查等待环。** 在登记同一个短事务里，从新等待指向的成员出发，沿其他未结束的 `bot_post`/`task_result` 等待走（至多 64 步），走回登记者就拒绝，稳定码 `wait_cycle`；图大到走不完也拒绝（走不完就排除不了环）。委派自己登记的“等结果”和问人镜像不查（它们记录的是已经发生的调用，不是请求系统挂住 Bot）。
6. **一个 15 秒的扫描器**（启动时先扫一遍）：到期的等待结束，对应问人卡片已结束的镜像等待结束，状态已是 `failed`/`cancelled` 却没走到任务钩子的任务补上，然后唤醒收件箱里要求叫醒的 Bot。所有状态在表里，重启不丢；没有进程内定时器。
7. **收件箱 `bot_inbox`。** 种类 `user_message`、`task_update`、`task_question`、`task_result`、`wait_result`、`progress`、`reaction`，带 `wakes`、`needs_reply`、`priority`、`consumed_by_run`。要叫醒的行：Bot 在该会话里空闲就开一个 `group_followup` 的新 run（`run_wakes.source='inbox'`，请求正文就是这些行），忙就等它结束（run 结束时也会再扫）；Bot 自己启动的 run 会在上下文里读到未读的行（`[task updates]`），不会再多叫醒一次。一对群里的单个 `bot_post` 等待仍走原来的“提及唤醒”（`addMentionWake`），所以原有的去重、上限和 `wake source` 校验不变。**Q6**：问人卡片还开着时人又发了消息，本阶段只存一行 `user_message`（已记为被新 run 读过），行为不变（新消息取代卡片）；保留卡片是阶段 4。
8. **委派就是建任务。** `AddHandoff`、`AddForwardHandoff`、`AddBotMessages` 在建子 run 的同一事务里建任务行和派活人持有的 `task_result` 等待（结果仍由原来的回传 run 交付，所以这条等待静默结束；到期没结束则照超时交付）。工具统一成 `delegate`（一人走 handoff 路径，多人走 message 路径，可带 `title` 和 `checklist`）；`handoff`、`message` 一个版本内保留为别名（可执行，不在工具列表里）。任务跟着 run 走：子 run 开始 → `in_progress`；成功 → `done`（清单还有未结束的项则 `review`；Bot 声明 `blocked` 或持有人问过话则保持 `blocked`/`input_required`）；失败 → `failed`；被停 → `cancelled`；重试 run 接手原任务；被新消息接管的 run 把任务交给后继 run。
9. **清单对账（C3-3）。** `task_checklist` 只有派活人能增删条目、改请求；持有人只能改状态（`open`/`done`/`skipped`/`blocked`）和原因，每个结束的项必须写原因。持有人 `say(outcome=done)` 时清单上还有 `open` 的项，退回一次——和 `pending` 退回**共用每个 run 一次的名额**，谁先用谁。`say` 多一个可选 `task_id`：把这条回答作为那个任务的结果提交。
10. **`pending` 要有真实的等待（C1-8）。** `outcome=pending`（以及没有 wake/handoff 的 `delegated`）本来要退回；现在这个 run 有未结束的等待就不退回（原来只豁免“做过定时”）。
11. **停止连带（C1-7、12）。** `CancelRunTree` 在取消 run 之后，取消这些 run 给出和持有的任务、任务之下所有未结束的任务、挂在它们上的等待，不分私聊和群。删除 Bot 在删除的同一事务里取消它派出的任务（及其子树里在跑的 run）、它的等待，别的 Bot 等它发言的等待被提前到期（由超时叫醒它们）；它自己持有的任务的 run 仍照旧挡住删除。从群里移出成员同理（只限那个群）。UI 的确认框先取 `GET /api/bots/{id}/task-impact?group_id=` 显示“还有 N 件任务会一起取消”。
12. **用户改需求要有版本（C1-13、Q15）。** 人发给持有任务的 Bot 的消息，系统自动把任务的 `spec_version` 加一、记一条 `task_revisions`，并给派活人和持有人各存一行 `task_update`（不叫醒，下次上下文里读到）。派活人用 `task_update` 改请求同样加版本，并叫醒持有人。结果带 `result_based_on_version`（run 开始时的版本）；比当前版本旧，就给派活人存一行 `task_result` 写明“基于第几版”，由它决定沿用还是重做。任务结束后再改则开新任务（终态不可变）。
13. **`task_ask`（C1-3）。** 持有人问派活人：任务变 `input_required`，问题在发问的 run 结束后叫醒派活人；派活人用 `task_update` 的 `answer` 回答，任务回到 `in_progress` 并叫醒持有人。任务树消息上限 60（Q18），超过拒绝 `task_message_limit`。（3 轮停滞上报是阶段 5b，`stall_count` 列先留着。）
14. **保留期（Q1）。** 每日一次清理，默认 30 天，Settings › Automatic behavior 的 `task_retention_days`（0 = 不清）。到期的**已结束委派任务**清掉请求正文、清单、版本记录，行本身留着，标题和一行结果还在；已结束的等待和已读的收件箱行删除。**不碰消息、附件、run，也就不碰 D38 的失败 run 文件。**
15. **等待中的说明不占 D50 名额。** `say(message)` 每个 run 最多 3 条（D50）；run 一旦登记了等待，从那一刻起的消息单独算 3 条，所以说明“我在等什么”不会用掉开工前那几条。
16. **上下文。** 每轮有等待的 Bot 多一块 `[waiting on]`（至多 1600 字符，每个等待一行，含到期时间）；持有任务的 run 多一块 `[task record]`（版本、派活人、清单）；有未读行的多一块 `[task updates]`；被收件箱叫醒的 run 的请求块是 `[wait result]`。“查看上下文”里这四块各有一个类型和七种语言的名字。
17. **工具文字（英文、只写原则、STE）。** 不放例句、不放关键词表。声明在工具列表里的新工具只有 `delegate` 和 `wait_for`；`cancel_wait`、`task_update`、`task_ask` 按需加载（`onDemandTools`，上下文里的 `[waiting on]`、`[task record]` 和问题行说明了怎么加载），`handoff`、`message` 变成别名后不再占列表。提示词核心里的“handoff”改成“delegate”，最长的提示词（计划里的定时后代，核心预算的天花板）反而短了 13 个字符。

## 日期

2026-10-23（PDT）

## 谁拍板

owner（实施方案第 3 节：Q1 30 天、Q2 扩 `work_items`、Q5 混合等待、Q6 卡片保留且新消息进收件箱、Q15 自动转给派活人并加版本号、Q17 三轮、Q18 60 条、Q19 按种类的超时，以及两条补充：清理不碰 D38 文件、等待中的说明不占 D50 名额）。

## 背景

C1 把现有的三套等待（`respond follow_up` 的 `run_waits`、委派回传、B1 最后完成者回传）加上问人，归成“发出调用，结果晚点到”；C3 要求每件被派的活有清单、说“做完”要对账；C2 的命令结束事件、定时回看也是同一种等待，等这张表先落地（阶段 3）。对标 Claude Code Agent Teams（共享任务表加邮箱）、A2A Task（含 input-required）、Anthropic 多智能体研究系统、长任务文章里“只许改 `passes`”的功能清单。

## 影响

- 与计划的差别（实现时选了不改变现有行为的做法）：
  - 委派的回传仍由原来的 `group_followup` 回传 run 交付，`task_result` 等待是镜像，静默结束；统一成“全部走收件箱”留给阶段 4 一起做。
  - `wake`（`say` 的 wake 列表）和 `send_group_message`（`team` 类）暂不建任务。
  - 一对群里的单个 `bot_post` 等待仍用提及唤醒，保持原有去重和上限。
  - `command_exit` 在表里有，`wait_for` 拒绝登记（`wait_kind_unavailable`），阶段 3 开放。
  - 用户不能在界面里取消 Bot 的等待（能停 run、取消任务，等待随之结束）。
- 同一个短事务里的写：建任务、建等待、结束等待、结算分组、写收件箱；不跨模型调用或 guest 调用持有事务（单连接 SQLite）。
- 不进导出（`portable*`）：等待、收件箱、清单是运行时状态，导出不带；删除 Bot 或会话时一起清。
- 阶段 3 需要：`wait_for` 开放 `command_exit`；终端 `notify_on_exit`；`terminal_cleanup.go` 不清理登记了等待的终端；`tofi update` 把未结束的 `command_exit` 等待算忙。阶段 4 需要：`bot_inbox` 的原子领取（现在 `consumeInboxForRun` 是 run 开始时读）；把 `flushInbox` 的“新开 run”换成中途插入；Q6 的保留卡片；把委派回传也收编进收件箱。

## 相关代码与文档

- `internal/app/tasks_schema.go`（表、迁移：`run_waits`、在跑的委派）、`work_items.go`（任务列、状态）、`tasks.go`（任务规则、清单、版本、委派建任务、run 生命周期钩子）、`waits.go`（登记、成环检查、分组结算、取消、期限、`[waiting on]`）、`bot_inbox.go`（收件箱、叫醒、上下文）、`wait_sweeper.go`（扫描器）、`task_cascade.go`（连带取消、影响数）、`task_retention.go`、`task_tools.go`、`task_delegate_exec.go`（原 `handoff`/`message` 的实现）
- 钩子：`app.go`（`SetRunStatus`、`finishRunOpts`、重启恢复、用户消息入口、路由）、`collaboration.go`（`AddForwardHandoff`、`AddBotMessage(s)`、`CancelRunTree`、`executeForQueue`）、`group_respond.go`、`say.go`、`chat_segments.go`、`deletion.go`、`group_maintenance.go`、`questions.go`、`mcp_schedule_context.go`（`wait` 来源校验改读 `waits`）
- UI：`ui/src/WorkPanel.tsx`、`TeamBoard.tsx`、`DeleteConversationDialog.tsx`、`AutomationSettings.tsx`
- 测试：`internal/app/tasks_test.go`、`tasks_say_test.go`、`waits_helpers_test.go`，以及改过的 `group_mention_test.go`、`group_scope_test.go`、`automation_settings_test.go`
