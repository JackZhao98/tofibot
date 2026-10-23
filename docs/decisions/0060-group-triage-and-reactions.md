# 0060 群里按角色分诊；人点的 reaction 成为真信号

> 编号 D60，索引见 [README](README.md)。实施方案阶段 5a（[collab-implementation-plan](../design/collab-implementation-plan.md)），设计在 [collab-todo C3 第 5、7 条](../design/collab-todo.md)。认领界面与任务卡片等阶段 2（任务与等待）合入后再做。

## 决定

**A. 分诊（owner 问题 16：每条没 @ 人的群消息调一次后台模型，只有 1 个在岗成员时跳过）**

1. 没 @ 谁的群消息，不再给“主助理，否则随机成员”。入口事务照旧排一个 run，但在岗成员 ≥2 时这个 run 的类型是 `triage`，由现有 `runKindTriage` 路径执行：不带工具，系统提示只有名单（名字、id、角色描述前 600 字、主助理标记）和选人原则；模型看得到本群最近的对话。入口事务里不调模型，所以不拖慢发消息。
2. 严格解析选中的成员 id（`strictTriageTarget`：精确 id、精确名字或 `{"bot_id":"..."}`）。选中后系统发一条“Handed to X.”提示，被选成员以普通的 `group_chat` run 回答（不再是 `group_task`，没有回给分诊 run 的 follow-up），提示行不进模型历史。
3. 退回旧规则：后台模型不可用、调用失败或答案无效，分诊 run 当场把消息交给旧规则选出的成员（主助理，否则随机），所以消息一定有人接。分诊 run 的 Bot 就是入口时按旧规则选出的那位；失败路径另行重选一次，防它已被归档。
4. 设置（Settings › Automatic behavior，每个自动行为都有设置）：`group_triage`（开关，默认开；关则完全回到旧规则）和 `group_triage_model`（默认“自动”= 现有的分诊/后台模型）。

**B. reaction**

1. 看得见：模型看到的历史窗口里，每条有 reaction 的消息后面带一行 `[reactions: 👍 person; 🎉 Birch]`（谁、哪个 emoji，每条最多列 8 个，其余写 “+N more”），算在历史预算里；系统提示里永远没有 reaction。
2. 会叫醒：**人**给某个 Bot 在群里的消息点 reaction，叫醒这个 Bot：低优先级（排在队尾，之后对它的新消息会取代这个排队的 run）、不要求回复（它可以什么都不发，也可以回应）。短时间内的一串 reaction 合并成一次叫醒（窗口 10 秒，第一个 reaction 开窗）。重复点同一个、窗口内又取消的不叫醒。Bot 点的 reaction 永远不叫醒任何人（防止来回客套）。被点消息的作者已归档或不再是成员，什么都不发生。叫醒 run 复用 `group_mention` 类型，`run_wakes.source = 'reaction'`，没有父 run；它读的是当前的对话，不是截止到被点消息。
3. 提示词原则：用 reaction 代替一句简短回复来确认、同意、表示看到、接下活；reaction 不改变任务状态（状态只走正式的接活和交结果，防止随手 ✅ 绕过对账）。

## 取舍

- 范围只到群：DM 里点 reaction 不叫醒（owner 问题 7 回答的是群场景；要扩到 DM 再说）。
- 窗口在内存里，重启丢掉窗口内的叫醒；reaction 本身已落库，下次 run 读历史就能看到。
- 叫醒的“低优先级、`needs_reply=0`”目前由 run 类型 + `reaction` 来源 + 提示词表达；阶段 2 的 `bot_inbox` 合入后改记为 `needs_reply=0` 的收件箱条目。
- 提示词预算：核心系统提示 +83 字符（DM 上限 8225 到 8308，结构化 DM 8060 到 8143，固定开销上限 9120 到 9203），均为实测。

## 日期

2026-10-23（PDT）

## 谁拍板

owner：问题 7（只有人点的叫醒，Bot 对 Bot 只可见）和问题 16（每条无 @ 群消息分诊，1 个成员跳过）。
