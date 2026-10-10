# Run Resume: 任务断点续跑与推理保留

Status: design only, 2026-10-10. 未实施，未改代码。所有"现状"均读自代码并标出 file:line。

目标：Bot 任务（用户对话触发的 run、定时/自动化 run）遇到中断不丢、自动续跑；一个任务内部，模型的推理过程不被悄悄扔掉。

---

## 1. 一页结论（给 owner）

### 用户会看到什么变化

| 场景 | 现在 | 之后 |
|---|---|---|
| 浏览器断网 | 服务端照跑；页面显示"实时连接中断 · 正在重连"，重连后补齐事件 | 不变（已经做对了，见 §2 案例 1），只补一处：重连超过 30 秒时给一句"连接已恢复"。 |
| 服务器连不上模型 | 重试约 30 秒，然后任务直接"失败" | 任务挂起，聊天里一句"模型服务暂不可达，任务已挂起，网络恢复后自动继续"；恢复后自动接着跑；30 分钟没恢复才停，并写清"做完了什么/没做什么/停在哪"。 |
| 工具调用途中断网（MCP、邮件、Notion） | 已有"效果不确定，先核实再动"的机制 | 不变，只补：只读型 MCP 工具可直接重做；有效果的照旧先核实。 |
| 服务器崩溃/断电/kill -9 | 重启后任务标"已中断"，草稿作废，要人手动重来 | 重启后从最后一个检查点自动继续；聊天里一句"服务已重启，任务从上一个检查点继续"。 |
| `tofi update` 升级 | 有任务在跑就拒绝升级（或 `--force` 打断） | 任务在下一个工具边界停住、存档、升级、自动续跑；聊天里一句"服务已更新，任务从上一个检查点继续"。 |
| 推理保留 | 任务中途每隔一段会把旧推理清掉（agent.go:2171） | 任务内推理全程保留；只有总结压缩时才让模型自己写下"当前思路"再压缩。 |
| 定时任务错过时间 | 已经是"补跑一次"（schedule.go:865） | 补跑时在输出里说明"原定 09:00，因服务离线于 09:37 执行"。 |

### 需要 owner 拍板的事（推荐项在前）

**D1. 等网络的上限**
- (a) 30 分钟 — 推荐。够覆盖路由器重启、ISP 抖动；超过这个时长继续等只会让用户以为任务挂死。
- (b) 10 分钟
- (c) 2 小时

**D2. 崩溃后自动续跑的次数上限（同一个 run）**
- (a) 3 次 — 推荐。防止"某个工具把服务搞崩 → 重启 → 续跑 → 再崩"的死循环；第 4 次标失败并说明"反复中断"。
- (b) 1 次
- (c) 不限

**D3. 升级时等任务到边界的时长**
- (a) 最多 120 秒 — 推荐。docker stop 宽限是 180 秒（tofi_host.py:1157），留 60 秒给关库。等不到边界的任务按崩溃路径处理（反正有检查点）。
- (b) 30 秒
- (c) 不等，直接按崩溃路径

**D4. Anthropic 模型下的"轻量压缩"（microCompact）**
- (a) 关闭，换取推理全程保留 — 推荐。原因见 §3.6：Anthropic 的 thinking 签名绑定前缀，改任何旧工具结果都会让后面的 thinking 全部被服务端丢弃（anthropic.go:305-310 的注释已写明）。关掉后靠入口截断（24000 字符，agent.go:82）和整体压缩兜底。
- (b) 保留轻量压缩，接受推理丢失
- (c) 先测量再定（§3.6 有测量步骤；但测量只能告诉我们"推理占多少"，改不了签名绑定这个事实）

**D5. 升级时有任务在跑**
- (a) 默认直接升级（drain + 续跑），`tofi update` 打印"N 个任务将在更新后续跑" — 推荐。
- (b) 保持现在的"拒绝，除非 --force"

**D6. 定时任务迟到**
- (a) 补跑一次，输出里说明迟到 — 推荐（owner 倾向）。
- (b) 跳过本次

---

## 2. 五种中断逐案对照

### 案例 1：用户浏览器断网

**现状（已验证）**
- 服务端 run 不依赖浏览器连接；流式文本每 50ms 落库到 `stream_drafts`（stream.go:26, 88-140），每条 delta 带 `revision` 并写进 `events` 表（stream.go:131-135）。
- SSE 接口按 `after` 游标或 `Last-Event-ID` 从 `events` 表回放（app.go:3918-3922, 3958-3968；Events() app.go:2094-2095 `id>?`）。
- 前端：断线后指数退避 1s×2ⁿ 封顶 10s 重连（App.tsx:1301-1312），10 秒连不上算失败重来（App.tsx:1330）；重连带游标（api.ts:108-116）；delta 按 `revision` 去重（App.tsx:1254-1258）；横幅文案 "实时连接中断 · 正在重连"（zh-CN/auth.json:59）。
- 页面刷新：快照接口带 `drafts` 和 `event_cursor`（app.go:2811），草稿气泡能恢复。

