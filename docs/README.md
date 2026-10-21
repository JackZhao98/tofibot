# TOFI 文档总索引

TOFI 是一个自托管的 AI Bot 工作台：Go 后端在 `internal/`，React 19 网页端在 `ui/`。这一页是仓库里所有文档的地图：先读什么、每份文档管什么、哪些还能当依据、哪些已经过时。

- 入口是仓库根的 [`AGENTS.md`](../AGENTS.md)（代码约定），本页是文档入口。
- 规则（决定 [D27](decisions/0027-docs-are-the-system.md)）：**每次合并、发版、owner 拍板，都在同一个提交里更新对应文档。** 哪种事件改哪份文件，见 [`process.md` 第 10 节](process.md)。
- 某份文档过时了，就在本页把它的状态改掉，写明被哪份取代。不删旧文档。
- 本页的状态审计做于 2026-10-20（PDT），基线 `748c774`（= `v0.1.4-rc.29`）。审计方法：逐份读文档，用 `grep`、`git log` 对照代码。

## 新人（人或 AI）阅读顺序

| 顺序 | 读什么 | 读完知道什么 |
|---|---|---|
| 1 | [`AGENTS.md`](../AGENTS.md) | 代码边界和每块子系统必须遵守的约定 |
| 2 | [`product/status.md`](product/status.md) | TOFI 现在能做什么、线上是哪一版、owner 的打分标准和评测结果 |
| 3 | [`decisions/README.md`](decisions/README.md) | owner 拍板过的每一条决定（D01–D31），以及为什么 |
| 4 | [`process.md`](process.md) | 怎么开分支、测试、评测、预览、发版、做安全审查、选模型 |
| 5 | [`product/roadmap.md`](product/roadmap.md) | 已完成、进行中、下一步、暂缓 |
| 6 | [`releases.md`](releases.md) | 每个版本（含每个 rc）带进了什么 |
| 7 | 动界面之前：[`design/README.md`](design/README.md) | 设计令牌、组件清单、设计原则、迁移方案 |

## 状态口径

| 状态 | 含义 |
|---|---|
| **现行** | 写的就是现在的代码和行为，可以当依据。局部滞后的在备注里点名。 |
| **历史** | 某次改动、评审或验收的记录。按当时是准的，但没人维护，不能当现状用。 |
| **已取代** | 关键结论已被后来的代码、文档或 owner 决定推翻。备注写明被谁取代。 |

## 1. 产品

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`product/status.md`](product/status.md) | 产品现状、当前版本、owner 评分标准（85 分可公开发布）和自评、按领域的能用功能与已知缺口、评测记录 | 现行 | 每次合并、发版、部署、跑完评测都要更新 |
| [`product/roadmap.md`](product/roadmap.md) | 已完成 / 进行中 / 下一步 / 暂缓 | 现行 | 做完一项就在同一提交里挪到"已完成"，写提交号和 rc |
| [`../README.md`](../README.md) | 对外介绍：安装、`tofi` 命令、从源码构建、许可 | 现行（部分滞后） | 最后修改 `f46d446`（10-09），早于 `735a40f`（10-11），安装流程里没有"先问前面有没有反向代理"、`--behind-proxy`、`tofi config trusted-proxies`；以 [`deploy/self-host/README.md`](../deploy/self-host/README.md) 为准 |
| [`agent-plan/proactive-followups.md`](agent-plan/proactive-followups.md) | 秘书主动跟进的设计：主助理、open loops、克制规则 | 现行 | P0（shadow）已实现，`internal/app/followups_*.go`；投递仍被拒，`internal/app/followups_http.go:160` `followup_delivery_unavailable`；P1 之后未做 |

