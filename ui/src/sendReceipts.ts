import type { Message, Run } from "./types";

export type ReceiptState = "sending" | "retrying" | "failed" | "delivered" | "read";
export type Receipt = { state: ReceiptState; readBy: string[]; error?: string; reason?: "offline" | "network" | "server" | "rejected"; auto?: boolean };

/** Failure codes that only a run which actually began executing can report. */
const STARTED_FAILURES = new Set(["execution_failed", "no_final_answer", "budget_exhausted", "tool_stuck", "resumed_repeatedly", "connection_interrupted"]);

/** A Bot has picked the message up: its run left the queue. A run that failed before it began does not count. */
export function runStarted(run: Run): boolean {
  if (run.status === "running" || run.status === "waiting" || run.status === "done") return true;
  if (run.status === "failed" || run.status === "interrupted") return Boolean(run.failure && STARTED_FAILURES.has(run.failure.code));
  return false;
}

/** Messages a person typed themselves. Notices, scheduled prompts and cards carry no receipt. */
export function carriesReceipt(message: Message): boolean {
  return message.role === "user" && !message.kind;
}

export function runsByTrigger(runs: readonly Run[]): Map<string, Run[]> {
  const out = new Map<string, Run[]>();
  for (const run of runs) {
    if (!run.trigger_message_id) continue;
    const list = out.get(run.trigger_message_id);
    if (list) list.push(run); else out.set(run.trigger_message_id, [run]);
  }
  return out;
}

/**
 * The one place a sent message's status is decided. Read means a Bot's run for this message reached
 * running; in a group any addressed Bot is enough. `laterReply` covers history whose runs were paged
 * out: a Bot answered after this message, so something read it (who is unknown).
 */
export function receiptFor(message: Message, runsForMessage: readonly Run[] | undefined, laterReply: boolean): Receipt | null {
  if (!carriesReceipt(message)) return null;
  if (message.local) return { state: message.local.state, readBy: [], error: message.local.error, reason: message.local.reason, auto: message.local.auto };
  const readBy = [...new Set((runsForMessage ?? []).filter(runStarted).map(run => run.bot_id))];
  if (readBy.length || laterReply) return { state: "read", readBy };
  return { state: "delivered", readBy: [] };
}
