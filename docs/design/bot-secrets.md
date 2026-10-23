# Bot 级密钥（B3，已实现，待发版）

问题：B3（`docs/design/ui-issues.md`）。owner 2026-10-23：要用安全输入框收密钥，并单独保存在 Bot 级别的 Secrets 里。

现状：`request_secret_input` 的引用只在当次 run、15 分钟内有效；`use_secret_input action=env` 把值写成共享电脑上的 `/workspace/.tofi-env/NAME`，所有 Bot 都能读（`internal/app/secrets.go`，apply 返回 `scope: shared_computer`）。设置里的密钥存在加密库 `vault.json`，记录已有 `bot_id` 字段（`secretRecord`）。

对标：GitHub Actions（只在用到的步骤注入环境变量、日志打码、不落盘）；1Password `op run`（执行时注入进程）；OpenAI Codex 云端（密钥只在准备脚本可用，Agent 开工前移除）；Claude Code（设置文件里的环境变量，不按 Agent 分库）。

**已定**

1. **存储复用现有加密库，不另起一套。** 记录带 `bot_id` 表示属于哪个 Bot。
2. **隔离级别 A：每条命令临时注入，不落盘，加打码。** 只有所属 Bot 跑命令时才把它注入那一条命令的环境；电脑上不留文件；输出和对话记录里出现原文自动打码；别的 Bot 用工具看不到、用不了。已知限制：同一台电脑、同一系统用户，恶意进程理论上能在命令运行期间读别的进程环境——软隔离。需要防 Bot 之间互相偷时再做每个 Bot 一个系统用户（B 档）。
3. **收。** 安全输入卡片多一个勾选“保存给这个 Bot，以后不用再输”和名字（变量名规则同设置页）。勾选由用户在卡片上做，Bot 不能替用户决定；不勾则和现在一样只用这一次。
4. **用。** Bot 跑命令时声明要用的密钥名字，系统只在这一条命令里注入。每轮上下文只列出它有哪些密钥的名字，从不给值。
5. **管。** Bot 设置加“密钥”一栏：名字、保存时间、最后使用时间，可删除、可替换，值永不显示。设置页现有环境变量仍是全局共享；两者同一个加密库，范围不同。
6. **同名时 Bot 自己的优先**（局部覆盖全局）。

## 已实现（分支 feat/b3-bot-secrets）

- **存储**：同一个加密库，记录 `Kind="bot_env"`、`BotID`、`Target`=变量名、`CreatedAt`、新字段 `LastUsedAt`（`internal/app/secrets.go` 的 `secretRecord`）。不用 `env`：设置页列表、重名规则和便携导出都按 `env` 选记录，Bot 密钥不能混进去。设置页 `/api/computer/credentials` 的列表和按 id 操作都排除 `bot_env`。重启保留（只清带 RunID 的记录）。删除 Bot 时一并清除（`deletion.go`）。
- **收**：`request_secret_input` 多一个可选参数 `suggested_name`（只是建议，工具 schema 拒绝其他多余参数，Bot 没有任何保存开关）。卡片上的“保存给这个 Bot”开关和变量名由人填，提交时 `POST /api/questions/{id}/answer` 带 `save_as`；只有 `AnswerUserFormSaving`（人答、私密输入卡、名字过 `allowedSecretEnv`）才会写 Bot 记录，同名同 Bot 覆盖。不勾则与以前完全一样。答复里只多一个 `saved_as`（名字，不含值）。
- **用**：唯一入口是 `computer_shell` 的 `secrets: [NAME]`。主机在派发那一刻把值放进这一条命令的环境（guest `shell.exec` 新增 `env` 字段，只对该子进程生效，名字过 `configuredEnvName`），电脑上不写文件；命令文本、恢复身份、工具参数里都没有值。Bot 自己的优先，没有才回退设置页同名全局凭证；别的 Bot 的记录按 `BotID` 在服务端过滤，拿不到。未知名字返回工具错误并列出可用名字。成功使用会写 `last_used_at`（只写 Bot 自己的记录）。终端工具（`computer_terminal`）不支持注入。
- **打码**：命令的输出（原文和 JSON 转义形式）在 `microVMTools` 的派发处替换成 `[secret masked]`，早于模型、对话记录、事件、检查点和日志；同一次 run 之后的所有 `computer_*` 工具结果也继续打码（命令可能把值写进文件再读）。已知限制：命令自己对值做了编码（base64、拆分）后输出，不在打码范围，属于“软隔离”（见上面隔离级别 A）。
- **知**：run 上下文里有一条 `[saved secrets]` 备注，只列名字，上限 400 字符，超出写“and N more”；没有密钥就不出现，系统提示词不变。
- **管**：`GET /api/bots/{id}/secrets`（名字、创建时间、最后使用，不含值）、`PUT /api/bots/{id}/secrets/{NAME}`（替换，仅限已有名字）、`DELETE`。走现有的 owner 会话鉴权。UI：Bot 设置页的“密钥”一栏（`ui/src/BotSecrets.tsx`）。Bot 没有读、列值、删的工具。
- **版本**：guest 要带新的 `env` 字段才能注入；旧 guest 会拒绝未知字段，注入失败而不是静默不注入。

文件：`internal/app/bot_secrets.go`、`bot_secrets_test.go`、`secrets.go`、`user_forms.go`、`questions.go`、`microvm.go`、`context.go`、`deletion.go`、`app.go`；`internal/computer/guest/actions.go`、`guest_test.go`；`ui/src/BotSecrets.tsx`、`bot-secrets.css`、`QuestionCard.tsx`、`question-card.css`、`questionTimeline.ts`、`userForm.ts`、`ContextPanels.tsx`、`locales/*/{tasks,bots}.json`、`scripts/test-user-form.mjs`。