## 2. 决定

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`decisions/README.md`](decisions/README.md) | 全部决定的总表和新增方法 | 现行 | 一条决定一个文件，`decisions/00NN-*.md` 对应 D*NN* |
| [`decisions/0001`](decisions/0001-persona-generator-no-examples.md) … [`decisions/0031`](decisions/0031-lookup-at-the-point-of-decision.md) | D01–D31 各自的原话要点、理由、影响的代码 | 现行 | 被推翻的决定标"已被 Dxx 取代"，不删；D24–D29 记为 10-21，但提交时间换算 PDT 是 10-20 晚，哪个为准待确认（见 `decisions/README.md`） |

## 3. 版本

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`releases.md`](releases.md) | 每个版本（v0.1.0 起，含每个 rc）改了什么、线上在跑哪版 | 现行 | rc 边界取自 `release.yml` 的 Actions 运行记录（复查命令在文内）；是 `agent-plan/wave-notes.md` 的接替 |
| [`agent-plan/wave-notes.md`](agent-plan/wave-notes.md) | 旧的发版波次清单：v0.1.1、v0.1.3 和截至 10-16 的"Next wave" | 历史（部分已取代） | 最后一次改动 `385050e`（10-16），之后的 `v0.1.4-rc.*` 没写进来。v0.1.3 #4 的 `<progress>` 和 #19 的 `respond` 收尾在 flag 开启时被 `say` 取代（`structured-output.md` §12）；#20 "多个 @ 交给第一个被 @ 的 Bot"被 [D05](decisions/0005-every-mentioned-bot-answers.md) 取代 |

## 4. 流程

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`process.md`](process.md) | 分支与 worktree、测试（退出码证明）、提示词规则、评测、UI 预览、发版、安全审查闸、模型分工、文档同步 | 现行 | 第 11 节列了流程上的待确认事项 |
| [`../AGENTS.md`](../AGENTS.md) | 代码边界和各子系统的约定（动效、侧栏、提问汇合点、安全闸、时间线性能、预览） | 现行 | 代码约定最后修改 `5592eb3`（10-20），已含 D10、D11、D21、D22、D24；本分支在顶部加了“从这里开始”（13 行，之后各文档引用的 `AGENTS.md` 行号已按新行号写）。改实现约定要同步改它 |
| [`../eval/rubric.md`](../eval/rubric.md) | 离线评测的评分细则（五个维度加布尔标记），配 `eval/run_eval.py` | 现行 | 10-19 仍在改（`717e0d8`） |
| [`verification.md`](verification.md) | 2026-10-01 从私有仓拆出源码时的验证记录 | 历史 | 只提交过一次（`35db690`）。":4 许可未定"已不成立（`LICENSE` Apache-2.0，`f19fcf8`；`ui/LICENSE` PolyForm Shield，`3bf937e`。`3bf937e` 当时给服务端定的是 AGPL-3.0，同日被 `f19fcf8` 改成 Apache-2.0）；":17 安装器未完成"已不成立（已有 `v0.1.0`–`v0.1.3`） |
| [`../EXPORT-MANIFEST.json`](../EXPORT-MANIFEST.json) | 10-01 拆仓时交付文件的 SHA-256 清单 | 历史 | `source_revision` 在本仓库不存在；1382 条哈希里 364 条与现文件不符、4 个文件已不在（10-20 重算）；`license_status` 已被 `LICENSE` 取代 |

## 5. 设计