**缺口**
- `thinking` 事件是"替换式"单行（stream.go:431-480 ThinkingCallback，replaceEvent :482-508 删旧插新），回放时只拿到最后一条，这是设计使然，不算丢。
- 重连成功后没有"已恢复"的反馈；长时间离线（>30s）回来时，用户不知道中间是否漏看。

**目标**：维持现状；加"连接已恢复"一次性提示（仅当离线超过 30 秒）。不做服务端改动。

### 案例 2：服务器连不上模型（或模型方故障）

**现状（已验证）**
- `RetryProvider`：最多 5 次，基础 1s、封顶 30s、指数退避（retry.go:12-50, 66-124），连接类错误与 5xx/429/529 可重试（errors.go:40-70, 117-131）。总计约 31s+抖动后返回 `max retries exceeded`。
- 流式请求一旦收到过任何 chunk 就不再重试（retry.go:150-153）。
- agent 层对流中断再给一次机会（agent.go:1242, 2923-2934），然后 `LLM call failed` 返回（agent.go:1280-1282）。
- app 层把 run 标 `failed`（app.go:3388-3395），前端按错误文本归为 `connection_interrupted`（run_failure.go:44-47）。
- 重试期间前端有 "Service busy, retrying in Ns"（chat.json:534, PublishRetry stream.go:511）。

**目标**
- N 次重试失败后，run 进入 `paused`（原因 `network`），检查点已在上一个边界写好（§3.3）。聊天里插一条 notice。
- 服务级探测器（不是每个 run 各探）按 5s→60s 退避探测模型方可达性；可达即把所有 `paused:network` 的 run 置回 `queued`，worker 自动续跑，再插一条 notice。
- `pause_until`（默认 30 分钟，D1）到期仍不可达：标 `failed`，用模板（不用模型）从检查点和 `tool_activities` 生成总结：已完成的工具、已发布的进度消息、停在哪一步。
- 期间服务重启：`paused` 行保持不动，启动时探测器看到有 paused 就开始探。

### 案例 3：外部工具调用途中断网（MCP / 邮件 / Notion）

**现状（已验证）**
- 运行时把未分类的工具错误一律记为 `uncertain_effect` / `verify_effect`（runtime.go:355-366）；观察型工具（Observation）则记 `observation_failed`、无副作用（runtime.go:361-364）。
- MCP：网络类错误（EOF、超时、-32603）判为 transient（mcp_outcome.go:45-49），派发后失败记 `mcp_result_unknown` uncertain（mcp_outcome.go:51-55）。
- 超时同理：读操作 transient、无副作用；其他 uncertain（tool_deadline.go:104-112）。
- 复跑防护：uncertain 之后同一 scope+operation 在本 run 内被拦（tool_recovery.go:118-130, 170-190），观察型只拦相同参数，可在前置条件修好后重试 3 次（tool_recovery.go:110-116, 150-154）。
- **缺口**：`call_mcp_tool` 的身份统一标 `OpaqueEffect`（identity.go:50-57, 66），MCP 的只读工具（readOnlyHint）也被当有副作用；extensions 包里没用 annotations（grep 无 ReadOnlyHint）。

**目标**
- 有效果的工具：维持"先核实、不重做；核实不了就告诉人"。这正是现有 `verify_effect` 合同，不改。
- 只读 MCP 工具：按 MCP `annotations.readOnlyHint=true` 标为 Observation，可直接重做。
- 工具途中崩溃/断电：进程没了，没有任何错误返回 — 这个由 §3.4 "在途步骤"处理：重启后把 `tool_activities` 里 `running` 的那条记为 uncertain（观察型为 transient 无副作用），续跑时把这个结果喂回模型。

### 案例 4：服务器崩溃 / 断电 / kill -9

**现状（已验证）**
- 启动时 `recoverInterruptedRuns`：所有 `running` 的 run → `interrupted`，错误 "service restarted after claim"（app.go:331-368）；活动草稿 cancelled、待答问题 run_done（app.go:364-368）；在途工具标 interrupted（app.go:404-424）。
- `queued` 的 run 启动时恢复 worker（app.go:2284-2296 `workerConversationIDs` → `startConversationWorker`），会正常执行。
- `waiting`（等人回答）的 run 有检查点（`run_input_waits`，input_continuation.go:18-32），回答后 `queued`→`running` 原子认领（input_continuation.go:179-221）。
- 已发布的进度消息（每个带工具调用的助手回合，PublishAssistantTurn stream.go:156-260）是落库的，用户看得到崩溃前模型说过的话。
- 注释明确写了"只对人工输入边界做检查点，不做通用重放日志"（input_continuation.go:3-6）— 这是设计选择，不是缺陷，但正是本次要改的点。

