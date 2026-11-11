import {ApiError, request} from "./api";
import {i18n} from "./i18n";
import type {CapabilityId, WebSourceEntry, WebToolsView} from "./types";

/**
 * The Administration API (user groups, design 6): groups, per-account policy and organization resources. Organization keys are
 * write-only: no response carries one, only `key_prefix` (32+ characters) and `key_hint` (16+). Everything here is served by the
 * gateway and answers 403 to anyone who is not an administrator with an updated password.
 */
export const CAPABILITIES: readonly CapabilityId[] = ["ai_models", "computer", "web_search", "web_fetch"];

/** One stored capability row: a group's policy or an account override. `source` is the computer source (`vm` or `e2b`) when provided. */
export type PolicyValue = {mode: string; source?: string; params?: unknown};
export type PolicyMap = Record<CapabilityId, PolicyValue>;

export type AdminGroup = {id: string; name: string; builtin: boolean; position: number; member_count: number; policies: Partial<PolicyMap>};
export type AccountPolicy = {group: {id: string; name: string}; overrides: Partial<PolicyMap>; effective: Partial<PolicyMap>};

/** The value a capability takes in `self_serve`, the group an account is in until an administrator moves it: today's behavior. */
export const SELF_SERVE: PolicyMap = {ai_models: {mode: "own"}, computer: {mode: "host"}, web_search: {mode: "own"}, web_fetch: {mode: "own"}};

export type OrgResourceId = "provider:anthropic" | "provider:openai" | "provider:compatible" | "computer:e2b" | "webtool:tavily" | "webtool:brave" | "webtool:firecrawl" | "webtool:brightdata" | "file_store" | "backup_store";
export type OrgResourceStatus = "stored" | "verified" | "rejected";
/** The organization's E2B computer image (`computer:e2b` only): custom (the admin's own template, never rebuilt) or the build for this TOFI version. */
export type OrgBuild = {
  phase: "none" | "preparing" | "ready" | "failed";
  custom: boolean;
  version: string;
  /** `template_build_failed`, `image_missing`, `invalid_key`, `e2b_unreachable`, `e2b_limit` or `template_stale_in_team`. */
  error_code?: string;
  updated_at?: string;
  /** The oldest computer image the server accepts; empty or absent when none is enforced. */
  min_guest_version?: string;
};
export type OrgResource = {
  id: OrgResourceId;
  kind: string;
  status: OrgResourceStatus;
  key_prefix?: string;
  key_hint?: string;
  verified_at?: string;
  /** A stable code: `invalid_key`, `no_quota`. The provider's own text is never returned. */
  error_code?: string;
  updated_at: string;
  build?: OrgBuild;
  config: {base_url?: string; models?: string[]; zone?: string; template_id?: string; endpoint?: string; region?: string; bucket?: string; prefix?: string; path_style?: boolean; kind?: "s3" | "disk"; delete_grace_days?: number; [key: string]: unknown};
};
/** A capability some group (or account override) provides while the organization holds nothing to serve it. */
export type OrgMissing = {capability: CapabilityId; group_ids: string[]; account_ids: string[]};
export type OrgResources = {resources: OrgResource[]; missing: OrgMissing[]};
export type OrgResourceInput = {key?: string; base_url?: string; models?: string[]; zone?: string; template_id?: string; workspace_id?: string};
/** `PUT file_store`: the bucket, its access key pair (the secret is `key`, write-only), or just `kind` to choose where new files go. */
export type FileStoreInput = {kind?: "s3" | "disk"; endpoint?: string; region?: string; bucket?: string; prefix?: string; path_style?: boolean; access_key_id?: string; key?: string; delete_grace_days?: number; confirm_public?: boolean};

/** `GET /api/admin/backups`: the Backups card's whole state. Keys are never in it. */
export type BackupSettings = {remote: "on" | "off"; schedule: "daily" | "6h" | "hourly" | "manual"; keep_daily: number; keep_weekly: number; keep_monthly: number; verify: "weekly" | "monthly" | "manual"; stale_after_hours: number};
export type BackupStatus = {running: boolean; last_attempt_at?: string; last_status?: "ok" | "failed" | "blocked"; last_code?: string; last_account?: string; last_success_at?: string; last_snapshot?: string; last_duration_ms?: number; prune_code?: string; verified_at?: string; verified_snapshot?: string; verify_status?: "ok" | "failed"; verify_code?: string; verify_duration_ms?: number};
export type BackupView = {configured: boolean; destination?: {endpoint: string; bucket: string; prefix: string}; settings: BackupSettings; key: {present: boolean; fingerprint: string; confirmed_at: string}; status: BackupStatus; stale: boolean; resource?: OrgResource};
export type BackupSnapshot = {name: string; at: string; reason: string; rows?: Record<string, number>; files?: number};