从 [`design/README.md`](design/README.md) 进，那里有阅读顺序。

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`design/tokens.md`](design/tokens.md) | 颜色、字体、间距、圆角、阴影、模糊、动效令牌的真实取值；z-index 现状和层级刻度提议（§9） | 现行（第二部分是提议） | 第二部分（层级刻度、合并方案）owner 点头前不是规范（[D28](decisions/0028-headless-design-system-migration.md)） |
| [`design/components.md`](design/components.md) | 每个组件的状态、焦点、键盘、关闭、位置、层级、动效、已知问题（C1–C41） | 现行 | 基线 `748c774`；改组件行为要同一提交更新 |
| [`design/principles.md`](design/principles.md) | 设计原则 P1–P8，每条带检查方法、冲突取舍和 PR 评审清单 | 现行 | 规则来自 D16–D23 等决定 |
| [`design/migration.md`](design/migration.md) | 迁到 Radix / Motion / dnd-kit 的分步方案、体积实测、风险和预览点 | 方案，等 owner 点头 | 点头前不按它改代码；§7 列了要 owner 定的 Q1–Q4 |
| [`../ui/src/locales/README.md`](../ui/src/locales/README.md) | Web UI 本地化规则 | 现行 | 7 种语言目录都在 `ui/src/locales/` |
| [`../ui/src/icons/README.md`](../ui/src/icons/README.md)、[`../ui/public/icons/README.txt`](../ui/public/icons/README.txt) | TOFI 自绘图标 v2.1 的用法 | 现行 | `ui/public/icons/manifest.json` 正好 144 个图标 |
| [`../ui/src/lib/tofi-avatar/README.md`](../ui/src/lib/tofi-avatar/README.md) | 猫头像动效库 v2 的 API | 现行 | 10-20 仍在改（`39a1822`） |
| [`../ui/src/motion-lab/HANDOFF.md`](../ui/src/motion-lab/HANDOFF.md) | Motion Lab 接入生产 UI 的交接说明（09-29） | 历史 | 文内自称"旧交接表是当时快照"；Motion Lab 仍在构建（`ui/vite.motion-lab.config.ts`） |
| [`../ui/test-fixtures/README.md`](../ui/test-fixtures/README.md) | 设计验收夹具说明 | 现行 | 内容很短 |
| [`../ui/test-fixtures/runtime/reply-card-audit.md`](../ui/test-fixtures/runtime/reply-card-audit.md) | 回复卡片手动审计夹具用法 | 历史 | 10-02 一次提交（`b70e2fd`）；现在是否还能跑：待确认 |
| [`../ui/public/oauth/ASSET-SOURCES.md`](../ui/public/oauth/ASSET-SOURCES.md) | OAuth 页面用的服务商图标来源 | 现行 | 仍被 `internal/app/oauth_service_page.go`、`oauth_completion.go` 引用 |
| [`../assets/README.md`](../assets/README.md) | README 头图来源 | 历史（一句过时） | 最后一句"许可待定"已被 `LICENSE` 取代 |

## 6. 技术规格与契约

