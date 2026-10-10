/**
 * Curated remote MCP presets.
 *
 * A preset only fills the safe, public parts of an MCP connection form. It
 * never contains credentials and it does not imply that Tofi can register an
 * OAuth client on the provider's behalf.
 */
import { i18n } from "./i18n";

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
  /** OAuth client is registered on the fly (dynamic client registration), so no Client ID is asked for. */
  dcr?: true;
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
// Catalog text is read at access time, so it follows the current UI language.
const googleSetup = () => [
  i18n.t("extensions:catalog.google_setup.preview_access"),
  i18n.t("extensions:catalog.google_setup.enable_apis"),
  i18n.t("extensions:catalog.google_setup.consent_screen"),
  i18n.t("extensions:catalog.google_setup.client_credentials"),
];

/**
 * Google Workspace's first-party MCP servers are separate endpoints. They
 * are currently in Google's public Developer Preview, so they are intentionally
 * represented as six presets rather than a fictional all-in-one endpoint.
 */
export const integrationCatalog: IntegrationPreset[] = [
  {id:"microsoft-learn", name:"Microsoft Learn", get description() { return i18n.t("extensions:catalog.microsoft_learn.description"); }, category:"service", url:"https://learn.microsoft.com/api/mcp", docsURL:"https://learn.microsoft.com/en-us/training/support/mcp", upstream:"vendor", get setup() { return [i18n.t("extensions:catalog.microsoft_learn.setup.no_account")]; }, auth:"none", status:"active"},
  {
    id: "google-gmail",
    googleAPIs: { api: "gmail.googleapis.com", mcp: "gmailmcp.googleapis.com" },
    name: "Google Gmail",
    get description() { return i18n.t("extensions:catalog.google_gmail.description"); },
    category: "google",
    url: "https://gmailmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/gmail/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/gmail.readonly", "https://www.googleapis.com/auth/gmail.compose"],
    get note() { return i18n.t("extensions:catalog.google_gmail.note"); },
    status: "developer-preview",
  },
  {
    id: "google-drive",
    googleAPIs: { api: "drive.googleapis.com", mcp: "drivemcp.googleapis.com" },
    name: "Google Drive",
    get description() { return i18n.t("extensions:catalog.google_drive.description"); },
    category: "google",
    url: "https://drivemcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/drive/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file"],
    get note() { return i18n.t("extensions:catalog.google_drive.note"); },
    status: "developer-preview",
  },
  {
    id: "google-docs",
    googleAPIs: { api: "docs.googleapis.com", mcp: "docsmcp.googleapis.com" },
    name: "Google Docs",
    get description() { return i18n.t("extensions:catalog.google_docs.description"); },
    category: "google",
    url: "https://docsmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/docs/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/documents.readonly", "https://www.googleapis.com/auth/documents"],
    get note() { return i18n.t("extensions:catalog.google_docs.note"); },
    status: "developer-preview",
  },
  {
    id: "google-sheets",
    googleAPIs: { api: "sheets.googleapis.com", mcp: "sheetsmcp.googleapis.com" },
    name: "Google Sheets",
    get description() { return i18n.t("extensions:catalog.google_sheets.description"); },
    category: "google",
    url: "https://sheetsmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/sheets/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/spreadsheets.readonly", "https://www.googleapis.com/auth/spreadsheets"],
    get note() { return i18n.t("extensions:catalog.google_sheets.note"); },
    status: "developer-preview",
  },
  {
    id: "google-slides",
    googleAPIs: { api: "slides.googleapis.com", mcp: "slidesmcp.googleapis.com" },
    name: "Google Slides",
    get description() { return i18n.t("extensions:catalog.google_slides.description"); },
    category: "google",
    url: "https://slidesmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/slides/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/drive.readonly", "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/presentations.readonly", "https://www.googleapis.com/auth/presentations"],
    get note() { return i18n.t("extensions:catalog.google_slides.note"); },
    status: "developer-preview",
  },
  {
    id: "google-calendar",
    googleAPIs: { api: "calendar-json.googleapis.com", mcp: "calendarmcp.googleapis.com" },
    name: "Google Calendar",
    get description() { return i18n.t("extensions:catalog.google_calendar.description"); },
    category: "google",
    url: "https://calendarmcp.googleapis.com/mcp/v1",
    docsURL: "https://developers.google.com/workspace/calendar/api/guides/configure-mcp-server",
    get setup() { return googleSetup(); },
    auth: "oauth",
    scopes: ["https://www.googleapis.com/auth/calendar.calendarlist.readonly", "https://www.googleapis.com/auth/calendar.events.freebusy", "https://www.googleapis.com/auth/calendar.events.readonly"],
    get note() { return i18n.t("extensions:catalog.google_calendar.note"); },
    status: "developer-preview",
  },
  {
    id: "github-readonly",
    get name() { return i18n.t("extensions:catalog.github_readonly.name"); },
    get description() { return i18n.t("extensions:catalog.github_readonly.description"); },
    category: "service",
    url: "https://api.githubcopilot.com/mcp/readonly",
    docsURL: "https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md",
    upstream: "vendor",
    readOnly: true,
    get setup() {
      return [
        i18n.t("extensions:catalog.github_readonly.setup.create_pat"),
        i18n.t("extensions:catalog.github_readonly.setup.enter_token"),
        i18n.t("extensions:catalog.github_readonly.setup.verify"),
      ];
    },
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    get note() { return i18n.t("extensions:catalog.github_readonly.note"); },
    status: "public-preview",
  },
  {
    id: "github",
    name: "GitHub",
    get description() { return i18n.t("extensions:catalog.github.description"); },
    category: "service",
    url: "https://api.githubcopilot.com/mcp/",
    docsURL: "https://github.com/github/github-mcp-server",
    upstream: "vendor",
    get setup() {
      return [
        i18n.t("extensions:catalog.github.setup.prepare_pat"),
        i18n.t("extensions:catalog.github.setup.check_scope"),
      ];
    },
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    get note() { return i18n.t("extensions:catalog.github.note"); },
    status: "public-preview",
  },
  {
    id: "notion",
    name: "Notion",
    get description() { return i18n.t("extensions:catalog.notion.description"); },
    category: "service",
    url: "https://mcp.notion.com/mcp",
    docsURL: "https://www.notion.com/help/notion-mcp",
    upstream: "vendor",
    get setup() {
      return [
        i18n.t("extensions:catalog.notion.setup.auto_client"),
        i18n.t("extensions:catalog.notion.setup.choose_workspace"),
        i18n.t("extensions:catalog.notion.setup.check_access"),
      ];
    },
    auth: "oauth",
    dcr: true,
    get note() { return i18n.t("extensions:catalog.notion.note"); },
    status: "active",
  },
  {
    id: "robinhood",
    name: "Robinhood",
    get description() { return i18n.t("extensions:catalog.robinhood.description"); },
    category: "service",
    url: "https://agent.robinhood.com/mcp/trading",
    docsURL: "https://robinhood.com/",
    upstream: "vendor",
    get setup() {
      return [
        i18n.t("extensions:catalog.robinhood.setup.auto_client"),
        i18n.t("extensions:catalog.robinhood.setup.sign_in"),
        i18n.t("extensions:catalog.robinhood.setup.agentic_account"),
      ];
    },
    auth: "oauth",
    dcr: true,
    get note() { return i18n.t("extensions:catalog.robinhood.note"); },
    status: "active",
  },
  {
    id: "linear-readonly",
    get name() { return i18n.t("extensions:catalog.linear_readonly.name"); },
    get description() { return i18n.t("extensions:catalog.linear_readonly.description"); },
    category: "service",
    url: "https://mcp.linear.app/mcp/readonly",
    docsURL: "https://linear.app/docs/mcp",
    upstream: "vendor",
    readOnly: true,
    get setup() {
      return [
        i18n.t("extensions:catalog.linear_readonly.setup.create_key"),
        i18n.t("extensions:catalog.linear_readonly.setup.enter_token"),
        i18n.t("extensions:catalog.linear_readonly.setup.verify"),
      ];
    },
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    get note() { return i18n.t("extensions:catalog.linear_readonly.note"); },
    status: "active",
  },
  {
    id: "linear",
    name: "Linear",
    get description() { return i18n.t("extensions:catalog.linear.description"); },
    category: "service",
    url: "https://mcp.linear.app/mcp",
    docsURL: "https://linear.app/docs/mcp",
    upstream: "vendor",
    get setup() {
      return [
        i18n.t("extensions:catalog.linear.setup.prepare_key"),
        i18n.t("extensions:catalog.linear.setup.check_scope"),
      ];
    },
    auth: "token",
    tokenHeader: "Authorization",
    tokenPrefix: "Bearer ",
    get note() { return i18n.t("extensions:catalog.linear.note"); },
    status: "active",
  },
  {
    id: "context7",
    name: "Context7",
    get description() { return i18n.t("extensions:catalog.context7.description"); },
    category: "service",
    url: "https://mcp.context7.com/mcp",
    docsURL: "https://context7.com/docs/resources/all-clients",
    upstream: "vendor",
    get setup() {
      return [
        i18n.t("extensions:catalog.context7.setup.create_key"),
        i18n.t("extensions:catalog.context7.setup.enter_key"),
      ];
    },
    auth: "token",
    tokenHeader: "Context7-API-Key",
    get note() { return i18n.t("extensions:catalog.context7.note"); },
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
  /** Historical vendor origin does not imply ongoing provider maintenance. */
  maintenance?: "retired";
  auth: "oauth" | "integration-token" | "bot-token" | "none";
  docsURL: string;
  reason: string;
};

