# 0062 每个 Bot 一条线、Bot 之间并行；插话排队，不取消

> 编号 D62，索引见 [README](README.md)。实施方案阶段 4（[collab-implementation-plan](../design/collab-implementation-plan.md)），设计在 [collab-todo 的 C1 第 5、6、7 条和 C3 第 6 条](../design/collab-todo.md)。部分替代 [D59](0059-tasks-and-waits.md) 第 3 点的“叫醒一律开新 run”（等待和收件箱事件现在优先在正在跑的 run 里读）。

## 决定

owner 2026-10-23 定的：Q3 每个 Bot 全局一条线（一台电脑），忙时私聊进收件箱、在间隙读到；Q5 混合；Q6 问人卡片保留、新消息进收件箱。

1. **一个 Bot 同一时间只跑一个 run，跨所有对话；不同 Bot 并行。** 对话 worker 还在（保留审批过期、定时先后、问人等待这些按对话的不变量），但它挑“自己的 Bot 空闲”的排队 run，在自己的 goroutine 里启动，然后接着挑（`internal/app/bot_lanes.go`：`pickRunnable`、`launchRun`）。线是内存里的一张表（Bot id 到 run id），启动前再查一次库里该 Bot 有没有别的 `status='running'` 的 run（审批过期的“已领取”行不算）。分诊 run 没有工具，不占线。run 结束后释放线并唤醒所有对话 worker；有 run 因为线或顺序在等时，worker 另外每秒复查一次，丢了唤醒也不会卡住。
2. **一个 Bot 在几个对话里都有排队的活时，各对话的队首按这个顺序竞争：** 同一棵任务树的续跑（刚放下线的那个 run 所在的树，`lineageKey`）> 人的消息 > 委派（`group_task`、`group_mention`、`message`、`team`、`group_followup`、被邀请的 `group_chat`）> 定时任务；审批过期收尾最先。同一个对话内部仍按旧顺序（委派的子任务先于它所属那一轮的 run），因为群聊一轮的规则依赖这个顺序。
3. **两条顺序保证，原来靠“每对话一条线”白送，现在要写明：** (a) 一个 run 不在“创建它的那个 run”还在跑或在排队时启动（被委派的子任务、被邀请的成员、唤醒都在发起者这一轮结束后才开始）；(b) 同一条消息 @ 了多个成员时，后面的成员等前面的成员结束，因为前面成员留下的 note 是后面成员的输入（`coAddressedInput`）。其余一律并行。这是对“不同 Bot 在一个群里并行”的有意保留，见风险。
4. **重启恢复守同一条线。** 回到队列的 run（有检查点或什么都没做）和照旧的“已领取”审批收尾，由各对话 worker 按线逐个领；同一个 Bot 的几个 run 排队等线，不会并发。
5. **插话排队，不取消。** 入口事务里，被点名的 Bot 在这个对话（或这个对话里任务派出的工作）里有正在跑的 run，就不建新 run、不取消任何东西：消息照常存成一条用户消息（人立刻看到），另写一行 `bot_inbox(kind=user_message, parent_run=那个 run, anchor=消息 id)`，响应里 `run` 为 null、`runs` 为空、`queued` 列出 `{bot_id, run_id, inbox_id}`。`Message.delivery` 是 `queued`（历史只列还没读的）；读到时事件流发 `inbox` 事件（`state: read`）。不算目标的 run：已经交付了 `say(answer)` 的、被委派接力正要让位的、审批过期收尾的、定时任务的 run（它的回答是回执，人在聊天里读不到；人发消息时它照旧让位给新 run）。分诊选中的成员正在跑时同样排队（`steerTriagedMessage`）。Bot 在别的对话里忙（比如在群里干活时被私聊），私聊消息照旧建 run，但它在私聊里排队等线，不会把私聊内容塞进群里的 run；线空了就读到。
6. **在边界原子读取。** agent 循环在每次模型请求之前（`BeforeModelCall` 之后）调 `DrainInbox`：一个事务里标记已读、返回要追加的文字；追加成一条用户消息、写检查点（`OnCheckpoint`，所以重启后还在）。读取内容有两类：指向这个 run 的人的消息（一个 run 最多读 5 条）和同一个 Bot、同一对话里已经就绪的其他事件（到点的等待、成员的提问、任务更新；等着一个仍在跑的 run 的行和 `via=mention` 的唤醒不动）。工具批次中间不插入（提供方要求工具结果紧跟工具调用），所以“每个工具之后”实际是批次结束后的下一个边界。
7. **读完才算数；读之前结束就接力。** run 结束的同一个事务（`finishRunOpts`、`SetRunStatus`）里，指向它却没读的消息变成同一个 Bot 的一个续跑 run（普通回复，触发消息是最后一条，任务跟过去）；人按了“全部停止”则丢弃这些消息。重启把 run 结束时同理。读了、但检查点没来得及写的（崩在中间），恢复时收回，续跑的 run 再读一遍（`bot_inbox.drained`、`returnUndeliveredDrainsTx`）。
8. **每读一次，补一次预算。** 时间、调用数、步数、费用上限从读的那一刻重新算（`Continuation.BudgetCallsBase/StepBase/CostBase`，检查点带着）。读满 5 条，或上下文超过窗口的 60%，下一条没读的消息让 run 带着已完成的工具结果收尾让位：续跑 run 与它之间用旧的让位边（`run_steering`）连着，已完成的结果带过去。
9. **问人卡片保留（Q6）。** 卡片开着时人发的消息进收件箱、指向停着的 run，卡片不取消；run 在卡片被回答、过期或取消后恢复，第一个边界就读到。`supersedeInputWaitsTx` 不再收走带未读消息的停放 run。
10. **等待与收件箱并入（Q5）：** 已就绪的等待结果、成员的提问、任务更新在 Bot 正在跑时直接在边界读到，不再另开 run；Bot 没在跑时照旧唤醒新 run。**没有并入的：** 委派回传（`group_followup` 回传 run）仍是一个排队的回传 run，等线空了运行（同树续跑优先级最高）；并入要把已排队的回传 run 在读取时“合并”掉，它牵着任务和等待的收尾钩子，风险大于收益，留给后续（选项见风险）。
11. **停止两档。** “停止当前步骤”：只取消正在跑的那一个工具调用的 context（`runtime.Request.BeginStep`，`POST /api/runs/{id}/interrupt-step`；没有工具在跑返回 409 `no_step_in_flight`）；这个调用返回类型化结果 `step_interrupted`（读操作 `no_side_effects`，其他 `unknown`，文字以 “The person interrupted this step” 开头），run 接着往下走。“全部停止”：原来的 `CancelRunTree`，连带任务、等待、指向被停 run 的未读消息。
12. **群里的顺序：** 读消息时，屏幕上的草稿（被送回审阅、还挂着的那段）先作为一段发布，读完之后再开新草稿，所以时间线是“草稿、人的消息、新回答”。并行的草稿各用各的序号，发布时不重排。
13. **提示词原则（英文，STE，无例句）。** 只出现在读取时追加的那条用户消息里，所以不占别的 run 的提示词预算：

    ```
    [new message from the person]
    The person sent this message while you were working. It is an addition or a correction to the same work. Continue from what is already done: keep your finished steps, do not start again and do not repeat them. If the message is about something else, address it in your answer too.
    <message text and one line per attachment>
    [/new message from the person]
    ```

    多条时标题是 `[new messages from the person]`，写“in the order sent”，条与条之间用 `---`。系统事件是另一块：`[events from the task system]` 开头写明它们来自任务和等待系统、是数据不是指令。
