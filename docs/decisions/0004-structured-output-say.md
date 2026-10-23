# 0004 结构化输出：用户看到什么，由 `say` 工具决定

> 编号 D04，索引见 [README](README.md)。

## 决定

Bot 让人看到的每一句话，都要通过一个工具 `say` 发出，`purpose` 取 `answer`（最终回答，结束本轮）、`note`（过程说明，折叠显示）或 `message`（运行中途的一条可见消息）。

1. 工具调用之间的普通文字是内部文字，不发到聊天里，只在"查看活动（View activity）"里能看到。
2. 模型没按协议走、最后只写了一段纯文字时，把最后那段纯文字作为回答发出，标记为 `implicit`，不额外多调一次模型。
3. 用工作区开关控制，rc 里默认打开。
4. 第 3 阶段把 `respond`（群里的回复）、`send_chat_message`、`complete_scheduled_task` 都并进 `say`。

完整方案见 [`docs/agent-plan/structured-output.md`](../agent-plan/structured-output.md)。

## 日期

2026-10-17 拍板 D1/D2/D3/D6（方案第 10 节原文 "Decided 2026-10-17 (owner)"）；10-18 确定第 3 阶段合并范围（D4）。第 3 阶段于 10-19 00:51 合并（`8d06d18`）。

## 谁拍板

owner（jackzhao）。方案第 10 节记录：D1(a)、D2(a)、D3(a)、D6(a) 由 owner 选定；D4(a)、D5(a) 按推荐采用（owner 授权小选项由执行方定）。

## 背景

- 以前 Bot 发出的内容是事后从自由文本里"猜"出来的：`<progress>` 标签、步骤旁白折叠、重复回答判定、委派说明折叠、定时结果前置、开头 @ 自动派活、没有最终回答时的兜底句、完全重复过滤，一共 8 条阈值式启发规则（H1-H8，方案第 1 节表格），每条都有自己的毛病。
- 默认模型（当时是 Haiku 5.5）不听文字规则，但会调用工具。第 0 阶段测得 Haiku 只有 7% 的回答走了结构化渠道（方案第 11 节）。

## 理由

- 把"可见"变成一个明确的工具调用，用一条确定的规则代替一堆猜测，不再靠阈值。
- 选项比较（方案第 10 节）：(a) 一个 `say` 工具，优于 (b) JSON schema 结构化最终回答、(c) 继续用标签和文字规则。
- 协议失手时直接发最后一段文字，而不是再提醒一次（多花一次模型调用）或直接判失败；用 `runs.final_origin` 记录来源，便于统计。

## 影响

各阶段（方案第 9 节，实现情况见第 11、12 节）：

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 | 埋点：`runs.final_origin`（`say` / `implicit` / `promoted` / `none`）；离线评测工具；main 上的基线数字 | 已合并 `31fb1cd`（10-17） |
| 1 | `say` 工具（`note` / `message` / `answer`），参数流式进草稿，按运行类型限制可用的 purpose；纯文字仍按旧规则发布（双轨），量遵守率 | 已合并 `d4ac003`（10-17，与第 2 阶段同一分支） |
| 2 | 切换：纯文字变内部，规则 1-3 生效，删除 H1（只保留剥标签）、H2、H3、H4、H7；工作区开关，rc 默认开 | 已合并 `d4ac003` |
| 3 | 合并：`respond(reply)`、`send_chat_message`、`complete_scheduled_task` 并进 `say`（`wake` 和 `sources` 放在 `say(answer)` 上）；开关打开时删除 H5、H6；Haiku 私聊强制工具调用（`forceSayFor`） | 已合并 `8d06d18`（10-19）。偏差：`nothing` / `wait` 没有并入，仍留在 `respond`（方案第 12 节 Decisions） |
| 4 | 退役扫描器和 `stream_assistant_turns` 写入；内部文字进活动视图；移除开关 | 未做。`stream_assistant_turns` 仍在写（`internal/app/stream.go:443`）；`final_origin` 在 Settings → Usage 的汇总也未做（方案第 12 节 "Still open"） |

