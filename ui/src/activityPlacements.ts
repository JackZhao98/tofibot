import { orderToolActivities, splitActivitySegments, compareActivityTime, type ActivityBoundary } from "./toolTimeline";
import type { TaskOwner } from "./taskIssuePresentation";
import type { TaskSegment } from "./TaskRunBlock";
import type { Message, ToolActivity } from "./types";

/** One block of a task owner in the conversation: the whole run, or one stretch of it. */
export type OwnerPlacement = { key: string; owner: TaskOwner; anchor?: TaskOwner["anchor"]; /** Orders blocks that share an anchor. */ at: string; segment?: TaskSegment };

/**
 * Where each task owner's blocks sit. The run's calls are cut wherever something else was drawn between them (a person's message,
 * another run's ticket or notice, the Bot's own say or progress message), so Activity reads as one linear timeline with the rest of
 * the conversation: the first stretch stays at the owner's anchor, each later one sits under the last drawn message before its first
 * call. A run nothing interrupts keeps one block, without a segment. `messages` are the rendered conversation messages, `notes` the
 * progress notes kept inside Activity (they close a stretch but draw nothing of their own).
 * One bucketing pass over the calls and one over the boundaries; each owner then costs O(its calls + boundaries).
 */
export function placeOwners({ owners, tools, messages, notes, anchorTime }: { owners: TaskOwner[]; tools: ToolActivity[]; messages: Message[]; notes: Message[]; anchorTime: (anchor: NonNullable<TaskOwner["anchor"]>) => string | undefined }): OwnerPlacement[] {
  const byRun = new Map<string, ToolActivity[]>();
  for (const tool of tools) { const list = byRun.get(tool.run_id); if (list) list.push(tool); else byRun.set(tool.run_id, [tool]); }
  const shared: ActivityBoundary[] = messages.map(message => ({ at: message.created_at, anchor: message.id })).sort((a, b) => compareActivityTime(a.at, b.at));
  const placements: OwnerPlacement[] = [];
  for (const owner of owners) {
    const run = owner.family.latest;
    const ids = new Set(owner.family.attempts.map(attempt => attempt.id));
    const own = owner.family.attempts.flatMap(attempt => byRun.get(attempt.id) ?? []).filter(tool => tool.conversation_id === run.conversation_id && tool.bot_id === run.bot_id);
    const ordered = orderToolActivities(own);
    const mine = own.length ? notes.filter(note => ids.has(note.run_id ?? "") && note.conversation_id === run.conversation_id).map(note => ({ at: note.created_at })) : [];
    const boundaries = mine.length ? [...shared, ...mine].sort((a, b) => compareActivityTime(a.at, b.at)) : shared;
    const headAt = owner.anchor ? anchorTime(owner.anchor) : undefined;
    const segments = splitActivitySegments(ordered, boundaries, headAt);
    const first = owner.family.attempts[0]?.created_at ?? run.created_at;
    if (segments.length < 2 && !segments[0]?.anchor) { placements.push({ key: owner.key, owner, anchor: owner.anchor, at: first }); continue; }
    segments.forEach((part, index) => {
      const last = index === segments.length - 1;
      const segment: TaskSegment = { tools: part.tools, index, count: segments.length, primary: last, from: index === 0 ? undefined : part.tools[0].started_at, until: last ? undefined : segments[index + 1].tools[0].started_at };
      // A stretch with no drawn message before it stays at the run's own place.
      const anchor = part.anchor ? { kind: "message" as const, id: part.anchor } : owner.anchor;
      placements.push({ key: index === 0 ? owner.key : `${owner.key}@${part.tools[0].call_id}`, owner, anchor, at: index === 0 && !part.anchor ? first : part.tools[0].started_at, segment });
    });
  }
  return placements;
}
