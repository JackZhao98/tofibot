# 审批边界：TOFI 在动手之前什么时候一定会问你

> 写于 2026-10-22 PDT。代码核对基线：`main` @ `256d4b9`（rc.34 门禁前）。起因：[trust.md](../product/trust.md) §「它到底什么时候会先问你」核实出 TOFI **不能**向用户保证「发送、发布、删除、付钱、改线上一律先问」。本页定义 TOFI 应当保证的边界、怎么做到、分几步发。
> 这是设计稿，不是决定：§6 的选择题由 owner 拍板后再进 `docs/decisions/`。每个事实带 `file:line`；没核实的标 **待确认**。

## 0. 一页结论

- 今天能硬保证的只有四条：邮件由你点发、装改连接由你批、人看不到的对话里永远不替你同意、「始终允许」可撤销有审计。**Bot 电脑上的命令没有任何闸**：`curl` 发请求、`ssh` 连服务器、`rm` 删东西都直接执行；浏览器和桌面点击靠模型自己标后果；付款和「生产环境」没有概念。
- 缺口的共同点是**执行路径比审批路径多**：审批只盖了 MCP 写操作、浏览器点击、改扩展三处，电脑上的 shell / 终端 / 密钥使用三条路径从未进闸。
- 补法不是给每条命令加审批（业界没有人这么做，Claude Code 也不这么做），而是照 Claude Code 沙箱和 Managed Agents 的办法**在网络层设出站边界**：Bot 的电脑只能连你允许过的目的地，新目的地先问你（Allow once / Always allow），定时任务里直接拒。一条结构化的闸，同时盖住外发（`curl`、`git push`、`mail`）、凭证使用（`ssh` 到新主机、用 token 调 API）和「改线上」（线上就是你加进清单的主机）。电脑里面的删除和改写不设闸，由磁盘备份兜底（D48）。
- 付款单独做结构性拒绝：TOFI 永远不往 `autocomplete="cc-*"` 的字段里打字，卡号由你接管输入（D11）；模型标了 `purchase` 的点击永远由人定，审核员无权放行。
- 对普通聊天零开销：闸都在 VM 出站和已有的工具路径上，不加模型调用，不加轮次。

## 1. 现状：每条执行路径今天的实际闸门

