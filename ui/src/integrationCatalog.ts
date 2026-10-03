/**
 * Curated remote MCP presets.
 *
 * A preset only fills the safe, public parts of an MCP connection form. It
 * never contains credentials and it does not imply that Tofi can register an
 * OAuth client on the provider's behalf.
 */
export type IntegrationPreset = {
  id: string;
  name: string;
  description: string;
  category: "google" | "service";
  /** Remote Streamable HTTP MCP endpoint. */
  url: string;
  docsURL: string;
  /** Provider-owned and community-maintained servers are distinct. */
  upstream?: "vendor" | "community";
  /** Provider exposes read tools only; this does not waive Tofi approvals. */
  readOnly?: true;
  /** Public enablement IDs, never inferred from a user's OAuth client ID. */
  googleAPIs?: { api: string; mcp: string };
  /** Human-readable prerequisites shown before saving the form. */
  setup: string[];
  auth: "oauth" | "token" | "none";
  /** Suggested API scopes; the provider may grant a narrower set. */
  scopes?: string[];
  /** Optional OAuth authorization-server metadata endpoint. */
  metadataURL?: string;
  /** Header used when auth is token based, e.g. Context7-API-Key. */
  tokenHeader?: string;
  /** Prefix used before a token value, e.g. `Bearer `. */
  tokenPrefix?: string;
  note?: string;
  status?: "active" | "developer-preview" | "public-preview";
};

export const googleAudienceURL = "https://console.cloud.google.com/auth/audience";
export const googleAPIEnableURL = (api: string) =>
  `https://console.cloud.google.com/apis/enableflow;apiid=${api}`;
const googleSetup = [
  "确认已获得 Google Workspace Developer Preview 访问资格。",
  "在 Google Cloud 项目中启用对应产品 API 和 MCP 服务。",
  "设置 OAuth 同意屏幕与测试用户，创建 Web 应用客户端并登记回调地址。",
  "填写下方 Client ID 和 Secret，添加后完成 Google 授权。",
];

/**
 * Google Workspace's first-party MCP servers are separate endpoints. They
 * are currently in Google's public Developer Preview, so they are intentionally
 * represented as six presets rather than a fictional all-in-one endpoint.
 */
