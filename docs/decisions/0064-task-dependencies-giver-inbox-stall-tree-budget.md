# 0064 拆任务与依赖解锁、派活人收件箱、停滞上报、按任务树限消息量

> 编号 D64，索引见 [README](README.md)。实施方案阶段 5b（[collab-implementation-plan](../design/collab-implementation-plan.md)），设计在 [collab-todo 的 C1 第 10 条和 C3 第 3、4、5 条](../design/collab-todo.md)。基于 [D59](0059-tasks-and-waits.md)（任务、等待、收件箱）和 [D62](0062-per-bot-lanes-and-queued-steering.md)（每个 Bot 一条线，所以任务才能真并行）。reaction 叫醒（Q7）已在阶段 5a 之前做完，本阶段不动。

## 决定

owner 2026-10-23 定的：Q17 停滞阈值 3 轮；Q18 每棵任务树 60 条消息；两者都是 Settings › Automatic behavior 里的设置。

1. **`delegate` 接受 `depends_on[]`（同一棵树的任务 id）。** 有未完成前置的任务是 `blocked`、没有 run（`assignee_run_id` 为空），前置列表存在 `work_items.depends_on`（逗号分隔；D59 时预留的列）。最后一个前置以 `done` 结束时，同一事务里把它改成 `todo` 并写一行叫醒持有人的收件箱（`task_update`，正文带需求原文和各前置的结果摘要）；叫醒新开的 run（或持有人正在跑时在边界读到）成为它的 `assignee_run_id`。前置以 `failed` 或 `cancelled` 结束时不解锁，改为通知派活人（`task_update`，叫醒），由派活人取消、移除依赖（`task_update` 的 `remove_depends_on`，没有未完成前置就开工）或重派。结束为 `review` 的前置不算完成，派活人验收为 `done` 才解锁。`settleClosedTaskTx` 是唯一入口，所以任何路径关闭任务都走同一套。
2. **依赖查环，规则与 `wait_cycle` 同型。** 在登记的同一事务里，从新依赖出发沿已有 `depends_on` 走（至多 64 步），走回该任务就拒绝，稳定码 `dependency_cycle`；图大到走不完也拒绝。其他稳定码：`dependency_not_found`、`dependency_not_visible`（只能依赖自己派的或本会话里的任务）、`dependency_ended`（前置已失败或取消）、`dependency_other_tree`（前置必须同属一棵树，在树内派活也只能依赖本树）、`dependency_locked`（`add_depends_on` 只能用在尚未开工的 `blocked` 任务上，且只有派活人能改）、`dependency_too_many`（12 个）。派活人对每个等待任务持有一个引擎投递的 `task_result` 等待，任务结束时作为 `wait_result` 叫醒它。
3. **一个 run 可以派出并行的多件任务。** 原来一个 run 只有一次直接 handoff（`handoff_count`）。现在第二次起的单人 `delegate`、以及所有带 `depends_on` 的 `delegate`，不占这次名额：任务建好后经收件箱叫醒持有人（`AddDependentTasks`）。没有依赖的第二件事因此立刻开工，与第一件并行；任务进同一棵树（第一件任务为根）。
4. **提示词原则**（英文，STE，无例句，放在 `delegate` 工具说明里）：拆宽泛的工作，每件任务一个负责人、一个结果；无关联的部分同一轮派出让它们并行；需要别的任务结果的部分用 `depends_on`；并行写代码之前先派一件定接口约定的任务，其余依赖它；任务数量与难度相称，简单的事一件就够。具体文字见 `task_tools.go` 的 `delegateToolDescription`。`task_update` 的说明加一句关于依赖的改动。
5. **派活人收件箱统一。** 持有人在任务的会话里发的每条可见消息（`say` 的 message、结果前的分段，不含内部笔记 `progress`）在存消息的同一事务里写一行派活人的 `bot_inbox`：`kind=progress`、`wakes=0`，正文带任务标题和消息前 600 字。不叫醒，派活人下次运行时在 `[task updates]` 里读到，正在跑时在边界读到。会叫醒的仍是三类：`task_ask` 的提问（`task_question`）、交结果（传统回传 run；依赖任务走 `wait_result`）、停滞上报。持有人交结果时，它本 run 为该任务写过的 progress 行标为已读（结果已带着）。
6. **停滞上报（Q17）。** 一轮 = 持有人的一个 run 带着任务结束而任务仍开着。每轮结束比较“进度签名”：清单每项的状态，加上任务是不是 `blocked`/`input_required`（`todo`/`in_progress`/`review` 之间的系统性移动不算进度）。第一次开工记下基线。没变就计数加一，变了计数归零；计数恰好等于阈值（设置 `task_stall_rounds`，默认 3，1 至 20）时，给这棵树的根 Bot（根任务的派活人，对人负责的那个）写一行叫醒的收件箱（`task_update`）。之后计数继续涨也不再报；要等有进展归零、再停滞满阈值才会再报。存放：`work_items.stall_count`（D59 预留）和新列 `stall_sig`。
7. **按任务树限消息总量（Q18）取代两个固定上限。** 删掉 `maxHandoffRounds`（run 链深度 8，三处）和 `maxMentionWakesPerLineage`（每条线 8 次提及叫醒）。树 = 一个根任务下的全部工作；没有任务的提及叫醒链按 run 血统成树（`run:<根 run>`），所以那里的死循环仍有上限。每棵树一行 `task_tree_budget`（`messages`、`reported`）。计 1 条的事：一次派活（`AddHandoff`、`AddForwardHandoff`、`AddBotMessage(s)`、依赖任务）、一次提及叫醒（自己等待超时的自叫醒不计，它另有 `wait_chain_limit`）、一次 `task_ask`/`task_update` 的消息、等某成员发言或等任务结果的等待（`registerWait`）。持有人给派活人的 progress **不计**，派活人对 `task_ask` 的回答不被上限拦。**上限（设置 `task_tree_message_limit`，默认 60，10 至 500）只拦新的成员间派活**：超过的派活和叫醒被拒（稳定码 `task_message_limit`，事务回滚，父 run 的 handoff 名额不变），Bot 照样可以回答人。计数到上限的那一次，在同一事务里给根 Bot 写一行叫醒的收件箱（让它向人汇报并问是否继续），并在根所在会话发一行 `system` 通知（`notice.type=tree_limit`，不进 Bot 历史），**每棵树只报一次**；之后被拒的尝试不再重复报。
8. **没有做的：** 任务卡片、成员列表、`claim_task`/`pass_task`、分诊输入属 5a，不在本决定。持有人带依赖的等待任务在群里没有“交接”通知行（任务卡片出来前，人只看到持有人开工后的发言）。`say` 一次最多叫醒 3 个 Bot 的限制（`say.go`、`group_respond.go`）保留：它限制一次发言的扇出宽度，不是链长，防止循环的是第 7 点的树预算。