| 路径 | 模型工具 | 今天的闸 | 出处 | 硬保证？ |
|---|---|---|---|---|
| 发邮件 | `prepare_email` | 只能起草；发送由主机在你点击后调 gog `/send` | `internal/app/mail_drafts.go:204`（工具）、`:342-356`（发送） | **是**（仅此通道） |
| 外部服务写操作（MCP） | `call_mcp_tool` | 三档：提供方标 `readOnlyHint` 直接跑；其余每次审（AutoReview 默认 `off` = 人来定）；「始终问我」每次人定、无人值守直接拒 | `internal/extensions/tool_tiers.go:285`、`internal/app/mcp_call_approval.go:94-103`、`auto_review_settings.go:19,77` | **部分**：三处绕过人——提供方的只读标注、审核员 `allow`（`auto` 模式）、「始终允许」(D24，`mcp_standing_allowance.go:170,188`，**定时任务里也生效** `mcp_call_approval.go:110-125`) 与「本对话记住」24 h（`mcp_remembered_approval.go:39,290`，仅人触发的 run `:309`） |
| 装/改连接与技能 | `manage_extensions` | 必须批准确切载荷（digest token，一次性）；无人值守或人看不到的对话直接拒 | `internal/app/extension_gate.go:44-79` | **是** |
| 浏览器点击 | `computer_browser browser.click` | 模型自标 `effect`（`none/submit/purchase/send/delete/publish/account/other_external`）；标了后果 → 审核模型 → `allow` 放行，否则让 Bot `request_approval`，人批后按元素标签一次性消费 | `internal/app/microvm.go:369`（参数）、`browser_read.go:222`（入口）、`:297-305`（`none` 不进闸）、`:345-379`（`guardAction`）、`:393`（审核员）、`:476`（claim） | **否**：标 `none` 就不进闸；审核员可放行任何后果（含 `purchase`） |
| 桌面点击/打字/按键 | `computer_desktop` | 同上，`desktop.click/type/key` 带 `effect` 才进 `guardAction` | `internal/app/microvm.go:435-458` | **否**，同上 |
| 电脑上的命令 | `computer_shell` | **无闸**。工具直接转 `shell.exec`。`DetectDestructiveAST` 只接在旧的 `tofi_shell` 路径上且只打日志（「TODO: ask user」） | `internal/app/microvm.go:470-479`；`internal/agent/agent.go:2024-2029`；`internal/agent/shell.go:173` 未被 `computer_shell` 调用 | **无** |
| 终端 | `terminal.open/write`（模型源 `"model"`） | 只有「人正在用这个终端」时拒绝（`terminal_busy`），无其他闸 | `internal/app/microvm.go:201-210` | **无** |
| 凭证使用 | `use_secret_input`（`shell_exec/env/ssh`）、`computer_ssh_keys` | 值不回显、输出扣留；但**用**密钥连哪台机器、用 token 调什么接口没有闸；`.tofi-env` 下的文件任何 Bot 的 shell 都能读 | `internal/app/secrets.go:323,354`、`ssh_keys.go:278` | **无** |
| VM 出站网络 | （所有电脑工具） | iptables 只拒私网/保留段，其余 `ACCEPT`；IPv6 关闭 | `deploy/microvm/manager.py:600-611` | **出站全开** |
| 付钱 | — | 没有付款概念；只有 `purchase` 这个自标后果 + 工作流指南一句「不要输入支付信息」 | `internal/app/workflow_guides.go:18` | **无** |
| 改线上/生产 | — | 没有「生产」概念；能改什么取决于给了什么钥匙 | — | **无** |
| 人看不到的对话 | 所有问人卡 + MCP 审批 | 一律拒绝，不挂卡 | `internal/app/questions.go:382`、`mcp_call_approval.go:133-140` | **是**（拒绝而非放行） |
| 登录/2FA/验证码 | `request_takeover` | 人在 Bot 电脑上亲手做 | D11，`internal/app/handover.go` | **是** |
| 发布文件 | `publish_file` | 只生成账户内附件，不对外 | `internal/app/artifact_publish.go:211` | 不需要闸 |

一句话：**三条有闸的路径（MCP 写、点击、改扩展）都是「人批确切内容、一次性消费、hidden 对话拒绝」的好设计；三条没闸的路径（shell、终端、凭证使用）全部通向同一个地方——VM 的出站网络。**

## 2. 承诺：TOFI 能白纸黑字写给用户的边界（少而硬）

只写代码能证明的；每条标明今天成立还是落地后成立。

| # | 承诺（对用户的原话） | 今天 | 落地后靠什么 |
|---|---|---|---|
| P1 | **邮件由你点发。** TOFI 起草，发出去那一下永远是你在卡片上点的。 | 成立 | 不变 |
| P2 | **它的电脑只连你允许过的地方。** 装包、查资料的常用站点默认允许；要连新的主机或服务（发请求、`ssh`、推代码），先问你：允许一次，或以后都允许。定时任务里遇到新地方不问、不连，报告给你。 | 不成立 | §3.1 出站边界 |
| P3 | **外部服务的写操作，没有你的决定不执行。** 默认每次问你；你开了审核员它才替你判「这是不是你要的」；你标「始终问我」的永远问你；你点过「始终允许」的以后不问（定时任务也不问），设置里随时撤。 | 成立（措辞已按代码收紧） | 不变 |
| P4 | **它不碰你的卡。** 任何卡号、安全码字段 TOFI 不会打字，要付款时请你接管输入；它标为「购买」的点击一定问你，审核员无权放行。 | 不成立 | §3.3 付款闸 |
| P5 | **装或改连接和技能，你批确切的那一份。** | 成立 | 不变 |
| P6 | **你看不到的对话里，它永远不会替你同意任何事。** | 成立 | 不变 |
| P7 | **每次放行都有收据。** 谁批的（你 / 审核员 / 「始终允许」）、什么时候、批了什么目标，在设置里能查，能撤。 | MCP 成立（`mcp_standing_allowance_uses`、`tool_activities.allowed_by`） | 扩展到出站与点击，§3.5 |

