# TOFI Plugin Library（连接目录）调研与接入指南

调研日期 2026-10-09。本文是 `tools-redesign.md` §3.2–3.4 "Connections" 目录（目录 → 连接 → 测试 → 完成）的内容来源：每个条目给出机器可用的目录字段 + 给自托管用户看的中文配置指南。只做文档，不改代码。

## 0. 口径与可信度

**核验方式**（按可信度从高到低）：

| 标记 | 含义 |
|---|---|
| **live** | 2026-10-09 对远程端点实测：`initialize` + `tools/list`（无鉴权端点），或读 401 的 `WWW-Authenticate` + `/.well-known/oauth-*` 元数据确认 DCR |
| **pkg** | 从 npm / PyPI 已发布包（tarball / wheel）源码里读出的工具名、环境变量、版本、`engines`、`requires_python` |
| **doc** | 厂商官方文档 / 官方仓库 README |
| **UNVERIFIED** | 未能从一手来源确认，上线前必须实测 |

- 版本号 = 2026-10-09 时 registry `latest`，"日期" = 该版本发布日期。GitHub API 当日对本机限流，星数取自仓库页面，是近似值。
- 内存数字全部是**估算（UNVERIFIED）**，要以 `runner.go` 启动后的 RSS 采样为准（redesign §3.4）。
- 未读取任何凭证，未 ssh。副作用说明：调研时为验证 loopback 回调，曾向 Sentry 和 Cloudflare 的 `/register` 发送真实 DCR 注册请求，在对方留下 4 个名为 "TOFI research probe" 的公共客户端（无令牌、未授权）。

## 1. 先读：TOFI 运行约束（决定了哪些插件能"一键添加"）

以下事实从仓库代码里读出，适用于所有本地插件：

| 约束 | 事实（来源） | 对目录的影响 |
|---|---|---|
| 安装方式 | `internal/mcprunner/install.go`：只支持 `npm`（`npm install --ignore-scripts`）、`pypi`（`python3 -m venv` + `pip install pkg==ver`）、`builtin_gog`。必须有固定版本和 `binary` 名。不支持 git URL、Docker、Go 二进制 tarball | Go/Docker-only 的服务器（gitea-mcp、pihole-mcp、portainer、SaseQ discord）不能一键装。依赖 postinstall 的 npm 包会坏（例如需要编译原生模块的） |
| Python 版本 | Runner 镜像是 `node:22-bookworm-slim` + Debian `python3`，即 **Python 3.11**（`deploy/mcp-runner/Dockerfile`） | `requires_python>=3.12/3.13` 的包装不上：postgres-mcp（≥3.12）、ha-mcp（≥3.13）、mcp-sqlite（≥3.12）、matrix-mcp（≥3.12）、tb1337/paperless-mcp（≥3.13）。Runner 用的是 pip，不是 uvx，所以"uvx 自动下载 Python"这条路**目前不存在** |
| Node 版本 | **Node 22** | `engines.node>=24` 的包装不上或运行报错：@doist/todoist-mcp、@baruchiro/paperless-mcp、ntfy-mcp-server。@sentry/mcp-server 要求 ≥22.13（应满足，UNVERIFIED 具体小版本） |
| 系统工具 | Runner 镜像**没有 `git`**（microvm 镜像有，Runner 镜像没有） | `mcp-server-git` 依赖 git 可执行文件，当前不可用 |
| CPU | TOFI 只支持 Linux x86_64（`deploy/self-host/tofi_host.py`） | x64-only 的包（@jaredtrent/jellyfin-mcp）可用 |
| 网络发起方 | **远程连接由 TOFI 服务器进程发起**；**本地插件由 Runner/VM 发起** | 家庭实验室服务：远程类（HA、n8n）要求 TOFI 主机能访问局域网地址；本地类（Proxmox、Nextcloud 等）要求 VM 能访问 |
| 内存 | VM 1 GiB；redesign 要求安装前空闲 ≥256 MiB，同时最多 2 个活跃插件 | 优先选远程（零 VM 成本）。重型 Python 包（Immich、Google Workspace）放后面 |
| OAuth 回调 | redesign §3.4：公网 https origin，或手工 loopback `http://127.0.0.1:43821/oauth/callback` | 每个 OAuth 条目都注明是否支持 DCR、是否已确认接受 loopback |

**建议**（不在本文范围内实现，列给 owner 定）：Runner 镜像加 `git`；Runner 镜像升级到 Node 24 + Python 3.12/3.13（或引入 `uv`，按包指定 Python）。这两项能直接解锁约 8 个插件。

## 2. 目录条目 Schema（与 redesign §3.4 对齐）

在现有 `IntegrationPreset`（`ui/src/integrationCatalog.ts`）基础上扩展。服务端 `GET /api/connections/catalog` 返回 JSON，UI 内置一份兜底。

```ts
type CatalogEntry = {
  id: string;                       // kebab-case，稳定；即 mcp.json 的 catalog_id
  display_name_i18n_key: string;    // connections:catalog.<id>.name（EN/zh-CN 文案进 locales）
  description_i18n_key: string;     // 一句话用途
  category: "work" | "homelab" | "search" | "dev" | "comms" | "computer";
  tier: "starter" | "standard" | "advanced";   // starter = v0.1 首屏；advanced 默认折叠
  maintainer: { kind: "vendor" | "official-reference" | "community"; name: string; repo_url: string };
  kind: "hosted" | "local";         // 在线服务 / 在电脑上运行
  // hosted:
  url?: string;                     // 可含 {placeholder}，如 "{base_url}/api/mcp"
  url_variants?: { id: string; url: string; label_i18n_key: string }[]; // 如 readonly
  extra_headers?: Record<string, string>; // 非密，如 X-MCP-Toolsets
  // local:
  package?: { registry: "npm" | "pypi"; name: string; version: string; binary: string;
              args: string[]; requires?: { node?: string; python?: string; binaries?: string[]; arch?: "x86_64" } };
  wakes_computer: boolean;          // local=true
  auth: {
    type: "none" | "token" | "oauth" | "env";  // env = 本地插件的环境变量表单
    // token（远程请求头）:
    header?: string; prefix?: string;          // 例 Authorization / "Bearer "
    // oauth:
    dcr?: boolean;                             // 有 registration_endpoint
    loopback_ok?: "verified" | "documented" | "unverified" | "rejected";
    scopes?: string[]; client_fields?: ("client_id" | "client_secret")[]; // 无 DCR 时需用户自备
    // 本地插件或多字段:
    fields?: { name: string; label_i18n_key: string; secret: boolean; required: boolean;
               placeholder?: string; help_url?: string; default?: string }[];
    alt?: CatalogEntry["auth"][];              // 备选认证（如 Linear: OAuth 或 API key）
  };
  setup_steps_i18n_keys: string[];  // 中文指南逐条进 locales
  docs_url: string; key_url?: string;          // "去哪拿 key" 链接
  probe: { tool: string; args: Record<string, unknown>; fallback?: "first_readonly_no_args" };
  tool_defaults: { auto: string[]; ask: string[]; blocked: string[] }; // glob；未列出的按 D2：readOnlyHint→auto，其余 ask
  reach?: "internet" | "lan-from-server" | "lan-from-computer"; // 用于 dns/timeout 错误提示
  resources?: { ram_mb_est?: number; note_i18n_key?: string };
  license: string;
  maintenance: { version?: string; released?: string; stars?: number; checked: "2026-10-09" };
  risk_i18n_keys?: string[];
  icon: { kind: "brand" | "generic"; generic: string /* TOFI 内置 glyph 名 */; brand_page?: string; terms_note?: string };
  status: "active" | "preview" | "experimental";
};
```

和现有字段的对应关系：`tokenHeader/tokenPrefix` → `auth.header/prefix`；`readOnly` → 用 `url_variants` 或 `tool_defaults` 表达；`upstream` → `maintainer.kind`；`setup` → `setup_steps_i18n_keys`；redesign 要求的 `probe`、`tool_defaults`、`display_name_i18n_key`、`wakes_computer` 都已包含。

**trust 默认（D2）**：读操作 auto，写操作 ask，破坏性操作 blocked。三个通用规则：
1. 服务器声明了 `readOnlyHint` 就用它；没声明时以条目里的 `tool_defaults` 为准。
2. 发外部消息或邮件（send/post/publish）一律 ask。
3. delete/purge/exec/raw-API 默认 blocked，owner 可以手动放开。

## 3. v0.1 Starter 集（12 个）

选择标准：在当前 Runner 约束下**一定装得上**；配置不超过 5 步；维护活跃；至少 3 个家庭实验室插件。