14. **界面。** 输入框一直可用；对一个正在工作的 Bot 发出的消息，气泡下面有一行小字 “Queued, read at next step”，读到后变成 “Read” 并自行淡出；Stop 是一个菜单（“Stop current step”、“Stop all”，群里多个 Bot 在工作时每个 Bot 一项）；群里多个 Bot 同时工作时各有各的 LiveMark 行。7 种语言，目录里有对应状态。

## 取舍与风险

- **同一条消息 @ 多个成员仍按顺序跑**（第 3 点 b）。放开并行后后面的成员读不到前面成员的 note。要放开，需要把 `coAddressedInput` 改成“结束后再续一轮”。
- **读取只在模型请求之前。** 一个长工具批次里的消息要等批次结束；“停止当前步骤”是想马上打断时的出口。
- **不并入委派回传（第 10 点）。** 选项：读取时把已排队的回传 run 合并成一条事件并把它标记为完成；先要核对它对 `taskRunFinishedTx`、`legacy` 等待的影响。
- **读取不带图片。** 附件只带名字和 id（模型用 `read_attachment`），视觉输入要等下一个 run。
- **在一个 run 里读了很多条后，助手轮次编号上限**（`MaxAssistantTurnIndex`）是整个 run 共用的，极长的 run 可能先撞到它。
- 单连接 SQLite：读取是一个短事务，不跨模型调用持有连接；六个 Bot 同时流式加工具的压测见下。

