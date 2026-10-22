/**
 * What a connection row tells the person. One honest status per row, derived from facts the row already has, so the
 * list reads the same for a remote MCP, a local plugin and an adapter.
 */
export type Phase = "idle" | "checking" | "ready" | "auth" | "authorizing" | "waiting" | "error" | "removing";
export type ConnectionKind = "ready" | "needs_auth" | "api_disabled" | "error" | "checking" | "waiting" | "unverified" | "working";

export function connectionKind(input: { phase: Phase; needsAuth: boolean; error: string; accountMissing?: boolean; apiDisabled?: boolean }): ConnectionKind {
  const { phase, needsAuth, error, accountMissing, apiDisabled } = input;
  if (phase === "checking") return "checking";
  // Tools answered, but the person's account was never authorised: that is not "connected".
  if (phase === "ready") return accountMissing ? "needs_auth" : apiDisabled ? "api_disabled" : "ready";
  if (phase === "authorizing" || phase === "removing") return "working";
  if (phase === "waiting") return "waiting";
  // A named, fixable problem: Google says the API is not enabled in the project (a structured error, never wording).
  if (apiDisabled) return "api_disabled";
  if (error) return "error";
  if (needsAuth || accountMissing) return "needs_auth";
  return "unverified";
}

/** The one action a row leads with; everything else lives in the "more actions" menu. */
export type PrimaryAction = "authorize" | "enable_api" | "retry" | "connect" | "continue" | "cancel" | "none";
export function primaryAction(kind: ConnectionKind, hasContinueLink: boolean, cancellable: boolean): PrimaryAction {
  if (kind === "needs_auth") return "authorize";
  if (kind === "api_disabled") return "enable_api";
  if (kind === "error") return "retry";
  if (kind === "unverified") return "connect";
  if (kind === "waiting") return cancellable ? "cancel" : hasContinueLink ? "continue" : "none";
  return "none";
}
