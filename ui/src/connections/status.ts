/**
 * What a connection row tells the person. One honest status per row, derived from facts the row already has, so the
 * list reads the same for a remote MCP, a local plugin and an adapter. Six states (docs/design/tools-and-connections.md
 * section 3.2): ready, needs_auth, api_disabled, error, paused, working. A connection nobody has probed yet is
 * "checking" (a quiet probe runs when the list first opens); there is no "not verified" state to show.
 */
export type Phase = "idle" | "checking" | "ready" | "auth" | "authorizing" | "waiting" | "error" | "removing";
export type ConnectionKind = "ready" | "needs_auth" | "api_disabled" | "error" | "paused" | "checking" | "waiting" | "working";

export function connectionKind(input: { phase: Phase; needsAuth: boolean; error: string; accountMissing?: boolean; accountExpired?: boolean; apiDisabled?: boolean; paused?: boolean }): ConnectionKind {
  const { phase, needsAuth, error, accountMissing, accountExpired, apiDisabled, paused } = input;
  // A paused connection keeps its configuration and is not probed or used; nothing else about it is news.
  if (paused) return "paused";
  // A Google account that is gone or whose grant Google rejected is not news the probe can improve: sign in again.
  if ((accountMissing || accountExpired) && phase !== "authorizing" && phase !== "removing" && phase !== "waiting") return "needs_auth";
  if (phase === "checking" || phase === "idle") return "checking";
  // Tools answered, but the person's account was never authorised: that is not "connected".
  if (phase === "ready") return accountMissing ? "needs_auth" : apiDisabled ? "api_disabled" : "ready";
  if (phase === "authorizing" || phase === "removing") return "working";
  if (phase === "waiting") return "waiting";
  // A named, fixable problem: Google says the API is not enabled in the project (a structured error, never wording).
  if (apiDisabled) return "api_disabled";
  if (error) return "error";
  if (needsAuth || accountMissing) return "needs_auth";
  return "checking";
}

/** A connection that needs the person to act: the settings rail counts these. */
export const needsAttention = (kind: ConnectionKind) => kind === "needs_auth" || kind === "api_disabled" || kind === "error";

/** The one action a row leads with; everything else lives in the connection's detail page. */
export type PrimaryAction = "authorize" | "resume" | "enable_api" | "retry" | "continue" | "cancel" | "none";
export function primaryAction(kind: ConnectionKind, hasContinueLink: boolean, cancellable: boolean): PrimaryAction {
  if (kind === "paused") return "resume";
  if (kind === "needs_auth") return "authorize";
  if (kind === "api_disabled") return "enable_api";
  if (kind === "error") return "retry";
  if (kind === "waiting") return cancellable ? "cancel" : hasContinueLink ? "continue" : "none";
  return "none";
}

/**
 * A connection's status from what the server stored, without probing: for pages that list connections but do not own their
 * checks (a Bot's Tools page). `null` when nothing is known yet, which such a page shows as no status at all.
 */
export function storedKind(server: { enabled?: boolean; oauth?: { connected?: boolean }; status?: { ok: boolean; error_class?: "auth" | "failed"; error_code?: string } }): ConnectionKind | null {
  if (server.enabled === false) return "paused";
  if (server.oauth && !server.oauth.connected) return "needs_auth";
  const status = server.status;
  if (!status) return null;
  if (status.ok) return "ready";
  if (status.error_code === "api_disabled") return "api_disabled";
  return status.error_class === "auth" ? "needs_auth" : "error";
}