**明确不承诺的**（写进对外文案，免得用户误读）：
- 它电脑里的文件它可以删、改、覆盖，不问你。你上传的文件是副本；电脑磁盘在手动备份里（D48）。
- 「只读」是服务提供方自己标的，TOFI 信官方连接器的标注（D47：社区连接不信）。
- 审核员是模型，它判断「是不是你要的」会有错；不想让它判的工具标「始终问我」。

## 3. 设计：每条路径怎么做到

原则：**结构化判断，不靠关键词**（conventions.md 总则；owner 规则 2026-10-07「风险看动作做什么，不看工具名」）。闸设在动作真正发生的那一层（网络、DOM 字段、HTTP 路由），不在命令文本上。

### 3.1 电脑上的命令：出站边界，不是命令审批

**对标**：Claude Code 的沙箱把文件系统和网络一起隔离，出站只放行允许的域名，其余由代理拒绝并把拒绝结果交给模型，模型再请求放行（设置里 `allowedDomains`，默认带包管理源）；Managed Agents 的沙箱同样按出站策略允许主机列表（具体字段名 **待确认**）。两者都**不逐条审 shell 命令**，命令级的 allow/deny 规则（`Bash(curl:*)`）只是补充。Muse 的 Sentinel 也是「出站内容过闸」而不是「命令过闸」。

**做法**（全部在 `deploy/microvm/manager.py` 的网络命名空间和主机侧，guest 不改）：

1. **默认拒绝出站，经代理放行。** 现有 `iptables` 链（`manager.py:600-606`）从「拒私网、其余 ACCEPT」改为「只放行到本命名空间里的出站代理；其余 REJECT」。代理是主机侧一个小 HTTP CONNECT/透明 TCP 代理（每账户一个，跑在 manager 进程里），按**目的主机名 + 端口**判定：在清单里 → 直通（TLS 不解包）；不在 → 立即 RST，并把 `(run, host, port, 时刻)` 写进一张「被拒出站」表。非 HTTP 协议（`ssh` 22、`git` 9418、数据库端口）同样按主机+端口走 TCP 层判定；DNS 由代理代答，所以主机名是可信的判定依据，不是解析后的 IP。
2. **清单有三层，都在设置里可见可改（D15）**：
   - 出厂清单：包管理源（pypi、npm、apt/ubuntu 镜像、crates、go proxy）、github.com、常见文档站——照 Claude Code 默认允许域名取，**待 owner 定**（§6 Q2）。
   - 系统自动项：已配置的 MCP 服务器主机、已连接的 Google 服务、模型提供方（Runner 在 VM 里调 MCP 时需要）。这些随连接的增删自动进出清单，不需要人管。
   - 用户项：你允许过的主机，每项带「允许一次 / 以后都允许 / 每次问我」和来源（哪个 run、哪张卡）。
3. **被拒 → 结构化结果 → 一张卡。** 工具结果不靠命令退出码猜：`computer_shell` 的结果里附上这次 run 在执行期间被拒的目的地（从被拒出站表按 run 取），作为 `tooloutcome` 的 `egress_blocked` 项，`NextAction = request_approval`，目标串是 `host:port`。Bot 用现有 `request_approval` 问，卡片由服务端填事实（像 `extension_gate.go` 的 `approval.facts`：主机、端口、这条命令、谁触发），不信模型的措辞。人批 → `claimApproval` 同一套一次性消费（`browser_read.go:476`），放行该主机到 run 结束；人选「以后都允许」→ 写入用户项（同 D24 的 standing 规则：绑定账户 + Bot + 主机，可撤，有 `use_count`）。
4. **无人值守**：定时任务或由定时任务引出的 run（`mcpRunUnattended`，`mcp_call_approval.go:401`）遇到不在清单的目的地，不开卡：同 `mcp_high_risk_unattended` 的做法，返回具名拒绝 `egress_unattended`，run 继续并在汇报里说明。「以后都允许」的主机在定时任务里生效（与 P3 的「始终允许」一致）。
5. **凭证使用因此自动进闸**：`ssh` 到一台新主机、用 token `curl` 一个新接口，都是「新目的地」。不需要识别命令里有没有密钥。`use_secret_input(ssh)` 装进 `~/.ssh` 的钥匙照旧，**用**的那一刻才问。
6. **终端**（`terminal.*`）走同一个 VM 出站，自动覆盖。

