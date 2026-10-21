# 0007 Bot 的职责范围是"主动负责的事"，不是拒绝闲聊的理由

> 编号 D07，索引见 [README](README.md)。

## 决定

Bot 人设里的"范围"只表示它负责什么、会主动去做什么，不限制聊天。别人问一个范围外的日常问题，它照常直接回答，给出一个普通人的看法，不提"这不在我的范围内"。只有问题确实需要它没有的专业知识时，才说明限制。owner 的原话："闲聊照常说"。

## 日期

2026-10-19（Pacific）。合并于 10-19 14:52（`70fea8b`）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 有分工的 Bot 会拒绝范围外的日常问题，还在末尾加上"这类事来找我"式的推销。原因是生成器把范围写成了聊天的边界，运行时的规则里也没有相反的说法（提交 `4a27d5c` 说明）。
- 这和 [0001](0001-persona-generator-no-examples.md) 同一类问题：毛病出在生成器写出的人设和共享规则，不在某个 Bot。

## 理由

- 一个有分工的同事，被问到午饭吃什么也会正常回答。范围是责任和主动性，不是聊天的边界。
- 按 0001 的做法，同时改共享规则和生成器两处，不改单个 Bot；原则写成抽象的话，不带例句。

## 影响

- 共享说话规则加了一句范围原则（`internal/app/prompt_budget.go:71` `voiceGuidance` 最后一句："Your persona's scope sets what you own and pursue on your own initiative; it places no limit on conversation. …"）。
- 生成器（`internal/app/onboarding.go:175` `botProfileInstructionsDescription`）要求范围只写成"它负责并主动做的事"：不写排他措辞，不写不负责什么，不写如何处理范围外的问题；用户说的限制要改写成对那个领域的负责。onboarding 提示词同步（`internal/app/onboarding.go:18`）。
- 测试 `TestScopePrincipleIsPresentAndExampleFree`（`internal/app/prompt_budget_test.go:325`）检查三处都含 scope 措辞、关键短语都在，并且范围原则里没有引号、举例和对照写法。
- 评测加了 `scope_refusal` 判定（`eval/rubric.md`）和探针 `dm_offscope_casual`、`group_offscope_casual`（`eval/probes.json`）。
- 私聊系统提示词上限从 5300 调到 5500 字符（提交 `4a27d5c` 说明）。
- 已存在的旧 Bot 人设里如果有排他措辞，不会自动改写（待确认：未见批量迁移）；共享规则那一句对所有 Bot 生效。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 共享规则 | `internal/app/prompt_budget.go:71`（`voiceGuidance`） |
| 生成器 | `internal/app/onboarding.go:18`、`internal/app/onboarding.go:175` |
| 测试 | `internal/app/prompt_budget_test.go:325` `TestScopePrincipleIsPresentAndExampleFree` |
| 评测 | `eval/rubric.md`（`scope_refusal`）、`eval/probes.json`、`eval/personas.json`、`eval/run_eval.py` |
| 提交 | `4a27d5c` Persona scope is responsibility and initiative, not a refusal to chat；`3d387cd` Scope wording: direct answers with no scope remark; generator drops exclusivity；合并 `70fea8b` fix/persona-scope-is-not-a-refusal |
| 首个仍存在的含此改动的标签 | `v0.1.4-rc.26` |
