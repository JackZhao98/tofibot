# 0042 群成员名单带上各自的职责，并写明按职责分工的原则

> 编号 D42，索引见 [README](README.md)。

## 决定

1. 群聊里每个 Bot 看到的成员名单，每一项除了名字和 id，还带该成员的职责：取它持久指令（`instructions`）开头的一行，压成单行，最多 140 个字符；自己那一项标 `[you]`。
2. 群聊提示词里原来的 “Address the user's request yourself” 换成一条分工原则：先做自己职责覆盖的部分；请求里还有同事职责更合适的部分，就带着具体说明交给那位同事，把结果并进给人的那一条回复里；一个成员能完整负责的请求留在该成员手里；闲聊直接回答。只写原则，不放例句、对照和关键词清单（D01）。
3. 私聊不带名单，也不带这条原则；人看不到的 Bot 之间的跟踪对话沿用原来的写法。
4. 可见性不变：群里只看到最终合并的结果和简短的转交行（D08、D09），不增加任何协调话语。

## 日期

2026-10-21（Pacific）。

## 谁拍板

待 owner 确认。起因和方案来自 owner 派的任务单（生产三次运行里一个 Bot 独自完成调研、写稿和配图），本条是实现时的行为取舍，尚未经 owner 逐条确认。

## 背景

- 评测 `deliv-terra-r0/r1/r2` 的 `deliv_group_announcement`（默认模型 codex-gpt-5.6-terra）：三次都是一个 Bot 自己查、自己写、自己调 `generate_image`；r0 里虽有一次 `handoff`，设计师仍没运行，由写手自己生成了图片。
- 代码里的原因：成员名单只有 `名字 (id)`，Bot 不知道谁是设计师；名字是 Bot 自己起的，看不出职责。提示词写的是 “Address the user's request yourself”，只给了在具体同事任务时调 handoff 的机制，没有说什么时候该分给同事。D05（被 @ 的每个 Bot 都自己回答，第一个不再协调）之后的几轮修复一直在压过度转交，这句话和缺失的职责信息一起让独自完成成了成本最低的路径。
- `generate_image` 和调研工具对每个 Bot 都开放，所以独自做完在工具上没有任何阻碍。

## 理由

- 业界多 Bot 团队产品里，协调者把多职责请求拆开，交给职责对口的队友，再汇总；一个人能完整负责的请求留在原处。
- 给 Bot 事实（各成员职责）和原则，比给规则和例句更稳，也符合 D01。
- 防过度转交：原则里写明单职责请求和闲聊不拆；既有的闲聊和单 @ 测试保持绿。

## 影响

- 实施状态：分支 `fix/group-real-delegation`，未合并。
- 提示词预算：群聊最长的提示词多 296 字符（固定开销上限 7803 到 8071，实测）；私聊不变。
- 本机评测（Sonnet 5.5，无电脑，不能生成图片）：三个 Bot 都运行的次数前后均为 3/3，原本就如此；该改动对 terra 的效果没有在本机验证（见 `docs/product/status.md`）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 实现 | `internal/app/context.go`（`memberRoleLine`、`groupDelegationGuidance`、群成员名单） |
| 测试 | `internal/app/group_delegation_test.go`：`TestGroupRosterCarriesEachColleaguesRole`、`TestDelegationPrincipleInGroupTurnsNotDMs`；`internal/app/prompt_budget_test.go` |
| 相关决定 | [D05](0005-every-mentioned-bot-answers.md)、[D08](0008-internal-text-never-in-chat.md)、[D09](0009-reliability-principles.md)、[D01](0001-persona-generator-no-examples.md) |