**为什么不审命令**：命令文本可以用 `python -c`、`base64`、别名、脚本文件绕过任何模式匹配（`shell.go:137` 的正则表就是这种设计，AGENTS.md 已标为遗留）；出站是物理层，绕不过。文件删除不设闸的理由见 §3.4。

**延迟与打扰**：清单内的连接经本地代理，多一次本机转发（亚毫秒级，**待实测**，验收里量 `pip install` 和 `git clone` 的前后差）；清单外的第一次是一张卡，批过就没有。普通聊天不经 VM，零影响。被拒的 DNS/TCP 立即 RST 而不是超时，Bot 不会卡 30 秒。

### 3.2 浏览器与桌面点击：把「后果」从自标变成能验证的

今天 `effect` 由模型填，`none` 不进闸（`browser_read.go:302`）。改成**两路合并，取严**：

1. **结构证据**（服务端从 DOM 读，`locateAction` 已经在点击前定位元素 `browser_read.go:313-327`）：元素在 `<form>` 内且是提交控件（`type=submit`、`<button>` 默认类型、`desktop.key Return` 落在表单字段上）→ 至少按 `submit` 对待；表单 `method=POST` 或目标跨站 → `other_external`。这是 HTML 语义，不是关键词。
2. **模型自标**照旧，但只能**加严不能放松**：`effect = max(结构证据, 自标)`。
3. **审核员的权限上限**：`purchase`、`account`（权限/设置）永远由人定，审核员只对 `submit/send/publish/delete/other_external` 有 `allow` 权。改 `guardAction`（`browser_read.go:354`）：这两类跳过 `reviewAction` 直接要卡。

对标：Anthropic 的 computer-use 指引（要求在有后果的动作前确认、不输入支付信息）；Grok Bot 的「发送/发布/删除/购买前审批」与 Allow once / Always allow（本页的 §3.1 第 3 步直接照搬这两个按钮）。

### 3.3 付款：结构性拒绝，不是模型判断

- `desktop.type` 与 `use_secret_input(browser_type)` 在打字前已经检查焦点字段（`secrets.go:354` 描述：只接受可见、聚焦的 HTTPS 密码框）。加一条同层的检查：焦点字段的 `autocomplete` 属于 `cc-number / cc-csc / cc-exp* / cc-name`，或 `<input type=tel>` 位于带 `cc-*` 字段的同一表单内 → 拒绝，具名 `payment_field_refused`，`NextAction = request_takeover`。卡号由人在接管卡里亲手输入（D11 已有的路径，登录就是这么做的）。
- 识别依据是 HTML 标准的 `autocomplete` 令牌，是结构不是关键词；没标的支付表单靠 §3.2 的 `purchase` 必须由人定兜底。
- 不承诺「它不会花钱」：订阅页面一个「确认」按钮不输卡号也能扣款，那一类靠 `purchase/submit` 的卡。

### 3.4 电脑里的删除与改写：不设闸，设兜底

- Bot 的电脑是它的工作台，`rm`、覆盖、`git reset` 都是正常工作。Claude Code 在沙箱内同样不审文件操作，靠「工作目录之外不可写」。
- TOFI 的对应物：你上传的文件是副本（D36）、磁盘进手动备份（D48）。再加一条便宜的兜底：**每个 run 开始前对 `/workspace/bots/<bot>` 做一次 reflink 快照**（ext4 reflink 已被 D48 的备份使用），run 结束 24 h 后清理；设置里「恢复到 run 开始前」。**待 owner 定**（§6 Q3），不做也能发。

### 3.5 收据：一张表看所有放行

- 现有：MCP 的 `mcp_standing_allowance_uses`、`tool_activities.allowed_by`、`click_approval_claims`。
- 补：出站放行与被拒各一行（host、port、run、决定者 `human/standing/default_list/system`、卡 id）。对标 OpenClaw 家族的「签名收据」：每行带服务端生成的 id，用户在设置 › 自动行为 › 收据里按 Bot、按目标筛，能从一行撤掉对应的「以后都允许」。
- 不做签名（单机自托管，服务端就是信任根；签名只在跨组织审计里有意义）。

### 3.6 「生产环境」：不造新概念

线上就是「你允许过的主机里，标了**每次问我**的那几个」。给用户项加一个「每次问我」开关（同 MCP 工具的 `high_risk`）：这台主机永远要人点，定时任务里永远拒。不引入环境标签、不解析 hostname 里的 `prod`。

