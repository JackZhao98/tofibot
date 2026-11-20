import type { Run, ToolActivity } from "../types";
import type { BotAvatarState } from "../ds/BotAvatar/BotAvatar";

/** What the header cat shows for one Bot in one conversation. A pending question for the person beats work, work beats a failed last run,
 *  and a Bot that has been quiet for a while is asleep. Nothing: awake and idle. */
export function botCatState(botId: string, conversationId: string, runs: Run[], activities: ToolActivity[], asleep: boolean, asked: ReadonlySet<string>): BotAvatarState | undefined {
  const mine = (run: Run) => run.bot_id === botId && run.conversation_id === conversationId;
  // `waiting` is also a run that parks on a delegated child: only a question pending for the person (`asked`: run ids) is 待确认.
  const parked = runs.filter(run => mine(run) && run.status === "waiting");
  if (parked.some(run => asked.has(run.id))) return "waiting";
  if (parked.length || runs.some(run => mine(run) && (run.status === "running" || run.status === "queued"))) return activities.some(activity => activity.bot_id === botId && activity.conversation_id === conversationId && (activity.status === "running" || activity.status === "queued")) ? "working" : "thinking";
  const last = runs.filter(run => mine(run) && ["done", "failed", "cancelled", "interrupted"].includes(run.status))
    .reduce<Run | undefined>((latest, run) => !latest || run.updated_at >= latest.updated_at ? run : latest, undefined);
  if (last?.status === "failed") return "error";
  return asleep ? "asleep" : undefined;
}
