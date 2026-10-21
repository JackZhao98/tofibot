# 0003 改完立刻用真实对话测，亲自读回复

> 编号 D03，索引见 [README](README.md)。

## 决定

改动装好后，立刻自己造真实对话去测，并亲自读 Bot 的回复，判断像不像人说的话。不等定时任务、不等某个排期自然触发：近时的定时、群里的多轮、重启后续跑，都当场造出来测。

## 日期

2026-10-16（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 单元测试只能证明代码按设计走，证明不了模型在真实提示词下说出来的话自然。前几轮的问题（重复回答、步骤旁白、汇报腔）都是在线上真实对话里才看到的（`docs/agent-plan/wave-notes.md:75-78` 各条注明了 "live 10-14 …"）。
- 等定时任务自己跑，一个问题要隔几个小时甚至一天才暴露，修复节奏被拖慢。

## 理由

- 回复自然不自然只能靠读。评分器和指标是辅助，最终要人（或协调者）读原文。
- 当场造情境，反馈周期从"等下一次定时"缩短到几分钟。

## 影响

- 每个影响 Bot 说话或任务流程的改动，交付前要有一次真实对话的验证，并写明读到了什么。
- 为此建了离线评测工具：`eval/run_eval.py` 起真实服务进程，用真实模型跑探针，再由评分模型按 `eval/rubric.md` 打分（合并 `31fb1cd`，10-17）。它补充但不替代读回复。
- 测试用的 Bot 和群要是全新的，见 [0013](0013-test-with-fresh-bots.md)。
- 自动化测试仍然只用合成数据（`AGENTS.md:23` "Use synthetic data for tests"）；真实对话验证由协调者在线上做，生产操作只由协调者执行（D30）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 离线评测 | `eval/run_eval.py`（文件头说明用法）、`eval/probes.json`、`eval/personas.json`、`eval/rubric.md`；合并 `31fb1cd` feat/structured-output-p0 |
| 任务完成评测 | `eval/run_tasks.py`，在分支 `eval/task-completion`（提交 `820121f`，工作区 `../tofibot-taskeval`），尚未合进本分支；结果在 `../tofibot-taskeval/eval/results/*/summary.md` |
| 线上验证记录 | `docs/agent-plan/wave-notes.md`（各条的 "live …" 字样）、`docs/agent-plan/structured-output.md` 第 12-14 节的实测数字 |
| 可选的真模型验收测试 | `internal/app/live_onboarding_acceptance_test.go`（`TestLiveCodexConversationalBotOnboarding`，需设 `TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE` 才运行） |
| 待确认 | 仓库里没有规定"每次改完必须做哪几条真实对话"的清单；现有流程见 [`docs/process.md`](../process.md) 第 3.5 节和第 7.5 节 |
