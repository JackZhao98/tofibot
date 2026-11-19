import type { Conversation, Message, Run, StreamDraft, ToolActivity } from "./types";

export const BOT_AWAKE_MS = 10 * 60_000;

export function botRecentlyActive(lastActivityAt: number, now: number): boolean {
  return lastActivityAt > 0 && now - lastActivityAt < BOT_AWAKE_MS;
}

export function latestBotWorkTime(botId: string, lastMessage: Conversation["last_message"], runs: Run[]): number {
  const replyAt = lastMessage?.role === "assistant" && lastMessage.sender_bot_id === botId
    ? Date.parse(lastMessage.created_at) || 0 : 0;
  return runs.reduce((latest, run) => run.bot_id === botId &&
    ["done", "failed", "cancelled", "interrupted"].includes(run.status)
    ? Math.max(latest, Date.parse(run.updated_at) || 0) : latest, replyAt);
}

// Human ingress also attaches a run_id. A scheduled trigger has role=user too,
// so inspect the owning run rather than treating run_id as a sender flag.
export function latestHumanMessageTime(messages: Message[], runs: Run[]): number {
  const byId = new Map(runs.map(run => [run.id, run]));
  return messages.reduce((latest, message) => {
    if (message.role !== "user" || message.sender_bot_id || message.kind) return latest;
    if (message.run_id) {
      const run = byId.get(message.run_id);
      if (!run || !["", "group_chat", "triage"].includes(run.kind ?? "") || run.parent_run_id ||
          (run.origin_conversation_id && run.origin_conversation_id !== message.conversation_id)) return latest;
    }
    return Math.max(latest, Date.parse(message.created_at) || 0);
  }, 0);
}

export function activeBotRuns(runs: Run[], drafts: StreamDraft[], activities: ToolActivity[]): Run[] {
  const known = new Map(runs.map(run => [run.id, run]));
  const live = new Map(runs.filter(run => run.status === "queued" || run.status === "running").map(run => [run.id, run]));
  // Stream/tool events can precede the run event. A terminal run, however,
  // wins over stale drafts or tools left behind after cancellation/completion.
  for (const draft of drafts) {
    if (draft.status === "active" && !known.has(draft.run_id)) live.set(draft.run_id, {
      id: draft.run_id, conversation_id: draft.conversation_id, bot_id: draft.bot_id,
      status: "running", created_at: draft.created_at, updated_at: draft.updated_at,
    });
  }
  for (const activity of activities) {
    if ((activity.status === "queued" || activity.status === "running") && !known.has(activity.run_id) && !live.has(activity.run_id)) live.set(activity.run_id, {
      id: activity.run_id, conversation_id: activity.conversation_id, bot_id: activity.bot_id,
      status: "running", created_at: activity.started_at, updated_at: activity.updated_at,
    });
  }
  return [...live.values()];
}