| # | 插件 | 类别 | 认证 | 在线/本地 | 入选理由 |
|---|---|---|---|---|---|
| 1 | Microsoft Learn | search | 无 | 在线 | 已在目录；live 验证；零配置 |
| 2 | Context7 | search | 无 key 可用，API key（header）可选 | 在线 | 已在目录；live 验证；写代码必备 |
| 3 | Exa 搜索 | search | 无 key 可用，API key（header）可选 | 在线 | live 验证匿名可用；通用网页搜索 + 网页抓取，均在厂商云端执行（无 SSRF） |
| 4 | GitHub（只读 / 完整） | work | PAT（Bearer） | 在线 | 已在目录；最常用 |
| 5 | Notion | work | OAuth + DCR | 在线 | 已在目录；DCR live 验证；无需建应用 |
| 6 | Linear | work | OAuth + DCR，或 API key | 在线 | 已在目录；`/mcp/readonly` live 存在 |
| 7 | Todoist | work | OAuth + DCR | 在线 | DCR live 验证；个人任务管理刚需（本地包需 Node 24，故只上远程） |
| 8 | **Home Assistant** | homelab | 长期令牌（Bearer） | 在线（局域网） | 官方内置；owner 在用；零 VM 成本 |
| 9 | **n8n** | homelab | 访问令牌（Bearer） | 在线（局域网） | 官方内置；把任意工作流变成工具，是家庭实验室的"万能胶" |
| 10 | **Proxmox VE** | homelab | API Token（环境变量） | 本地（pypi，Python ≥3.11） | owner 在用；包活跃（2026-10-07 发版）；有工具白名单，可以只开只读 |
| 11 | **Calibre-Web** | homelab | 用户名/密码（OPDS Basic） | 本地（npm，Node ≥22） | owner 在用；6 个工具全只读，风险最低（但新项目、采用度低） |
| 12 | 电脑上的文件（Filesystem） | computer | 无 | 本地（npm） | redesign 已规划；官方参考实现；版本已修复 CVE |

**第二梯队**（可以一起上，排序只是为了首屏精简）：DeepWiki 和 Cloudflare 文档（都无需鉴权、live 验证）；Jellyfin（npm，x64 二进制，owner 在用，但采用度低）；Grafana（pypi 内带 Go 二进制）；Sentry 和 Atlassian（OAuth + DCR）；Fastmail（官方，DCR）；Nextcloud（pypi）；IMAP 邮箱（pypi）；CalDAV（npm）。

**Immich 和 Pi-hole 没进 v0.1**：Immich 没有官方 MCP，社区版依赖重（PyAV）、采用度低（约 50★）。Pi-hole v6 唯一成熟的服务器只发 Go 二进制，Runner 装不了。npm 上的替代品已超过 10 个月未更新。详见 §6。

**统计**：§4 涉及 **48** 个插件，分 5 类：search 11、work 7、homelab 11、dev 11、comms 8。其中 v0.1 有 12 个；约 9 个受当前 Runner 约束暂时不能一键安装（Paperless、Gitea、Postgres、Git、SQLite、ha-mcp、Todoist 本地版、Slack 官方版、Obsidian），条目中已标注。另有约 25 个落选候选，见 §6。

---

## 4. 条目详情

格式说明："测试调用"即 Test 阶段的 probe。"信任"按 auto / ask / blocked 列出。每个指南都假定从 **设置 › 连接 › 添加连接 › 目录** 进入。

### 4.1 搜索 / 文档（search）

#### microsoft-learn — Microsoft Learn / 微软 Learn 文档 ★starter
- 描述：Search and fetch official Microsoft & Azure docs and code samples. / 检索并读取微软与 Azure 官方文档和代码示例。
- 维护：vendor（Microsoft）https://github.com/MicrosoftDocs/mcp · 文档 https://learn.microsoft.com/en-us/training/support/mcp
- 传输：在线 `https://learn.microsoft.com/api/mcp`（**live**）
- 认证：无（**live**）
- 指南：
  1. 在目录中选择 Microsoft Learn，点「添加」。
  2. 点「测试连接」。不需要账户（浏览器直接打开这个地址会返回 405，属于正常现象）。
- 测试：`microsoft_docs_search {"query":"Azure Functions timeout"}`（**live**；工具还有 `microsoft_code_sample_search`、`microsoft_docs_fetch`，全部 `readOnlyHint=true`）
- 信任：全部 auto
- 许可：托管服务；仓库为 CC-BY-4.0 文档 · 维护：厂商托管，活跃
- 图标：微软商标规则限制 logo 使用（https://www.microsoft.com/en-us/legal/intellectualproperty/trademarks）→ **generic `book`**

#### context7 — Context7 / Context7 库文档 ★starter
- 描述：Up-to-date, version-specific library docs and code examples. / 最新、按版本的开源库文档与示例代码。
- 维护：vendor（Upstash）https://github.com/upstash/context7（约 6 万★）
- 传输：在线 `https://mcp.context7.com/mcp`（**live**，server 4.2.0）
- 认证：不填 key 也能用（**live**）。key 走请求头：`Authorization: Bearer ctx7sk-…`（doc），也可以用现有预设里的 `Context7-API-Key`（4.2.0 源码同时接受 `context7-api-key`、`x-api-key`）。获取 key：https://context7.com/dashboard
- 免费额度：1,000 次/月，用完硬性阻断（doc）
- 指南：
  1. 打开 https://context7.com/dashboard 登录，创建 API Key（可选，但建议创建：匿名限流更严）。
  2. 在 TOFI 选择 Context7，把 key 粘贴进去，点「测试连接」。
- 测试：`resolve-library-id {"libraryName":"react","query":"useEffect cleanup"}`（**live**）。⚠ 现在 `query` 是**必填**，旧工具 `get-library-docs` 已改名为 `query-docs(libraryId, query)`。redesign §3.4 写的 probe `{libraryName:"react"}` 会校验失败，**需要更新**。
- 信任：全部 auto · 许可 MIT · 维护：npm `@upstash/context7-mcp@4.2.0`（2026-10-07）
- 风险：多个 Bot 共用一个 key 时额度消耗快
- 图标：未找到品牌条款 → generic `book`

#### exa — Exa Search / Exa 智能搜索 ★starter
- 描述：Neural web search and page fetch for agents. / 面向 AI 的语义网页搜索与网页正文抓取。
- 维护：vendor（Exa Labs）https://github.com/exa-labs/exa-mcp-server（约 5k★）
- 传输：在线 `https://mcp.exa.ai/mcp`（**live**，server 3.4.2）
- 认证：匿名可用但有限流（**live**）。key 走请求头 `x-api-key: <key>` 或 `Authorization: Bearer <key>`（doc；未带 key 实测）。**不要用** `?exaApiKey=` 放在 URL 里，会被写进日志。获取 key：https://dashboard.exa.ai/api-keys
- 免费额度：注册送 $10，每月重置（doc）
- 指南：
  1. 不填 key 直接添加即可试用。
  2. 长期使用建议到 https://dashboard.exa.ai/api-keys 创建 key，以 `x-api-key` 请求头填入。
  3. 点「测试连接」。
- 测试：`web_search_exa {"query":"Model Context Protocol specification","objective":"find the official spec site","numResults":2}`（**live**；`query`、`objective` 都是必填）
- 信任：`web_search_exa`、`web_fetch_exa` auto（`readOnlyHint=true`）；`agent_run`（需要通过 `?tools=` 打开）ask
- 许可 MIT · 维护：npm 3.4.2（2026-10-06）
- 风险：查询内容会发给第三方；网页抓取在 Exa 云端执行，碰不到 TOFI 的局域网
- 图标：https://exa.ai/brand（条款 UNVERIFIED）→ generic `search`

#### deepwiki — DeepWiki / DeepWiki 代码库百科
- 描述：AI-generated wiki and Q&A for public GitHub repos. / 公开 GitHub 仓库的 AI 百科与问答。
- 维护：vendor（Cognition）https://docs.devin.ai/work-with-devin/deepwiki-mcp
- 传输：在线 `https://mcp.deepwiki.com/mcp`（**live**）· 认证：无（**live**）
- 指南：
  1. 直接添加，然后点「测试连接」。
  2. 只支持公开仓库，`repoName` 的格式是 `owner/repo`。
- 测试：`read_wiki_structure {"repoName":"modelcontextprotocol/servers"}`（**live**）。注意第三个工具叫 `ask_wiki_question`（**live**），文档里写的 `ask_question` 是旧名。
- 信任：全部 auto（服务器没有声明 readOnlyHint，需要在条目的 `tool_defaults` 里写明）
- 风险：`read_wiki_contents` 输出很大；有限流
- 图标：generic `book`