**目标**
- 每个工具边界写检查点（§3.3）。启动时：有检查点的 `running` run → `queued`（attempt+1，resumes+1），在途工具按 §3.4 处理；没检查点的（老数据、首次模型响应前就崩）→ 照旧 `interrupted`（或首次响应前崩的直接 `queued`，因为什么都没发生）。
- 续跑第一条 notice："服务已重启，任务从上一个检查点继续"。
- resumes 超过上限（D2）→ `failed`："任务反复中断，已停止"。

### 案例 5：计划内升级（`tofi update`）

**现状（已验证）**
- `upgrade` → `wait_for_idle`：数所有账户库里 `queued/running/waiting` 的 run（tofi_host.py:131, 1309-1342），有就等 `--wait` 分钟然后拒绝，`--force` 直接打断（tofi_host.py:1342-1370）。
- 停容器 `docker stop --time 180`（tofi_host.py:1157）。
- 进程收到 SIGTERM：HTTP 5 秒优雅关闭 → `s.Close()`（main.go:49-70）→ `stopConversationWorkers` 直接 cancel 所有 run 的 context（collaboration.go:1024-1039）→ execute 看到 closing 就返回、状态留 `running`（app.go:3382-3386）→ 下次启动标 interrupted。
- 已有的精细边界钩子：`BeforeModelCall`（agent.go:1019-1025；app 侧 steeringBoundary app.go:3296-3312）和 `OnToolEvent running`（app.go:3342-3350），用于"被新消息抢占"，正好是 drain 需要的两个切点。

**目标（drain = 案例 4 的优化）**
- `Close()` 先置 `draining=true`，`BeforeModelCall` 和工具派发前的钩子返回 `errDraining` → run 在边界停住、状态 `queued`（错误字段 `drain`），最多等 D3 秒；超时的 run 走崩溃路径（有检查点，一样能续）。
- `tofi_host.py` 的活跃判定改为只把 `running` 当阻塞，且默认不再拒绝（D5），改为提示"N 个任务将在更新后续跑"。
- 续跑 notice："服务已更新至 vX.Y.Z，任务从上一个检查点继续"（版本来自 server_info）。

---

## 3. 技术设计

### 3.1 现有可复用的骨架

| 部件 | 位置 | 复用方式 |
|---|---|---|
| `Continuation`（消息、工具恢复账本、用量、计数器） | continuation.go:33-62 | 直接扩展成通用检查点 |
| `ValidateContinuation` | continuation.go:66-147 | 按 kind 放宽"必须有等待中的工具"（:79） |
| `newSuspendedResult` 组装逻辑 | continuation.go:158-193 | 抽成 `snapshotContinuation()`，边界处调用 |
| `resumeContinuation` + `SkippedToolCalls` → `batch_skipped` | continuation.go:210-240 | 在途工具 + 批次剩余调用的回填 |
| `ToolRecoveryRecord` / 复跑防护 | tool_recovery.go:17-28, 98-168 | 不变 |
| runtime 封装 `{version, model, agent}` 的 encode/decode | runtime/types.go:36, runtime.go:39-80 | 不变 |
| `tracker.restoreContinuation` | trace.go:37-62 | 放宽"必须找到等待工具" |
| 认领原子性 `claimInputContinuation` | input_continuation.go:179-221 | 改成通用 `claimRun` |
| `approval_expiry_recoveries` 的"启动后可恢复的后台任务"模式 | approval_expiry.go:17-33 | 网络等待/超时总结照抄这个模式 |
| `expiry_interrupted_effect` 把在途工具记 uncertain | approval_expiry.go:410-414 | 复制为 `restart_interrupted_effect` |

### 3.2 数据模型

