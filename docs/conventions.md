# 代码约定

这份文件写的是代码库**已经在遵守**的 Go 和 TypeScript/React 约定，每条都带文件出处。它不立新的风格规则：没有出处的写法不算约定。和 `AGENTS.md` 的关系：`AGENTS.md` 管每个子系统的契约（问人、安全闸、动效、层级……），本文管跨子系统通用的写法。UI 的设计规则在 [`design/`](design/README.md)，加新页面的步骤在 [`design/new-page.md`](design/new-page.md)，流程（分支、测试汇报、发版）在 [`process.md`](process.md)。

两条总的取向：

- 数据结构和错误码是给机器读的，文字是给人读的。**机器的判断永远基于结构化的码，不基于消息文字。**
- 对标 Anthropic 公开的做法：给 Agent 的工具怎么设计见 [Writing effective tools for AI agents](https://www.anthropic.com/engineering/writing-tools-for-agents)（工具少而有用、返回有意义的信息、描述写清楚、用评测迭代），上下文怎么给见 [Effective context engineering for AI agents](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)（最小工具集、用轻量标识符按需取而不是全部预载），整体架构见 [Building effective agents](https://www.anthropic.com/engineering/building-effective-agents)（先用简单的、可观察的结构）。TOFI 里对应的落点是 `internal/tooloutcome`（工具返回的结构化结果）、"Built-in Skills"（正文按需 `read_skill` 才加载）和 `internal/app/prompt_budget_test.go`（提示词预算）。

## 1. Go：包和文件布局

- 模块 `github.com/JackZhao98/tofibot`，Go 版本以 `go.mod` 为准。可执行入口在 `cmd/`（`tofi`、`tofi-guest`、`tofi-mcp-runner` 等），其余全在 `internal/`，没有对外的包。
- `internal/app` 是服务本体：一个**平铺**的大包（约 430 个文件），HTTP 路由、存储、各功能都在里面，按**功能**起文件名，不按层：`questions.go`、`handover.go`、`mcp_standing_allowance.go`、`builtin_skills.go`。同一功能的东西放在同一个文件或同前缀的几个文件里，不拆 `models/`、`handlers/` 子目录。
- 有清晰边界的东西才单独成包：`agent`（循环）、`runtime`（运行与恢复）、`provider`（模型提供方）、`extensions`（MCP 与 Skill）、`computer`（Bot 的电脑与 guest）、`tooloutcome`（工具结果与恢复决定）、`saystream`、`shadowreview`、`codexauth`、`mcprunner`。包的依赖方向是 `app → 其余`，其余包不 import `app`。
- 测试文件与源文件同名加 `_test.go`，放在同一个目录（`mcp_standing_allowance.go` / `mcp_standing_allowance_test.go`）；跨功能的验收类测试叫 `*_acceptance_test.go`。平台相关文件用构建标签和后缀（`portability_file_unix.go` / `portability_file_other.go`，`internal/computer/guest/*_linux.go`）。
- 文件头注释写"这个文件管什么、为什么这样"，不重复函数名。例：`internal/app/builtin_skills.go` 开头的那段。
- 格式：`gofmt`。CI 的 `scripts/check-gofmt.sh` 拦新的未格式化文件；`.gofmt-baseline` 里的旧文件只减不增（见 `process.md` 3.3），新代码一律 `gofmt -w` 后再提交。`go vet ./...` 在 CI 里跑，目前无告警。

## 2. Go：错误处理与分类

**规则（D 系列近期决定里反复出现）：分类只看结构化的码，不看消息文字。**

- HTTP 错误体固定为 `{"error": {"code": "...", "message": "..."}}`，由 `writeErr(w, status, code, msg)` 写（`internal/app/app.go:3032`）。`code` 是小写下划线的稳定标识，前端用它查翻译（`ui/src/i18n/errors.ts` 的 `errorText`），`message` 只是英文兜底。新的用户可见错误一定给 `code`。
- 工具调用的结果是 `tooloutcome.Outcome`（`internal/tooloutcome/outcome.go`）：`Status`（`need_approval`、`validation_error`、`transient_failure`、`permanent_failure`、`denied`、`approval_expired`、`uncertain_effect`）、`Code`、`Certainty`（`not_executed` 等）、`NextAction`。包注释写明："Remote prose is never used to authorize retries or classify execution certainty." 用 `tooloutcome.New(...).Err()` 返回、`tooloutcome.FromError` / `errors.As` 取回。文件缺失是例子：guest 回 `file_not_found`（`tooloutcome.FileNotFound`），主机按码分类，不看底层 stat 错误的措辞（`AGENTS.md` "publish_file paths and failures"）。
- 可重试的提供方错误由 `provider.IsRetryable`（`internal/provider/errors.go:100`）判断：HTTP 状态、`errors.Is(err, ErrStreamIncomplete)`、`errors.As` 到 `*ResponseFailedError` 的码。新的瞬时错误码加进 `retryableResponseFailure`（`AGENTS.md` "Provider errors and failed runs"）。
- 用 `errors.Is` / `errors.As` 判断，需要带信息就定义类型或哨兵，用 `%w` 包装。
- 给机器看的错误体可以带上游详情，给人看的提示只给具名原因加状态，日志两者都不带原文（见第 5 节）。
- **已知遗留（不要照着写）**：少数地方仍在匹配 `err.Error()` 文字，例如 `internal/app/mail_drafts.go:47`（`duplicate column name`）、`internal/app/child_failure_return.go:35`（`no such table`）、`internal/app/computers.go:1250`、`internal/agent/shell.go:375`、`internal/agent/agent.go:2068`、`internal/computer/client.go:486,502`、`internal/computer/guest/protocol.go:273`。碰到这些代码时顺手换成码或哨兵；新代码不许新增。
- `writeErr(w, 500, "storage", err.Error())` 这类写法（`internal/app/app.go` 多处）把内部错误文字放进了 `message`；新代码的 500 给固定的英文短句，细节写日志。

## 3. Go：context 与超时

- 能拿到请求或运行的 `ctx` 就传下去，函数第一个参数 `ctx context.Context`（`internal/app` 里约 230 处）。取消要能一路传到出站调用。
- 外部调用都有上限，上限写成命名常量或在构造处写明，不裸放：`http.Client{Timeout: ...}`（`internal/app/model_settings.go:507` 10 s、`update_notice.go` 的 `updateFetchTimeout`、`account_computer.go:60` 75 s），`context.WithTimeout`（`approval_expiry.go:296`、`computer_control.go:52`）。对话式的等待有自己的到期（问题卡默认 30 分钟，`AGENTS.md` "Asking a human"）。
- 后台任务没有请求 `ctx` 时才用 `context.Background()` 再套 `WithTimeout`（`artifact_publish.go:41`），并 `defer cancel()`。
- 提供方的重试有总预算（`RetryProvider`，`TotalBudget` 2 分钟，`Retry-After` 最多听 60 s）；不要在工具里写自己的轮询循环（"Asking a human" 一节明令）。
- SQLite 连接 `busy_timeout(10000)`、`SetMaxOpenConns(1)`（`internal/app/app.go:319,332`），所以不要在持有事务时再去开第二条连接。

## 4. SQL 与迁移

- 存储是 SQLite（WAL，`foreign_keys=ON`，`app.go:564`），没有迁移文件目录。表结构在 `internal/app` 里以 `migrate<功能>(db *sql.DB) error` 函数的形式存在，由 `app.go` 中的初始化链依次调用（`migrateStandingAllowances`、`migrateBuiltinSkillSetting` 等，`app.go:740-778`）。
- 迁移必须**幂等**：`CREATE TABLE IF NOT EXISTS`，加列用 `ensureColumn(db, table, column, ddl)`（`app.go:781`，先读 `PRAGMA table_info`）。每次启动都会跑，所以不能依赖"只跑一次"。例：`internal/app/mcp_standing_allowance.go:71`。
- 新增列或表要走全每道闸：导出/导入（`portable*`）是否带它、账户删除是否清它、测试夹具是否建它。`AGENTS.md` "Sidebar order and pins" 里写明 `pin_order`/`sort_order` 刻意不进导出。
- 查询一律参数化（`?`），值不拼进 SQL。动态部分（表名、列名）只能来自代码里的常量。
- 密码与密钥只进保险库（`AnswerUserForm`），不进 SQL、事件或工具结果（`AGENTS.md` "Asking a human"）。
- 改已有表之前先读它现在的真实结构（`PRAGMA table_info` / 现有 `migrate*` 函数），不凭记忆写 DDL。

## 5. 日志

- 用标准库 `log.Printf`，前缀写方括号的区域名：`[expiry]`、`[computer]`、`[accounts]`（`approval_expiry.go:359`、`computer_watchdog.go:179`、`accounts.go:489`）。`internal/pkg/logger` 把输出同时写到 stdout 和滚动文件（10 MB × 3 份，保留 28 天），并接管标准库 `log`。
- 一条日志写：哪个区域、哪个对象（id）、什么失败、`%v` 错误。不写用户内容。
- **日志里永远不放**：密码和任何密钥或令牌、`secret_ref` 之外的凭据值、请求头里的授权信息、用户在聊天里发的内容或表单输入、上游服务原文（提供方的响应体、MCP 的错误体）、完整的带查询串的 URL（审批卡里的地址是脱敏过的，见 `AGENTS.md` "Security gates"）。头部名字可以记，值不行（`internal/extensions/management.go:70` 的 `maskedHeaders` 是现成的）。
- 键盘按键、剪贴板、远程屏幕上的内容不是答案、事件、结果或表的一部分（`AGENTS.md` "Asking a human" 的交接卡一段）。

## 6. 测试

- 测试只用合成数据（`AGENTS.md` "Core source boundaries"）。用户的真实例子只进隔离的验收测试，不进产品提示词。
- Go：标准 `testing`，辅助函数 `t.Helper()`，临时目录用 `t.TempDir()`，HTTP 用 `httptest`，夹具放在测试文件里（例：`mcp_standing_allowance_test.go` 的 `approveAlways`）。慢的压力测试用 `testing.Short()` 跳过（`backend_perf_test.go:134`）。
- **每个修复都配一个"撤掉修复就变红"的测试**（D30 规则 6，做法见 `process.md` 3.2）：把产品代码撤回、测试保留，看到非零退出码，再恢复看到 0，汇报里写两个退出码。
- **用退出码证明通过**，不数 `ok` 行（`process.md` 3.1）。管道会吞退出码；退出码 0 也不一定是跑过了（见下一条）。
- **UI 浏览器脚本和 `PLAYWRIGHT_MODULE`**：`ui/package.json` 没有 Playwright 依赖，脚本从环境变量 `PLAYWRIGHT_MODULE`（Playwright 的 `index.mjs` 的**绝对路径**）加载它。两种写法并存：`import(process.env.PLAYWRIGHT_MODULE || "playwright")`（没设就找不到包，非 0 退出）和"没设就打印 `SKIP` 并 `exit 0`"（`process.md` 3.1 列了 7 个）。所以汇报时除了退出码还要说明输出里没有 `SKIP`。`test:dialogs` 和 `test:catalog` 没设就直接失败，是更好的写法，新脚本照它们写。CI 的装法见 `.github/workflows/ci.yml` 的 `ui-guards` 作业。
- 预算测试量的是生产包（`ui/scripts/lib/chat-fixture.mjs` 用 vite 打包再测），不量开发服务器。
- 时间敏感的断言本地跑；CI 用 `TOFI_SKIP_TIMING=1` 跳过（`test:dialogs`、`test:overlays`）。新加的时间断言要写进同一个开关，不然 CI 上会抖。
- Python：`unittest`，标准库发现（`python3 -m unittest discover -s <目录> -p 'test_*.py'`）。缺依赖的测试要响亮地跳过（stderr 和报告里写 SKIPPED）。
- 基线文件只减不增：`ui/scripts/layers-baseline.json`、`ui/scripts/motion-tokens-baseline.json`、`ui/scripts/i18n-baseline.json`、`.gofmt-baseline`。

## 7. 提示词（只写原则）

完整规则在 [`process.md` 第 4 节](process.md)。要点：

- 我们写的模型系统提示词和内置 Skill 指令都用英文；保留用户内容和第三方元数据原样。
- 提示词里不放例句、不写 "Not X, but Y" 式对比、不放硬编码关键词列表（D01）；`internal/extensions/builtin_skills/tests/test_skill_text.py` 对 SKILL.md 强制这条。具体知识放脚本里，不放进说明文字。
- Bot 人设有问题，改人设生成器的提示词（`set_bot_profile`），不改单个 Bot。
- 改提示词要有前后评测对比；提示词变长要量预算并调整 `internal/app/prompt_budget_test.go` 的上限。
- 工具的描述和返回值按 Anthropic 的工具设计建议写：说清楚做什么、什么时候用、返回有意义的信息，错误给出下一步（`tooloutcome` 的 `NextAction`）。

## 8. 前端：状态与取数

- API 调用只通过 `request()` / `api.*`（`ui/src/api.ts`）：同源、`credentials: "same-origin"`、读请求默认 15 s 超时、`401` 广播 `tofi:unauthorized`、错误抛 `ApiError(status, message, code)`。组件不直接 `fetch("/api/…")`。
- 取数的组件用 `AbortController`，卸载时取消；设置页加载失败要有重试入口（`AutomationSettings.tsx`、`StandingAllowances.tsx`）。
- **共享数据用共享 hook，一个请求多处用**：`useModelCatalog`（`ui/src/useModelCatalog.ts`）把 `GET /api/models` 合并去重（3 s 内复用）、窗口重新聚焦且超过 10 s 才重取、`invalidateModelCatalog()` 主动失效，重取时旧列表留在屏幕上。新的"多个选择器都要"的数据照这个形状做，不要每个组件各取一次。
- **没有东西在动就不轮询**。真要轮询时：只轮询确实在变的东西，没变就退避（`ui/src/useQuestions.ts` 的 `questionPollDelay`：有待答的问题 3 s，其余指数退避到 15 s），后台轮询不翻 `loading`、不替换没变的列表（`sameData.ts`），事件驱动的变化把退避拉回快档。服务端配合 ETag：`internal/app/questions.go:735-760` 对列表算 `ETag`、`Cache-Control: private, no-cache`，没变返回 `304`。设置页的列表走事件刷新（`tofi:standing-allowances-changed`），不轮询。
- 页面读数据用"一次聚合读加具名降级"，别让一页发十几个请求。移动端设置列表右侧的提示只在列表显示时取，且不探测外部服务（`SettingsShell.tsx` 的 `useMobileHints`）。
- 高频状态放局部，不放大组件的 state：流式文本在 `draftStream.ts`，不进 Workspace state；输入框每键不许重排（D02）。不确定就按 `process.md` 3.4 的预算脚本量。
- `memo` 组件显示文字时自己调用 `useTranslation`；跨组件稳定的回调用 `RowHandlers` 这样的稳定对象。
- 文案、键、复数、`errorText`：`ui/src/locales/README.md` 与 [`design/new-page.md`](design/new-page.md) 第 4 节。
- 不用 `React.lazy` + `Suspense` 包 Radix 模块；懒加载块在 `lazyPanels.tsx` 登记（`AGENTS.md` "Web UI menus and popovers"）。

## 9. CSS 与令牌

- 样式文件按区域放在 `ui/src/*.css` 与 `ui/src/settings/*.css`；全局样式表的导入顺序由 `ui/src/main.tsx` 决定，目录页（`ui/src/catalog/main.tsx`）必须同序，`test:catalog` 守。
- 颜色、动效、层级只用 `ui/src/design-tokens.css` 的令牌（`design/tokens.md`）。守卫：`test:final-v2-tokens`、`test:motion-tokens`、`test:layers`。字面时长、`cubic-bezier()`、裸 `ease`、数字 z-index 都会挂。
- 每个 `backdrop-filter` 都带 `-webkit-backdrop-filter` 孪生。
- 每个动画有 `prefers-reduced-motion: reduce` 分支。深色模式只由 `html[data-theme="dark"]` 触发，CSS 里没有 `prefers-color-scheme`。
- 设置页的选择器带 `.workspace .settings-page-body` 前缀（`settings/automation.css`、`settings/settings-polish.css`）。
- 圆角、间距、阴影没有测试守，贴近同类组件已有的值，不为新页面新造一档。
- 桌面客户端分支用 `html[data-desktop]` / `isDesktop`，每个分支写明原因。

## 10. 注释

- **和周围的密度一致。** `internal/app` 里多数函数没有逐行注释，只在非显然的地方写"为什么"；`ui/src` 里的 hook 和模块头有一段说明契约和理由（`useModelCatalog.ts`、`overlays/ModalDialog.tsx`）。不要在一个安静的文件里堆满注释，也不要在一个解释型的文件里一行不写。
- 注释写原因和约束，不复述代码；写清不变量、被拒绝的做法和出处（决定编号 `D31`、文档位置）。
- 过时的注释比没有注释更糟：改行为就改注释。文档里的行号、commit、命令在提交时核对过（`process.md` 第 10 节）。
- 文档用中文，模型提示词、代码注释和标识符用英文（现有文件如此）。

## 11. 给 AI 会话

- 先读 `AGENTS.md`，再读 `docs/process.md`；动界面前读 `docs/design/`。
- 不 push、不打标签、不 ssh、不碰线上（D30）；不用裸 `git stash`；不读不打印密钥。
- 改了约定，在同一个提交里改本文或 `AGENTS.md`。
