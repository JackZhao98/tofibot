# Bot 协作与跟进待办（owner 逐个过设计）

owner 2026-10-23：群聊协作是一个大坑，要重点打磨。下面几项先记下（C4 技能为同日追加），owner 要从头一个个过设计，过完一项再动手一项。每项写：现状（已查代码）、缺口、状态。

| # | 问题 | 现状（代码查实，2026-10-23，rc.40） | 缺口 | 状态 |
|---|---|---|---|---|
| C1 | Bot 能否给自己设一个动态的待跟进事项（非定时任务） | `create_work_item` 只记录承诺，说明写明“不开始执行、不安排之后的运行”（`internal/app/work_items.go`）；主动跟进 `followups_sweep.go` 由后台每 90 分钟从**用户自己**的私聊消息里抽取，P0 只观察、不发消息；群里 `respond follow_up` + `wait_for` 只能等**同一轮里某个指定 Bot** 发言，10 分钟超时（`group_respond.go`，`run_waits` 表，重启不丢）；想“过一会再看”只能建一次性定时任务 | 没有 Bot 自己设、到点叫醒自己的等待（等某 Bot 发言 / 等终端命令结束 / 等一段时间） | 待过设计 |
| C2 | Bot 能否挂起后台任务、同时继续对话、回头再看输出 | `computer_terminal` 是常驻 PTY：长命令在 run 结束后继续跑，之后可按 cursor 读输出，人在 Terminal 里看得到（`terminal.go`）。但用户新消息会在下一个工具间隙取消正在跑的 run（`steered by newer user message`，新 run 只拿到“哪些工具已做完”的摘要）；命令跑完不会叫醒 Bot；VM 重启结束会话（休眠是否保住未实测）。变通：委派给同事 Bot，同事在隐藏对话里干，原 Bot 被聊天打断不影响同事，结果回传再叫醒原 Bot | 一说话就打断手上的活；后台任务完成没有回到 Bot 的通知 | 待过设计 |
| C3 | 群内别的 Bot 发言（没 @ 谁），是否有 Bot 检查、总结、判断要不要继续 | 没有。Bot 发言后只有这几种会叫醒别人：`say` 的 `wake` 或 `handoff`；@ 一个没在跑的成员（`group_mention.go`）；有人正用 `follow_up` 等它；委派结果回传给派活的 Bot。提示词明写“文字里的 @ 只是指代”。除此之外，一轮结束群就停住 | 群里没有“这件事做完没有、该谁接着做”的负责人；B2（答应不干活）在群里尤其严重的根子之一 | 待过设计 |
| C4 | Bot 能否给自己装技能（只给自己用，bot scope） | 能装，但只能装成**全工作区**：`manage_extensions` 的 `skill_install` 写到共享目录 `<DataDir>/skills/<name>/`（`internal/extensions/management.go`），工具说明写“对所有 Bot 可用”；每次装/删都要人批准，定时/无人值守 run 里拒绝（`internal/app/extension_gate.go`）。按 Bot 限制只有 `skill_access(skill_name, bot_id)` 一张表，**只有用户在设置里能改**，没有行 = 所有 Bot 可用（`internal/app/skill_access.go`）。没有编辑动作，改 = 删 + 装，两次批准；Bot 能删任何没被限制的技能，包括 owner 装的。来源只有内联文本，无签名、无内容审查。提示词里每个技能一行描述，正文 `read_skill` 时才加载（D41）。文档互相打架：`contracts/alpha-api.md` 写“默认不授权、逐 Bot 开启”，代码相反；`docs/design/settings-ia.md` 提议删掉按 Bot 授权，代码还在 | 没有 Bot 私有技能；Bot 装的东西默认全员可见；只能删了重装；删除权限过宽；契约和设计稿与代码不一致 | 待过设计（owner：现有实现有问题，要重新设计） |

初步方向（未定，过设计时再议）：一轮有一个协调者负责收尾，成员发言后回到它判断；Bot 有一个通用的“稍后叫醒我”（从 `run_waits` 扩展，设置里可见可改）；用户发新消息不打断正在干的活（改动最大，放最后）。对标 Anthropic 多智能体研究系统：主导者负责收尾，子任务完成自动回到主导者。