**新表 `run_checkpoints`**（每账户库一份，data/tofi.db 与 data/accounts/*/tofi.db 同构，accounts.go:327）

```sql
CREATE TABLE IF NOT EXISTS run_checkpoints(
  run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
  attempt INTEGER NOT NULL,          -- 写入时的认领代次，见 3.5
  seq INTEGER NOT NULL,              -- run 内边界序号，单调递增
  kind TEXT NOT NULL CHECK(kind IN ('turn','tool')),  -- 模型响应后 / 工具结果后
  checkpoint_json TEXT NOT NULL,     -- runtime 封装 {version, model, agent: Continuation}
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
```

不复用 `run_input_waits`：它的 `question_id NOT NULL UNIQUE` 外键和终态触发器（input_continuation.go:18-32）都绑着问题卡片；人工输入等待仍走它，两张表并存，认领时先查 waits 再查 checkpoints。

**`runs` 表加列**（走 `ensureColumn`，app.go:522-535 同款）
- `attempt INTEGER NOT NULL DEFAULT 0` — 每次 queued→running 认领 +1
- `resumes INTEGER NOT NULL DEFAULT 0` — 崩溃/升级后续跑次数
- `pause_reason TEXT NOT NULL DEFAULT ''`、`pause_until TEXT NOT NULL DEFAULT ''`
- `resume_notice TEXT NOT NULL DEFAULT ''` — 续跑时要插的 notice 种类（restart/update/network），由认领方消费后清空

**`Continuation` 加字段**（continuationVersion → 2；v1 仍可读，视为 `kind=input_wait`）
- `Kind string`：`input_wait` | `boundary`
- `Attempt int`
- `boundary` 时 `QuestionID/WaitingToolCallID/WaitingToolName` 允许为空；`SkippedToolCalls` 语义改为"最后一个助手回合里尚未得到结果的调用"（可为空）。

**为什么扩展而不是加姊妹类型**：验证逻辑（用量对账 continuation.go:131-145、账本校验 :80-84、批次匹配 :86-130）、runtime 的封装/解封/事件恢复、`resumeContinuation` 的 batch_skipped 回填都要原样用；姊妹类型要抄 ~150 行并维护两份。差异只在"有没有一个等待中的工具"，一个 kind 字段就能表达。

### 3.3 检查点时机与写入成本

生产 run 全部 `ToolsOnly:true`（runtime.go:418），工具**顺序**执行（并行分支仅 `!cfg.ToolsOnly`，agent.go:1572）。所以边界是线性的：

1. `turn`：模型响应追加为 assistant 消息之后、第一个工具派发之前（agent.go:1404-1412 之后）。此时推理项已附在消息上（:1409-1411）。
2. `tool`：每个工具结果追加之后（顺序路径 agent.go:1845 `appendAndEmitAs` 之后）。

新钩子 `cfg.OnCheckpoint(kind string, c *Continuation) error`，app 侧实现为 `UPSERT run_checkpoints ... WHERE attempt=?`。写失败 → run 失败（和 `OnSuspend` 失败一致，runtime.go:502-504）。

在途标记不需要新写：`tool_activities` 的 `running` 行在派发前就已落库（trace.go:173-178 → app.go:3342-3350 `RecordToolEvent`），崩溃后它就是"在途"的证据。

成本：
- 每个边界一次整体 JSON 快照（同 `run_input_waits` 做法），单行 UPSERT，WAL 模式（app.go:479）。
- 大小由三样决定：消息文本（入口已截到 24000 字符/条，agent.go:82, 1839）、Anthropic 的 thinking 内容块（明文，可能每回合数 KB 到数十 KB）、OpenAI 的 `encrypted_content`（每回合数 KB 到数十 KB）。典型长任务估 0.2–2 MB；上限沿用 16 MB（input_continuation.go:16）。
- 每个工具边界写 1 MB 对本地 SQLite 是毫秒级；工具本身通常秒级。先按全量快照做，Phase 1 测量实际大小；超过 4 MB 中位数再考虑"只写增量"（deferred）。

### 3.4 续跑：在途步骤与批次剩余

启动恢复（改 `recoverInterruptedRuns` app.go:331-392）对每个 `running` 的 run：

1. 读 `run_checkpoints`。没有 → 若 `tool_activities` 为空且无已发布回合（`stream_assistant_turns`）→ 直接 `queued`（什么都没发生）；否则照旧 `interrupted`。
2. 有 → `resumes+1`；若 `resumes > 上限(D2)` → `failed`，错误 `resumed_repeatedly`。
3. 否则 `queued`，`resume_notice='restart'`；草稿照旧 cancelled（流到一半的文本属于未完成回合，见 §3.7）；待答问题**不**再 run_done（等人输入的 run 不在 `running`，不受影响）。
4. 在途工具：`tool_activities` 里 `running` 的行 → `failed` + outcome：
   - 观察型（risk=Observation）：`Transient / restart_interrupted / no_side_effects / retry`
   - 其他：`Uncertain / restart_interrupted_effect / unknown / verify_effect`（文案照 approval_expiry.go:410）
   同批次里 `queued` 的行 → `Permanent / batch_skipped / not_executed`（approval_expiry.go:411-413 同款）。

认领时（`claimRun`，§3.5）组装恢复消息：`resumeContinuation` 的推广版 —— 对检查点里最后一个助手回合中没有结果的调用，按上面的 outcome 逐个补 tool 消息（在途那条用它的 outcome，其余 batch_skipped）；没有未决调用就不补任何东西，直接下一次模型调用。模型看到 `verify_effect` 会先核实，复跑防护保证同一有效果操作不会被原样重做（tool_recovery.go:118-130）。

模型调用途中崩溃：检查点是上一个边界，没有未决调用；流出的半截文本作废（草稿 cancelled），模型重新生成这一回合。

### 3.5 认领 / 租约 / 幂等

- 每个库只有一个进程在写（账户库各自一个 `Server`，同进程内 accounts.go:327）；进程内 `s.runs[r.ID]` 保证同一 run 不会被两个 goroutine 执行（app.go:3026-3033）。
- 持久层租约 = `runs.status='running'` + `attempt`。`claimRun` 一个事务里：`UPDATE runs SET status='running', attempt=attempt+1 WHERE id=? AND status='queued'`（行数必须为 1），同事务读取检查点并校验 `checkpoint.attempt < 新 attempt`（旧代次写的才合法），人工等待照旧把 waits 置 claimed（input_continuation.go:208-214）。
- 检查点写入 `WHERE attempt=当前代次`；旧 goroutine（理论上不存在，但防御）写不进。
- "一个 run 绝不续跑两次"：status 从 `queued` 单次跃迁 + attempt 单调 + 启动恢复在一个事务里（app.go:332-388 已是事务）。
- 卡死清扫器（stuck_runs.go:69-136）不变：它把超时 run 标 failed，续跑的 run 也受它约束。

### 3.6 状态机

```
queued ──claim──▶ running ──done──▶ done
  ▲                 │ ├─ suspend(human) ──▶ waiting ──answer──▶ queued
  │                 │ ├─ network fail ────▶ paused ──probe ok──▶ queued
  │                 │ │                        └─ pause_until ──▶ failed
  │                 │ ├─ drain / crash+checkpoint ─────────────▶ queued
  │                 │ ├─ crash, no checkpoint ─────────────────▶ interrupted
  │                 │ └─ error / cancel ───────────────────────▶ failed / cancelled
  └─────────────────┘
```

新增状态 `paused` 要过的闸（加字段/加状态走全每道闸）：
- `SetRunStatus` 允许集合（app.go:1710-1713）
- 终态判定：app.go:1723、tool_activity.go:107,130、conversation_task_state.go:117、work_execution.go:99
- `input_wait_terminal_cleanup` 触发器的 `NOT IN ('queued','running','waiting')`（input_continuation.go:25-26）→ 加 `paused`
- 启动恢复 `interruptOrphanToolActivitiesTx` 的保留条件（app.go:430-433）
- 定时任务去重 `status IN ('queued','running')`（schedule.go:882-886）→ 加 `paused`；否则挂起期间会再开一次
- 活跃 run 计数：app.go:991, 1126；tofi_host.py:131
- 前端 `run_status` 文案（zh-CN/chat.json:435 附近；en chat.json:442-449）：`paused` → "等网络" / "On hold: provider unreachable"
- 前端终态集合 `isTerminalRun`（ui/src/runFamily.ts:4）

### 3.7 推理保留

**事实**
- 推理项结构与两家实现：provider.go:39-66；OpenAI 加密项采集 openai.go:533-545（非流）/785-800（流）；Anthropic 把整回合内容块（含带签名的 thinking）作为一个 ReasoningItem 原样回放 anthropic.go:34-36, 484-510。
- 推理只附在带工具调用的助手回合上（agent.go:1409-1411），run 内每次请求都回放。
- 检查点存的是 `provider.Message`，推理随人工输入等待一起保存（continuation.go:35-38 注释）。
- **丢失点 1**：`microCompact` 把最近 6 条之外的所有消息 `ReasoningItems = nil`（agent.go:2171），触发条件是积累 8000 token 的可压缩工具结果或上下文过半（agent.go:2202-2224）。
- **丢失点 2**：`dropOldToolImages` 每轮都改旧工具结果（去图 + 追加注记，agent.go:2226-2251）。对 Anthropic 这同样是"历史编辑"。
- **丢失点 3**：提供方拒绝回放一次，本 run 后续全部不再回放（agent.go:1302-1305；anthropic.go:80-100；openai.go:118-134），日志里一行，用户和审计都看不到。
- **Anthropic 约束**：代码注释明确"历史编辑（压缩、裁剪）会使后面的 thinking 块失效；用 `block_binding.prefix_mismatch_behavior=drop_block` 让服务端丢弃而不是报错"（anthropic.go:305-310）。也就是说：对 Anthropic，改任何旧工具结果 = 该点之后的推理全部被丢，不管我们本地留不留。
- **OpenAI**：加密推理项自包含，回放要求是"紧跟在它产生的函数调用之前"（provider.go:50-53）；没有前缀绑定的说法。本地保留即可，需实测确认改旧工具结果后不触发 `ReasoningReplayRejected`。
- **估算漏项**：`EstimateContextUsage` 只按 `EncryptedContent/4` 计推理（tokens.go:134-136），Anthropic 的 `Content` 块完全没计入 —— Anthropic 推理在上下文估算里是 0。

**设计**
1. `microCompact` 删除 :2171 那行，只裁工具结果。
2. 按提供方分流：
   - OpenAI：裁工具结果 + 保留推理。
   - Anthropic（`hasAnthropicReplay(messages)` 为真时，anthropic.go:528-535）：`microCompact` 与 `dropOldToolImages` 都不改历史（D4）。上下文压力交给整体压缩（阈值从 0.80 降到 0.70，agent.go:1081）。截图类 run 例外：图是最大的上下文开销，Anthropic 下允许去图，但这意味着桌面任务在 Anthropic 上推理会随每次去图被丢 — 文档里明说，由 owner 接受或改用 OpenAI 跑桌面任务。
3. `ReasoningReplayRejected` 时写一条 `events` 行（type `run`，字段 `reasoning_replay: off`）并在工具活动面板显示"推理回放已关闭（提供方拒绝）"，不再静默。
4. `EstimateContextUsage` 补上 Anthropic 内容块的 token 估算（按 thinking 文本长度/4）。
5. **整体压缩写"当前思路"**：现在 `compactMessages` 把消息摊平成文本让总结器读（agent.go:2073-2131），加密推理读不到。改为：用**同一模型、原始 transcript 并回放推理**，追加一条 user 指令"现在写交接摘要"，摘要模板在原 6 节之上加第 7 节 `Current line of thinking`：为什么走这条路、排除了什么、下一步打算做什么。这样模型能读自己的 thinking。溢出触发的压缩（agent.go:1244-1252，上下文已装不下）保留现有摊平方案作兜底。
6. **测量步骤**（Phase 2 验收项）：在 `OnContextEstimate` 的落库（app.go:3327-3331 `recordContextEstimate`）加三个分量：文本、工具结果、推理。各挑 2 个真实长任务（≥30 次工具调用）在两家模型上跑，记录推理占比随步数的曲线。判定：若 50% 填充时推理占比 > 35%，把整体压缩阈值再降到 0.65；绝不回到"悄悄丢推理"。

**跨用户回合**：`messages` 只存文本（app.go:483），下一次 run 由 `buildContextPartsWith` 从消息和 `summaries` 重建（app.go:3098），推理不跨 run。两家提供方也都只在当前工具循环里用上一回合的推理。按 owner 原则（长期记忆=重要的事），维持现状，不做跨回合推理保留。

**唯一不可避免的丢失**：中断那一刻正在生成的回合。推理项随响应完成才可用（OpenAI 在 `output_item.done`，openai.go:785；Anthropic 在内容块结束时带签名），而 agent 只在整个响应返回后才追加 assistant 消息（agent.go:1404）。崩溃时这一回合的推理、文本、工具调用全部丢失，续跑时模型从上一个边界重新生成这一回合。已流出的草稿文本（`stream_drafts`）作废。这是精确的损失范围，不多也不少。

### 3.8 网络等待（案例 2）

- 判定：`engine.Run` 返回的错误满足 `provider.IsRetryable`（errors.go:40-70）且不是 429/配额/鉴权（run_failure.go:60-66 的标记）→ 网络类。
- app.go:3388 处新增分支：`SetRunStatus(paused)`、`pause_reason='network'`、`pause_until=now+30m`、插 notice。首次模型响应前就失败的 run 没检查点也可以 paused（续跑等于重来，什么都没发生）。
- 探测器：`Server` 级 goroutine，有 paused:network 的 run 时启动，用提供方最便宜的只读调用（OpenAI/Anthropic `GET /v1/models`，Codex 用其鉴权探活）做探测，退避 5s→60s；成功：所有 `paused:network` → `queued`，`resume_notice='network'`，`wakeConversationWorkers()`（collaboration.go:1041）。
- 到期：`failed`，错误 `provider_unreachable`，并插一条总结 notice，内容来自 `tool_activities`（已完成/失败的工具名）、`stream_assistant_turns`（已发布回合数）和检查点里最后一个助手回合（停在哪个工具）。不调用模型。
- 模型正在重试阶段的 UI 仍用现有 "Service busy, retrying in Ns"。

### 3.9 Drain（案例 5）

- `Server.Close()`（app.go:2310）第一步：`s.draining=true`，然后等待最多 D3 秒直到 `s.runs` 为空。
- `steeringBoundary`（app.go:3296）和 `OnToolEvent running`（app.go:3345）处：`draining` → 返回 `errDraining`。agent 的 `BeforeModelCall` 失败路径（agent.go:1019-1025）会原样把错误带回；app 侧（app.go:3388 之前）识别 `errDraining` → `SetRunStatus(queued)`，`resume_notice='update'`，不 cancel context（已经在边界，检查点已写）。
- 等不到边界的（工具跑很久）：按崩溃路径，`stopConversationWorkers` 照旧 cancel。
- `tofi_host.py`：`ACTIVE_RUN_STATES` 改为 `('running',)`（tofi_host.py:131）；`wait_for_idle` 改为"有 N 个 running 就打印将续跑，继续"（D5）；`docker stop --time 180` 不变。
- 版本号进 notice：续跑认领时读 `server_info` 的版本写入文案。

### 3.10 定时任务迟到

- 现状：`ClaimDueSchedules` 把错过的 tick 合并成一次（schedule.go:863-870），occurrence 记 `scheduled_for_utc`（schedule.go:133-146, 955）。已经是"补跑一次"。
- 加：`context.go:627-630` 的 schedule 系统提示里，若 `now - scheduled_for_utc > 5min`，追加一句：`This occurrence was due at {due}; it started at {now} ({late} late) because the service was offline. State the delay in one sentence in your result.`
- UI：定时任务 occurrence 行显示"迟到 37 分钟"。
- 不新增状态，不新增表。

### 3.11 聊天里的续跑文案（交易台口吻，不用汇报腔）

| 时机 | zh-CN | en |
|---|---|---|
| 重启续跑 | 服务已重启，任务从上一个检查点继续。 | Service restarted. Resuming from the last checkpoint. |
| 升级续跑 | 服务已更新至 {version}，任务从上一个检查点继续。 | Updated to {version}. Resuming from the last checkpoint. |
| 挂起等网 | 模型服务暂不可达，任务已挂起，网络恢复后自动继续。 | Model provider unreachable. Task on hold; resumes when the connection returns. |
| 网络恢复 | 网络已恢复，任务继续（挂起 {minutes} 分钟）。 | Connection restored. Resuming (held {minutes} min). |
| 等网超时 | 模型服务 {minutes} 分钟内未恢复，任务已停止。已完成：{done}。未完成：{todo}。停在：{step}。 | Provider unreachable for {minutes} min. Task stopped. Done: {done}. Not done: {todo}. Stopped at: {step}. |
| 在途工具卡片 | 中断时在执行，结果待核实 | Was running at interruption; effect unverified |
| 反复中断 | 任务反复中断（{n} 次），已停止。 | Task interrupted {n} times. Stopped. |
| 定时迟到（模型输出里） | 本次原定 {due}，因服务离线于 {started} 执行。 | Due {due}; ran at {started} after the service was offline. |
| 浏览器恢复 | 连接已恢复 | Connection restored |

实现：复用 `Kind: "notice"` 消息（collaboration.go:324 同款），`notice_data` 带 `{kind: resume, reason, version, held_minutes}`，前端按 kind 渲染，文案走 locales。

---

## 4. 分阶段实施（每阶段可独立合并）

### Phase 0：测量与地基（无行为变化）
- `EstimateContextUsage` 计入 Anthropic 内容块；`recordContextEstimate` 落三分量。
- `ReasoningReplayRejected` 写 run 事件。
- 测试：单元测试覆盖估算；`go test ./internal/agent ./internal/app` 退出码 0。
- 验收：两家模型各跑 1 个真实长任务，拿到推理占比曲线，写进本文档附录。

### Phase 1：推理不再被丢
- 删 agent.go:2171；Anthropic 下关闭 `microCompact`/`dropOldToolImages` 的历史编辑（D4）；压缩阈值 0.70。
- `compactMessages` 改为原 transcript + 回放推理 + 第 7 节"当前思路"；溢出路径保留旧方案。
- 测试：构造 40 步 transcript，断言 microCompact 后每条 assistant 消息的 `ReasoningItems` 保持；Anthropic 适配器测试断言 microCompact 后 `hasAnthropicReplay` 的消息未被改写；OpenAI 真实调用（acceptance，带 key）断言裁剪旧工具结果后 `ReasoningReplayRejected == false`。
- 验收：Phase 0 的长任务重跑，`OmitReasoningReplay` 全程为 false。

### Phase 2：边界检查点 + 崩溃续跑（案例 4）
- `Continuation.Kind/Attempt`、版本 2、验证放宽；`snapshotContinuation`；`OnCheckpoint` 钩子；`run_checkpoints` 表；`runs` 加列；`claimRun`；启动恢复改造；在途工具 outcome；续跑 notice。
- 测试：
  - 单元：ValidateContinuation 对 v1/v2 两种 kind；resume 组装对"无未决调用 / 在途观察型 / 在途有效果 / 批次剩余"四种。
  - 集成（内存 SQLite）：模拟 running + checkpoint + 一条 running 的 tool_activity，重开 Store，断言 queued、resumes=1、该 activity 为 failed+uncertain，续跑后模型收到的 tool 消息为 `restart_interrupted_effect`。
  - **真实 kill -9**：测试 VM（Proxmox VM 106，可清空）上起服务，给 Bot 一个 ≥10 步、含一次 `call_mcp_tool` 写操作的任务；在工具 `running` 期间 `kill -9` 进程；重启；断言：聊天出现"服务已重启…"、run 最终 done、该 MCP 写操作在 `tool_activities` 为 failed/uncertain、后续模型调用里出现核实步骤、外部目标只被写了一次（用 MCP 测试桩计数）。
  - 续跑上限：循环 kill 4 次，第 4 次后 run 为 failed `resumed_repeatedly`。
- 验收：以上全部退出码 0；`tofi_host.py` 本阶段不动。

### Phase 3：网络等待（案例 2）+ 只读 MCP 重做（案例 3）
- `paused` 状态过全部闸（§3.6 清单）；探测器；到期总结；MCP `readOnlyHint` → Observation。
- 测试：
  - 单元：错误分类（连接类 → paused；401/配额 → failed 不变）。
  - **断网实测（仅测试 VM）**：`iptables -I OUTPUT -d api.openai.com -j DROP`（或 `/etc/hosts` 指向黑洞地址）在任务中途生效 60 秒；断言：≈30 秒后 run 为 paused、notice 出现；撤掉规则后 ≤60 秒内 run 回到 running、第二条 notice 出现、任务完成。再做一次把规则留 31 分钟（`pause_until` 测试时可配成 2 分钟）：断言 failed + 总结 notice 含工具名清单。
  - 挂起期间重启服务：paused 保持，探测器继续，恢复后续跑。
- 验收：退出码 0；UI 的 `paused` 文案两语种齐全，`npm --prefix ui run test:i18n` 通过。

### Phase 4：Drain + 升级（案例 5）+ 定时迟到
- `Close()` drain；`errDraining` 分支；`tofi_host.py` 活跃判定与提示；续跑 notice 带版本；定时迟到提示。
- 测试：
  - 单元：drain 时 `BeforeModelCall` 返回 errDraining → run queued、检查点 seq 不变；工具在途时等待至 D3 秒。
  - **升级中实测（测试 VM）**：任务跑到第 5 步时执行 `tofi update --manifest <本地清单>`；断言：host 打印"1 个任务将在更新后续跑"、容器在 ≤180 秒内停止、新版本起来后聊天出现"服务已更新至 …"、任务完成、没有工具被执行两次（MCP 桩计数）。
  - 定时迟到：把 schedule 的 `next_at_utc` 设到 40 分钟前，重启，断言 occurrence 执行一次且模型输出含迟到说明；`schedule_restart_acceptance_test.go` 的三相矩阵继续通过。
- 验收：退出码 0；`docs/agent-plan/safe-updates.md` A3 条目更新为新行为。

### Phase 5：浏览器"连接已恢复"（案例 1，可选，前端一行）

---

## 5. 风险与对应证明

| 风险 | 处理 | 证明它的测试 |
|---|---|---|
| **有效果的工具被执行两次**（最高风险） | 在途工具永远记 uncertain 并要求核实；复跑防护拦同 scope+operation（tool_recovery.go:118-130）；批次剩余 batch_skipped 不重放；认领 attempt 单调 | Phase 2 kill -9 实测的 MCP 桩计数 = 1；Phase 4 升级实测同样计数 |
| 同一 run 被两个执行者续跑 | `UPDATE ... WHERE status='queued'` 行数=1；`s.runs` 进程内去重；检查点写 `WHERE attempt=?` | 集成测试并发两次 claimRun，仅一次成功 |
| 崩溃循环 | `resumes` 上限（D2） | Phase 2 循环 kill 测试 |
| 检查点过大拖慢工具边界 | 16 MB 上限；Phase 0/2 测量中位数；超标转增量日志（deferred） | Phase 2 验收记录每边界写入耗时 p95 < 50ms |
| Anthropic 推理被服务端丢弃而我们不知道 | D4 关闭历史编辑；`ReasoningReplayRejected` 可见化；估算补齐 | Phase 1 Anthropic 适配器测试；长任务 `OmitReasoningReplay` 全程 false |
| 关闭 microCompact 后 Anthropic 上下文更早撑满 | 阈值 0.70；整体压缩带"当前思路" | Phase 0 测量；Phase 1 长任务不出现 context overflow |
| 挂起期间定时任务重复开火 | schedule 去重 IN 列表加 `paused` | Phase 3 单元：paused 的 occurrence family 下 `ClaimDueSchedules` 不产生新 run |
| 网络探测误判（提供方 5xx 但网络通） | 探测只看可达性；run 续跑后真正的 5xx 仍走 RetryProvider；两次 paused 之间计数，第 3 次 paused 直接 failed | Phase 3 单元：模拟探测成功但模型调用 5xx |
| 升级后检查点格式不兼容 | 版本字段；v1 仍可读；新版本写 v2；回滚到旧版本时 v2 检查点校验失败 → 走 `interrupted`（不是崩溃） | 单元：旧版本 ValidateContinuation 读 v2 返回明确错误 |
| 草稿作废让用户误以为内容丢了 | 续跑 notice 紧跟作废草稿后出现；§3.7 文档化这是唯一损失 | Phase 2 实测截图 |

---

## 附录：待测量数据（Phase 0 填）

- 推理 token 占比曲线（OpenAI / Anthropic 各 2 个任务）
- 检查点大小中位数 / p95（按边界数）
- 每边界写入耗时 p95