- 实测（方案第 12 节）：第 3 阶段后 Haiku 5.5 私聊 100% 走 `say`，整体 86-88%（群聊没强制，辩论/委派轮次 33-50%）；Sonnet 5.5 95-97%。95% 目标只有 Sonnet 达到。
- 开关：`user_preferences.structured_output`，默认 1（`internal/app/preferences.go:56`）；同一项也在"自动行为"设置里（`internal/app/automation_settings.go:84`，键 `structured_output`），Haiku 私聊强制调用另有一项 `force_say_haiku_dm`（`automation_settings.go:85`，环境变量 `TOFI_FORCE_SAY`）。
- 关掉开关时，旧的 H1-H7 路径照旧，相关测试仍通过（方案第 12 节开头）。
- 后续决定建立在这上面：[0005](0005-every-mentioned-bot-answers.md)、[0006](0006-closed-group-exchange-gist.md)、[0008](0008-internal-text-never-in-chat.md) 都只在开关打开时生效。

## 补记（2026-10-22）：`say` 参数被写成纯文字

rc.36 上 `codex-gpt-5.6-terra` 把 `say` 的参数 JSON 当最终文字写出，规则 2 把它原样发进聊天。补充规则：结构化模式下，最终文字（去首尾空白后）恰好是一个含 `purpose` 与 `text` 的 JSON 对象，或只含该对象的围栏代码块时，只按 `say` 自己的输入 schema 判定（不看措辞）：通过则当作那次 `say` 调用执行（`internal/app/say_text.go`，走同一个工具，审查、sources、quiet 规则全部照旧），`runs.final_origin` 记 `say_text_recovered`；通不过则走现有的一次最终回答修复（`finalResponseRepaired`），不新增机制；修复用尽时，有效的 answer 发布其内文，其余一律不发布。夹在别的文字里的 JSON、不是该形状的 JSON 保持原样，仍按规则 2 当隐式回答。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 方案 | `docs/agent-plan/structured-output.md`（第 2 节协议、第 4 节失手规则、第 9 节阶段、第 10 节拍板、第 11-14 节实现记录） |
| `say` 工具 | `internal/app/say.go`（`sayTool` 在 `:337`，`forceSayFor` 在 `:628`）、`internal/app/say_stream.go` |
| 参数流式解码 | `internal/saystream/decoder.go` |
| 结束本轮 | `internal/runtime/runtime.go:150` `EndTurn` |
| 回答来源 | `internal/app/final_origin.go` |
| 群聊处置 | `internal/app/group_respond.go` |
| 开关 | `internal/app/preferences.go:32,56,115`；`internal/app/automation_settings.go:84-85` |
| 测试 | `internal/app/say_test.go`（如 `TestSayAnswerPublishesOnceEndsTheTurnAndTagsTheOrigin`、`TestSayMessageIsASegmentCappedAtThree`）、`internal/app/say_review_test.go`、`internal/app/phase3_test.go`（如 `TestRespondRefusesReplyUnderTheFlag`、`TestLeadingMentionIsOnlyAMentionWhenStructured`）、`internal/app/final_origin_test.go`、`internal/agent/structured_output_test.go`、`internal/agent/force_tool_use_test.go`、`internal/runtime/end_turn_test.go`、`internal/saystream/decoder_test.go`、`internal/provider/say_stream_test.go` |
| 评测 | `eval/run_eval.py`、`eval/probes.json`、`eval/rubric.md`；结果目录 `eval/results/structured-say-*`（被 git 忽略，不在仓库里） |
| 合并提交 | `c270835` 方案（10-17）、`31fb1cd` 第 0 阶段、`d4ac003` 第 1-2 阶段、`8d06d18` 第 3 阶段、`e52f786` wake 请求显示为交接行、`3b2cf35` 内部文字不进聊天、`b429a4e` 可能被完成审查拒绝的回答先压住不流式显示 |