### 6.1 运行时与对话

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`agent-plan/structured-output.md`](agent-plan/structured-output.md) | `say` 工具协议（answer / note / message）、Phase 0–3、评测数字、§13 群聊、§14 每个被 @ 的 Bot 都回答 | 现行（有滞后） | 对应 D04、D05、D06、D08；开关默认开（`internal/app/automation_settings.go:84`）。最后更新 `237f5ce`（10-19），之后的 `say` 改动（`b429a4e`、`26065b1`、`f342b83`、`6bc0103`）没写进来 |
| [`agent-plan/run-resume.md`](agent-plan/run-resume.md) | 任务断点续跑和推理保留的设计（Phase 0–5） | 现行（状态行过时） | 第 3 行写"Phases 0, 1 … not started"不对：Phase 1（`25a739c`、`4a169df`）、Phase 2（`526a979`）已合并。Phase 3–5 没找到实现，待确认 |
| [`runtime-reliability.md`](runtime-reliability.md) | 工具失败的恢复契约：outcome 类型、重试、uncertain 防重放、预算耗尽、文件写入身份 | 现行（一行已取代） | 第 25 行 `approval_expired` "续期生成新的待批卡片"已被取代：从 `62ada32` 起过期审批改为收尾，`internal/app/approval_renewal.go:12` 一律拒绝续期。"Auto Review scope"一节是 10-02 的探测记录（历史） |
| [`agent-plan/mcp-tool-tiers.md`](agent-plan/mcp-tool-tiers.md) | MCP 工具分级：read_only / write / high_risk，无人值守时的拒绝 | 现行（缺一块） | 代码在 `internal/app/mcp_tool_tiers.go`。10-20 的"总是允许"常驻授权（D24，`internal/app/mcp_standing_allowance.go`）没写进来，现只在 `AGENTS.md` "Security gates" 一节 |
| [`../internal/extensions/DISCOVERY.md`](../internal/extensions/DISCOVERY.md) | 按 run 发现扩展工具：协议、目录检索、两跳调用 | 现行 | 只提交过一次（`36a9920`）；与后加的 `...WithCallGate`（`internal/extensions/discovery.go:34`）是否对齐：待确认 |
| [`agent-plan/tools-redesign.md`](agent-plan/tools-redesign.md) | 10-09 工具/连接审计和重设计契约 | 历史（部分已取代） | `main@5be5851` 的快照，行号按另一个仓库根写。审批结论被 `mcp-tool-tiers.md` 取代。§3.5 hybrid direct exposure 没做（`internal/extensions/discovery_instructions.go:14` 仍是两跳），放弃还是推迟：待确认 |
| [`../contracts/alpha-api.md`](../contracts/alpha-api.md) | 早期 Alpha API 和运行时契约 | **已取代** | 鉴权写的是 `auth:{mode:"none"}`，现为 `"accounts"`（`internal/app/accounts.go:407`）；"无 `@` 进入 Luna triage"已不存在，未 @ 的消息由主助理接，多个 @ 按 D05。不要当依据 |

### 6.2 自托管、安装与更新

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`../deploy/self-host/README.md`](../deploy/self-host/README.md) | 自托管安装的完整说明：选项、`tofi` 命令、安全更新、发版、已知限制 | 现行（部分滞后） | 最后修改 `735a40f`（10-11）。之后的 `fad7b75`（10-13，新装时每台账户电脑在预算够时给 2 vCPU，内存取预算的四分之一并限在 2–4 GiB）和 `4f7a979`（10-19，TLS 代理后的 Secure cookie 和 HSTS）都没改这份文件：第 502 行仍写"1 vCPU / 1 GiB each"，全文没有 HSTS |
| [`agent-plan/one-command-install.md`](agent-plan/one-command-install.md) | 一键安装的批准计划（10-07）和端到端验收清单 | 只有验收一节现行 | `AGENTS.md` 把第 272–289 行的验收当作安装器脱离 release-candidate 的门槛；其余正文是历史（如":65 还没有 `.github/`"）。验收是否已在干净 KVM 上整体通过：待确认 |
| [`agent-plan/installer-backlog.md`](agent-plan/installer-backlog.md) | 安装器和自托管待办 | 现行（标题过时） | 第 1、2 项已完成（`735a40f`）；第 5 项 zram/swap 未做（`deploy/microvm/init.sh` 无相关代码），但第 3 行只写了"3 and 4 are open"。第 3、4 项：待确认 |
| [`agent-plan/safe-updates.md`](agent-plan/safe-updates.md) | 安全更新的实现契约：更新前备份、补丁版自动更新、页面更新提示 | 历史 | 已实现：`deploy/self-host/tofi_host.py:126-130`、`:1603`，`ui/src/UpdateNotice.tsx`。仍成立的一条：`io.tofi.data-schema` 还是常量 `tofi-account-data-v1`（`Dockerfile:42`） |

