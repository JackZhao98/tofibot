import { i18n } from "./i18n";

export type OwnerSession = { enabled: boolean; setup_required: boolean; authenticated: boolean; password_transport_allowed: boolean; multi_account?: boolean; owner?: { id?: string; must_change_password?: boolean; username: string; email: string; role?: string } };

export type AuthField = "username" | "email" | "password";
const fields: readonly AuthField[] = ["username", "email", "password"];

/** A failed auth call: the stable server code, the field it names (if any) and catalog text. */
export class AuthError extends Error {
  constructor(message: string, readonly code: string, readonly field?: AuthField) { super(message); this.name = "AuthError"; }
}

const known = ["invalid_credentials", "invalid_bootstrap", "setup_unavailable", "rate_limited", "password_transport_required",
  "invalid_username", "invalid_email", "weak_password", "common_password", "password_contains_identity"] as const;

/** Raw JSON call. The server answers failures with `{error:{code,field?}}`; the words live in the catalog. */
export async function authFetch(path: string, body?: unknown, signal?: AbortSignal): Promise<unknown> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 15_000);
  const aborted = () => controller.abort();
  signal?.addEventListener("abort", aborted, { once: true });
  if (signal?.aborted) controller.abort();
  try {
    const response = await fetch(`/api/auth/${path}`, { method: body ? "POST" : "GET", credentials: "same-origin", cache: "no-store", signal: controller.signal,
      headers: body ? { "Content-Type": "application/json" } : undefined, body: body ? JSON.stringify(body) : undefined });
    const value = await response.json().catch(() => null);
    if (!response.ok) {
      const code: unknown = value?.error?.code;
      const field = fields.find(item => item === value?.error?.field);
      const key = code === "invalid_request" ? (path === "setup" ? "error.invalid_setup_request" : "error.invalid_request")
        : known.find(item => item === code) ? `error.${code as typeof known[number]}` as const : "error.unavailable";
      throw new AuthError(i18n.t(key, { ns: "auth" }), typeof code === "string" ? code : "unavailable", field);
    }
    return value;
  } catch (cause) { if (controller.signal.aborted) throw new AuthError(i18n.t("auth:error.timeout"), "timeout"); throw cause; }
  finally { window.clearTimeout(timeout); signal?.removeEventListener("abort", aborted); }
}

export async function authRequest(path: string, body?: unknown, signal?: AbortSignal): Promise<OwnerSession> {
  const value = await authFetch(path, body, signal) as OwnerSession | null;
  if (!value || typeof value.enabled !== "boolean") throw new Error(i18n.t("auth:error.invalid_session"));
  return value;
}

/** Checks the one-time setup key without consuming it. Throws AuthError("invalid_bootstrap") on a wrong key. */
export async function verifySetupKey(secret: string): Promise<void> {
  await authFetch("setup/verify", { bootstrap_secret: secret });
}
