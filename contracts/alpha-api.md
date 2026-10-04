# Alpha API 与运行时契约

接口使用 JSON snake_case。当前工作区没有 owner token 或登录页；写请求遵守同源 `Origin` 检查，Codex 账户通过官方 device flow 连接。

## HTTP

- `GET /api/server-info` 是客户端进入工作区前的服务发现接口，无需模型配置。返回 `{service:"tofi",protocol_version:1,instance_id,auth:{mode:"none"},tenancy:{mode:"single"}}`，不含账户、聊天或凭据，禁止缓存；实例身份跨重启保持。其他方法返回 405。
- `GET /health` 返回 `ok`、`environment` 和 `instance_id`；验收客户端必须验证后两项；`GET /api/config` 返回 provider、默认模型和 `model_configured`。Codex 授权使用 `GET/DELETE /api/auth/codex`、`POST /api/auth/codex/connect` 和 `POST /api/auth/codex/connect/{session}/poll`。
- `GET/POST /api/bots`、`PATCH /api/bots/{id}` 管理 Bot；`GET /api/conversations` 列出固定 DM 和群；`POST /api/groups` 创建群。
- 普通用户创建 Bot 使用 `POST /api/bots` 的 `{onboarding:true,client_creation_id:"<uuid>"}`，返回默认名称 `New Bot` 和固定 DM；服务端原子保存英语职责问候，同一创建 ID 重试不会生成第二个 Bot。无需模型调用即可创建。旧的显式名称/指令创建方式继续用于工具组队和兼容调用。
- `GET /api/conversations/{id}/messages` 支持 `before_seq`、`limit`，并返回 `event_cursor`。`POST /api/conversations/{id}/messages` 接受 `{content,client_message_id,attachment_ids?}`；可仅发送附件，可兼容接受 `recipient_bot_id`，返回 `{message,run,runs?}`。相同 `client_message_id` 在同一会话幂等。
- 群消息不要求先选接收者：无 `@` 进入 Luna triage，`@Bot` fanout 到点名成员并中断该群旧的 queued/running continuation；未知或歧义 mention 返回 400。持久队列让正常发送排队；retry 在目标会话已有运行时才可能返回 `conversation_busy` 409。
- `GET /api/conversations/{id}/events?after=N` 是 SSE；也接受 `Last-Event-ID`。当前 app 事件类型包括 `message`、`run`、`delta`、`memory`、`memory_deleted`、`schedule`、`tool`、`bot`；客户端应按事件 ID 续接并按实体 ID 去重。`delta` 在每条消息内使用稳定 `message_id` 和单调 `revision`；消息快照同时包含未完成草稿，发布时沿用其 ID。同一 Run 可以先发布完整发言、继续使用工具，再生成下一条消息；下一条消息使用新的草稿 ID，不能按 Run ID 合并覆盖已经发布的正文。
- `GET /api/workspace/events?after=N` 是工作区级 SSE；也接受 `Last-Event-ID`。事件名固定为 `workspace`，载荷固定为 `{scope:"bots"|"groups"|"config",revision:<integer>}`，只表示对应快照失效，不携带消息、指令或凭据。`bots` 覆盖 Bot 创建及资料更新，`groups` 覆盖群创建及成员变化，`config` 覆盖 Codex 连接状态变化；客户端收到后重新请求 `/api/bots`、`/api/conversations` 或 `/api/config`。连接建立会立即 flush，事件 ID 可用于断线续接。
- `GET /api/conversations/{id}/runs`；`POST /api/runs/{id}/cancel` 取消该 Run 的树；`POST /api/runs/{id}/retry` 为失败或中断的原请求创建新的 Run。用户取消的请求不自动恢复。
- `GET/POST /api/conversations/{id}/memories`、`GET/PATCH/DELETE /api/memories/{id}` 管理会话记忆。
- schedule API：`GET/POST /api/conversations/{id}/schedules`、`POST /api/schedules/{id}/pause`、`POST /api/schedules/{id}/resume`、`DELETE /api/schedules/{id}`。
- `GET /api/conversations/{id}/tools` 返回最近的 `{activities:[...]}`；可用 `limit` 限制数量。加载中的消息页可传 `run_ids`（最多 50 个不同 ID）取得无参数/结果的精确 `{summaries:[...]}`；展开单个运行时传 `run_id` 和可选 `offset`，得到最多 25 条详情、`tool_count` 与 `has_more`。这两种按运行查询不能混用，以避免历史详情扩大初始载荷。

