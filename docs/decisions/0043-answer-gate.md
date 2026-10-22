# 0043 回答闸：自称“有缺口”的回答，在没查过之前先退回一次

> 编号 D43，索引见 [README](README.md)。

## 决定

1. **`say` 多一个必填字段 `resolution`**（枚举 `resolved` / `gap`），排在 `purpose` 之后、`text` 之前（Go 的 map 序列化按字母序，`purpose` < `resolution` < `text`）。说明写成原则：模型如实给自己的回答贴标签；`gap` 指不知道、没找到、向对方要信息、因缺信息而拒绝，或在请求取决于这个人说过的话时给了泛泛的回答。其他 `purpose` 一律填 `resolved`。
2. **闸在 `say(answer)` 里，和完成复核共用同一份“单次修复预算”**（`reviewed`）。同时满足这些条件，这条回答被当作可重试的瞬时拒绝退回一次（`answer_gap_check`，附一段私下的原则性提醒），草稿不展示给人，沿用完成复核的 `demote` 兜底：设置“答前先查”为开；标签是 `gap`；本次 run 没有任何查找动作；预算没花过。判定函数 `answerGateDue(enabled, label, lookedUp, budgetSpent)` 只读这四个值，不读回答文字；`TestGateDecisionReadsOnlyLabelAndToolRecord` 还检查源文件没有正则或字符串匹配。
3. **“查找动作”由工具记录决定**：`search_history`、`inspect_recent_runs`、`list_memory`、`web_search`、`web_fetch`、`read_attachment`、`computer_browser/desktop/terminal/action`，以及对非记账类工具的只读调用（身份风险 `observation`，例如连接的 MCP 只读工具）。成功还是失败都算“试过”。重启后的续跑从 `tool_activities` 恢复（`hasLookupAttempt`，MCP 只读调用在行里没有风险字段，恢复时不认，结果是多退回一次，不会丢回答）。
4. **正常路径零成本。** 标签先于正文到达（字段顺序），流式解码器（`saystream.Decoder.HoldForResolution`）在标签出现前压住 answer 的正文；标签是 `resolved` 就立刻按原样流式输出，只有 `gap` 且闸会触发的回答才被压住，退回时草稿从未进入聊天。已解决的回答没有额外模型调用、没有额外查找、没有额外延迟。若模型把标签写在正文之后，正文在标签出现时一次放出。
5. **范围**：私聊、群聊、定时任务（定时任务里先试再答；文本路径的回执修复也会花掉这份预算，避免在 reserved repair 里再退回一次）、隐藏会话，规则与完成复核相同。非结构化输出（`structured_output` 关闭）没有 `say`，也就没有标签，不适用。
6. **设置 D15**：设置 › 自动行为 › 检查，键 `answer_gate`，默认开，7 种语言文案。
7. **提示词预算**：系统提示词不变；工具定义里每次结构化请求多一个字段，`resolution` 的 schema 426 字节（`TestSayLabelIsRequiredInTheSchema` 设上限 430）。

## 与 Anthropic 指引的关系

这是 Anthropic《Building effective agents》里 evaluator–optimizer 工作流的最省钱形态：评估者不是第二次模型调用，而是模型自己给的结构化标签加一个确定性检查（工具记录）；优化者就是同一个模型带着一段固定提醒再答一次。偏离之处与原因：

- 循环最多一轮（指引是循环到满足标准），因为每次退回都是一次完整的带上下文的调用，owner 要的是“别白白变慢变贵”；
- 评估对象是“试过没有”，依据是环境里的事实（工具记录），而不是对输出质量的打分，这是指引里“每一步从环境取得真实信号”的做法；
- 反馈是固定的原则性提醒，不是生成的批评；
- 评估者就是生成者本人，所以标签诚实与否是风险。**升级路径**：如果发现模型把该标为 `gap` 的回答标成 `resolved`（见下面的实测，泛泛作答就是这样漏掉的），再加一个便宜的评审，做法参照 Managed Agents 的 outcomes grader（小模型、只在没有查找记录时、只看标签与请求是否相称）。目前没加。

## 日期

2026-10-21（太平洋时间）。

## 谁拍板