#### cloudflare-docs — Cloudflare Docs / Cloudflare 文档
- 维护：vendor https://github.com/cloudflare/mcp-server-cloudflare · Apache-2.0
- 传输：在线 `https://docs.mcp.cloudflare.com/mcp`（**live**）· 认证：无（**live**）
- 指南：直接添加，然后点测试。
- 测试：`search_cloudflare_documentation {"query":"Workers KV limits"}`（**live**）· 信任：全部 auto
- 图标：Cloudflare 的 logo 需要许可（https://www.cloudflare.com/trademark/）→ generic `book`

#### tavily — Tavily Search / Tavily 搜索
- 描述：Agent-oriented search, extract, map and crawl. / 面向智能体的搜索、正文提取、站点地图与爬取。
- 维护：vendor https://github.com/tavily-ai/tavily-mcp（约 2.4k★）· MIT
- 传输：在线 `https://mcp.tavily.com/mcp/`（401 + PRM **live**）
- 认证：OAuth + DCR（**live**：`registration_endpoint https://mcp.tavily.com/register`），或者 `Authorization: Bearer tvly-…`（doc）。**不要用** `?tavilyApiKey=`。获取 key：https://app.tavily.com/home
- 免费额度：1,000 credits/月，不需要绑卡
- 指南：
  1. 到 https://app.tavily.com 注册，复制 `tvly-` 开头的 key。
  2. 在 TOFI 选「API key」方式粘贴（TOFI 会以 Bearer 请求头发送），或者直接用「使用 Tavily 登录」。
  3. 点「测试连接」（会消耗 1 credit）。
- 测试：`tavily_search {"query":"Model Context Protocol"}`（工具名取自 npm 0.2.22 源码：`tavily_search/extract/crawl/map/research`；远程端点的命名 **UNVERIFIED**，以 `tools/list` 为准）
- 信任：search/extract/map auto；`tavily_crawl`、`tavily_research`（每次 4–250 credits）ask
- 图标：generic `search`

#### firecrawl — Firecrawl / Firecrawl 网页抓取
- 维护：vendor https://github.com/firecrawl/firecrawl-mcp-server（约 7.5k★）· MIT · npm `firecrawl-mcp@3.29.1`（2026-10-09）
- 传输：在线 `https://mcp.firecrawl.dev/v2/mcp`（来自 subagent 的 live 结论；不带 key 时只有 `firecrawl_scrape/search/parse` 三个工具）
- 认证：`Authorization: Bearer fc-…`（README 明确写了"不要把 key 放进 URL"，旧的 `/{KEY}/v2/mcp` 路径格式不要用）。免费 1,000 credits/月
- 指南：
  1. 不带 key 可以试用。
  2. 正式使用时在 firecrawl.dev 控制台复制 `fc-` 开头的 key，以 Bearer 请求头填入。
- 测试：`firecrawl_scrape {"url":"https://example.com","formats":["markdown"]}`
- 信任：scrape/search/parse auto；crawl/agent ask · 图标：generic `globe`

#### brave-search — Brave Search / Brave 搜索
- 维护：vendor https://github.com/brave/brave-search-mcp-server（约 1.5k★）· MIT
- 传输：**本地** npm `@brave/brave-search-mcp-server@2.1.4`（2026-09-17），binary `brave-search-mcp-server`（**pkg**）；没有官方远程端点
- 认证：环境变量 `BRAVE_API_KEY`（secret）。获取 key：https://api-dashboard.search.brave.com/app/keys
- 价格：每月送 $5 免费额度，之后 $5/千次，**必须绑信用卡**（doc，brave.com/search/api）
- 指南：
  1. 在 https://brave.com/search/api/ 注册并选 Search 计划，绑卡（仅用于验证身份）。
  2. 生成 key。
  3. 在 TOFI 粘贴到 `BRAVE_API_KEY`。
  4. 建议环境变量 `BRAVE_MCP_ENABLED_TOOLS="brave_web_search brave_news_search"`，减少工具数量。
  5. 测试（会唤醒电脑）。
- 测试：`brave_web_search {"query":"Model Context Protocol","count":3}`（**pkg**：还有 `brave_local/news/image/video/place_search`、`brave_llm_context`、`brave_summarizer`）
- 信任：全部 auto（只读，但每次调用都计费）· 内存约 60–100 MB
- 图标：https://brave.com/brave-branding-assets/（条款 UNVERIFIED）→ generic `search`

#### fetch — Web Fetch / 网页读取（在电脑上运行）
- 维护：official-reference https://github.com/modelcontextprotocol/servers/tree/main/src/fetch · MIT
- 传输：本地 pypi `mcp-server-fetch==2026.8.18`，binary `mcp-server-fetch`（**pkg**，Python ≥3.10 ✓）
- 认证：无。可选参数 `--user-agent=…`；**不要**加 `--ignore-robots-txt`
- 测试：`fetch {"url":"https://example.com","max_length":2000}`
- 信任：**`fetch` 默认 ask**。README 明确警告它能访问内网地址；它在 VM 里运行，构成指向家庭局域网（路由器、HA、NAS 管理页）的 SSRF 通道。只有在 VM 出站流量已过滤私网段之后才建议设为 auto。
- 建议：普通用户优先用 Exa 的 `web_fetch_exa` 或 Firecrawl（在厂商云端抓取）
- 图标：generic `globe`

#### jina / perplexity（advanced，简述）
- **Jina AI Reader**：远程 `https://mcp.jina.ai/v1`（**live** `tools/list`，有 12 个工具：`read_url`、`search_web`、`search_arxiv` 等），认证用 `Authorization: Bearer jina_…`。subagent 实测不带 key 调 `read_url` 返回 401，**按必须带 key 处理**。可以用 `?include_tools=read_url,search_web` 限制工具。Apache-2.0。
- **Perplexity**：远程 `https://api.perplexity.ai/mcp`（OAuth PRM，来自 subagent 的 live 结论）或 npm `@perplexity-ai/mcp-server@1.3.0`（**pkg**：`perplexity_search/ask/reason/research`）。纯付费；`perplexity_research` 应设为 ask。

### 4.2 笔记 / 文档 / 工作（work）

#### github / github-readonly — GitHub ★starter（目录里已有）
- 描述：Read and manage repos, issues, PRs and Actions. / 读取和管理 GitHub 仓库、Issue、PR、Actions。
- 维护：vendor https://github.com/github/github-mcp-server（约 33k★，MIT，v2.0.2 于 2026-10-08）
- 传输：在线 `https://api.githubcopilot.com/mcp/`；只读版 `…/mcp/readonly`（两个都 **live**，返回 401 + PRM）；单工具集用 `…/mcp/x/{toolset}[/readonly]`（doc）
- 认证：PAT `Authorization: Bearer <token>`。OAuth **没有 DCR**（**live**：AS 是 `https://github.com/login/oauth`，没有 `registration_endpoint`），所以目录里只提供 PAT。
- 可选请求头（doc，`docs/remote-server.md`）：`X-MCP-Toolsets`（如 `repos,issues,pull_requests`）、`X-MCP-Tools`、`X-MCP-Readonly: true`、`X-MCP-Lockdown: true`（隐藏没有推送权限的用户创建的公开 issue 内容，可以降低提示注入）
- 指南：
  1. 打开 https://github.com/settings/personal-access-tokens/new 创建 Fine-grained token，设置过期时间；如果是组织资源，组织可能要求管理员审批。
  2. Repository access 选「Only select repositories」。
  3. 只读最小权限：Metadata: Read、Contents: Read、Issues: Read、Pull requests: Read。要写入就把对应项改为 Read and write。
  4. 在 TOFI 中选「GitHub（只读）」或「GitHub」，粘贴 token。
  5. 测试连接。注意：远程服务器只服务 github.com，GHES 不支持。
- 测试：`get_me {}`（doc）
- 信任：只读端点 → 全部 auto。完整端点：`create_*`、`issue_write`、`create_or_update_file`、`push_files` ask；`merge_pull_request`、`delete_file` blocked（owner 可以放开）
- 风险：公开 issue 是提示注入的入口（开启 Lockdown 可缓解）；classic PAT 权限过宽
- 图标：https://brand.github.com/foundations/logo（仓库 `docs/integrations/icons/` 已有官方 SVG 候选；不得改动，也不得暗示背书）

