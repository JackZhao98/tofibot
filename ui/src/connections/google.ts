import { useCallback, useEffect, useState } from "react";
import { request } from "../api";

/**
 * Google, as the Connections page knows it: which API each service needs, the project an uploaded OAuth client
 * belongs to, and the one console page that enables an API in that project. The same rules live in
 * internal/googleapi (Go); this copy only serves the person's own file before it is uploaded.
 */
export const GOOGLE_SERVICE_API = {
  gmail: "gmail.googleapis.com",
  calendar: "calendar-json.googleapis.com",
  drive: "drive.googleapis.com",
  docs: "docs.googleapis.com",
  sheets: "sheets.googleapis.com",
  slides: "slides.googleapis.com",
} as const;
export type GoogleService = keyof typeof GOOGLE_SERVICE_API;
/** Product names, not translated. */
export const GOOGLE_SERVICE_NAME: Record<GoogleService, string> = { gmail: "Gmail", calendar: "Google Calendar", drive: "Google Drive", docs: "Google Docs", sheets: "Google Sheets", slides: "Google Slides" };

/** The services the personal Google connection (gogcli) can authorise, in the order they are offered. */
export const GOG_SERVICES = ["gmail", "calendar", "drive"] as const satisfies readonly GoogleService[];
export type GogService = typeof GOG_SERVICES[number];
/** "Gmail, Google Calendar": the product names of the services the account authorised ("" when the server did not say). */
export function googleServicesLabel(services: readonly string[] | undefined): string {
  return GOG_SERVICES.filter(service => services?.includes(service)).map(service => GOOGLE_SERVICE_NAME[service]).join(", ");
}

const MCP_API = (service: GoogleService) => `${service}mcp.googleapis.com`;
const KNOWN = new Set<string>([...Object.values(GOOGLE_SERVICE_API), ...(Object.keys(GOOGLE_SERVICE_API) as GoogleService[]).map(MCP_API)]);
const PROJECT_ID = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/;
const PROJECT_NUMBER = /^[0-9]{6,20}$/;
const CLIENT_ID = /^([0-9]{6,20})-[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$/;

export type GoogleClientInfo = { clientId: string; projectId: string; projectNumber: string; project: string };

/** Reads the project out of a downloaded Web OAuth client JSON; null when it is not one. */
export function parseGoogleClient(text: string): GoogleClientInfo | null {
  let web: { client_id?: unknown; client_secret?: unknown; project_id?: unknown } | undefined;
  try { web = (JSON.parse(text) as { web?: typeof web }).web; } catch { return null; }
  if (!web || typeof web.client_id !== "string" || typeof web.client_secret !== "string" || !web.client_id || !web.client_secret) return null;
  const projectNumber = CLIENT_ID.exec(web.client_id)?.[1] ?? "";
  const projectId = typeof web.project_id === "string" && PROJECT_ID.test(web.project_id) ? web.project_id : "";
  return { clientId: web.client_id, projectId, projectNumber, project: projectId || projectNumber };
}

/** The page that enables `api` in `project`; the enable flow (Google asks which project) without a valid one; "" for an API TOFI does not know. */
export function googleEnableURL(api: string, project?: string): string {
  if (!KNOWN.has(api)) return "";
  if (project && (PROJECT_ID.test(project) || PROJECT_NUMBER.test(project))) return `https://console.cloud.google.com/apis/library/${api}?project=${encodeURIComponent(project)}`;
  return `https://console.cloud.google.com/apis/enableflow;apiid=${api}`;
}

/** A link from the server is only used when it is the console's own page. */
export function isConsoleURL(value: string | undefined): value is string {
  try { const u = new URL(value ?? ""); return u.protocol === "https:" && u.hostname === "console.cloud.google.com"; } catch { return false; }
}

/** The service a Google API belongs to, for "Enable Gmail API". */
export function googleServiceForAPI(api: string): GoogleService | "" {
  for (const service of Object.keys(GOOGLE_SERVICE_API) as GoogleService[]) if (GOOGLE_SERVICE_API[service] === api || MCP_API(service) === api) return service;
  return "";
}

export type GoogleOverview = {
  client: { configured: boolean; project_id?: string; project_number?: string };
  apis: { service: GoogleService; api: string; mcp_api: string; enable_url: string; mcp_enable_url: string }[];
};

/** The workspace's one Google OAuth client (project only, never the secret). */
export function useGoogleOverview(refreshToken: number) {
  const [overview, setOverview] = useState<GoogleOverview | null>(null);
  const refresh = useCallback(async () => {
    try { const value = await request<Partial<GoogleOverview>>("/api/extensions/google"); setOverview({ client: value.client ?? { configured: false }, apis: value.apis ?? [] }); } catch { setOverview(null); }
  }, []);
  useEffect(() => { void refresh(); }, [refresh, refreshToken]);
  return { google: overview, refreshGoogle: refresh };
}