协调者按 owner 的原话设计（Bot 的“笨”回答让人又好气又好笑，得追问；要一个会对自己的非答案不满意、继续查的守门人；不要每次都查、不要变慢变贵）。owner 的常设方向：TOFI 以 Anthropic 公布的 agent 实践为基准。具体阈值和字段名是实现决定，待 owner 事后确认。

## 背景

完成复核（`taskCompletionFinalReview`）只在 run 做过实质工具工作时才运行，纯对话回答，也就是 owner 抱怨的这种，没有任何闸。工具里有 `search_history`，Bot 不用。

## 理由

- “永远先查历史”会让每条回复都更慢更贵；让模型先自己说“这条是不是缺口”，只有缺口才付代价。
- 判定只看结构（标签和工具记录），不看文字：owner 明确不要关键词或短语检测。

## 实测（本机，Sonnet 5.5 做 Bot，Sonnet 5.5 评分，全新 Bot，每项 3 轮；“前”是 `main` 的二进制，“后”是本分支）

种子历史直接写进 messages 表：事实在最前，后面 42 条各约 1500 字符的用户消息把它挤出 32000 字符窗口（Bot 自己的旧回复会被压成一行，所以填充必须是用户消息）。任务集在测试员的草稿区，不进仓库。

| 任务 | 前：完成 / 答错或无据 / 查过 | 后（第一版说明） | 后（改过 `gap` 的说明） |
|---|---|---|---|
| 回忆：问猫粮该避开什么（事实在被挤出窗口的旧消息里） | 0/3，1/3，0/3 | 0/3，2/3，0/3 | 1/3，0/3，1/3 |
| 回忆：问报名截止和地址 | 3/3，1/3，3/3 | 3/3，0/3，3/3 | 未重测 |
| 网上可查（美联储最近一次决议） | 3/3，0/3，3/3 | 3/3，0/3，3/3 | 未重测 |
| 守卫：问候 | 0/3（见下），0/3，0/3 | 0/3，0/3，3/3 | 0/3，1/3，3/3 |
| 守卫：直接问题 | 3/3，0/3，0/3 | 3/3，0/3，0/3 | 未重测 |

- **没解决的**：主要的失败样子是模型把泛泛的建议当成“已解决”交出（“我这儿没有你猫的记录，给你个通用版”这类），标签是 `resolved`，闸不会动。第一版说明下 0/3 查；把“请求取决于这个人说过的话时给泛泛回答也算缺口”写进说明后 1/3 查。标签诚实度是这个设计的上限，升级路径见上。
- **守卫**：直接问题 1.00 次模型调用、没变慢（10.0 s 对 12.4 s，网络噪声量级）。评测器在问候后会补一句“go ahead”（没有逐轮拆开核对问候本身有没有触发，只看到整个任务的调用数和查找），Bot 回答说不知道指什么，这是真缺口，被退回后去查了历史和记忆（3 次都查），模型调用从 2.0 变 4.0，耗时 18.9 s 变 24.0 s。这是闸按设计动作的代价，不是正常路径的开销，但它不是零。
- 每次评测费用（含建 Bot）：前 $2.89，后 $3.05，改说明后重测 $1.47。样本很小（n=3），上面的数只能说方向。

## 影响

实施状态：已实现，在分支 `feat/answer-gate`，未合并、未发版。

代码：`internal/app/answer_gate.go`（判定、提醒、查找工具表、续跑恢复）、`internal/app/say.go`（schema、字段、退回）、`internal/app/say_stream.go`、`internal/saystream/decoder.go`（压住正文直到标签出现）、`internal/app/app.go`（工具记录、预算共用、定时任务回执修复花预算）、`internal/app/automation_settings.go`（`answer_gate`）、`ui/src/AutomationSettings.tsx` 和 7 种语言的 `settings.json`。测试：`internal/app/answer_gate_test.go`、`internal/saystream/decoder_test.go`；现有的 say 测试都加了 `resolution`。

## 相关

[D04](0004-structured-output-say.md)、[D09](0009-reliability-principles.md)、[D15](0015-automatic-behaviour-settings.md)、[D40](0040-lookup-at-the-point-of-decision.md)。
