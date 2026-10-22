import { i18n } from "../i18n";

export type OAuth = { client_id: string; client_secret?: string; scopes?: string[]; auth_server_metadata_url?: string; connected?: boolean };
export type TestResult = { ok: boolean; tool_count?: number; auth_required?: boolean; diagnostics?: { message: string; code?: string; api?: string; enable_url?: string }[] };
export type MCPTransport = "streamable_http" | "sse";
export type MCPStatus = { checked_at: string; ok: boolean; tool_count: number; error_class?: "auth" | "failed"; error_code?: string; api?: string; enable_url?: string };
export type MCP = { name: string; url: string; transport?: MCPTransport; status?: MCPStatus; headers?: Record<string, string>; tool_allowlist?: string[]; tool_denylist?: string[]; oauth?: OAuth; google_client?: boolean; provenance?: { origin: string; kind?: string; package?: string; version?: string } };

/** Tool tiers (docs/agent-plan/mcp-tool-tiers.md): read_only (marked by the provider, or by you) runs without review, write is reviewed each time, high_risk ("Always ask me") always asks a person. */
export type ToolTier = "read_only" | "write" | "high_risk";
export type ToolTierView = { name: string; description?: string; tier: ToolTier; source: "owner" | "provider" | "default"; override?: ToolTier; changed?: boolean; duplicated?: boolean; read_only_hint?: boolean; destructive_hint?: boolean };
export type ToolTierList = { tools: ToolTierView[]; read_only: number; write: number; high_risk: number; checked?: boolean; error_class?: "auth" | "failed"; diagnostics?: { message: string }[] };
export const TIERS: readonly ToolTier[] = ["read_only", "write", "high_risk"];

export const toolsEndpoint = (name: string) => `/api/extensions/mcp/${encodeURIComponent(name)}/tools`;
export const endpoint = (name: string) => `/api/extensions/mcp/${encodeURIComponent(name)}`;
export const normalizeTiers = (value: Partial<ToolTierList> | null | undefined): ToolTierList => ({ tools: Array.isArray(value?.tools) ? value!.tools : [], read_only: value?.read_only ?? 0, write: value?.write ?? 0, high_risk: value?.high_risk ?? 0, checked: value?.checked, error_class: value?.error_class, diagnostics: value?.diagnostics });

/** Turns a raw failure into the sentence a person can act on; unknown text passes through. */
export const message = (e: unknown) => {
  const text = e instanceof Error ? e.message : String(e);
  if (text === "Failed to fetch") return i18n.t("extensions:error.offline");
  if (text === "MCP server connection or discovery failed") return i18n.t("extensions:error.discovery_failed");
  if (text.includes("HTTP redirects must use localhost")) return i18n.t("extensions:error.redirect_needs_https");
  if (text.includes("OAuth discovery or client registration failed")) return i18n.t("extensions:error.oauth_discovery_failed");
  if (text.startsWith("OAuth connection could not be started")) return i18n.t("extensions:error.oauth_start_failed");
  if (text === "MCP endpoint changed; re-enter or remove saved headers before saving") return i18n.t("extensions:error.endpoint_changed_headers");
  if (text === "MCP endpoint changed; explicitly configure OAuth for the new endpoint before saving") return i18n.t("extensions:error.endpoint_changed_oauth");
  if (text === "OAuth metadata destination changed; re-enter or remove the saved client secret before saving") return i18n.t("extensions:error.metadata_changed_secret");
  return text;
};
/** Protocol names, not translated. */
export const transportLabels: Record<MCPTransport, string> = { streamable_http: "Streamable HTTP", sse: "SSE" };
export const post = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });
export const words = (value: string) => value.split(/[,\n]/).map(x => x.trim()).filter(Boolean);

/** A server added from "Local plugins" is saved as `local_<plugin id>`; that name is plumbing, never shown. */
export const localPluginId = (serverName: string) => serverName.startsWith("local_") ? serverName.slice(6) : "";