### 6.3 账户数据迁移（portability）

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`portability-secrets-v3.md`](portability-secrets-v3.md) | bundle v3：选中的保险库环境变量、不生效的恢复胶囊 | 现行 | 现行格式（`internal/app/portability_http.go:16` 返回 version 3） |
| [`portability-v1.md`](portability-v1.md) | bundle v1：范围、加密、原子导入 | 历史 | 入口"Settings → account → data migration"已改到 设置 › 高级（`ui/src/App.tsx` `openSettings("advanced")`）；16 MiB 限额仍一致 |
| [`portability-attachments-v2.md`](portability-attachments-v2.md) | bundle v2：附件字节导出/导入 | 历史 | 限额一致（`internal/app/portability_attachments.go:25-26`）；文中"浏览器导入验收未完成"后来是否补做：待确认 |
| [`portability-review-fixes.md`](portability-review-fixes.md) | v1 评审修复和合成浏览器验收 | 历史 | 回归测试仍在：`internal/app/portability_review_regression_test.go` |

### 6.4 设置页与界面计划

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`agent-plan/settings-redesign.md`](agent-plan/settings-redesign.md) | 设置页重设计契约（9 个 tab，多账户管理员再加 admin；Phase 1–3） | 历史 | 现在是 12 个 tab（`ui/src/SettingsShell.tsx:12`），比契约 §1.1 的 10 个多了 followups、automation。状态行写 Phase 1（`fb74527`）、Phase 2（`d43d8dc`）已上线，Phase 3 等 tools-redesign WS2、WS6；Phase 3 是否完成：待确认 |
| [`agent-plan/i18n-server-strings.md`](agent-plan/i18n-server-strings.md) | 服务端面向用户字符串的 i18n 计划（phase 4） | 现行（计划未执行） | 文中数字是 10-08 的；10-20 统计 `internal/app/*.go`（不含测试）609 处 `writeErr(`，32 处带中文 |
| [`agent-plan/i18n-worklist.md`](agent-plan/i18n-worklist.md) | Web UI i18n 第二阶段转换清单 | 历史 | 已做完（`e513667`、`63c1dce`）；规则以 `ui/src/locales/README.md` 为准 |

### 6.5 连接与插件目录

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`agent-plan/plugin-library.md`](agent-plan/plugin-library.md) | 连接目录调研：目录字段和中文配置指南（10-09） | 历史 | 现目录 `ui/src/integrationCatalog.ts` 18 个 id，notion-token、discord、slack、yahoo-finance 为 `held` |
| [`agent-plan/plugin-library-mainstream.md`](agent-plan/plugin-library-mainstream.md) | 首屏推荐服务调研第二稿（10-09） | 历史 | 调研稿，随合并提交 `6544e42`（feat/remember-approval）进入仓库 |
| [`integrations/token-catalog-review.md`](integrations/token-catalog-review.md) | token 鉴权服务第一轮评审 | 历史 | 结论和目录一致（四个 `held`）；配套证据 [`integrations/acceptance.json`](integrations/acceptance.json) |
| [`integrations/notion-adapter-candidate.md`](integrations/notion-adapter-candidate.md) | Notion token 版 stdio 适配器候选 | 历史 | 代码在 `internal/mcprunner/notion_adapter.go`；仍未进可添加目录 |
| [`integrations/notion-package-qualification.md`](integrations/notion-package-qualification.md) | Notion 2.5.2 包来源核验：暂缓 | 历史 | 一次性核验（`0be8060`） |
| [`integrations/icons/NOTICE.md`](integrations/icons/NOTICE.md) | GitHub、Discord 官方图标候选和来源 | 历史 | 仍未进 `ui/public`、`ui/src`；是否继续保留：待确认 |

### 6.6 验收证据（`docs/acceptance/`）