- `POST /api/conversations/{id}/attachments` 上传 multipart 文件；`GET /api/attachments/{id}` 下载。每条消息最多 8 个附件，单个文件 20 MB；文本工具读取上限 128 KB，附件绑定必须属于同一聊天。
- `POST /api/conversations/{id}/read` 接受 `{seq}`，读游标只能前进且不能超出当前最后消息；聊天列表返回 `unread_count`。
- MCP：`GET/POST/PUT /api/extensions/mcp`，`PUT/DELETE /api/extensions/mcp/{name}`，`POST .../{name}/test`、`POST .../{name}/oauth/start`、`GET .../{name}/oauth/callback`、`POST .../{name}/oauth/disconnect`。
- Skills：`GET/POST /api/extensions/skills`；`POST /api/extensions/skills/{name}/bots/{bot_id}` 接受 `{enabled}`；`DELETE /api/extensions/skills/{name}`。
- 电脑：`GET /api/computers`、`POST /api/computers/pairings`、`POST /api/computers/pair`、`DELETE /api/computers/{id}`。设备凭据只返回一次，服务端只存摘要。
- 设备用 Bearer 凭据请求 `PATCH /api/computers/{id}/capabilities`、`GET /api/computers/{id}/jobs`、`POST /api/computers/{id}/jobs/{job_id}/result`。UI/工具可通过 `POST /api/computers/{id}/actions`、`GET /api/computers/{id}/actions/{job_id}` 创建和读取任务；设备离线或能力未授权时拒绝执行。

### 多账号 Worker 云电脑的删除与重建

此接口用于 accounts 模式下的隔离 Worker 云电脑，与上面的设备配对删除不同。要求当前启用、已修改初始密码的 Admin 会话；写请求仍受同源检查保护。身份由服务端根据账号解析，客户端不能提供文件路径、槽位或运行时目标。

- `GET /api/admin/accounts/{account_id}/computer` 返回账号、电脑 ID、`generation`、`state`、原操作 ID、阶段、错误、`supported`、槽位、预留磁盘字节与 `resources_released`。查询不会启动或重新预留电脑。
- `DELETE /api/admin/accounts/{account_id}/computer` 要求严格 JSON：`operation_id`、`expected_generation`、`confirm_computer_id`、`confirm_account_id`、`confirm_username`、`acknowledge_data_loss:true`。UUID 必须为规范形式，名称与身份必须与当前账号一致。失败后使用原操作 ID 与原 generation 重试；旧操作不能影响后来重建的电脑或复用槽位的其他账号。
- `POST /api/admin/accounts/{account_id}/computer/recreate` 要求相同身份确认字段与 `quota_gib`（整数 8..1024）。仅在删除已验证后显式重建；账号必须已启用。重新 admission 后分配新的 generation；此调用不会立即启动 manager。失败或回复丢失时原操作 ID 和容量不可改变。

删除前，App 保存意图、封锁新的电脑操作、取消并等待该账号的旧请求及后台运行时退出；Worker 独立保存删除 tombstone 后停止 manager。只有在进程/cgroup、文件占用、挂载、网络清理与固定目录的文件身份均验证通过后，才逐项删除该电脑的磁盘、jail、日志、socket、资源状态文件和经摘要验证的生成配置。电脑目录内属于该电脑的恢复副本也会删除；共享 release 镜像不会删除。未知文件、符号链接、外部硬链接、跨挂载或外来所有者均保留数据并拒绝完成。部分删除保存原始 inode 清单供重启后继续，不能重新推测目标。

账号、登录权限、App 聊天记录、服务端凭据、附件元数据、电脑目录外备份与其他账号数据保留。Guest 中的工作区文件、应用、浏览器登录状态、工具/插件数据及其授权、聊天附件字节永久丢失。Guest 附件在开始清理时标记为 `unavailable:true`，下载返回 410；后续空白重建不会恢复旧附件 ID。管理界面的动作确认必须展示账号/电脑身份和上述损失，勾选确认并输入完整电脑 ID 后才能提交。

生命周期为 `active → deleting → deleted`，失败保持 `cleanup_failed` 与原操作 ID。App 的重建未确认状态为 `recreating`。只有 `deleted` 才声明资源已释放并清除磁盘承诺、内部预留与槽位；运行时预算在停止证明通过后释放。登录、状态轮询、恢复账号、隐式 ensure/reserve/abort/quota 以及重启均不能绕过 tombstone。旧电脑 adoption 依赖现存 inode 的所有权证明，当前明确不支持删除；未解决的离线 resize 也必须先完成已有操作。

## 主要对象

客户端启动顺序为服务发现、身份与工作区选择、聊天初始化。当前只实现无登录的单工作区模式，发现成功后可直接进入聊天；未来需要登录或多住户时扩展相应步骤。客户端不能把未知 `protocol_version`、`auth.mode` 或 `tenancy.mode` 当作当前模式放行，也不能把某个 HTTP 端口能连接等同于 Tofi 服务就绪。服务连接与 Codex 模型账户连接是两件独立的事。

`Message` 包含 `conversation_id`、`seq`、`role`、`kind`、`sender_bot_id`、`run_id`、`content` 和 `created_at`。`kind` 可为 `notice` 或 `forward_result`；notice 还带：