export const integrationCatalog: IntegrationPreset[] = [
  {id:"microsoft-learn", name:"Microsoft Learn", description:"搜索官方技术文档与代码示例。", category:"service", url:"https://learn.microsoft.com/api/mcp", docsURL:"https://learn.microsoft.com/en-us/training/support/mcp", upstream:"vendor", setup:["无需账号或密钥，添加后即可使用官方公开文档。"], auth:"none", status:"active"},
  {
    id: "google-gmail",
    googleAPIs: { api: "gmail.googleapis.com", mcp: "gmailmcp.googleapis.com" },
    name: "Google Gmail",
    description: "查找邮件、阅读内容、撰写草稿。",
    category: "google",
    url: "https://gmailmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/gmail/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/gmail.readonly", "https://www.googleapis.com/auth/gmail.compose"],
    note: "Google Workspace Developer Preview；启用 gmail.googleapis.com 和 gmailmcp.googleapis.com。具体工具可能要求 gmail.modify 或 mail.google.com。",
    status: "developer-preview",
  },
  {
    id: "google-drive",
    googleAPIs: { api: "drive.googleapis.com", mcp: "drivemcp.googleapis.com" },
    name: "Google Drive",
    description: "查找和整理云端文件。",
    category: "google",
    url: "https://drivemcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/drive/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file"],
    note: "Google Drive MCP 属于 Developer Preview；启用 drive.googleapis.com 和 drivemcp.googleapis.com。",
    status: "developer-preview",
  },
  {
    id: "google-docs",
    googleAPIs: { api: "docs.googleapis.com", mcp: "docsmcp.googleapis.com" },
    name: "Google Docs",
    description: "阅读和编辑文档。",
    category: "google",
    url: "https://docsmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/docs/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/documents.readonly", "https://www.googleapis.com/auth/documents"],
    note: "Google Docs MCP 属于 Developer Preview；启用 docs.googleapis.com 和 docsmcp.googleapis.com。",
    status: "developer-preview",
  },
  {
    id: "google-sheets",
    googleAPIs: { api: "sheets.googleapis.com", mcp: "sheetsmcp.googleapis.com" },
    name: "Google Sheets",
    description: "读取数据、编辑表格与公式。",
    category: "google",
    url: "https://sheetsmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/sheets/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/spreadsheets.readonly", "https://www.googleapis.com/auth/spreadsheets"],
    note: "Google Sheets MCP 属于 Developer Preview；启用 sheets.googleapis.com 和 sheetsmcp.googleapis.com。",
    status: "developer-preview",
  },
  {
    id: "google-slides",
    googleAPIs: { api: "slides.googleapis.com", mcp: "slidesmcp.googleapis.com" },
    name: "Google Slides",
    description: "阅读和制作演示文稿。",
    category: "google",
    url: "https://slidesmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/slides/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/presentations.readonly", "https://www.googleapis.com/auth/presentations"],
    note: "Google Slides MCP 属于 Developer Preview；启用 slides.googleapis.com 和 slidesmcp.googleapis.com。",
    status: "developer-preview",
  },
  {
    id: "google-calendar",
    googleAPIs: { api: "calendar-json.googleapis.com", mcp: "calendarmcp.googleapis.com" },
    name: "Google Calendar",
    description: "查看日程与空闲时间。",
    category: "google",
    url: "https://calendarmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/calendar/api/guides/configure-mcp-server",
    setup: googleSetup,
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/calendar.calendarlist.readonly", "https://www.googleapis.com/auth/calendar.events.freebusy", "https://www.googleapis.com/auth/calendar.events.readonly"],
    note: "当前预设仅提供日程读取与空闲查询。需启用 calendar-json.googleapis.com、calendarmcp.googleapis.com。",
    status: "developer-preview",
  },
  {
    id: "github-readonly",
    name: "GitHub 只读",
    description: "读取仓库、Issue 与 Pull Request。",
    category: "service",
    url: "https://api.githubcopilot.com/mcp/readonly",
    docsURL: "https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md",
    upstream: "vendor",
    readOnly: true,
    setup: [
      "创建仅覆盖所需仓库和读取权限的 fine-grained PAT。",
      "在下方私密令牌输入框填写 PAT；无需粘贴到聊天或请求头 JSON。",
      "添加后检查服务连接和可用工具；账号及组织策略仍适用。",
    ],
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    note: "GitHub 托管的只读端点；Tofi 的工具确认与允许/禁用策略仍适用。来源与配置已核对，尚未用真实账号验证。",
    status: "public-preview",
  },
  {
    id: "github",
    name: "GitHub",
    description: "仓库、Issue 与 Pull Request。",
    category: "service",
    url: "https://api.githubcopilot.com/mcp/",
    docsURL: "https://github.com/github/github-mcp-server",
    upstream: "vendor",
    setup: [
      "准备权限尽量收窄的 fine-grained PAT，并在 Tofi 作为 Bearer token 填入。",
      "连接前检查 GitHub MCP 的 toolset 和仓库权限。",
    ],
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    note: "官方远程服务器由 GitHub 托管；可用性和 toolset 可能受账号或 Copilot 资格影响。",
    status: "public-preview",
  },
  {
    id: "notion",
    name: "Notion",
    description: "查找和整理知识库。",
    category: "service",
    url: "https://mcp.notion.com/mcp",
    docsURL: "https://www.notion.com/help/notion-mcp",
    upstream: "vendor",
    setup: [
      "Tofi 会向服务申请 OAuth 客户端，无需手动填写 Client ID。",
      "添加后点击授权，选择要连接的 Notion 工作区。",
      "确认连接身份的页面权限符合预期。",
    ],
    auth: "oauth",
    note: "Notion 当前优先维护托管远程 MCP；Enterprise 管理员可以限制可连接的 AI 应用。",
    status: "active",
  },
  {
    id: "linear-readonly",
    name: "Linear 只读",
    description: "查找项目、任务与评论。",
    category: "service",
    url: "https://mcp.linear.app/mcp/readonly",
    docsURL: "https://linear.app/docs/mcp",
    upstream: "vendor",
    readOnly: true,
    setup: [
      "创建只包含 Read 权限、仅覆盖所需团队的 Linear API key。",
      "在下方私密令牌输入框填写 key。",
      "添加后确认授权范围和工具连接。",
    ],
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    note: "Linear 文档说明此端点仅提供读取工具；Tofi 的工具确认与允许/禁用策略仍适用。连接配置已核对，尚未用真实账号验证。",
    status: "active",
  },
  {
    id: "linear",
    name: "Linear",
    description: "管理项目、任务与评论。",
    category: "service",
    url: "https://mcp.linear.app/mcp",
    docsURL: "https://linear.app/docs/mcp",
    upstream: "vendor",
    setup: [
      "准备 Linear API key 或 Bearer token；写入权限不需要时请选择只读端点。",
      "连接前检查团队和工作区权限。",
    ],
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    note: "Linear 主传输是 Streamable HTTP；/mcp/readonly 只提供读取工具，/sse 是已弃用的兼容路径。",
    status: "active",
  },
  {
    id: "context7",
    name: "Context7",
    description: "获取当前版本的库文档和示例。",
    category: "service",
    url: "https://mcp.context7.com/mcp",
    docsURL: "https://context7.com/docs/resources/all-clients",
    upstream: "vendor",
    setup: [
      "在 Context7 控制台创建 API key。",
      "在 Tofi 使用 Context7-API-Key 请求头填入 key。",
    ],
    auth: "token",
    tokenHeader: "Context7-API-Key",
    note: "Context7 文档也列出 Authorization: Bearer 形式；本预设使用已验证的 Context7-API-Key 请求头。",
    status: "active",
  },
];

