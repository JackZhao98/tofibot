# 0012 非必要不用 Opus；子代理默认 Sonnet 写代码、Haiku 收集

> 编号 D12，索引见 [README](README.md)。

## 决定

1. 评测里不再把 Opus 当 Bot 的模型来跑（10-19 / 10-20）。
2. 10-20 推广为一般原则："非必要不用 Opus"。
3. 开发工作里派出去的子代理默认：写代码用 Sonnet，收集资料用 Haiku。

## 日期

2026-10-19 至 2026-10-20（Pacific）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 任务完成评测（15 条一句话任务，全新 Bot，生产环境）在同一轮里比较了三个模型。完成数 Haiku 10、Sonnet 10、Opus 11，差距只有 1 条；但 15 条任务的估算费用 Haiku $1.30、Sonnet $2.11、Opus $5.50，建 Bot 的 onboarding 费用 $0.94、$2.41、$4.88（`../tofibot-taskeval/eval/results/prod-haiku-r1/summary.md`、`prod-sonnet-r1/summary.md`、`prod-opus-r1/summary.md`；上下文文件记这一轮为 rc.17，但结果生成于 10-19 17:01–17:28 PDT，那时 rc.18、rc.19 已发布，实际版本待确认，见 `docs/releases.md` rc.17 一节）。
- 自托管产品的用户不会默认用 Opus 跑 Bot，评测应该测用户实际会用的模型。

## 理由

- Opus 多完成 1 条任务，费用是 Sonnet 的两倍以上，在用户实际会选的模型上测更有意义。
- 开发时的子代理也一样：多数工作 Sonnet 足够，收集资料 Haiku 足够，Opus 只留给确有必要的地方。

## 影响

- Bot 模型的评测只跑 Haiku 和 Sonnet。之后的轮次（rc.21、rc.26）只有 Haiku 和 Sonnet 的结果（上下文文件中的数字：rc.21 Haiku 11、Sonnet 11；rc.26 Haiku 10、Sonnet 11）。
- 与现有代码不一致、需要处理的地方：
  - `eval/run_tasks.py:51` 的 `DEFAULT_MODELS` 仍是 `claude-haiku-5-5,claude-sonnet-5-5,claude-opus-5-5`（在未合并的分支 `eval/task-completion`，提交 `820121f`）。按本决定应去掉 Opus。
  - 两个评测脚本的评分模型默认都是 `claude-opus-5-5`（`eval/run_eval.py:652`、`eval/run_tasks.py:1171`）。评分模型不是 Bot 模型，不在第 1 条的范围内；按第 2 条是否也要换，待确认（owner 未明确）。
  - `eval/run_eval.py:649` 的 `--models` 默认已是 `claude-haiku-5-5`，符合本决定。
- 产品里的默认模型分工见 [0014](0014-sonnet-conversation-haiku-background.md)；那是产品行为，本条是评测和开发流程。
- 子代理的模型分工是开发流程约定，不在产品代码里；见 [`docs/process.md`](../process.md) 第 9.3 节。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 评测脚本 | `eval/run_eval.py:41-46`（价格表）、`:649`（`--models` 默认）、`:652`（`--grader-model` 默认）；`eval/run_tasks.py:51`、`:1171`（分支 `eval/task-completion`，工作区 `../tofibot-taskeval`） |
| 评测结果 | `../tofibot-taskeval/eval/results/prod-opus-r1/summary.md`、`prod-sonnet-r1/summary.md`、`prod-haiku-r1/summary.md` |
| 模型价格来源 | `internal/provider/models.go`（`eval/run_eval.py:41-46` 的注释说明价格取自这里） |
| 流程 | `docs/process.md`（"什么情况用什么模型"） |
