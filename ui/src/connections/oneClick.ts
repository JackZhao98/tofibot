import type { IntegrationPreset } from "../integrationCatalog";
import type { OAuthRoute } from "../mcpOAuthRoute";
import { endpoint, type MCP } from "./shared";

/**
 * One click for a service that registers its own OAuth client (dynamic client registration: Notion, Robinhood): the tile saves the
 * preset's defaults and goes straight to sign-in, with no form between. A service already added, or a route the page cannot take
 * (`blocked`: the form explains why), keeps the form. Token services keep their one-field form.
 */
export const isOneClick = (preset: IntegrationPreset, servers: { name: string; url: string }[], route: OAuthRoute) =>
  preset.auth === "oauth" && Boolean(preset.dcr) && route.mode !== "blocked" && !servers.some(server => server.name === preset.id || server.url === preset.url);

/** What the form would save for this preset with nothing changed. */
export const presetDefaults = (preset: IntegrationPreset): MCP => ({
  name: preset.id, url: preset.url, transport: "streamable_http", headers: {}, tool_allowlist: [], tool_denylist: [],
  oauth: { client_id: "", client_secret: "", scopes: preset.scopes ?? [], auth_server_metadata_url: preset.metadataURL ?? "", ...(preset.oauthRedirect ? { redirect_policy: preset.oauthRedirect } : {}) },
});

/**
 * The sign-in tab must open inside the click that started the flow, or browsers block it; the server's pending page holds it until the
 * provider's address is known. Only the routes that use a tab get one (a native or computer route has none).
 */
export function openSignInTab(name: string, route: OAuthRoute): Window | null {
  if (route.mode !== "web" && route.mode !== "paste") return null;
  const tab = window.open(`${endpoint(name)}/oauth/pending`, "_blank");
  if (tab) tab.opener = null;
  return tab;
}