/** `GET /api/admin/org/web-tools`: the organization's chains. Keys go through the resources endpoints; this holds order, switches, address and caps. */
export type OrgWebProvider = {id: string; chain: "search" | "fetch"; label: string; kind: "api" | "self_hosted"; paid: boolean; enabled: boolean; status: "ready" | "needs_key" | "needs_address" | "error"; key_prefix?: string; key_hint?: string; zone?: string; base_url?: string; error_code?: string; tested_at?: string};
export type OrgWebTools = {search_order: string[]; fetch_order: string[]; disabled: string[]; searxng_url: string; daily_search?: number | null; daily_fetch?: number | null; providers: OrgWebProvider[]};

const enc = encodeURIComponent;
const send = (method: string, body?: unknown): RequestInit => ({method, ...(body === undefined ? {} : {body: JSON.stringify(body)})});

export const adminOrgApi = {
  groups: () => request<AdminGroup[]>("/api/admin/groups"),
  createGroup: (body: {name: string; policies?: Partial<PolicyMap>}) => request<AdminGroup>("/api/admin/groups", send("POST", body)),
  patchGroup: (id: string, body: {name?: string; policies?: Partial<PolicyMap>}) => request<AdminGroup>(`/api/admin/groups/${enc(id)}`, send("PATCH", body)),
  deleteGroup: (id: string) => request<{deleted: boolean; id: string}>(`/api/admin/groups/${enc(id)}`, send("DELETE")),
  moveMembers: (id: string, accountIds: string[]) => request<{group_id: string; moved: number}>(`/api/admin/groups/${enc(id)}/members`, send("PUT", {account_ids: accountIds})),
  accountPolicy: (id: string) => request<AccountPolicy>(`/api/admin/accounts/${enc(id)}/policy`),
  setAccountPolicy: (id: string, body: {group_id?: string; overrides?: Partial<PolicyMap>}) => request<AccountPolicy>(`/api/admin/accounts/${enc(id)}/policy`, send("PUT", body)),
  resources: () => request<OrgResources>("/api/admin/org/resources"),
  putResource: (id: OrgResourceId, body: OrgResourceInput | FileStoreInput) => request<OrgResource>(`/api/admin/org/resources/${enc(id)}`, send("PUT", body)),
  deleteResource: (id: OrgResourceId) => request<{deleted: boolean; id: string; missing: OrgMissing[]}>(`/api/admin/org/resources/${enc(id)}`, send("DELETE")),
  retryBuild: () => request<OrgResource>("/api/admin/org/resources/computer:e2b/build", send("POST")),
  testResource: (id: OrgResourceId) => request<OrgResource>(`/api/admin/org/resources/${enc(id)}/test`, send("POST")),
  webTools: (signal?: AbortSignal) => request<OrgWebTools>("/api/admin/org/web-tools", {signal}),
  putWebTools: (body: {search_order?: string[]; fetch_order?: string[]; disabled?: string[]; searxng_url?: string; daily_search?: number; daily_fetch?: number}) => request<OrgWebTools>("/api/admin/org/web-tools", send("PUT", body)),
};

const backupApi = {
  state: () => request<BackupView>("/api/admin/backups"),
  settings: (body: Partial<BackupSettings>) => request<BackupView>("/api/admin/backups/settings", send("PUT", body)),
  run: () => request<{started: boolean}>("/api/admin/backups/run", send("POST")),
  verify: () => request<{started: boolean}>("/api/admin/backups/verify", send("POST")),
  recoveryKey: (body: {import?: string} = {}) => request<{recovery_key?: string; fingerprint: string; confirmed_at: string}>("/api/admin/backups/recovery-key", send("POST", body)),
  confirmKey: () => request<BackupView>("/api/admin/backups/recovery-key/confirm", send("POST", {})),
  snapshots: () => request<{snapshots: BackupSnapshot[]}>("/api/admin/backups/snapshots"),
  accountSnapshots: (id: string) => request<{snapshots: BackupSnapshot[]; undo_available: boolean}>(`/api/admin/accounts/${enc(id)}/backups`),
  restoreAccount: (id: string, body: {snapshot: string; confirm_username: string}) => request<{restored: boolean}>(`/api/admin/accounts/${enc(id)}/restore`, send("POST", body)),
  undoRestore: (id: string) => request<{undone: boolean}>(`/api/admin/accounts/${enc(id)}/restore/undo`, send("POST", {})),
};
export {backupApi};

