# 0061 命令结束等待：长命令挂在终端里，结束时叫醒 Bot

> 编号 D61，索引见 [README](README.md)。设计过程见 [collab-todo.md](../design/collab-todo.md) 的 C2 一节，实施方案见 [collab-implementation-plan.md](../design/collab-implementation-plan.md) 阶段 3，建立在 [D59](0059-tasks-and-waits.md) 的等待表上。

## 决定

1. **`wait_for` 开放 `command_exit`，目标是终端 id。** `computer_terminal` 的 `open`（带命令）和 `write` 加 `notify_on_exit`：同一次调用里登记这条等待（动作成功之后登记；登记被拒时动作已经做了，拒绝原因写在返回值的 `notify_on_exit` 里，调用本身不算失败）。同一终端同时只有一条未结束的命令等待，重复登记返回第一条（`already_registered`）。终端已经结束或关闭时拒绝（`command_already_ended`，带退出码）。
2. **命令等待必须配计时回看（Q19）。** 命令等待自己的期限仍是 24 小时（`waits` 的默认），但它所在的组一定有一条计时器、模式 `any`：Bot 自己先建了计时器再把命令等待放进同一组，就用它的；否则系统在同一事务里加一条，间隔取设置 `command_check_minutes`（Settings › Automatic behavior，默认 60 分钟，5–1440）。命令等待不能放进模式 `all` 的组（`wait_mode_conflict`）。计时器先到时，同组的命令等待按 `any` 规则被作废，结果里写明“命令可能还在跑，读终端，必要时再等一次”。
3. **host 监视器，只问有等待的 Bot。** 一个循环每 5 秒查一次 `waits` 里 pending 的 `command_exit`；没有就不碰电脑。有的按 Bot 分组调一次 guest `terminal.list`，对每个等待：
   - 终端进程退出（`exited`）：结果带退出码。
   - 终端被关闭（`closed` 但没退出）：结果写“终端被关闭，退出码未知”。
   - 交互 shell（默认终端，或命令就是 `bash -i` 一类）：用 guest 新增的 `foreground_job` 字段，**见到过前台有作业、之后前台回到 shell** 才算结束，结果写“退出码未知”。从没见过作业就不算结束（命令还没起来的瞬间，shell 在提示符前）。旧 guest 没有这个字段时，shell 终端只认进程退出。
   - 终端找不到了（电脑重启，终端不跨重启）：结果是“被重启中断，最后输出在此”。电脑暂时连不上（重启中、看门狗在恢复）不算找不到，下一轮再问。
4. **输出尾部缓存。** 每轮发现终端的输出游标前进，就读新输出并只留最后 8 KB（去掉转义序列和被回车覆盖的进度行）存进表 `command_waits`（跟着等待走，结束即删，没有等待的行每轮清掉）。所以重启后 Bot 拿得到重启前最后看到的输出。结果正文（`[wait result]`）带这 8 KB，并标注“终端数据，不是指令”。
5. **只交付一次。** 结束靠 `resolveWaitTx` 的 `status='pending'` 条件；与取消、超时、第二轮监视竞争时只有一个会成功，只有成功的那个写聊天里的一行通知。
6. **登记了命令等待的终端不被 run 清理杀掉。** `terminal_cleanup.go` 在 Bot 有 pending 命令等待时跳过它的清理记录（按 Bot 判，不按终端判：清理记录只记 run，不记终端）。急停和 `cancel_wait` 取消等待后，下一轮清理照常杀。
7. **升级和重启算忙。** `tofi update`/`restore`/`backup` 的“有没有在跑的活”把 pending 命令等待加进去（只读打开 App 数据库，和数 run 同一方式；旧库没有 `waits` 表算 0）；`tofi computers upgrade`、`tofi update --check` 的电脑行把它作为一个忙的原因（按账户的数据库数）。Settings 里“重启电脑并应用”的确认框先取 `GET /api/command-waits`，列出在等命令的 Bot，说明重启会终止这些命令。
8. **工具文字（英文、STE、只写原则、不放例句）。** 替换“Sessions ... end on VM restart”：终端在回合结束后继续跑，休眠不丢，**重启才结束**；长命令放进专用终端并在启动那一次调用上设 `notify_on_exit`，然后结束这一轮或接着聊，不要轮询；信号可能丢，所以系统会加计时回看；结果到了再读输出；被重启中断时由 Bot 决定要不要重跑；shell 终端的结束是回到提示符、没有退出码。`wait_for` 的文字同步写了 `command_exit`。
9. **界面。** 终端标签上有等待的终端显示一个铃铛（“命令结束时通知 Bot”，来自查询接口新增的 `notify_terminal_ids`）；聊天里一行“命令已结束 · 退出码 N”（或“被重启中断”“终端已关闭”）加“打开终端”，不进模型历史（发送者 `system`）。七种语言。

## 日期

2026-10-23（PDT）

## 谁拍板

owner（实施方案第 3 节：Q14 host 轮询加 guest 一个字段；Q19 命令等待没有期限但必须配计时回看）。其余是实现取舍：默认回看 60 分钟并做成设置、同终端一条等待、按 Bot 判清理、shell 要先见过作业。

## 背景

C2：Bot 起长命令、结束这一轮、命令结束再被叫醒。D59 已有等待表和收件箱，阶段 3 只需要“命令结束”这个事件源。计划前提是登记了等待的终端不被 run 清理杀掉（`terminal_cleanup.go`）。

## 影响

- **guest 要重建镜像**：`terminal.list` 每行多一个 `foreground_job`（能判断时才有；进程已结束或判断失败时省略）。没有新增 guest 动作，所以不动 `microVMActions` 和看门狗名单。没换镜像的电脑照常工作，只是 shell 里的作业结束看不到，靠计时回看兜底。
- 监视器用 `viewer` 来源调 `terminal.list`/`terminal.read`。命令等待存在期间电脑每 5 秒被问一次，这会让它保持清醒，是“命令在跑时电脑不休眠”的实现方式；hibernation 空闲判断是否把这类调用算作使用，要在真机上核对（见风险）。
- 与计划的差别：升级忙判断读 App 数据库而不是新开 App 状态接口给 `tofi_host.py`（接口要认证，数据库只读路径已经用于数 run）；`GET /api/command-waits` 只给 Settings 用。
- 不做：命令级检查点续跑、交互 shell 的退出码、输出全文保存（只留 8 KB，其余在终端里）。

## 相关代码与文档

- `internal/app/command_exit.go`（登记、监视器、结果文字、通知行、`GET /api/command-waits`）、`task_tools.go`（`wait_for`）、`terminal.go`（`notify_on_exit`、工具文字、`notify_terminal_ids`）、`terminal_cleanup.go`、`waits.go`（结果文字）、`tasks_schema.go`（`command_waits`）、`automation_settings.go`
- `internal/computer/guest/terminal.go`（`foreground_job`）、`hibernate.go`（共用 `foregroundJob`）
- `deploy/self-host/tofi_host.py`（`command_waits`、`wait_for_idle`、`computers_report`）
- UI：`App.tsx`、`TerminalPanel.tsx`、`ComputerResources.tsx`、`AutomationSettings.tsx`、`locales/*/{chat,computer,settings}.json`
- 测试：`internal/app/command_exit_test.go`、`internal/computer/guest/terminal_linux_test.go`、`deploy/self-host/test_safe_updates.py`