#### notion — Notion ★starter（目录里已有）
- 描述：Search, read and edit pages and databases in your workspace. / 搜索、读取、编辑 Notion 工作区中的页面与数据库。
- 维护：vendor https://developers.notion.com/docs/mcp · 托管服务
- 传输：在线 `https://mcp.notion.com/mcp`（**live**）
- 认证：OAuth + **DCR**（**live**：`https://mcp.notion.com/register`，scope `default`，PKCE S256）。托管版**没有** token 模式。loopback：unverified（主流客户端都用 localhost 回调，应该可行）
- 指南：
  1. 点「使用 Notion 登录」，TOFI 会自动注册客户端。
  2. 在 Notion 授权页选择工作区，点「允许」。
  3. 跳转失败时，用「粘贴跳转后的地址」完成授权。
  4. 测试连接。
  5. 说明：托管版按**你本人**的权限访问，**不需要**逐页「Connect to integration」（那是旧的 token 方式才需要）。企业版管理员可能禁用了 MCP。
- 测试：`notion-search {"query":"TOFI"}`（doc；工具名会随版本变化，以 `tools/list` 为准）
- 信任：`notion-search`、`notion-fetch`、`notion-get-*`、`notion-query-*` auto；`notion-create-*`、`notion-update-*`、`notion-move-pages`、`notion-duplicate-page`、`notion-create-comment` ask（`update-page` 的 `replace_content` 会覆盖整页，属高风险）；`notion-*-session`（会启动 Notion 自己的 agent）blocked
- 图标：Notion 品牌规范 https://notion.notion.site/Notion-s-brand-usage-guidelines-How-to-use-Notion-s-brand-in-your-marketing-689045e1761348f887a3c5c80295e00f（只能用来标示集成，不能修改）。仓库此前把 Notion 图标列为 held → 暂用 generic `note`
- 相关：`notion-local`（`@notionhq/notion-mcp-server@2.5.2`）**保持 held**。厂商 README 已声明不再维护；见 `docs/integrations/notion-package-qualification.md`

#### linear / linear-readonly — Linear ★starter（目录里已有）
- 维护：vendor https://linear.app/docs/mcp · 托管
- 传输：在线 `https://mcp.linear.app/mcp`；只读版 `/mcp/readonly`（两个都 **live** 401 + PRM，doc 也列出了只读版）
- 认证：OAuth + **DCR**（**live**：`https://mcp.linear.app/register`，scopes `read write`），**或者** `Authorization: Bearer <lin_api_…>`（doc：官方 FAQ 明确支持 API key）。获取 key：https://linear.app/settings/account/security
- 指南：
  1. 推荐「使用 Linear 登录」。只需要只读时选只读版。
  2. 或者到 Settings › Account › Security & Access 新建 Personal API key：权限选 Read（只读），可限定团队。粘贴进 TOFI。
  3. 多个工作区要各连一次。
- 测试：`list_teams {}`（**UNVERIFIED**，Linear 文档没有列出工具名；没有这个工具时退回 `first_readonly_no_args`）
- 信任：`list_*`、`get_*` auto；create/update/save/comment ask
- 图标：https://linear.app/brand（不得改动、不得暗示背书）→ brand 或 generic `checklist`

#### todoist — Todoist / Todoist 待办 ★starter
- 描述：Read and manage Todoist tasks, projects, labels and comments. / 读取并管理 Todoist 的任务、项目、标签和评论。
- 维护：vendor（Doist）https://github.com/Doist/todoist-mcp（原名 todoist-ai，约 550★，MIT）
- 传输：在线 `https://ai.todoist.net/mcp`（**live**）。本地 npm `@doist/todoist-mcp@13.4.1` 需要 **Node ≥24**，当前 Runner 装不了；旧包 `@doist/todoist-ai@9.0.0` 只是一个转发 shim（**pkg**），**不要用**。
- 认证：OAuth + **DCR**（**live**：AS 是 `https://todoist.com`，`registration_endpoint https://todoist.com/oauth/register`，资源 scope 是 `data:read_write`，**没有只读 scope**）。远程端点是否接受 API token 作为 Bearer：UNVERIFIED。
- 指南：
  1. 点「使用 Todoist 登录」，授权（会授予全部数据的读写权限）。
  2. 测试连接。
  3. 由于没有只读 scope，写操作只能靠 TOFI 的信任设置拦截。
- 测试：`user-info {}`（**pkg**，`readOnlyHint=true`）
- 信任（**pkg** 工具名）：`user-info`、`get-overview`、`find-*`、`fetch-object`、`get-*-stats`、`list-workspaces`、`view-attachment` auto；`add-*`、`update-*`、`complete-tasks`、`uncomplete-tasks`、`reschedule-tasks`、`project-move`、`manage-assignments`、`import-project-template`、`reorder-objects` ask；`delete-object` blocked
- 图标：https://www.todoist.com/press（条款 UNVERIFIED）→ generic `checklist`

#### atlassian — Atlassian (Jira & Confluence) / Atlassian（Jira 与 Confluence）
- 维护：vendor https://github.com/atlassian/atlassian-mcp-server（只有文档，服务端是托管的）
- 传输：在线 `https://mcp.atlassian.com/v2/mcp`（doc，推荐；加 `?tools=all` 得到平铺的工具列表，便于逐个设置信任）。`/v1/mcp` 仍可用（**live** 401），`/v1/sse` 已于 2026-06-30 停用。
- 认证：OAuth + **DCR**（**live**：`https://mcp.atlassian.com/v1/register`，支持 `none`）。loopback：doc 写明默认放行 `127.0.0.1` 和 `localhost`（来自 subagent 读的文档）。**使用公网 https 回调需要组织管理员在 Rovo MCP server › 域名设置里加白名单。** 备选：API token（需要管理员开启），`Basic base64(email:token)`
- 指南：
  1. 只支持 Atlassian **Cloud**。点「使用 Atlassian 登录」，选择站点后同意授权。
  2. TOFI 用 https 回调时，请管理员到 admin.atlassian.com › Rovo › Rovo MCP server 添加 TOFI 域名。
  3. 测试连接。
- 测试：`atlassianUserInfo {}`
- 信任：get/search/lookup/`executeRead` auto；create/edit/transition/comment/`executeWrite` ask；`delete*`、`executeDestructive` blocked
- 图标：https://atlassian.design/foundations/logos（第三方使用有限制）→ generic `checklist`

#### gitlab — GitLab
- 维护：vendor（GitLab 内置）https://docs.gitlab.com/user/model_context_protocol/mcp_server/
- 传输：在线 `https://gitlab.com/api/v4/mcp` 或 `https://<host>/api/v4/mcp`（**live** 401 + PRM）
- 认证：OAuth + **DCR**（**live**：`https://gitlab.com/oauth/register`，scope 列表里有 `mcp`）。PAT 方式：UNVERIFIED（官方文档只写了 OAuth）
- 版本与层级（subagent 读文档所得）：18.3 实验 → 18.6 beta → 19.2 起 Free 版可用 → 19.5 GA。需要顶级群组 Owner（或自建实例管理员）打开「允许访问 MCP 服务器」。**层级信息是 subagent 结论，标注 UNVERIFIED**
- 指南：
  1. 让群组 Owner 在 设置 › 通用 › 权限 中开启 MCP 访问。
  2. 在 TOFI 填 GitLab 地址，点「使用 GitLab 登录」。
  3. 测试连接。
  4. 如果实例关闭了 DCR，需要管理员预建 OAuth 应用，并把 Client ID 填进「高级」。
- 测试：`get_mcp_server_version {}`
- 信任：get/list/search auto；create/save/add ask；`accept_merge_request` blocked
- 社区备选（适合旧版 CE）：npm `@zereight/mcp-gitlab@2.2.0`（**pkg**，2026-10-08，MIT，环境变量 `GITLAB_PERSONAL_ACCESS_TOKEN`、`GITLAB_API_URL`），工具超过 100 个、包含大量 delete，必须配白名单
- 图标：https://about.gitlab.com/press/press-kit/ → brand（允许指称性使用）或 generic

#### obsidian — Obsidian（advanced，只提供指南）
- 现实情况：笔记库在用户的笔记本上，**VM 看不到**。
- 推荐方案：把笔记库同步进 VM（官方 `obsidian-headless`，需付费 Sync；或者用 Git/Syncthing），再用「电脑上的文件」指向该目录。
- 备选：`uvx mcp-obsidian==0.2.3` + Local REST API 插件。要求 VM 能访问笔记本的 27124 端口，而且客户端硬编码了 `verify_ssl=False`，**不推荐**。
- 图标：https://obsidian.md/brand（集成用途需要联系 Obsidian）→ generic `note`

### 4.3 家庭实验室（homelab）