/** Words for a failed backup call, from the server's stable code; the account is named when the server names one. */
export function backupError(cause: unknown): string {
  if (cause instanceof ApiError && cause.code) return i18n.t(`settings:admin.org.backup.error.${cause.code}` as "settings:admin.org.backup.error.generic", {defaultValue: orgError(cause)});
  return i18n.t("settings:admin.org.backup.error.generic");
}

const errorCodes = ["invalid_policy", "group_exists", "group_limit", "group_builtin_readonly", "group_in_use", "not_found", "invalid_key", "invalid_workspace", "invalid_template", "invalid_zone", "invalid_base_url", "invalid_models", "invalid_address", "no_models", "no_quota", "provider_unreachable", "endpoint_not_allowed", "invalid_request", "custom_template", "forbidden", "storage", "endpoint_unreachable", "credentials_rejected", "bucket_not_found", "write_denied", "conditional_put_unsupported", "range_unsupported", "delete_denied", "bucket_is_public", "invalid_endpoint", "invalid_bucket", "invalid_prefix", "invalid_grace", "file_store_in_use"] as const;

/** Words for a failed Administration call, from the server's stable code. The provider's own text is never shown. Resolved at call time. */
export function orgError(cause: unknown): string {
  if (cause instanceof ApiError && cause.code && (errorCodes as readonly string[]).includes(cause.code)) return i18n.t(`settings:admin.org.error.${cause.code}` as "settings:admin.org.error.generic");
  return i18n.t("settings:admin.org.error.generic");
}

/** The modes a capability offers, in the order the editor lists them. `provided` on the computer also takes a source (VM or E2B). */
export function modesFor(capability: CapabilityId): string[] {
  switch (capability) {
    case "ai_models": return ["own", "provided"];
    case "web_search": return ["own", "provided", "off"];
    case "web_fetch": return ["own", "provided", "builtin"];
    case "computer": return ["host", "own", "provided", "none"];
  }
}

/**
 * The AI models row offers three choices, stored as a mode plus one parameter: `own` (mode own), `provided` (provided, allow_own
 * true: members may also use their own sources) and `provided_only` (provided, allow_own false). The server reads a missing
 * `allow_own` as true.
 */
export type AiChoice = "own" | "provided" | "provided_only";
export const AI_CHOICES: readonly AiChoice[] = ["own", "provided", "provided_only"];
export function aiChoiceOf(value: PolicyValue | undefined): AiChoice {
  if (!value || value.mode !== "provided") return "own";
  const allow = (value.params as {allow_own?: unknown} | undefined)?.allow_own;
  return allow === false ? "provided_only" : "provided";
}
export function aiValueFor(choice: AiChoice): PolicyValue {
  if (choice === "own") return {mode: "own", params: {}};
  return {mode: "provided", params: {allow_own: choice === "provided"}};
}

/** A value for `mode`; the computer's provided mode takes the VM where the host runs one, else E2B. */
export function valueFor(capability: CapabilityId, mode: string, hostHasVm: boolean): PolicyValue {
  if (capability === "computer" && mode === "provided") return {mode, source: hostHasVm ? "vm" : "e2b", params: {}};
  return {mode, params: {}};
}

/** The resource ids any one of which satisfies a provided capability (mirrors the server's `orgNeeds`). */
export function needsOf(capability: CapabilityId, value: PolicyValue): OrgResourceId[] {
  if (value.mode !== "provided") return [];
  if (capability === "ai_models") return ["provider:anthropic", "provider:openai", "provider:compatible"];
  if (capability === "web_search") return ["webtool:tavily", "webtool:brave"];
  if (capability === "web_fetch") return ["webtool:firecrawl", "webtool:brightdata"];
  if (capability === "computer" && value.source === "e2b") return ["computer:e2b"];
  return [];
}

