import type { OwnerPlacement } from "./activityPlacements";
import type { Bot, Message, ToolActivity, ToolActivityRunSummary } from "./types";
import type { Question } from "./questionTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { TaskDetailState } from "./TaskRunBlock";

export interface TaskBlockSigInput {
  tools: readonly ToolActivity[]; questions: readonly Question[]; drafts: readonly MailDraft[]; messages: readonly Message[];
  summaries: readonly ToolActivityRunSummary[]; details: Record<string, TaskDetailState | undefined>; botById: ReadonlyMap<string, Bot>;
  /** Primitives and records the block also reads (connection, Activity setting, live line, retry child, names). */
  extra: readonly unknown[];
}

const indexes = new WeakMap<readonly object[], Map<string, object[]>>();
/** The items of a list by run id. Built once per list identity, so asking for every row's records is not rows times records. */
function ofRuns<T extends { run_id?: string | null }>(list: readonly T[], ids: Iterable<string>): T[] {
  let index = indexes.get(list as readonly object[]);
  if (!index) {
    index = new Map();
    for (const item of list) { const id = item.run_id ?? ""; const bucket = index.get(id); if (bucket) bucket.push(item); else index.set(id, [item]); }
    indexes.set(list as readonly object[], index);
  }
  const out: T[] = [];
  for (const id of ids) { const bucket = index.get(id); if (bucket) out.push(...(bucket as T[])); }
  return out;
}

/**
 * Everything a task block draws, as a flat list of identities: the records of its own run family, plus the settings it reads. The
 * block is a pure function of these, so a row whose list is unchanged would draw the same block and can be skipped. The records are
 * the state's own objects (they keep their identity until they change); the owner and placement objects are rebuilt on every
 * arrival and so are read through, never compared.
 */
export function taskBlockSig(placement: OwnerPlacement, input: TaskBlockSigInput): unknown[] {
  const { owner, segment } = placement;
  const latest = owner.family.latest;
  const ids = owner.family.attempts.map(attempt => attempt.id);
  const matches = (item: { conversation_id: string; bot_id: string }) => item.conversation_id === latest.conversation_id && item.bot_id === latest.bot_id;
  const sig: unknown[] = [placement.key, owner.key, placement.anchor?.kind, placement.anchor?.id, placement.at];
  if (segment) sig.push("seg", segment.index, segment.count, segment.primary, segment.from, segment.until, segment.tools.length, ...segment.tools);
  sig.push("runs", ...owner.family.attempts);
  sig.push("tools", ...ofRuns(input.tools, ids).filter(matches));
  const questions = ofRuns(input.questions, ids).filter(matches);
  sig.push("questions", ...questions, ...questions.map(question => input.botById.get(question.bot_id)));
  const drafts = ofRuns(input.drafts, ids).filter(matches);
  sig.push("drafts", ...drafts, ...drafts.map(draft => input.botById.get(draft.bot_id)));
  sig.push("summaries", ...ofRuns(input.summaries, ids));
  sig.push("details", ...ids.map(id => input.details[id]));
  sig.push("messages", ...ofRuns(input.messages, ids).filter(message => message.conversation_id === latest.conversation_id));
  sig.push("sender", input.botById.get(latest.bot_id), "extra", ...input.extra);
  return sig;
}