> 通用提示：
> - 远程类（HA、n8n）由 **TOFI 服务器**发起连接，本地类由 **VM** 发起连接，先确认对应机器能访问局域网 IP 和端口。
> - 自签证书：远程类会报 `tls_untrusted`。可信局域网内优先用 `http://局域网IP:端口`；本地类用各自的 `*_VERIFY_SSL=false` 开关。
> - 一律建**专用低权限账号**，不要用管理员 token。

#### home-assistant — Home Assistant / Home Assistant 智能家居 ★starter
- 描述：Query and control devices you've exposed to Assist. / 查询并控制已公开给 Assist 的设备。
- 维护：**官方，HA core 内置**集成 `mcp_server` · https://www.home-assistant.io/integrations/mcp_server/ · Apache-2.0
- 传输：在线 Streamable HTTP `{base_url}/api/mcp`（doc；也可以用 `/api/mcp/<api_id>`）。HA 2025.2 引入该集成（doc）。改为 Streamable HTTP 的版本：subagent 称是 2025.11，**UNVERIFIED**
- 认证：**长期访问令牌** `Authorization: Bearer <token>`（doc）。HA 的 OAuth 是 IndieAuth，**不是 DCR**（doc：client_id 必须是客户端的 base URL），当前 TOFI 流程不适配 → 目录里只提供 token 方式。
- 字段：`base_url`（非密，例 `http://192.168.1.10:8123`）、`token`（secret）
- 指南：
  1. 设置 › 设备与服务 › 添加集成 › 搜索「Model Context Protocol Server」，添加后 LLM API 至少选 Assist。
  2. 设置 › 语音助手 › **公开（Expose）**：只勾选允许 AI 查看或控制的实体。这是**唯一的权限边界**，未公开的实体对 MCP 不可见（doc）。
  3. 集成选项里「需要管理员账户」**默认开启**（doc）→ 令牌必须属于管理员。如果要用专用的非管理员账户，先关掉这个选项。
  4. 左下角用户名 › 安全 › 长期访问令牌 › 创建令牌，复制（只显示一次）。
  5. 在 TOFI 填地址和令牌，点测试。确认 TOFI 主机能访问 HA。
- 测试：`GetLiveContext {}`。subagent 称 HA 2026.9 起工具名带分组前缀（如 `homeassistant__GetLiveContext`），**UNVERIFIED**（来源是第三方 issue）→ probe 按名称后缀匹配 `GetLiveContext`，找不到时用 `first_readonly_no_args`
- 信任（按 `__` 之后的后缀匹配）：`GetLiveContext`、`GetDateTime`、`HassGetState`、`HassGetWeather` auto；`HassTurnOn/Off`、`HassLightSet`、`HassSetPosition`、`HassClimate*`、`HassMedia*`、`HassVacuum*`、`HassBroadcast`、`HassListAddItem`、脚本类工具 ask；默认不 block，**靠 Expose 控制范围**，门锁、报警器、车库门不建议公开
- 风险：长期令牌在 MCP 之外拥有完整的 REST/WebSocket 权限，必须密封保存；公开的脚本会变成工具
- 图标：HA logo 属 Open Home Foundation，保留所有权利，商用需许可 → **generic `home`**
- 进阶（advanced）：社区的 `ha-mcp`（homeassistant-ai，约 5k★，87 个管理工具）。PyPI 8.6.0 要求 Python ≥3.13，Runner 装不了；可以走它的 HACS 组件远程 URL（URL 本身就是凭证，加 `/readonly` 后缀限制为只读）

#### n8n — n8n / n8n 工作流 ★starter
- 描述：Run your n8n workflows as tools. / 把你的 n8n 工作流当作工具调用。
- 维护：**官方，n8n 内置** "Instance-level MCP" · https://docs.n8n.io/advanced-ai/accessing-n8n-mcp-server/ · Sustainable Use License
- 传输：在线 `{base_url}/mcp-server/http`（doc）
- 认证：`Authorization: Bearer <token>`（doc：API key 标签页会自动生成个人访问令牌）。也支持 OAuth，DCR 情况 UNVERIFIED
- 版本：MCP 访问 beta 约在 1.121 前后；当前文档写的是 2.x（设置页需要 2.33 以上）。准确的最低版本 UNVERIFIED
- 指南：
  1. 管理员到 设置 › Instance-level MCP 开启「Enable MCP access」。
  2. 点 Connect 打开对话框，切到 API key 标签，复制令牌。
  3. 在每个要给 AI 用的工作流里打开「Available in MCP」（默认不会暴露任何工作流）。
  4. 在 TOFI 填 `https://<n8n>/mcp-server/http` 和令牌，测试。
- 测试：`search_workflows {}`
- 信任：`search_workflows`、`get_workflow_details` auto；`execute_workflow` ask；工作流构建、发布类工具 blocked
- 注意：所有连接的客户端都能看到所有开放了 MCP 的工作流，无法按客户端细分（doc）
- 图标：generic `workflow`

#### proxmox-ve — Proxmox VE / Proxmox 虚拟化 ★starter
- 描述：Inspect nodes, VMs, containers, storage and snapshots. / 查看节点、虚拟机、LXC、存储和快照。
- 维护：community https://github.com/RekklesNA/ProxmoxMCP-Plus（约 576★，MIT；原版 canvrno/ProxmoxMCP 已停更）
- 传输：本地 pypi `proxmox-mcp-plus==0.5.27`（**pkg**，2026-10-07，Python ≥3.11 ✓），binary `proxmox-mcp-plus`
- 认证（环境变量）：`PROXMOX_HOST`、`PROXMOX_PORT`（8006）、`PROXMOX_USER`（如 `tofi@pve`）、`PROXMOX_TOKEN_NAME`、`PROXMOX_TOKEN_VALUE`（secret）、`PROXMOX_VERIFY_SSL`、`MCP_TOOL_ALLOWLIST`（逗号分隔；写错名字会导致启动失败）
- 指南：
  1. 在 PVE 界面：数据中心 › 权限 › 用户 › 添加 `tofi@pve`。
  2. 数据中心 › 权限 › API 令牌 › 添加（用户 `tofi@pve`，令牌 ID `mcp`），复制 Secret（只显示一次）。
  3. 数据中心 › 权限 › 添加 › API 令牌权限：路径 `/`，角色 **PVEAuditor**（只读）。需要启停虚拟机时，再只对 `/vms/<id>` 加 `PVEVMUser`。**绝不要**给 Administrator 或 `root@pam`。
  4. PVE 默认是自签证书 → `PROXMOX_VERIFY_SSL=false`（或把 CA 装进 VM）。
  5. TOFI 默认填写 `MCP_TOOL_ALLOWLIST=get_nodes,get_cluster_status,get_vms,get_containers,get_storage`。
  6. 测试（确认 VM 能访问 `https://<pve>:8006`）。
- 测试：`get_nodes {}`
- 信任：`get_*`、`list_*`、日志类 auto；`start_*`、`stop_*`、`create_*`、快照创建 ask；`delete_*`、`rollback_snapshot`、`execute_vm_command`、`execute_container_command`（等于宿主机上的 root shell）blocked
- 资源：Python 约 100–150 MB
- 风险：真正的边界是 Proxmox 的 RBAC；项目发版频繁，固定版本后要主动升级
- 图标：未找到使用条款 → generic `server`

#### calibre-web — Calibre-Web / Calibre-Web 电子书库 ★starter
- 描述：Read-only search of your Calibre-Web library and shelves via OPDS. / 通过 OPDS 只读搜索电子书库与书架。
- 维护：community https://github.com/ni-c/calibreweb-mcp（新项目，星数很少，但已收录于官方 MCP registry）· MIT
- 传输：本地 npm `calibreweb-mcp@0.3.1`（**pkg**，2026-09-07，Node ≥22 ✓，无 install 脚本）
- 认证（环境变量）：`CALIBRE_WEB_URL`（填根地址，程序会自动拼接 `/opds`）、`CALIBRE_WEB_USERNAME`、`CALIBRE_WEB_PASSWORD`（secret）、可选 `CALIBRE_WEB_INSECURE_TLS`
- 指南：
  1. Calibre-Web › 管理 › 用户管理：新建只读用户（只给查看/下载权限）。
  2. 浏览器访问 `http://<ip>:8083/opds`，确认能用该账号登录。
  3. 如果反代或 OAuth 挡住了 Basic 认证，需要让 `/opds` 直通。
  4. 在 TOFI 填三项，测试。
- 测试：`get_stats {}`
- 信任：全部 6 个工具（`search_books`、`list_books`、`list_shelves`、`get_shelf_books`、`get_cover`、`get_stats`）auto（工具列表和"只读"结论来自 subagent 读源码，UNVERIFIED）
- 风险：采用度低，但只读、低风险 · 图标：generic `book`