// ---- The organization's search and fetch chains, in the shape the member's chain editor draws ----

const WEB_CODE: Record<string, string> = {invalid_key: "auth_failed", no_quota: "quota_exceeded", provider_unreachable: "network"};
const DEFAULT_DAILY = 300;

function webView(org: OrgWebTools, test?: WebToolsView["test"]): WebToolsView {
  const entries = (chain: "search" | "fetch", order: string[]): WebSourceEntry[] => order.flatMap(id => {
    const p = org.providers.find(item => item.id === id && item.chain === chain);
    if (!p) return []; // the order also names the Bot's own computer, which an organization does not provide
    const status: WebSourceEntry["status"] = !p.enabled && p.status === "ready" ? "off" : p.status;
    return [{
      id: p.id, label: p.label, kind: p.kind, paid: p.paid, fixed: false, enabled: p.enabled, status, error_code: p.error_code ? WEB_CODE[p.error_code] ?? p.error_code : undefined, key_hint: p.key_hint,
      zone: p.zone, base_url: p.base_url, fields: p.kind === "self_hosted" ? ["base_url"] : p.id === "brightdata" ? ["key", "zone"] : ["key"],
      testable: p.kind === "api", tested_at: p.tested_at,
    }];
  });
  return {
    enabled: true, computer: false, search: entries("search", org.search_order), fetch: entries("fetch", org.fetch_order), search_offered: true,
    limits: {search: org.daily_search ?? DEFAULT_DAILY, fetch: org.daily_fetch ?? DEFAULT_DAILY}, test,
  };
}

/** Failures are worded from the stable code, in the UI's language; the server's sentence is not shown. */
async function words<T>(work: () => Promise<T>): Promise<T> {
  try { return await work(); }
  catch (cause) { throw cause instanceof ApiError ? new ApiError(cause.status, orgError(cause), cause.code, cause.data) : cause; }
}

/** The chain editor's adapter for the organization (`/api/admin/org/web-tools` and the resource endpoints that hold the keys). */
export function createOrgWebToolsAdapter() {
  let last: OrgWebTools | null = null;
  const take = (org: OrgWebTools, test?: WebToolsView["test"]) => { last = org; return webView(org, test); };
  const reload = async (test?: WebToolsView["test"]) => take(await adminOrgApi.webTools(), test);
  return {
    load: (signal?: AbortSignal) => words(async () => take(await adminOrgApi.webTools(signal))),
    setOrder: (chain: "search" | "fetch", order: string[]) => words(async () => take(await adminOrgApi.putWebTools(chain === "search" ? {search_order: order} : {fetch_order: order}))),
    setEnabled: (chain: "search" | "fetch", id: string, enabled: boolean) => words(async () => {
      const entry = `${chain}/${id}`;
      const disabled = (last?.disabled ?? []).filter(item => item !== entry);
      return take(await adminOrgApi.putWebTools({disabled: enabled ? disabled : [...disabled, entry]}));
    }),
    setLimits: (limits: {search?: number; fetch?: number}) => words(async () => take(await adminOrgApi.putWebTools({...(limits.search !== undefined ? {daily_search: limits.search} : {}), ...(limits.fetch !== undefined ? {daily_fetch: limits.fetch} : {})}))),
    save: (id: string, body: {key?: string; zone?: string; base_url?: string}) => words(async () => {
      if (id === "searxng") return take(await adminOrgApi.putWebTools({searxng_url: body.base_url ?? ""}));
      await adminOrgApi.putResource(`webtool:${id}` as OrgResourceId, {key: body.key, ...(body.zone ? {zone: body.zone} : {})});
      return reload();
    }),
    remove: (id: string) => words(async () => {
      if (id === "searxng") return take(await adminOrgApi.putWebTools({searxng_url: ""}));
      await adminOrgApi.deleteResource(`webtool:${id}` as OrgResourceId);
      return reload();
    }),
    test: async (id: string) => {
      try {
        await adminOrgApi.testResource(`webtool:${id}` as OrgResourceId);
        return await reload({id, ok: true, code: ""});
      } catch (cause) {
        const code = cause instanceof ApiError && cause.code ? WEB_CODE[cause.code] ?? "network" : "network";
        return reload({id, ok: false, code});
      }
    },
  };
}
