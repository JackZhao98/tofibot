# 0009 可靠性原则：带假设交付、写=交出成稿、群里只给最终结果、核实要真查

> 编号 D09，索引见 [README](README.md)。

## 决定

五条原则，适用于所有 Bot：

1. **带着说明的假设交付**：信息不全但能继续时，按合理假设做完，用一句话说明假设，然后交付；不要用一串问题收尾。只有缺口真正卡住工作时才问，而且只问一次。
2. **"写"就是交出成稿**：被要求写或起草一样东西，就在聊天里给出完整成稿；发送永远是人自己的一步。
3. **群里只显示最终合并的结果**：几个 Bot 合作产出一个结果时，人只看到一条最终结果，中间给下一位的材料不进聊天。
4. **说"已核实/最新"必须在本次运行里真查过**：没查就说明是凭记忆。
5. **不认识的名字先查再问**：碰到陌生的名字、作品、术语，先搜索，不要先问人那是什么。

## 日期

2026-10-19（Pacific）。第 5 条的私聊部分早在 10-16 已上线（`383b5c8`），10-19 扩展到群聊。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里；第 5 条有 owner 10-16 的原话"不懂的事情不会想着去搜"（`docs/agent-plan/wave-notes.md:99`）。

## 背景

- Bot 常在能做完的时候停下来列问题让人回答；被要求"写一封邮件"时去找发送渠道而不是交出文字；群里多个 Bot 合作时，中间稿和最终稿都出现在聊天里；没有调用任何查询工具却说"我核实过"；遇到不认识的名字反问用户。这些都是 owner 在真实使用里遇到的（具体对话不在仓库里，待确认）。
- 评测里专门有对应判定：`verified_without_lookup`、`asked_instead_of_lookup`（`eval/rubric.md`）。

## 理由

- 可靠的同事会先把事做完并说清前提，而不是把问题推回来。
- 发送是对外、不可逆的动作，应由人决定；写作任务的交付物是文字本身。
- 人要的是结果，不是 Bot 之间的往来。
- "已核实"是对事实的承诺，没有查询记录就不能这么说。

## 影响

- 第 1、2 条写进共享的任务完成规则 `taskCompletionGuidance`（`internal/app/context.go:204`："proceed on sensible assumptions, state them in a line, and deliver…"、"sending is only ever the person's step"）；`ask_user_question` 的说明改为只在"没有合理的可说明假设"时使用（测试 `TestSharedGuidanceDeliversWithAssumptionsAndWritesTheText` 检查）。10-20 又把假设原则放进每个决策点（`ask_user_question`、`say`、人设生成器，提交 `f342b83`，10-20 09:31；测试 `TestAssumptionPrincipleSitsAtEachPointOfDecision`），并补充"指令本身就是确认，先做再宣布，报阻塞前先试遍所有途径"（合并 `6bc0103`）。
- 第 2 条的配套：`prepare_email` 可以接受没有收件人的草稿，卡片提示补收件人，收件人为空时发送按钮禁用、服务端拒绝（`internal/app/mail_drafts.go`、`ui/src/MailDraftCard.tsx`；测试 `TestMailDraftWithoutRecipientIsEditableAndCannotBeSentUntilToExists`）。
- 第 3 条：同一条消息被 @ 的几个 Bot（见 [0005](0005-every-mentioned-bot-answers.md)）中，给后一位的材料放在折叠的 `say(note)` 里由后一位读取，只有完成的那位发出结果（`internal/app/group_dedupe.go:406` 的 `[co-addressed]` 说明："only the member that completes it publishes it"）。10-20 加了兜底：最后一位失败时，把最新的折叠部分提升为结果，不会以沉默收场（提交 `772750b`）。
- 第 4、5 条写进私聊和群聊的提示词（`internal/app/context.go`；测试 `TestVerifiedClaimsNeedALookupInDMsAndGroups` 检查 "rests on a lookup made in this run"、"Search unfamiliar names or terms before asking about them, in groups as in DMs" 等措辞）。10-20 起 Anthropic 模型总能用服务端网页搜索（合并 `97cfa6a`，`internal/app/web_search.go`、`internal/provider/anthropic.go`），设置项 `web_search` 默认开（`internal/app/automation_settings.go:86`）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 共享规则 | `internal/app/context.go:204`（`taskCompletionGuidance`） |
| 群内单一结果 | `internal/app/group_dedupe.go`（`coAddressedContext` `:368`，说明文字 `:406`） |
| 邮件草稿 | `internal/app/mail_drafts.go`、`ui/src/MailDraftCard.tsx`、`ui/src/mail-draft-card.css`、`ui/src/locales/*/mail.json` |
| 网页搜索 | `internal/app/web_search.go`、`internal/provider/anthropic.go`、`internal/runtime/export_web_search.go` |
| 测试 | `internal/app/deliver_with_assumptions_test.go`（`TestSharedGuidanceDeliversWithAssumptionsAndWritesTheText`、`TestMailDraftWithoutRecipientIsEditableAndCannotBeSentUntilToExists`、`TestCoAddressedNoteStatesTheSingleResultPrinciple`、`TestCoAddressedInputMemberEndsSilentAndOwnerSeesOneMessage`、`TestCoAddressedResultNeverEndsInSilenceWhenTheFinalMemberFails`、`TestAssumptionPrincipleSitsAtEachPointOfDecision`、`TestRouteAndConfirmationPrinciplesArePresentAndExampleFree`）；`internal/app/verification_guidance_test.go`（`TestVerifiedClaimsNeedALookupInDMsAndGroups`、`TestGroupFramingKeepsLookupBeforeAsking`）；`internal/app/web_search_test.go`、`internal/provider/anthropic_websearch_test.go` |
| 评测 | `eval/rubric.md`（`verified_without_lookup`、`asked_instead_of_lookup`、`asked_back`）、`eval/probes.json` |
| 合并提交 | `383b5c8` fix/look-up-unknown-names（10-16）；`977b1ec` fix/deliver-with-assumptions（10-19 19:09，内含 `6cf218a`）；`55c5515` fix/verified-needs-lookup（10-19 19:20）；`97cfa6a` fix/reliability-step2（10-20 10:10）；`6bc0103` fix/act-dont-ask（10-20 10:36） |