#### jellyfin — Jellyfin / Jellyfin 影音库
- 维护：community https://github.com/jaredtrent/jellyfin-mcp（Go，约 40★，MIT）
- 传输：本地 npm `@jaredtrent/jellyfin-mcp@2026.924.1`（**pkg**：`os:[linux] cpu:[x64]`，无 install 脚本；TOFI 只跑在 x86_64 上，可用），binary `jellyfin-mcp`。参数 `--read-only --toolsets discovery,media,analytics`
- 认证（环境变量）：`JELLYFIN_URL`、`JELLYFIN_API_KEY`（secret）、`JELLYFIN_USER_ID`
- 指南：
  1. 控制台 › 高级 › API 密钥 › 新建（API key 拥有管理员级全部权限，无法限权）。
  2. 默认用只读参数启动，`JELLYFIN_USER_ID` 填你的用户名。
  3. 测试。
- 测试：`jellyfin_libraries {}` · 信任：查询类 auto；播放控制 ask；`jellyfin_system_control`、用户、插件、库管理 blocked
- 图标：generic `film`

#### grafana — Grafana / Grafana 可观测性
- 维护：vendor https://github.com/grafana/mcp-grafana（约 3.5k★，Apache-2.0）
- 传输：本地 pypi `mcp-grafana==2.0.2`（**pkg**：wheel 内带 manylinux x86_64 Go 二进制，**不需要 Docker**），binary `mcp-grafana`，参数 `--disable-write`。Grafana Cloud 用户另有托管端点 `https://mcp.grafana.com/mcp`（**live** 401 + PRM，OAuth + DCR，只支持 Cloud）
- 认证（环境变量）：`GRAFANA_URL`、`GRAFANA_SERVICE_ACCOUNT_TOKEN`（secret）
- 指南：
  1. Administration › Users and access › Service accounts › Add，角色选 **Viewer**。
  2. Add service account token，复制 `glsa_…`。
  3. 在 TOFI 填 URL 和令牌，固定使用 `--disable-write`。
  4. subagent 指出 `grafana_api_request` 不受 `--disable-write` 约束 → 在条目里设为 blocked。
  5. 测试。
- 测试：`search_dashboards {"query":""}`（doc）
- 信任：search/list/get/query_* auto；update/create ask；`delete_*`、`grafana_api_request` blocked
- 图标：https://grafana.com/trademark-policy/ → generic `chart`

#### nextcloud — Nextcloud / Nextcloud 私有云
- 维护：community https://github.com/cbcoutinho/nextcloud-mcp-server（约 380★，AGPL-3.0，几乎每天发版）
- 传输：本地 pypi `nextcloud-mcp-server==0.199.1`（**pkg**，Python ≥3.11 ✓），binary `nextcloud-mcp-server`，参数 `run --transport stdio`
- 认证（**pkg** 环境变量）：`NEXTCLOUD_HOST`、`NEXTCLOUD_USERNAME`、`NEXTCLOUD_PASSWORD`（**应用密码**，secret）、可选 `NEXTCLOUD_VERIFY_SSL`、`NEXTCLOUD_CA_BUNDLE`
- 指南：
  1. 个人设置 › 安全 › 设备与会话 › 创建新应用密码。
  2. 在 TOFI 填三项。
  3. 不要开启语义搜索（需要向量库，1 GiB 的 VM 撑不住）。
  4. 测试。
- 测试：`nc_calendar_list_calendars {}`（**pkg** 中有此工具名；参数 UNVERIFIED）
- 信任（**pkg** 工具名）：`nc_*_list_*`、`nc_*_get_*`、`nc_*_search_*` auto；`*_create_*`、`*_update_*`、`nc_notes_append_content` ask；`*_delete_*`、`nc_mail_send_message`、`nc_share_create_public_link` blocked
- 说明：这一个插件就覆盖了 Nextcloud 用户的日历（CalDAV）和通讯录（CardDAV）需求。工具超过 100 个，属于大服务器，适合走 `find_tools`
- 图标：generic `cloud`

#### paperless-ngx / gitea / uptime-kuma / karakeep（advanced，受 Runner 约束）
- **Paperless-ngx**：首选 `@baruchiro/paperless-mcp@2.2.1`（约 150★）要求 **Node ≥24** ✗；`tb1337/paperless-mcp`（pypi 0.1.0）要求 Python ≥3.13 ✗，且自称"高度实验"；`@nloui/paperless-mcp` 最后发布于 2024-12，已停更 ✗ → **等 Runner 升级后再上**。届时务必设置 `PAPERLESS_MCP_UPLOAD_PATHS=/nonexistent`，否则它可以上传 VM 里的任意文件。
- **Gitea**：官方 `gitea/gitea-mcp`（gitea.com，Go）只提供二进制或 Docker ✗。可以写成「自行运行 `gitea-mcp -t http`，再用自定义 URL + `Authorization: Bearer <token>` 接入」的指南（来自 subagent 读 README，UNVERIFIED）。
- **Uptime Kuma v2**：`@davidfuchs/mcp-uptime-kuma@0.11.22`（**pkg**，Node ≥22 ✓，MIT）可以装。但它通过 Socket.IO 用**完整的用户密码**或 JWT 登录，没有 API key → 列为 advanced，删除类工具 blocked。
- **Karakeep**：官方 `@karakeep/mcp@0.33.1`（**pkg**，AGPL-3.0，工具名 `search-bookmarks`、`get-bookmark`、`create-bookmark`、`delete-*` 等）。环境变量 `KARAKEEP_API_ADDR`、`KARAKEEP_API_KEY` → 标准条目，`delete-*` blocked。

### 4.4 开发 / 数据（dev）

#### filesystem — Files on computer / 电脑上的文件 ★starter
- 维护：official-reference https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem
- 传输：本地 npm `@modelcontextprotocol/server-filesystem@2026.8.31`（**pkg**），binary `mcp-server-filesystem`，参数是允许访问的目录（绝对路径）
- 认证：无 · 字段：允许的目录（默认填 VM 的工作目录，**不要**填 `/`、`~`、`.ssh` 或存放 .env 的目录）
- 指南：
  1. 选择要开放的目录。
  2. 添加。
  3. 测试（会唤醒电脑）。说明：访问的是 **VM 里的文件**，不是你本机的文件。
- 测试：`list_allowed_directories {}`（**pkg**）
- 信任（**pkg** 工具名）：`read_*`、`list_*`、`directory_tree`、`search_files`、`get_file_info` auto；`write_file`、`edit_file`、`create_directory`、`move_file` ask
- 风险：CVE-2025-53109/53110（符号链接逃逸、前缀绕过）已在 ≥2025.7.1 修复，当前固定版本安全 · 图标：generic `folder`
- 同类 on-computer starter（redesign 已规划）：`@modelcontextprotocol/server-memory@2026.8.31`（**必须**设置 `MEMORY_FILE_PATH` 为持久的绝对路径，否则数据写进 npm 缓存目录，会丢；`delete_*` 设为 ask）、`@modelcontextprotocol/server-sequential-thinking@2026.8.31`（工具名是 `sequentialthinking`，没有下划线）、pypi `mcp-server-time==2026.8.18`（务必传 `--local-timezone`，测试用 `get_current_time {"timezone":"America/Los_Angeles"}`）

#### sentry — Sentry / Sentry 错误监控
- 维护：vendor https://github.com/getsentry/sentry-mcp · npm 许可 FSL-1.1-ALv2
- 传输：在线 `https://mcp.sentry.dev/mcp`，可加路径 `/{org}/{project}` 限定范围（**live** 401 + PRM）。自建 Sentry 用本地 npm `@sentry/mcp-server@0.42.0`（**pkg**，Node ≥22.13）
- 认证：OAuth + **DCR**（**live**：`/oauth/register`；subagent 实测 DCR **接受 loopback 回调**）。也可以用静态令牌，请求头前缀是 **`Sentry-Bearer `**，不是 `Bearer `（subagent 读 README 所得）
- 指南：
  1. 填组织 slug（可选再填项目 slug）。
  2. 点「使用 Sentry 登录」，在同意页**只勾选 Inspect**（需要处理问题时再勾 Triage）。
  3. 测试。
- 测试：`find_organizations {}`
- 信任：`find_*`、`get_*`、`search_*` auto；`update_issue`、`add_issue_note`、`analyze_issue_with_seer`（消耗 Seer 额度）ask；`create_*`、`delete_*` blocked
- 图标：https://sentry.io/branding/