export const heldIntegrations: HeldIntegration[] = [
  {
    id: "notion-token", get name() { return i18n.t("extensions:held.notion_token.name"); }, get description() { return i18n.t("extensions:held.notion_token.description"); },
    upstream: "vendor", maintenance: "retired", auth: "integration-token",
    docsURL: "https://github.com/makenotion/notion-mcp-server/blob/730ae781ba28beeaf0865025a3f2ed4c25ea2387/README.md",
    get reason() { return i18n.t("extensions:held.notion_token.reason"); },
  },
  {
    id: "discord", name: "Discord", get description() { return i18n.t("extensions:held.discord.description"); },
    upstream: "community", auth: "bot-token", docsURL: "https://github.com/SaseQ/discord-mcp",
    get reason() { return i18n.t("extensions:held.discord.reason"); },
  },
  {
    id: "slack", name: "Slack", get description() { return i18n.t("extensions:held.slack.description"); },
    upstream: "vendor", auth: "oauth", docsURL: "https://docs.slack.dev/ai/slack-mcp-server/",
    get reason() { return i18n.t("extensions:held.slack.reason"); },
  },
  {
    id: "yahoo-finance", name: "Yahoo Finance", get description() { return i18n.t("extensions:held.yahoo_finance.description"); },
    upstream: "community", auth: "none", docsURL: "https://github.com/Alex2Yang97/yahoo-finance-mcp",
    get reason() { return i18n.t("extensions:held.yahoo_finance.reason"); },
  },
];

const authLabelKeys = {
  oauth: "extensions:auth.oauth",
  token: "extensions:auth.token",
  none: "extensions:auth.none",
  "integration-token": "extensions:auth.integration_token",
  "bot-token": "extensions:auth.bot_token",
} as const;

export const integrationAuthLabel = (auth: IntegrationPreset["auth"] | HeldIntegration["auth"]): string =>
  i18n.t(authLabelKeys[auth]);

export const integrationOriginLabel = (upstream: IntegrationPreset["upstream"]): string =>
  upstream === "vendor" ? i18n.t("extensions:origin.vendor") : upstream === "community" ? i18n.t("extensions:origin.community") : i18n.t("extensions:origin.preset");

export const matchesIntegration = (item: Pick<IntegrationPreset, "name" | "description">, query: string): boolean =>
  `${item.name} ${item.description}`.toLowerCase().includes(query.trim().toLowerCase());
