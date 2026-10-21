# 0005 被 @ 的每个 Bot 都自己回答，按 @ 的顺序

> 编号 D05，索引见 [README](README.md)。

## 决定

一个人在群里的一条消息 @ 了几个 Bot，每个被 @ 的 Bot 都各自跑一次、各自回答，按消息里 @ 的先后顺序。第一个被 @ 的 Bot 不再负责协调其他 Bot，也不再决定别人要不要发言。

## 日期

2026-10-19（Pacific）。合并于 10-19 13:33（`2e46dc9`）。

## 谁拍板

owner（jackzhao）。方案原文："Owner decision: when a person's message @mentions several Bots, every mentioned Bot gets its own run; the first no longer decides whether the others speak."（`docs/agent-plan/structured-output.md` 第 14 节）

## 背景

- rc.15 上 owner 写了"@A @B <问题> 各一句话"。只有 A 跑了：第一个被 @ 的 Bot 当领头，自己决定要不要转给别人。结果 A 一个人把 B 的角度也答了，B 根本没说话。领头真的转交时，又会出现一段给 B 的交代，再加一段领头的重述（方案第 14 节 Problem）。
- 之前的设计（`docs/agent-plan/wave-notes.md:61` 第 20 条）是"多人 @ 的消息交给第一个被 @ 的 Bot，由它分配"。

## 理由

- 人 @ 了谁，就是想听谁说。让第一个 Bot 替别人决定，违背了人的意图。
- 去掉协调层后，交代、重述和跟进都不需要了，成本也下降：评测里每条探针的费用降了约 23%（方案第 14 节表格）。

## 影响

- 派发：开关打开时，`postMessage` 用 `parseMentions` 解析出所有被 @ 的 Bot（句中 @ 和句首 @ 同等对待），在同一个事务里给每个 Bot 建一个根运行，`queue_seq` 按 @ 顺序（`internal/app/app.go:3281`、`:3318`）。
- 顺序由队列保证：同一会话一次只跑一个运行，按优先级再按 `queue_seq` 排；重启后排在后面的根运行仍是 `queued`，各只跑一次。
- 后答的 Bot 能看到先答的回复：`coAddressedReplies`（`internal/app/group_dedupe.go:332`）把其他根运行的回复加进历史；`coAddressedContext`（`group_dedupe.go:368`）加一句 `[co-addressed]` 说明。
- 防重复：`coAddressedRootTx`（`group_dedupe.go:100`）在 `AddHandoff`、`addMentionWake`、带 `wake` 的 `say(answer)` 三处拦住"把活转给同一条消息里已被 @ 的 Bot"，拒绝码 `recipient_already_addressed`（可重试，未执行）。没被 @ 的 Bot 仍可以被转交或唤醒。
- 代价：同一条消息里被 @ 的两个 Bot 不能互相唤醒，多轮来回需要人再发一条消息（方案第 14 节 Cost of the rule）。
- 只 @ 一个 Bot、没 @ 任何 Bot（交给 Primary Assistant）、以及关掉开关时，行为不变；关掉开关仍是旧的协调者设计。
- 实测（方案第 14 节）：被 @ 的 Bot 实际回答了的比例，Haiku 21/24 升到 24/24，Sonnet 22/24 升到 24/24；可见的协调文字 Haiku 12% 降到 3%，Sonnet 15% 降到 0%。
- 后续在 [0009](0009-reliability-principles.md) 里补了"群里只给人看最终合并的结果"：需要合作产出一个结果时，只有最后完成的那个成员发出。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 方案 | `docs/agent-plan/structured-output.md` 第 14 节 |
| 派发 | `internal/app/app.go:3281`（`postMessage`）、`:3318`（`parseMentions`）、`:1621-1654`（`AddHandoff` 里的 co-addressed 检查） |
| 去重与上下文 | `internal/app/group_dedupe.go:100`（`coAddressedRootTx`）、`:332`（`coAddressedReplies`）、`:368`（`coAddressedContext`） |
| 其他改动文件 | `internal/app/context.go`、`internal/app/group_mention.go`、`internal/app/say.go` |
| 测试 | `internal/app/group_multi_mention_test.go`：`TestEveryMentionedBotAnswersInOrderAndSeesTheEarlierReply`、`TestMidSentenceMentionsCountLikeLeadingOnes`、`TestOneMentionIsUnchanged`、`TestMultipleMentionsKeepTheCoordinatorWithTheFlagOff`、`TestAddressedBotCannotBeHandedOffToOrWokenForTheSameMessage`、`TestSayWakingAnAddressedBotIsRefusedThenThePlainAnswerPublishes`、`TestRestartBetweenTheTwoRunsRunsTheSecondExactlyOnce` |
| 评测 | `eval/probes.json`（新增一句话各答、句中 @ 两条探针，字段 `mentioned`）、`eval/run_eval.py`（指标"被 @ 的 Bot 是否回答"）；结果 `eval/results/multimention-*`（被 git 忽略） |
| 合并提交 | `2e46dc9` feat/wake-every-mentioned-bot |
| 被取代的旧决定 | `docs/agent-plan/wave-notes.md:61`（第 20 条 fix/group-coordinator-routing） |
