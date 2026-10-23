# 0056 放行就动手：答应不算交付，自标“还有活没做”的回答退回一次

> 编号 D56，索引见 [README](README.md)。

## 决定

群聊里多个 Bot 协作时只答应不干活（[ui-issues B2](../design/ui-issues.md)，会话 34514775）。修法分两层，都只改通用原则和通用机制，不按措辞或题型判断：

**A. 提示词（所有 run）**

1. 核心原则加一句：“A go-ahead or an agreed plan is the instruction to do that work now, with your tools.”（`taskCompletionGuidance`，对标 Anthropic 提示词最佳实践的 `default_to_action`）。
2. 群聊分工：“On a go-ahead for planned work, do your part now and start the owners of the other parts.”（`groupDelegationGuidance`）。
3. 群聊“只回最新一条”的提示：放行指向它批准的那个方案，要执行（`groupLatestMessageLine`），不再被当成寒暄短回。
4. 同事结果回传：“A result that is only a plan gets carried out before you conclude.”（`[completed colleague task]`）。
5. 委派任务块在 say 协议下改成用 `say(message)` 确认、`say(answer)` 交成果（原文“用普通 assistant 消息确认”在 say 协议下是私下文字，模型便用 `say(answer)` 确认，回合随即结束）。
6. 删掉电脑说明里的 “Use tools only when ready; otherwise report status.”。

**B. 机制（所有非定时 run；定时任务有自己的回执）**

1. `say` 新增必填字段 `outcome`：`done` / `blocked` / `delegated` / `pending`，由模型自标（位置在 `purpose` 之后、`text` 之前）。
2. `say(answer)` 自标 `pending`，或自标 `delegated` 却既没 `wake` 也没 handoff，且本轮没有建/改定时任务时，退回一次（`answer_pending_work`，Transient，下一步 `retry`）：现在就用工具做，或叫醒负责的成员；做不了就说清阻碍和试过的路。同一回答再发一次即接受。对标 Claude Code 的 Stop hook（拦住停下、给出理由、接着做）。
3. 每个 run 只退回一次，不占完成复核与回答把门的次数；可能被退回的回答不边写边显示，自标完成/阻塞的照常流式。

## 日期

2026-10-22 晚（PDT）

## 谁拍板

owner（选择题：A+B、所有非定时 run、修完在新群复测、不先查线上）。

## 背景

查代码确认：群聊、委派、隐藏 trace 的 run 都拿得到电脑工具（`toolsUnmarked` 无条件挂 `microVMTools`，`internal/app/app.go`）。不干活的原因是：`say(answer)` 发出即结束本轮；完成复核只在用过干活工具后才启动（`completion_final_review.go`），零工具的回答从不复核；回答把门只管定时任务（`answerGateScope`）；提示词把“开始吧”引向短回复，委派确认的写法与 say 协议冲突。

## 理由

提示词管大多数情况；模型不听时由自标 + 退回一次兜底。自标而非关键词，和回答把门同一思路，正常回答零额外开销。

## 影响

实施：分支 `fix/b2-agree-then-act`（提示词 `9c2e4a5`，机制 `1400c8d`），随 rc.39 发布。线上效果待在新建验证群复测。提示词长度上限按实测挪：私聊 8200→8225、结构化私聊 8025→8060、最长固定开销 8995→9120。

## 相关代码与文档

- `internal/app/context.go`：`taskCompletionGuidance`、`groupLatestMessageLine`、`groupDelegationGuidance`、`[current handoff task]`、`[completed colleague task]`
- `internal/app/microvm.go`：`microVMEnvironmentPrompt`
- `internal/app/say.go`：`sayToolSchema`、`sayOutcomeDescription`、`answerPendingWorkReminder`、`runMadeSchedule`
- `internal/app/say_stream.go`、`internal/saystream/decoder.go`（`Outcome()`）
- 测试：`internal/app/say_outcome_test.go`、`TestHandoffAcknowledgementFollowsSayProtocol`、`TestProductionPromptSizeMatrix`
