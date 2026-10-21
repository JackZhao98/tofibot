# 0001 人设由生成器负责，提示词里不放例句

> 编号 D01，索引见 [README](README.md)。

## 决定

Bot 的人设由人设生成器写出：新 Bot 在 onboarding 私聊里通过 `set_bot_profile` 把名字和人设一起存下。人设有毛病时，改生成器（onboarding 提示词和 `set_bot_profile` 的字段说明），不手工改某一个 Bot 的人设。所有给模型的提示词都不放例句、不写 "Not X, but Y" 式的对照、不放写死的关键词清单。

## 日期

2026-10-15（Pacific）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 10-15 owner 用 14 条探针给 Bot 回复的"像不像人话"打分，平均 5.9/10（`docs/agent-plan/wave-notes.md:87`，提交 `c5ab24f` 的说明）。
- 问题出在人设本身：生成器写出的人设带回复格式、固定句式和样例措辞，所有用它的 Bot 都跟着变得公式化。逐个改 Bot 只能修一个，下一个新 Bot 还是一样。
- 提示词里的例句会被模型照抄，"Not/But" 对照会把被否定的那一半也教给模型；关键词清单只覆盖写进去的那几个词。

## 理由

- 人设是生成出来的，公式化是生成器的问题（提交 `c5ab24f` 说明原文："a formulaic persona is the generator's fault"）。修生成器，以后所有新 Bot 都受益。
- 提示词只讲原则，不给例子：原则能推广到没见过的情况，例子只会被模仿。

## 影响

- 生成器只要求写：这个 Bot 是谁、替用户做什么事、负责的范围、哪些对外或不可逆的动作要先确认、性格。明确禁止规定回复结构、句子顺序、固定开头结尾和样例措辞（`internal/app/onboarding.go:175` `botProfileInstructionsDescription`；`internal/app/onboarding.go:18` `onboardingSystemPrompt`）。
- 共享的说话规则（`voiceGuidance`）去掉了所有例子，补上原则：长度跟问题匹配、先回应玩笑/抱怨/感谢、不做例行的结尾客套、不主动翻旧账（`internal/app/prompt_budget.go:71`）。
- 以后改 Bot 说话方式的修复，应落在生成器或共享规则里，不写进单个 Bot。后续的 [0007](0007-scope-is-not-a-refusal.md) 就是照这个做法改的生成器。
- 已经存在的老 Bot 人设不会被自动重写（待确认：仓库里没看到批量重写人设的迁移）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 生成器提示词 | `internal/app/onboarding.go:18`（`onboardingSystemPrompt`）、`internal/app/onboarding.go:175`（`botProfileInstructionsDescription`）、`internal/app/onboarding.go:186`（`set_bot_profile` 工具） |
| 共享说话规则 | `internal/app/prompt_budget.go:71`（`voiceGuidance`） |
| 实现提交 | `c5ab24f` prompt: personas describe who, not how to format; no examples in prompts（10-15 12:24）；合并 `dbc62ea` fix/persona-generator-no-examples |
| 守护测试 | `internal/app/prompt_budget_test.go:325` `TestScopePrincipleIsPresentAndExampleFree`（检查范围原则里没有引号、`e.g.`、`for example`、`such as`、`but not`、`rather than` 等例句和对照写法）；`internal/app/deliver_with_assumptions_test.go:267` `TestRouteAndConfirmationPrinciplesArePresentAndExampleFree` |
| 评测 | `eval/personas.json`、`eval/rubric.md`（评分时也"不看特定词句"，只按原则判断） |
| 记录 | `docs/agent-plan/wave-notes.md:87`（Next wave 第 1 条 Human-likeness pass 1） |
| 首个仍存在的含此改动的标签 | `v0.1.4-rc.26`（更早的 rc 标签已按 D25 清理） |
