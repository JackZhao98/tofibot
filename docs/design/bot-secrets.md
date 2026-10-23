# Bot 级密钥（B3 设计，进行中）

问题：B3（`docs/design/ui-issues.md`）。owner 2026-10-23：要用安全输入框收密钥，并单独保存在 Bot 级别的 Secrets 里。

现状：`request_secret_input` 的引用只在当次 run、15 分钟内有效；`use_secret_input action=env` 把值写成共享电脑上的 `/workspace/.tofi-env/NAME`，所有 Bot 都能读（`internal/app/secrets.go`，apply 返回 `scope: shared_computer`）。设置里的密钥存在加密库 `vault.json`，记录已有 `bot_id` 字段（`secretRecord`）。

对标：GitHub Actions（只在用到的步骤注入环境变量、日志打码、不落盘）；1Password `op run`（执行时注入进程）；OpenAI Codex 云端（密钥只在准备脚本可用，Agent 开工前移除）；Claude Code（设置文件里的环境变量，不按 Agent 分库）。

**已定**

1. **存储复用现有加密库，不另起一套。** 记录带 `bot_id` 表示属于哪个 Bot。
2. **隔离级别 A：每条命令临时注入，不落盘，加打码。** 只有所属 Bot 跑命令时才把它注入那一条命令的环境；电脑上不留文件；输出和对话记录里出现原文自动打码；别的 Bot 用工具看不到、用不了。已知限制：同一台电脑、同一系统用户，恶意进程理论上能在命令运行期间读别的进程环境——软隔离。需要防 Bot 之间互相偷时再做每个 Bot 一个系统用户（B 档）。

**待讨论**：收密钥的流程（安全输入卡片上“保存给这个 Bot”）、Bot 设置里的密钥列表（删除、替换、最后使用时间）、Bot 怎么按名字用、和设置页共享环境变量的关系。