| 文档 | 管什么 | 状态 | 备注 |
|---|---|---|---|
| [`acceptance/approval-expiry/`](acceptance/approval-expiry/review.json)（json/txt/png，无说明文字） | 过期审批改为收尾（有界的只读恢复，再给终态总结）的证据 | 现行 | 对应 `62ada32`；行为在 `internal/app/approval_renewal.go:12`、`approval_expiry.go` |
| [`acceptance/conversation-routing/README.md`](acceptance/conversation-routing/README.md) | Bot 和群的稳定链接 `/b/<id>`、`/g/<id>` | 现行 | `ui/src/conversationRoute.ts`，测试 `test:conversation-route(-browser)` |
| [`acceptance/display-metadata/README.md`](acceptance/display-metadata/README.md) | 记忆和定时任务的 title/description 及编辑冲突检测 | 现行（局部滞后） | 第 39 行只列了 once/interval/daily 三种定时，之后加了 weekly（`internal/app/schedule.go:25` `scheduleWeekly`）；weekly 的元数据编辑是否有同等验收：待确认 |
| [`acceptance/autoreview-schedule-lineage.md`](acceptance/autoreview-schedule-lineage.md) | 定时任务 AutoReview 授权来源链 | 历史 | 主体在 `internal/app/schedule_authorization.go`；第 47 行列的 `TestScheduledMCPQualifiedPolicyModesAndOneUse` 已在 `b9a1db7` 删除 |
| [`acceptance/runtime-reliability/README.md`](acceptance/runtime-reliability/README.md) | 10-02 运行时可靠性的合成 Web 验收（20 个用例） | 历史 | "过期可续"、"重复续期产生新卡片"两项已被 approval-expiry（`62ada32`）取代 |
| [`acceptance/runtime-reliability/review-fixes.md`](acceptance/runtime-reliability/review-fixes.md) | 评审意见 → 实现 → 回归测试对照 | 历史 | 测试仍在，如 `internal/app/runtime_review_regression_test.go` |
| [`acceptance/stop-tool-traces/`](acceptance/stop-tool-traces/review.json)（txt/json） | Stop 时工具记录少读一列的修复证据 | 历史 | 修复在 `7fa7317` |
| [`acceptance/autoreview-default-off.md`](acceptance/autoreview-default-off.md) | 10-03 AutoReview 候选版验收："空的 qualified policy registry"，auto 模式不能自动执行 | **已取代** | 被 `b9a1db7`（10-04）和 [`mcp-tool-tiers.md`](agent-plan/mcp-tool-tiers.md) 推翻：现在 auto 模式下审核员 allow 就执行；"不重试"改成格式错或为空时重试一次。只剩"默认关"仍成立（`internal/app/auto_review_settings.go:19`） |

## 7. 已知的文档缺口和待确认

还没有现行文档写到的东西：

| 缺口 | 现在去哪看 |
|---|---|
| "总是允许"常驻授权（D24）没写进 `mcp-tool-tiers.md` | `AGENTS.md` "Security gates"，[`decisions/0024`](decisions/0024-standing-tool-allowance.md)，[`design/components.md` §16](design/components.md) |
| 设置 › 自动行为（D15）没有专门的规格 | `internal/app/automation_settings.go`，[`decisions/0015`](decisions/0015-automatic-behaviour-settings.md) |
| `structured-output.md` 10-19 之后的 `say` 改动（`b429a4e`、`26065b1`、`f342b83`、`6bc0103`） | 提交本身（`git show <提交>`）；部分在 [`decisions/0004`](decisions/0004-structured-output-say.md)、[`decisions/0009`](decisions/0009-reliability-principles.md)、[`product/status.md` §4](product/status.md) |

文档和代码对不上、需要 owner 或后续核实的（都已在上表对应行写"待确认"）：

1. `run-resume.md` Phase 3–5 是否做过。
2. `tools-redesign.md` 的 hybrid direct exposure 是放弃还是推迟。
3. `settings-redesign.md` Phase 3 是否完成。
4. `one-command-install.md` 的端到端验收是否已在干净 KVM 上整体通过。
5. `portability-attachments-v2.md` 的浏览器导入验收是否补做。
6. `installer-backlog.md` 第 3、4 项。
7. `docs/integrations/icons/` 是否继续保留。
8. `deploy/self-host/README.md` 要补 `fad7b75`、`4f7a979` 的改动（已核实没同步，见 6.2 表），由谁在哪个提交补：待确认。