### 3.7 对延迟与打扰的总账

| 场景 | 今天 | 落地后 |
|---|---|---|
| 普通聊天（不碰电脑） | — | 不变，零新增调用 |
| `pip install` / `git clone` 常用源 | 直连 | 经本地代理，预计 < 1 ms/连接，**待实测** |
| `curl` 一个新 API / `ssh` 一台新机 | 直接执行 | 一张卡；批「以后都允许」后不再问 |
| 定时任务里连新地方 | 直接执行 | 不执行，汇报里说明 |
| 点网页「购买」 | 审核员可放行 | 一定问你 |
| 输卡号 | 模型可能照做 | 拒绝，请你接管 |
| 删它电脑里的文件 | 直接 | 直接（可选 run 前快照） |

## 4. 对标表

| 边界 | TOFI 今天 | TOFI 本设计 | Claude Code | Claude computer use / Managed Agents | Grok Bot | Meta Muse | OpenClaw / ZeroClaw / TrustClaw |
|---|---|---|---|---|---|---|---|
| 命令执行 | 无闸 | 不审命令，审出站 | 权限模式（default / acceptEdits / plan / bypass）+ 规则 `Bash(cmd:*)` allow/deny/ask + 沙箱 | 沙箱 + 出站策略；工具可标「需确认」，由会话事件回传决定（字段 **待确认**） | 云电脑；有后果的动作前审批 | 云 VM；Sentinel 出站闸 | supervised 模式逐步确认 |
| 出站网络 | 全开（私网除外） | 默认拒，清单放行，新目的地问 | 沙箱 `allowedDomains`，拒绝结果回给模型 | 允许主机列表 | **待确认** | 出站内容过 Sentinel | **待确认** |
| 外部服务写 | 三档 + 审核员 + 始终允许 | 不变 | MCP 工具逐个 allow/ask；`readOnlyHint` **待确认**是否免审 | 同左 | 审批 + Allow once / Always allow | **待确认** | **待确认** |
| 付款 | 无 | 拒输卡号 + `purchase` 必问人 | 不适用 | 指引：不输入支付信息、有后果先确认 | 购买前审批 | **待确认** | **待确认** |
| 「生产」 | 无 | 主机级「每次问我」 | 规则/hook 自定 | 不适用 | 「生产变更前审批」 | **待确认** | **待确认** |
| 收据 | MCP 有 | 全路径 | 转录 + hook | 会话事件 | **待确认** | **待确认** | 签名收据 |
| 无人值守 | MCP 有具名拒绝；shell 无 | 全路径具名拒绝 | `dontAsk` 模式拒绝需问的 | — | **待确认** | — | — |

TOFI 领先且本设计保留的：服务端生成的审批事实、绑定工具定义的「始终允许」、hidden 对话拒绝、一次性 claim。**不抄**：逐命令审批（Claude Code 的 default 模式对 Bash 每条问，是因为它跑在你的真机上；TOFI 的 Bot 有自己的 VM）、命令正则黑名单。

## 5. 分步落地（每步可单独发布）