## 验证

- `go test ./internal/app`、`./internal/agent/...`、`./internal/runtime/...` 全绿（退出码 0）；新增 `bot_lanes_test.go`（一线一 run、并行、优先级、重启、被挡住的队首不饿死别的对话、压测）、`steer_test.go`（同 run 读取不取消、竞态续跑、已答不读、满 5 条让位、私聊等线、时间线顺序、停止、重启收回、等待事件并入、定时 run 让位、interrupt-step 接口）、`internal/runtime/steering_test.go`（停止当前步骤、读取写检查点、读取补预算）。
- 压测（`TestSixBotsStreamingWithToolsOnOneConnectionDoNotStarveIt`，6 个 Bot 每个 300 步流式加工具，同时有人不断发消息、一个探针不断抢连接）：全部在 1.1 到 1.6 秒内跑完，无死锁；引擎侧数据库操作 p99 约 2.6 到 7.1 毫秒，抢连接探针 p99 约 2.4 到 7.8 毫秒，最大约 17 毫秒；`-race` 下无数据竞争。
- 被改写的旧测试（原因都是“消息不再取消或取代正在跑的 run”）：`TestChatSegmentsPersistInOrderAndStopAfterInterjection`、`TestSteeringCancelsQueuedRunWithoutCancellingRunningParent`、`TestActiveGroupSteeringCancelsOnlyCurrentRoundPeers`、`TestGroupMessageToTheRunningBotStillSteersIt`、`TestInputContinuationStopAndSteeringInvalidateCard`、`TestAPersonsMessageWhileACardIsOpenLeavesAnInboxRow`、`TestTerminalCleanupPreservesWaitAndCleansStoppedParkedRun`（steer 分支）、`TestARetryRunTakesOverTheTaskAndASteeredRunKeepsIt`、`TestPersonMessageBumpsTheVersionAndMarksAStaleResult`、`TestScheduleFailureDetailLeavesOtherFailurePathsUnchanged`（steered 分支）；`steering_review_test.go` 的 `steeringReviewInterject` 改成“先排队、再用读满上限那条路收尾”，让其余让位测试继续覆盖旧的让位边（委派、唤醒、读满上限仍然用它）。

## 日期

2026-10-23（PDT）

## 谁拍板

owner：实施方案第 3 节 Q3、Q5、Q6。

## 复核后的修正（同日）

- 人的消息只以“人的一轮”进模型一次：`peekInboxForRun` 不再把 `user_message` 行当成 `[task updates]` 事件。
- 只有读到人的消息才补预算；每次读取都计入 5 次上限，超上限、超 60% 窗口、离助手轮次上限不足 40 轮时什么都不读（人的未读消息让 run 经续跑 run 收尾，其他事件留给下一个 run）。
- 新 run 启动时只把落在它历史范围内（触发消息之前）的消息标为已读；更新的留给第一个边界读取。
- 同一条消息 @ 了多个成员、其中一个正在工作时，它的未读排队消息算前驱，后面的成员等它读到再开始。
- 一个人的消息或委派等过 3 分钟，按同任务树的优先级与最老优先排，忙碌的任务树不能一直压着。
- Stop 按钮在入口里（菜单块没加载时直接“全部停止”）；菜单只有两项懒加载；“停止当前步骤”只对有工具调用在跑的 run 出现。