## 审查后补充

- **树只有一个解析器** `treeKeyForRunTx`：沿 run 的父链，最近的一个：run 自己继承的 `runs.tree_key`、它持有（assignee）或派出（origin）的任务的根；都没有则是 `run:<根 run>`。建任务、依赖任务的树、预算、提及叫醒都问它。每行收件箱存 `tree_key`（默认取 `root_task_id`，再取 `parent_run` 的解析结果），叫醒出的 run 继承第一行的 key——所以限量报告、停滞报告、依赖失败通知叫醒的根 Bot 仍在原来的树里，到限后不能靠这次叫醒开一棵新树。`work_items.origin_run_id` 加了索引。
- **并行派出的几件任务共用一个 mode=all 的等待组**，派活人只在最后一件结束后被叫醒一次；`fanOutPendingTx` 把尚未结束的收件箱路径任务算作同胞，直接子 run 先结束时不提前回传；等待组结果在直接子 run 还在跑时先等，回传 run 已存在则丢弃，所以人只收到一次回传。
- 解锁行只由叫醒出的 run 读取（边界读取和上下文读取都跳过），一个 run 只绑一件解锁任务，其余的行等下一个 run。因此持有人正忙时解锁的任务要等它这一轮结束才开工。
- 停滞：run 结束时还有未结束的等待或自己派出的未结束任务，不算静默；签名含子任务状态。**不含结果消息 id**：持有人每轮都发一条未被验收的结果，是同一个停滞，不是进展。
- 取消一棵子树会通知子树外依赖它的任务的派活人；依赖环检查只数没见过的节点，上限 256 个，新建任务不走环检查（新 id 不可能成环）。

## 与计划的差别

- 计划写“wake 上限 3（`group_respond.go:174`）”；实际防循环的叫醒上限是 `maxMentionWakesPerLineage`（8，`group_mention.go`），那一行的 3 是单次发言的扇出宽度。取代的是前者，后者保留。
- 计划没写一个 run 只能 handoff 一次这条限制；不放开它，“并行派几件”做不成，所以加了第 3 点。
- 旧测试：`handoff_limits_test.go`（第八轮拒绝）改成按树预算拒绝；`group_mention_test.go` 的 `TestWakeCapTripsAndIsLogged`（8 次叫醒上限）改成 `TestTreeBudgetStopsMentionWakeLoopAndReportsOnce`；`say_test.go` 的 `TestStructuredRunKeepsShortAnswersAfterAHandoff` 原来靠第八轮上限收场，夹具改成回传 run 不再派活；`final_origin_test.go` 的 `TestFinalOriginLeadingMentionDispatch` 同理。

## 风险

- 等待任务的派活人等待在解锁前用 24 小时硬上限，解锁时才从现在起算通常的 2 小时（`unlockTaskTx`）；前置链超过 24 小时仍会超时。
- 等待任务不经 handoff，没有群里的“交接”行；5a 的任务卡片补这个。
- 持有人暂停（`pending`）也算一轮；它在等一条长命令时每次被叫醒而清单没变，会在第 3 次被报。要避免就调高 `task_stall_rounds`。
- 树预算按“成员之间的消息”计，不是群里全部消息；Bot 回答人不计。
