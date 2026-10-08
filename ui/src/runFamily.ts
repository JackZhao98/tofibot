import type { Message, Run } from "./types";
import { i18n } from "./i18n";

export function isTerminalRun(run: Pick<Run, "status">) {
  return ["done", "failed", "cancelled", "interrupted"].includes(run.status);
}

/** Only explicit, matching retry links belong together. Missing legacy links stand alone. */
export function buildRetryFamilies(runs: Run[]) {
  const byID = new Map(runs.map(run => [run.id, run]));
  const parentOf = (run: Run) => {
    const parent = run.parent_run_id ? byID.get(run.parent_run_id) : undefined;
    return parent && parent.id !== run.id && ["failed", "interrupted"].includes(parent.status)
      && Boolean(run.trigger_message_id) && parent.trigger_message_id === run.trigger_message_id
      && parent.conversation_id === run.conversation_id && parent.bot_id === run.bot_id
      && (parent.kind ?? "") === (run.kind ?? "") && run.kind !== "schedule" ? parent : undefined;
  };
  const families = new Map<string, Run[]>();
  for (const run of runs) {
    let root = run;
    const seen = new Set([run.id]);
    let parent = parentOf(root);
    while (parent && !seen.has(parent.id)) {
      seen.add(parent.id); root = parent; parent = parentOf(root);
    }
    // Corrupt cycles must not collapse otherwise independent audit records.
    if (parent) root = run;
    const family = families.get(root.id) ?? [];
    family.push(run); families.set(root.id, family);
  }
  return [...families.entries()].map(([rootId, attempts]) => {
    const depth = (run: Run) => {
      let value = 0; let parent = parentOf(run); const seen = new Set([run.id]);
      while (parent && !seen.has(parent.id)) { value++; seen.add(parent.id); parent = parentOf(parent); }
      return value;
    };
    const nanoseconds = (run: Run) => {
      const match = /^(.*?)(?:\.(\d{1,9}))?Z$/.exec(run.created_at);
      const seconds = Date.parse(`${match?.[1] ?? run.created_at}Z`);
      return Number.isFinite(seconds) ? BigInt(seconds) * 1_000_000n + BigInt(`${match?.[2] ?? ""}000000000`.slice(0, 9)) : 0n;
    };
    attempts.sort((a, b) => {
      const topology = depth(a) - depth(b);
      if (topology) return topology;
      const timeA = nanoseconds(a), timeB = nanoseconds(b);
      return timeA < timeB ? -1 : timeA > timeB ? 1 : a.id.localeCompare(b.id);
    });
    return { rootId, latest: attempts[attempts.length - 1], previous: attempts.slice(0, -1), attempts };
  });
}

/** Collapse history only where the loaded page has a durable, visible audit anchor. */
export function retryFamilyAnchor(family: ReturnType<typeof buildRetryFamilies>[number], messages: Message[]) {
  if (family.latest.kind === "schedule") return undefined;
  const loaded = messages.filter(message => message.conversation_id === family.latest.conversation_id);
  const trigger = loaded.find(message => message.id === family.latest.trigger_message_id);
  if (trigger) return trigger.id;
  const replies = loaded.filter(message => message.role === "assistant" && message.run_id === family.latest.id).sort((a, b) => a.seq - b.seq);
  // Completed progress may fold into the final answer; anchor to that answer first.
  return (replies.find(message => message.kind !== "progress") ?? replies[0])?.id;
}

export function runFailureText(run: Run) {
  const t = i18n.getFixedT(null, "tasks");
  if (run.stop_reason === "approval_expired" || run.failure?.code === "approval_expired") return t("run_failure.approval_expired");
  if (run.status === "cancelled") return t("run_failure.cancelled");
  if (run.status === "interrupted") return t("run_failure.interrupted");
  if (run.status !== "failed") return "";
  if (run.failure?.code === "no_final_answer") return t("run_failure.no_final_answer");
  if (run.failure?.code === "budget_exhausted") return t("run_failure.budget_exhausted");
  const interrupted = run.failure?.source === "runtime" && run.failure.code === "connection_interrupted"
    || (!run.failure && /stream read error|connection reset|unexpected EOF|INTERNAL_ERROR.*received from peer/i.test(run.error ?? ""));
  return interrupted ? t("run_failure.connection_interrupted") : t("run_failure.failed");
}