#### cloudflare — Cloudflare API
- 维护：vendor https://github.com/cloudflare/mcp · Apache-2.0
- 传输：在线 `https://mcp.cloudflare.com/mcp`（**live** 401，scope `user:read account:read`）。旧的单产品服务器（`bindings.`、`observability.` 等）已弃用（subagent 读 README 所得）
- 认证：OAuth + **DCR**（**live**：`https://mcp.cloudflare.com/register`，支持 `none`）。客户端可以只申请 `.read` 类 scope。也可以用 `Authorization: Bearer <API token>`；**不支持**带 IP 过滤的令牌
- 指南：
  1. 推荐 OAuth，同意页只勾 Read 类权限。
  2. 或者到 https://dash.cloudflare.com/profile/api-tokens 创建自定义令牌，只加 Read 权限。
  3. 测试。
- 测试：`whoami {}`
- 信任：`whoami`、`search`、`docs` auto；**`execute` 一律 ask**（它是单一工具，靠执行模型写的 JS 调任意 API，读写混在一起，适合配合 reviewer 判断效果）
- 图标：generic `cloud`

#### postgres — PostgreSQL（advanced，暂不能一键安装）
- 首选的 `postgres-mcp`（crystaldba，Postgres MCP Pro）0.3.0 要求 **Python ≥3.12**，Runner 装不了。另外：
  - 0.3.0 **默认是 unrestricted 模式**；
  - subagent 报告存在 CVE-2026-85620（`FROM` 子句里的函数绕过白名单），修复 PR 至今未合并（**UNVERIFIED**，这是 subagent 的结论，CVE 编号本人未核实）；
  - 已 16 个月没有发版。
- 备选 Google MCP Toolbox（npm `@toolbox-sdk/server`，`--prebuilt=postgres`）：x64 二进制约 315 MB，没有内置只读模式。
- **结论**：v0.2 再做。要上线就必须：只读角色（`GRANT SELECT` + `default_transaction_read_only`，**不给** `pg_read_server_files` 或超级用户）、`execute_sql` 设为 ask。指南里的 SQL 模板见 subagent 报告，可直接沿用。
- 参考实现 `@modelcontextprotocol/server-postgres` 已归档，存在只读绕过且没有修复版本 → 拒绝。

#### 其他 dev（advanced）
- **Git**（`mcp-server-git==2026.8.18`，≥2025.12.18 已修复 CVE-2025-68143/68144/68145）：Runner 镜像缺 `git`，**补上 git 之后**即可上线。`git_add`、`git_commit`、`git_checkout` ask，`git_reset` blocked。
- **Supabase**：远程 `https://mcp.supabase.com/mcp?project_ref=…&read_only=true`（**live** 401）。DCR 但是机密客户端（会签发 client secret）；loopback 支持 UNVERIFIED。
- **Kubernetes**：`kubernetes-mcp-server@0.0.67`（npm，**pkg** 版本），用 `--read-only` 加 `view` 角色的 kubeconfig。advanced。
- **SQLite**：可用的只读实现 `mcp-sqlite` 要求 Python ≥3.12 ✗；参考实现有不修复的 SQL 注入 ✗ → 暂缓。

### 4.5 沟通 / 日历 / 邮件（comms）

> 通用规则：收到的消息、邮件、日历邀请都是**不可信文本**（提示注入的主要入口）。读操作 auto；凡是对外发送、转发的操作一律 ask；删除、撤回、改权限的操作 blocked。

#### fastmail — Fastmail（vendor）
- 传输：在线 `https://api.fastmail.com/mcp`（**live** 401，scope `https://www.fastmail.com/dev/mcp`）
- 认证：OAuth + **DCR**（**live**：`https://api.fastmail.com/oauth/register`，公共客户端 `none`）。同意页可以选读取、修改、发送三档。备选：类型为 MCP 的 API token（Bearer，UNVERIFIED）
- 指南：
  1. 点「使用 Fastmail 登录」，同意页先只选「读取」。
  2. 测试。
  3. 撤销授权：设置 › 隐私与安全 › Connected apps。
- 测试：工具名 UNVERIFIED，用 `first_readonly_no_args`
- 信任：readOnlyHint 的工具 auto；整理、草稿类 ask；发送 ask，且默认不申请 Send 档
- 这是本类别里唯一真正"零配置"的官方邮件、日历方案

#### email-imap — Email (IMAP/SMTP) / 邮箱（IMAP/SMTP）
- 维护：community https://github.com/Wh1isper/mcp-email-server（约 350★，从 ai-zerolab 迁来）
- 传输：本地 pypi `mcp-email-server==1.11.1`（**pkg**，Python ≥3.11 ✓），binary `mcp-email-server`，参数 `stdio`（**pkg**）
- 认证（**pkg** 环境变量）：`MCP_EMAIL_SERVER_EMAIL_ADDRESS`、`MCP_EMAIL_SERVER_USER_NAME`、`MCP_EMAIL_SERVER_PASSWORD`（secret）、`MCP_EMAIL_SERVER_IMAP_HOST/PORT/SSL`、`MCP_EMAIL_SERVER_SMTP_HOST/PORT/SSL/START_SSL`、`MCP_EMAIL_SERVER_ACCOUNT_NAME`
- **关键护栏（pkg）**：`MCP_EMAIL_SERVER_ALLOWED_MUTATIONS` 的默认值是 `draft,organize,delete,send,append`，即**全部开放** → TOFI 必须默认写入 `draft,organize`（或留空，即只读），并设置 `MCP_EMAIL_SERVER_ALLOWED_RECIPIENTS` 白名单
- 指南：
  1. 生成应用专用密码：
     - Gmail：需先开两步验证，然后到 https://myaccount.google.com/apppasswords 生成；
     - iCloud：https://account.apple.com › 登录与安全 › App 专用密码；
     - Fastmail：直接用上面的官方插件。
  2. 填写 IMAP（993）和 SMTP（465）地址。
  3. 测试。
  4. 注意：Outlook.com 个人账户已停用应用密码。
- 测试：`list_available_accounts {}`（**pkg**）
- 信任（**pkg** 工具名）：`list_*`、`get_emails_content` auto；`send_email`、`forward_email`、`move_emails`、`archive_emails`、`mark_emails_as_read`、`set_email_*` ask；`delete_emails`（永久删除）blocked
- 与 TOFI 自带的 mail 功能可能重叠，上线前需要先确认

#### dav — CalDAV / CardDAV 日历与通讯录
- 维护：community https://github.com/PhilflowIO/dav-mcp（约 40★，MIT）
- 传输：本地 npm `dav-mcp@4.3.1`（**pkg**，2026-10-08，Node ≥18 ✓）
- 认证（环境变量）：`CALDAV_SERVER_URL`、`CALDAV_USERNAME`、`CALDAV_PASSWORD`（应用密码）
- 指南：
  1. 服务器地址：Nextcloud `https://<host>/remote.php/dav`，Radicale `https://<host>:5232/`，iCloud `https://caldav.icloud.com`（UNVERIFIED）。
  2. 生成应用密码。
  3. 测试。
- 测试：`list_calendars {}`
- 信任：list/query auto；create/update ask；`delete_*`、`*_raw` blocked
- 更轻的备选：`caldav-mcp@0.10.0`（**pkg** 工具名：`list-calendars`、`list-events`、`create-event`、`update-event`、`delete-event`，以及 todo 相关工具；MIT，约 100★）

#### microsoft-365 — Microsoft 365 / Outlook（advanced）
- 维护：community https://github.com/Softeria/ms-365-mcp-server（约 1k★，MIT）
- 传输：本地 npm `@softeria/ms-365-mcp-server@0.160.0`（**pkg**），参数 `--preset mail --read-only`
- 认证：**设备码登录**。不需要回调地址，非常适合 VM：先让 Bot 调用 `login`，用户去 https://microsoft.com/devicelogin 输入代码，再调用 `verify-login`。默认使用 Softeria 的多租户应用（风险：依赖第三方的应用 ID）
- 测试：`get-current-user {}` · 信任：list/get auto；`send-mail` 等写操作 ask；`delete-*` blocked
- 几乎每天发版 → 固定版本，主动升级

#### slack-official / slack-token（advanced）
- **官方** `https://mcp.slack.com/mcp`：**没有 DCR**（**live**：metadata 里没有 `registration_endpoint`）。必须自建 Slack 内部应用，填 client_id/secret，并在应用里开启 MCP。签发的是**用户令牌**（以你本人身份发言）；`127.0.0.1` 回调可能被拒（UNVERIFIED）→ 需要 https origin。
- **社区** `slack-mcp-server@1.3.0`（korotovsky，约 1.9k★，MIT；**pkg**：通过 optionalDependencies 下载各平台的 Go 二进制，没有 install 脚本，应该能装，UNVERIFIED）。认证 `SLACK_MCP_XOXP_TOKEN`；发消息默认关闭，用 `SLACK_MCP_ADD_MESSAGE_TOOL=<频道白名单>` 打开。**禁止**提供 README 里的 xoxc/xoxd「隐身模式」：它绕过工作区管理员，并且很可能违反 Slack 条款。
- 两者都列为 advanced；现有 held 记录继续保留。