```json
{"type":"forward","from_bot_id":"...","to_bot_id":"...","target_conversation_id":"...","target_run_id":"..."}
```

`Run` 包含 `conversation_id`、`bot_id`、`status`、`parent_run_id`、`model`、`kind`、`origin_conversation_id`、`trigger_message_id`、`queue_seq` 及时间字段。`kind=triage` 是内部路由执行，不是可聊天 Bot；UI 应显示路由状态。

`Schedule` 的 `kind` 为 `once`、`interval` 或 `daily`，包含 `bot_id`、`content`、`timezone`、`next_at_utc`、`status` 及对应的 interval/daily 字段。`ToolActivity` 以 `(run_id,call_id)` 标识一次 provider tool call，状态为 `queued`、`running`、`completed`、`failed` 或 `interrupted`，并包含参数、结果和 `truncated`。

## 协作与持久化行为

每个 Bot 有一个 canonical DM。DM handoff 在目标 Bot 的 DM 中创建带当前任务边界的子 Run，并在来源 DM 记录 notice；完成后来源 DM 收到 `forward_result`，消息保留真实目标 Bot 身份。群 handoff 留在群会话内。handoff 深度上限为 8；当前 Run 不能 handoff 给自己。

群消息、schedule fire、run、message 和事件按 SQLite 事务落盘。schedule 的 occurrence 以 `(schedule_id,scheduled_for_utc)` 和唯一 `run_id` 去重；重启只由共享队列恢复已持久化的 queued 工作，已 running 的工作不会自动重放。取消或重试不应假定外部 tool 副作用可回滚。

完成的中间发言在工具执行前持久化，群内 handoff 的接收者可以读到发言正文；半截草稿不进入模型历史。每 Run 最多发布 30 个中间回合，按 `(run_id,turn_index)` 幂等，正文、事件、幂等记录与下一条草稿身份原子提交。发布失败中止后续工具；取消后拒绝迟到发布。最终回复单独完成 Run，不能覆盖前面的发言。

## Provider 与扩展边界

runtime `Request` 使用 `OnDelta` 传递增量、`OnAssistantTurn` 发布完整的非最终发言、`OnToolEvent` 记录工具生命周期；provider tool call 的 `call_id` 必须在请求、结果、retry 和持久活动记录中保持配对。Codex provider 使用 OAuth 设备凭据及其请求 headers/models/streaming 协议；默认配置为 `openai_codex` / `codex-gpt-5.6-luna`，不要恢复已退役的旧 5.4 默认。非流式 Chat 结果可由 provider 聚合为完整响应。

扩展是可选的。`TOFI_MCP_CONFIG` 默认 `data/mcp.json`，支持远程 Streamable HTTP、headers、allow/deny 和按 Bot 授权。`bot_allowlists:null` 保持所有 Bot 可用；空对象表示全部禁用。OAuth 使用 PKCE、一次性有时限 state、原子私有凭据文件、刷新互斥和撤销后的失效保护。管理 API 隐藏凭据；配置变更于下次 run 生效。

`TOFI_SKILLS_DIR` 默认 `data/skills`。安装包最多 1 MB UTF-8 内容，必须有名称匹配的 SKILL.md，拒绝符号链接和路径越界。安装后默认不授权，按 Bot 开启；运行时提供 list_skills、read_skill、read_skill_file。暂不支持 stdio、本地进程、脚本或依赖安装。

`create_bot`、`create_group`、`invite_bot` 和 `send_group_message` 用于组队。组队操作在一次任务树内有创建/派发上限并持久幂等；`send_group_message` 只能启动另一个群的任务。在当前群回复会自动发布，需要继续分工时使用 handoff。

电脑任务只派发已声明的能力，有限超时、单次领取，重启不重放不确定的原生动作。Mac 文件能力限制在用户选择的工作目录；屏幕和输入能力需真实 macOS 权限。服务主机目前仅声明 host.info，不能把它描述为已经可运行 Bash。共享 VM 和每 Bot 用户/组后置。

## 通过聊天设定新 Bot

新 Bot 在自己的私信中等待用户分配职责，按用户回复的语言澄清或确认。现有 Agent Loop 使用 `set_bot_profile` 保存自身的简短名字、长期职责与沟通风格，工具不接受目标 Bot ID，也不修改模型或电脑/扩展授权。该入口可供 Bot 在自己 canonical DM 中由用户发起的普通任务里设置或更新自身资料；初次设定完成后，只有用户明确要求时才允许后续修改，省略字段保持原值。组聊、定时、handoff 和其他非用户触发任务不提供该工具。名字、DM 标题及 `bot` 事件一起原子落盘；初次设定还会将设定状态标记为完成。事件载荷为 `{conversation_id,bot}`，客户端立即同步侧栏及群成员使用的 Bot 资料。历史消息、私信 ID 和基于随机 Bot ID 的 Gaze 头像保持不变。