export const getIntegrationPreset = (id: string): IntegrationPreset | undefined =>
  integrationCatalog.find((preset) => preset.id === id);

/** Research status only. No endpoint or installation arguments can be saved. */
export type HeldIntegration = {
  id: string;
  name: string;
  description: string;
  upstream: "vendor" | "community";
  auth: "oauth" | "integration-token" | "bot-token" | "none";
  docsURL: string;
  reason: string;
};

export const heldIntegrations: HeldIntegration[] = [
  {
    id: "notion-token", name: "Notion 令牌接入", description: "通过 Notion integration token 连接。",
    upstream: "vendor", auth: "integration-token",
    docsURL: "https://github.com/makenotion/notion-mcp-server/blob/730ae781ba28beeaf0865025a3f2ed4c25ea2387/README.md",
    reason: "令牌接入仍需核对安装包和实际运行；目前可选择上方的 Notion OAuth 连接。",
  },
  {
    id: "discord", name: "Discord", description: "社区维护的 Discord Bot 连接。",
    upstream: "community", auth: "bot-token", docsURL: "https://github.com/SaseQ/discord-mcp",
    reason: "尚未核实兼容的安装方式与连接权限，暂未开放添加。",
  },
  {
    id: "slack", name: "Slack", description: "Slack 提供的远程 MCP 服务。",
    upstream: "vendor", auth: "oauth", docsURL: "https://docs.slack.dev/ai/slack-mcp-server/",
    reason: "需要注册 Slack 应用并完成用户授权；Bot token 不适用于此接入。",
  },
  {
    id: "yahoo-finance", name: "Yahoo Finance", description: "社区维护的行情查询方案。",
    upstream: "community", auth: "none", docsURL: "https://github.com/Alex2Yang97/yahoo-finance-mcp",
    reason: "尚未核实可安装的兼容版本；社区方案无需 Token。",
  },
];

export const integrationAuthLabel = (auth: IntegrationPreset["auth"] | HeldIntegration["auth"]): string =>
  ({ oauth: "OAuth 授权", token: "访问令牌", none: "无需密钥", "integration-token": "Integration token", "bot-token": "Bot 令牌" })[auth];

export const integrationOriginLabel = (upstream: IntegrationPreset["upstream"]): string =>
  upstream === "vendor" ? "提供方维护" : upstream === "community" ? "社区维护" : "连接预设";

export const matchesIntegration = (item: Pick<IntegrationPreset, "name" | "description">, query: string): boolean =>
  `${item.name} ${item.description}`.toLowerCase().includes(query.trim().toLowerCase());