#### discord（advanced）
- `mcp-discord`：npm latest 1.3.4 发布于 2025-07-10，已滞后 GitHub v1.5.1；而且默认暴露大量删除、建频道、建 webhook 的工具 → advanced，且以 blocked 为主。必须使用**独立的 Bot 应用**（不要复用 TOFI 聊天前端的 token）。Java 版 SaseQ 拒绝。

#### google（保持现状）
- Gmail 走 gog（现有路径）。官方 Workspace MCP（开发者预览）**没有 DCR**（**live**：accounts.google.com 没有 `registration_endpoint`），按 D5 决策藏在「高级 › 自带 Google OAuth 客户端」后面。
- 指南里要写清楚的负担：
  1. 新建 GCP 项目；
  2. 启用 API **和**对应的 MCP API；
  3. 同意屏幕选 External/Testing，并添加测试用户；
  4. 创建 Web 客户端并登记 TOFI 回调；
  5. **Testing 模式下 refresh token 7 天过期**（Google 官方文档）。可以改为发布成 In production（未验证）来避免每周重新授权，代价是会出现未验证警告。
- 社区的 `@cocal/google-calendar-mcp@2.7.0` 和 `workspace-mcp@2.0.1` 都要在 VM 内本地回调（localhost:3500 / 8000），用户的浏览器访问不到 → 暂不收录。

---

## 5. 图标策略

- **默认用 TOFI 内置 generic glyph**（144 个，24×24，currentColor）。几乎所有厂商 logo 都附带限制性商标条款（HA、微软、Cloudflare、Obsidian、Atlassian、Google 等都明确限制商用或改动）。
- 可以用 brand 的情况：厂商公开了品牌页，且允许"标示集成"（GitHub 已有官方 SVG 候选；Linear、Sentry、GitLab、Discord 有品牌页）。使用时不改动、不加特效、不暗示背书，延续 `docs/integrations/icons/NOTICE.md` 的流程，逐个审。
- 社区图标库：selfh.st/icons（CC BY 4.0，需署名）、homarr-labs/dashboard-icons（Apache-2.0）。这两个库只授权其中的图片文件，**不授予商标权**。如果使用，要附商标声明。本文不下载任何图标。

## 6. 落选 / 暂缓清单

| 候选 | 原因 |
|---|---|
| `@modelcontextprotocol/server-postgres` | 已归档，npm 上已弃用；`COMMIT;…` 可绕过只读，且 npm 上从未发布修复版本 |
| `mcp-server-sqlite`（参考实现） | 已归档；SQL 注入官方不修复 |
| `postgres-mcp`（crystaldba） | 需要 Python ≥3.12；默认 unrestricted；据报有未修复的白名单绕过；暂缓到 v0.2 |
| `@modelcontextprotocol/server-brave-search`、`server-puppeteer` | npm 已弃用；puppeteer 也装不进 1 GiB 的 VM |
| Immich（`immich-mcp` PyPI 0.5.7） | PyPI 元数据指向 `github.com/immich-app/immich-mcp`，但**该页面 404**，来源存疑；最后发布于 2025-09 → 拒绝。社区版 `immich-photo-manager`（约 50★，依赖 PyAV，较重）暂缓 |
| Pi-hole（hexamatic/pihole-mcp） | 质量不错，但只提供 Go 二进制或 .deb，Runner 装不了；npm 上的 `mcp-pihole-server@1.1.0` 最后发布于 2025-11（已超过 10 个月）→ 暂缓，等 Runner 支持"二进制发布包"安装 |
| Gitea 官方 gitea-mcp | 只有 Go 二进制或 Docker → 改为"自行运行 + 自定义 URL"的指南 |
| Paperless-ngx（三个实现） | Node 24 / Python 3.13 / 已停更，三条路都走不通 → 等 Runner 升级 |
| ha-mcp（PyPI） | Python ≥3.13；可以改用它的 HACS 远程 URL（advanced） |
| PyPI `jellyfin-mcp`（Knuckles-Team） | 同一天批量生成的包，可信度低；npm 上的 jaredtrent 版更好 |
| Portainer MCP | 官方自称 "provided as-is"，可以代理原始 Docker API；且需要与 Portainer 版本严格对齐 |
| Docker MCP | VM 里没有 Docker；远程 `DOCKER_HOST` 等于把那台宿主机的 root 权限交出去 |
| TrueNAS 官方 MCP | Go 二进制，research preview |
| Frigate、Syncthing、Mealie、Audiobookshelf | 没有维护良好的 MCP |
| `@doist/todoist-ai` | 已改名，只是 shim；新包要求 Node 24 → 只上远程 |
| Telegram（chigwell，用户会话） | session string 等于账号的完整登录凭证；PyPI 同名包被抢注；只能从 git 安装 |
| Telegram / ntfy / Gotify / Pushover 的 Bot API 类 MCP | 和 TOFI 自己的聊天通道重复（共用 token 还会 409 冲突），或者项目很小；ntfy-mcp-server 要求 Node 24 → 建议做成 TOFI 内置工具 |
| Slack xoxc/xoxd 隐身模式 | 绕过管理员，可能违反 Slack 条款 |
| SaseQ discord-mcp | Java / Docker |
| Matrix（`matrix-mcp`） | Python ≥3.12，只有 1★ |
| Microsoft Work IQ（官方） | 预览阶段，需要 M365 Copilot 许可，不支持个人账户 |
| Google 社区日历和 Workspace 服务器 | OAuth 回调在 VM 的 localhost 上，用户浏览器访问不到 |
| Asana | 新版 `mcp.asana.com/v2/mcp` 据报不支持 DCR（UNVERIFIED，live 只看到 v1 的 `/mcp` 有 `/register`）→ 暂缓 |
| 第三方托管代理（Smithery、Apify 等） | 不是厂商本身，会经手用户的 key |
| 所有"key 放在 URL 里"的写法 | 改为请求头写法 |

## 7. 未能核实 / 上线前必须实测

1. 各 OAuth 提供方是否接受 loopback `http://127.0.0.1:43821/oauth/callback`：已确认接受的只有 Sentry 和 Cloudflare（实测）、Atlassian（文档）；Notion、Linear、Todoist、Tavily、GitLab、Fastmail、Supabase、Grafana Cloud 都**未验证**。
2. 需要鉴权才能列出的工具名（Linear `list_teams`、Atlassian、Fastmail、Tavily 远程版、Notion 当前名称、HA 2026.9 是否加前缀）→ probe 一律带 `fallback: first_readonly_no_args`。
3. HA 改为 Streamable HTTP 的最低版本；n8n MCP 的最低版本；GitLab 各版本对应的层级。
4. 依赖 subagent 报告、我没有复核的项：postgres-mcp 的 CVE-2026-85620；Sentry 的 `Sentry-Bearer` 前缀；Grafana 的 `grafana_api_request` 不受 `--disable-write` 约束；Cloudflare 旧服务器的弃用名单；Brave 免费档在 2026 年取消（brave.com 页面写的是"$5 免费额度/月 + 需要绑卡"，这一点我核实过）。
5. 全部内存估算值。
6. `slack-mcp-server` 在 `--ignore-scripts` 下能否通过 optionalDependencies 拿到二进制。
7. Sentry 仓库的许可证：README 写 MIT，npm 写 FSL。

## 8. 给 owner 的决策点

| # | 决策 | 建议 |
|---|---|---|
| P1 | v0.1 目录是否就用 §3 的 12 个（含 4 个家庭实验室插件） | 是。DeepWiki 和 Cloudflare 文档零成本，可以顺带上 |
| P2 | Runner 镜像加 `git`，并升级到 Node 24 + Python 3.12+（或引入 uv） | 做。可以解锁 Git、Todoist 本地版、Paperless、Postgres、ha-mcp、ntfy 等约 8 个插件 |
| P3 | 是否给 Runner 增加"GitHub 发布二进制 + 校验和"的安装类型 | v0.2 再做；能解锁 Pi-hole、Gitea 官方版 |
| P4 | HA 的 IndieAuth（client_id = TOFI origin，不注册）是否要支持 | 暂不做；长期令牌已经够用 |
| P5 | 品牌图标 | 默认用 generic；brand 图标逐个走 NOTICE 审核 |
