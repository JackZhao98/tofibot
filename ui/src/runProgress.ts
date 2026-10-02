import type { Message, Run } from "./types";

/** Fold only a successfully finished run with a visible final answer. */
export function foldCompletedProgress(messages: Message[], runs: Run[]) {
  const doneRuns = new Set(runs.filter(run => run.status === "done").map(run => run.id));
  const finalByRun = new Map<string, Message>();
  for (const message of messages) {
    if (message.role === "assistant" && message.run_id && doneRuns.has(message.run_id) && !message.kind) {
      finalByRun.set(message.run_id, message);
    }
  }
  const progressByFinalId = new Map<string, Message[]>();
  const visible = messages.filter(message => {
    if (message.kind !== "progress" || !message.run_id) return true;
    const final = finalByRun.get(message.run_id);
    if (!final || final.seq <= message.seq) return true;
    const progress = progressByFinalId.get(final.id) ?? [];
    progress.push(message);
    progressByFinalId.set(final.id, progress);
    return false;
  });
  return { visible, progressByFinalId };
}
