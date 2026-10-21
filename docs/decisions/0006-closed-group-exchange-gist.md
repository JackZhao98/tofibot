# 0006 群里已结束、某 Bot 没参与的对话，只以摘要给它

> 编号 D06，索引见 [README](README.md)。

## 决定

群聊里一段已经结束、某个 Bot 又没参与的对话，在这个 Bot 的历史里只以一条摘要（gist）出现，不再给它完整原文。目的是不让它把已经结束的话题重新拎出来。

## 日期

2026-10-19（Pacific）。合并于 10-19 02:25（`4f94dd4`）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里；问题与做法记录在 `docs/agent-plan/structured-output.md` 第 13 节。

## 背景

- rc.12（Haiku）：两个 Bot 辩论完一个话题后，owner 在群里打了个招呼；没参与辩论的 Primary Assistant 回了招呼，还顺带发表了自己对那个已结束话题的看法（方案第 13 节 Problem）。
- 之前已经加过两句原则（"最新消息要什么就回应什么，结束的话题保持结束"），评测里复活率降到 0/6，但线上还在出现（方案第 12 节 Follow-up 第 7 条）。所以改法从改措辞换成改 Bot 看到的内容。

## 理由

- 模型看到完整的辩论原文，就容易接着辩。只给摘要，它知道发生过什么，但不会被原文牵着走。
- 规则只看运行记录和名字提及，不从文字里推断别的；摘要用模板拼出，不额外调用模型。

## 影响

- 规则（`internal/app/group_exchange_gist.go:38` `foldClosedGroupExchanges`，在 `internal/app/context.go:470` 调用）：一段"对话"指从一条人的消息到下一条人的消息之间。群运行回答一条新的人的消息时，之前每段对话如果"已结束"且"这个 Bot 没参与"，就换成一条摘要。
  - 已结束：这条人的消息触发的所有运行及其后代都不在 `queued/running/waiting`，并且都没有在下一条人的消息之后发言。
  - 参与过：这个 Bot 在其中发过言，或在其中有非 `group_chat` 的运行（被 @、被唤醒、被转交、跟进），或被人点名。
- 摘要格式（`closedExchangeGist`，`group_exchange_gist.go:98`）：人的消息（160 字以内），每个 Bot 的消息取前两句（260 字以内），超过 6 行时保留前 2 行和后 4 行；加一句"全文仍在历史里，可用 `search_history` 查"；开头带 `[message_id=…]` 标签（不带时 Haiku 10 次里有 4 次把标签抄进回复）。
- 实测（Haiku 5.5，方案第 13 节）：招呼探针 10 次，提到旧话题 6 次降到 1 次；追问探针 6 次都还能说出双方和话题。已知缺口：辩论在很靠后的一句里让步时，摘要可能漏掉。
- 可在设置里关闭："自动行为"中的 `group_exchange_gists`，默认开（`internal/app/automation_settings.go:87`、`:284`；测试 `TestGroupExchangeGistsFollowTheSetting`）。
- 适用范围（`groupExchangeGistsApply`，`group_exchange_gist.go:28`）：人能看到的群聊、运行有触发消息、设置项开着，并且运行是 `group_chat` 或直接回答人的根运行（无父运行）。它不看结构化输出开关。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 方案 | `docs/agent-plan/structured-output.md` 第 13 节（含 rc.13、rc.14 两条后续） |
| 实现 | `internal/app/group_exchange_gist.go`（`groupExchangeGistsApply` `:28`、`foldClosedGroupExchanges` `:38`、`closedExchangeGist` `:98`、`exchangeRunFamily` `:199`）；调用点 `internal/app/context.go:470` |
| 设置项 | `internal/app/automation_settings.go:87`（`group_exchange_gists`） |
| 测试 | `internal/app/group_exchange_gist_test.go`：`TestFinishedExchangeTheBotMissedReachesItAsGist`、`TestExchangeStaysVerbatimWhileUnsettled`、`TestExchangeStaysVerbatimWhenTheBotWasAddressed`、`TestExchangeStaysVerbatimWhenTheBotPosted`；`internal/app/automation_settings_test.go:391` `TestGroupExchangeGistsFollowTheSetting` |
| 评测 | `eval/probes.json`（`group_greeting_after_debate`、`group_followup_on_finished_debate`）、`eval/rubric.md`（`revived_prior_topic`）、`eval/run_eval.py`（`topic_regex`、`answer_must_mention`）；结果 `eval/results/group-finished-exchange/`（被 git 忽略） |
| 合并提交 | `4f94dd4` fix/group-finished-exchange-context |
| 相关的旧改动 | `c53a25e` fix/history-own-replies-gist（10-16：Bot 自己较早的回复也以摘要回放，`docs/agent-plan/wave-notes.md:97`） |
