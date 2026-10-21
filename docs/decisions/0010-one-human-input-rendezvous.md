# 0010 向人要输入只走一个机制：questions

> 编号 D10，索引见 [README](README.md)。

## 决定

Bot 向人要任何输入（提问、审批、网页表单、单个密钥），都统一走 questions 这一个会合点（rendezvous）。私密输入（密钥）也并进 questions，不再有单独的机制。人看不到的隐藏会话里，拒绝所有需要人输入的卡片。

## 日期

2026-10-19（Pacific）。合并于 10-19 15:38（`73cbfe2`）。

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

- 合并前，私密输入（`request_secret_input`）有自己的一套：工具里有一个 350 ms 的阻塞轮询循环、10 分钟硬过期、单独的 `/api/secret-inputs` 路由，界面上是单独的 `ui/src/SecretInputCard.tsx` 和它自己的轮询（提交 `07b37c5`、`50d26fb` 说明）。同一件事和 questions 是两条路。
- 隐藏会话（Bot 之间的内部运行）里没人能回答，发出卡片只会让运行一直挂着。

## 理由

- 一个机制只需要做对一次：暂停运行、重启后还在、会过期、界面统一由 `QuestionCard` 显示。
- 隐藏会话里直接拒绝，比挂起等一个不会来的人好；隐藏运行要找人，只能通过它的回复回到派活的 Bot 或会话。

## 影响

- 所有 Bot 对人的提问都经过 `internal/app/questions.go`：通过 `runtime.SuspendForUserInput` 和 `run_input_waits` 挂起运行，重启后仍在，默认 30 分钟过期，界面是 `ui/src/QuestionCard.tsx`（`AGENTS.md` "Asking a human (one rendezvous)" 一节）。
- 模型工具：`ask_user_question`、`request_approval`、`ask_user_form`（网页字段；密码字段封进保险库，返回绑定来源的 `secret_ref`）、`request_secret_input`（没有网站的单个密钥：实际上是存一个只有一个密码字段的表单问题；它的 `secret_ref` 绑定运行、Bot 和会话，15 分钟有效，只能被 `use_secret_input` 用于 env/ssh/shell_exec，绝不会被输入到网页里）。
- 不允许新增第二种机制：工具里不轮询、不加单独的 HTTP 路由、不加回调钩子。密码只写进保险库（`AnswerUserForm`），不进 SQL、事件或结果（`AGENTS.md` 同一节）。
- 以上所有工具和 MCP 审批闸门，在人看不到的会话里都拒绝：`requireHumanPresent`（`internal/app/questions.go:382`），结果码 `conversation_not_user_visible`。
- `request_secret_input` 保留对模型的名字，但变成一个只有一个密码字段的表单问题的薄封装；阻塞轮询、10 分钟硬过期和 `/api/secret-inputs` 路由删除（提交 `07b37c5`）。删除了已不用的 ask 工具（合并标题 "dead ask tool removed"）。
- 顺带修了一个锁顺序问题：`AnswerUserForm` 先取数据库连接再取保险库锁，旧顺序会和导入导出事务形成死锁环（提交 `07b37c5`）。
- 例外：邮件草稿（`prepare_email`）仍是单独的卡片路径（`AGENTS.md` 同一节）。
- 后来的接管卡片（[0011](0011-handover-cards.md)）就是在这个会合点上加的新问题类型，没有另起机制。
- 10-16 已在生产上验证私密输入端到端：测试值在 154 张表里 0 处出现，日志 0 行，数据文件 0 处（`docs/agent-plan/wave-notes.md:95` 第 11 条；这是合并前旧实现的验证，合并后的生产验证待确认）。

## 相关代码与文档

| 类别 | 位置 |
|---|---|
| 规则说明 | `AGENTS.md` "Asking a human (one rendezvous)" |
| 会合点 | `internal/app/questions.go`（`requireHumanPresent` `:382`） |
| 网页表单与私密输入 | `internal/app/user_forms.go`、`internal/app/secrets.go`、`ui/src/userForm.ts` |
| 界面 | `ui/src/QuestionCard.tsx`；删除 `ui/src/SecretInputCard.tsx`、`ui/src/secret-input-card.css` |
| 测试 | `internal/app/questions_visibility_test.go:17` `TestHumanInputToolsRefuseHiddenConversation`；`internal/app/secret_input_fold_test.go`（如 `TestRequestSecretInputSuspendsAcrossRestartAndYieldsWorkingReference`、`TestPrivateInputIsNotReachableThroughTheWebpageFormTool`、`TestPrivateInputReferenceIsNeverTyped`、`TestSecretToolsRegisterOnlyUseSecretInput`）；`ui/scripts/test-user-form.mjs` |
| 合并提交 | `73cbfe2` refactor/one-input-rendezvous（内含 `07b37c5` secrets: fold request_secret_input into the form rendezvous、`50d26fb` ui: drop SecretInputCard、`ae96cd7` questions: refuse ask/approval/form in a conversation no person can see、`a4c0c4a` review fixes） |