| 步 | 做什么 | 改哪里 | 验收（退出码为准，D30） | 量 |
|---|---|---|---|---|
| **S0 观察** | 出站代理只记不拒：每个 run 结束时「run 健康」里列它连过的主机（去重）；被拒表与收据表建好 | `deploy/microvm/manager.py`、`internal/app/run_health*`、迁移 | 造一个 run 跑 `curl example.com`，健康页列出 `example.com:443`；`go test ./internal/app -run Egress` 绿；`pip install` 前后耗时差 < 5% | M |
| **S1 清单与闸** | 默认拒 + 三层清单 + `egress_blocked` 结果 + `request_approval` 卡（服务端事实）+ Allow once / Always allow + 无人值守具名拒绝 + 设置页 | 同上 + `internal/app/microvm.go`（结果附加）+ `questions.go`（卡事实）+ 新 `internal/app/egress_allowance.go`（照 `mcp_standing_allowance.go`）+ UI 设置 › 自动行为 | 验收测试：未知主机 `curl` 返回 `egress_blocked`；批一次后同 run 成功、下个 run 再问；「以后都允许」后不问且 `use_count` 增；定时 run 返回 `egress_unattended` 且无卡；撤销后下次调用再问；hidden 对话拒绝；`ssh` 22 同样被拒并可放行 | L |
| **S2 付款闸** | `cc-*` 字段拒绝打字 + `purchase/account` 跳过审核员 | `internal/app/secrets.go`、`browser_read.go:354`、guest 的字段探测（已有密码框探测的同一处） | 夹具页（synthetic）：卡号框 → `payment_field_refused`；`purchase` 点击在 `auto` 模式下仍出卡；现有 `click` 测试全绿 | S |
| **S3 结构后果** | DOM 提交证据与自标取严 | `browser_read.go:313-327`（`locateAction` 多返回 `in_form/submit/method/cross_site`） | 夹具页：自标 `none` 点提交按钮仍进闸；读链接不进闸；`test:stream-cost` 等预算不动 | S |
| **S4 收据页** | 设置 › 自动行为 › 收据：MCP + 出站 + 点击一张表，可撤 | UI + `/api/receipts` | 目录页状态；撤销一行后下次调用再问 | S |
| **S5 可选** | run 前 reflink 快照 + 「恢复到 run 开始前」 | guest + 设置 | 快照存在、恢复后文件一致、24 h 清理 | M |
| **文案** | trust.md 的「能对外说的版本」改为 §2 的 P1–P7 | `docs/product/trust.md`、`AGENTS.md` Security gates | 与代码逐条对得上 | S |

顺序：S0 → S1 是主线（P2 在 S1 落地）；S2、S3 独立可并行；S4 在 S1 后。**S1 放宽/收紧安全，按 D30 要对抗性审查**：重点查代理绕过（IPv6 已关、DNS 代答、`--resolve` 直连 IP 也走 TCP 判定）。

## 6. 需要 owner 拍板的选择（推荐在前）

**Q1 出站默认姿态**
- A（推荐）默认拒绝、清单放行、新目的地问你；定时任务拒。= P2。
- B 只记录不拒（S0 永久态），设置里给用户自己开 A。承诺 P2 写不出来。
- C 保持全开。trust.md 继续写「电脑上的命令没有闸」。

**Q2 出厂清单范围**
- A（推荐）包管理源 + github.com + 已连接服务与 MCP 主机（自动）。装包不打扰，连新东西才问。
- B 只放系统自动项，包管理源也要批一次。更紧，首次装包必有一张卡。
- C 空清单，全部由用户批。

**Q3 电脑内删除与改写**
- A（推荐）不设闸，靠 D36 副本 + D48 备份；对外文案明说。
- B 加 S5 run 前快照（可恢复，不问）。多一个可选步。
- C 对 `/workspace/bots/<bot>` 之外的删除问人。要回到命令解析，与 §3.1 原则冲突，不建议。

**Q4 付款**
- A（推荐）`cc-*` 字段拒绝 + `purchase/account` 必问人。
- B 只做 `purchase` 必问人，不做字段拒绝。
- C 保持现状。

**Q5 「以后都允许」在定时任务里**
- A（推荐）生效，与 MCP 的 D24 一致；卡片提示和设置里写明。
- B 定时任务另需单独勾选「定时任务也允许」。多一个开关，更保守。

**Q6 审核员默认**
- A（推荐）保持 `off`（默认人定），不动。
- B 默认 `auto`。减少卡片，但 P3 变成「审核员替你判」。

**Q7 「生产」**
- A（推荐）不造概念：主机级「每次问我」开关。
- B 引入「生产」标签，可贴在主机和 MCP 服务器上，贴了就是 `high_risk`。多一个名词。

## 相关代码与文档

- 现状核实：[../product/trust.md](../product/trust.md) §「它到底什么时候会先问你」
- MCP 三档与审核员契约：[../agent-plan/mcp-tool-tiers.md](../agent-plan/mcp-tool-tiers.md)；D24、D47
- 问人汇合点与接管卡：`AGENTS.md` "Asking a human"、D10、D11
- 安全闸现状：`AGENTS.md` "Security gates"
- 网络：`deploy/microvm/manager.py:585-611`
- 点击闸：`internal/app/browser_read.go:297-379`、`internal/app/microvm.go:369,435-458`
- 备份与磁盘：D48
