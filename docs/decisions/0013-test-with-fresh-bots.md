# 0013 用全新的 Bot 和群来测

> 编号 D13，索引见 [README](README.md)。

## 决定

测试和评测一律新建 Bot 和群（owner 原话："开全新Bot和群来测"），不用已经用了很久、历史被污染的旧 Bot 和旧群。

## 日期

2026-10-20（Pacific）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 10-16 的模型 A/B 发现：同样的问题，全新 Bot 在 Haiku/Sonnet/Opus 5.5 上得分 7.1/7.2/7.6，用了很久的旧 Bot 只有 6.6。差距来自模型模仿自己历史里的回复（`docs/agent-plan/wave-notes.md:97`，第 13 条）。
- 所以用旧 Bot 测，测到的是旧历史的影响，而不是这次改动的效果；旧 Bot 的人设也是按旧版生成器写的（见 [0001](0001-persona-generator-no-examples.md)、[0007](0007-scope-is-not-a-refusal.md)），不代表新用户会得到的 Bot。

## 理由

- 新用户拿到的就是全新 Bot：它的人设由当前的生成器写出，没有历史。只有这样测，才是在测用户真正会遇到的东西。
- 每次测试互不污染，结果可以比较。

## 影响

- 离线评测：每一轮都用新的数据目录和新 Bot（`eval/run_eval.py:14` 文件头 "--runs N (fresh data dir and fresh Bots per run)"），Bot 通过真实的 onboarding 路径创建（`create_persona_bot`，`eval/run_eval.py:203`）。
- 任务完成评测：每次任务都通过真实 onboarding（人设生成器）新建 Bot，群任务新建群；Bot 和群不在任务、轮次、模型之间复用（`eval/run_tasks.py:9-12` 文件头，`make_fresh_world` 在 `:148`；分支 `eval/task-completion`，提交 `820121f`，未合并）。提交 `c42716e` 还给每个任务的 Bot 起不重复的名字。
- 线上手工验证（[0003](0003-test-live-immediately.md)）也照此执行：新建 Bot 和群再测。
- 相关产品改动：旧 Bot 历史的影响也在产品里处理了，较早的自己回复以摘要回放（合并 `c53a25e` fix/history-own-replies-gist，`docs/agent-plan/wave-notes.md:97`）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 离线评测 | `eval/run_eval.py:14`、`:203`（`create_persona_bot`） |
| 任务评测 | `eval/run_tasks.py:9-12`、`:148`（`make_fresh_world`）、`:645`（`r_create_bot`，远程模式）；提交 `820121f`、`c42716e`（分支 `eval/task-completion`） |
| 依据 | `docs/agent-plan/wave-notes.md:97` |
| 流程 | [`docs/process.md`](../process.md) 第 5 节（评测） |
