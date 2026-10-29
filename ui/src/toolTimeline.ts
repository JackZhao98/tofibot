import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "./types";
import { isMidRunKind } from "./runProgress";
import { i18n, type Language } from "./i18n";

/** A failed request is not evidence that its external effect did not occur. */
export function toolExecutionState(tool: Pick<ToolActivity, "status" | "outcome">): "not_executed" | "in_progress" | "completed" | "unknown" | "approved" {
  const outcome = tool.outcome;
  // A recorded human approval is a resolved wait step, not an effect: the actual
  // execution is a separate call_mcp_tool activity, so nothing is uncertain here.
  if (outcome?.status === "approval_recorded" && tool.status === "completed") return "approved";
  if (outcome?.status === "uncertain_effect" || outcome?.code === "mcp_result_unknown" || outcome?.execution_certainty === "unknown") return "unknown";
  if (outcome?.execution_certainty === "not_executed") return tool.status === "completed" ? "unknown" : "not_executed";
  if (tool.status === "completed") return "completed";
  if (tool.status === "running" || tool.status === "queued") return "in_progress";
  // A failed row with no recorded outcome (for example a name the run never had)
  // was refused before dispatch. Only an explicit unknown certainty is uncertain.
  if (tool.status === "failed" && !outcome) return "not_executed";
  return "unknown";
}


export interface ToolTimeline {
  beforeMessageId: Map<string, ToolActivity[]>;
  afterMessageId: Map<string, ToolActivity[]>;
  fallback: ToolActivity[];
}

export interface ToolRunAnchors<T> {
  beforeMessageId: Map<string, T[]>;
  afterMessageId: Map<string, T[]>;
}

/** Summarize attempts, not whether the user's task was completed. */
export function toolAttemptIssues(items: Pick<ToolActivity, "status" | "outcome">[]): string {
  const failures = items.filter(item => item.status === "failed" && (!item.outcome || ["failed", "unknown"].includes(toolDisplayState(item)))).length;
  const interrupted = items.filter(item => item.status === "interrupted").length;
  const pending = items.filter(item => item.status === "queued" || item.status === "running").length;
  const t = i18n.getFixedT(null, "tasks");
  return [failures ? t("tool.attempt.failed", { count: failures }) : "", interrupted ? t("tool.attempt.interrupted", { count: interrupted }) : "", pending ? t("tool.attempt.pending", { count: pending }) : ""].filter(Boolean).join(" · ");
}

export function toolDisplayState(activity: Pick<ToolActivity,"status"|"outcome">): string {
  // The answer review sends a draft back to the Bot; the Bot revises it. That is the check working, not a failure.
  if (activity.outcome?.code === "answer_review") return "reviewed";
  if (toolExecutionState(activity) === "unknown") return "unknown";
  if (activity.status === "failed" && activity.outcome?.status === "approval_expired") return "expired";
  if (activity.status === "failed" && activity.outcome?.code === "batch_skipped" && activity.outcome.execution_certainty === "not_executed") return "skipped";
  const certainty = toolExecutionState(activity);
  if (certainty === "unknown") return "unknown";
  if (certainty === "not_executed") return "not_executed";
  if (certainty === "approved") return "approved";
  return activity.status;
}

const displayStates = ["queued", "running", "completed", "failed", "interrupted", "expired", "not_executed", "reviewed", "approved", "unknown"] as const;
export function toolDisplayLabel(activity: Pick<ToolActivity,"status"|"outcome">, locale?: Language): string {
  const t = i18n.getFixedT(locale ?? null, "tasks");
  const state = toolDisplayState(activity) === "skipped" ? "not_executed" : toolDisplayState(activity);
  return (displayStates as readonly string[]).includes(state) ? t(`step.state.${state as typeof displayStates[number]}`) : t("step.state.unconfirmed");
}

type PreciseTime = { milliseconds: number; nanoseconds: number };

// Date.parse truncates RFC3339Nano to milliseconds. The server writes UTC
// timestamps with up to nine fractional digits, so retain the fraction when
// comparing assistant and tool boundaries.
function preciseTime(value: string): PreciseTime | null {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d+))?Z$/.exec(value);
  if (match) {
    const milliseconds = Date.parse(`${match[1]}Z`);
    if (!Number.isFinite(milliseconds)) return null;
    return { milliseconds, nanoseconds: Number((match[2] ?? "").padEnd(9, "0").slice(0, 9)) };
  }
  const milliseconds = Date.parse(value);
  return Number.isFinite(milliseconds) ? { milliseconds, nanoseconds: 0 } : null;
}

/** Orders two ISO timestamps to the nanosecond; an unreadable one sorts last. */
export function compareActivityTime(a: string, b: string) { return compareTime(preciseTime(a), preciseTime(b)); }

function compareTime(a: PreciseTime | null, b: PreciseTime | null) {
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  return a.milliseconds - b.milliseconds || a.nanoseconds - b.nanoseconds;
}

/** Reconnect/detail pages must not replace newer certainty with older evidence. */
export function reconcileToolActivity(previous: ToolActivity | undefined, next: ToolActivity): ToolActivity {
  if (!previous) return next;
  const order = compareTime(preciseTime(previous.updated_at), preciseTime(next.updated_at));
  if (order > 0) return previous;
  if (order < 0) return next;
  // A list preview of the same version never replaces the full result already loaded.
  if (next.result_preview && !previous.result_preview && previous.status === next.status) return previous;
  const before = toolExecutionState(previous), after = toolExecutionState(next);
  if (before === after) return next;
  // Equal-version contradictory snapshots cannot confirm an external effect.
  // Retain any completed result as a business record while projecting unknown.
  const record = previous.status === "completed" ? previous : next;
  return { ...record, outcome: { ...record.outcome, status: "uncertain_effect", code: record.outcome?.code ?? "", execution_certainty: "unknown", message: "", next_action: "" } };
}

/** Keep tool order tied to the immutable queued timestamp, not completion time. */
export function orderToolActivities(activities: ToolActivity[]) {
  // Parse each timestamp once, not once per comparison: a run of hundreds of calls re-sorts on every tool event.
  const keyed = activities.map(activity => ({ activity, at: preciseTime(activity.started_at) }));
  keyed.sort((a, b) => compareTime(a.at, b.at) || a.activity.call_id.localeCompare(b.activity.call_id));
  return keyed.map(item => item.activity);
}

export function compareToolActivities(a: ToolActivity, b: ToolActivity) {
  return compareTime(preciseTime(a.started_at), preciseTime(b.started_at)) || a.call_id.localeCompare(b.call_id);
}

type ToolActionCode = "form" | "answer" | "takeover" | "connection" | "secret_request" | "secret_use" | "search_tools" | "search_history" | "inspect_runs" | "computer_help" | "send_message" | "complete_schedule" | "profile" | "connected_tool" | "browse" | "documents" | "command" | "computer" | "contact" | "call";
const namedToolActions: Record<string, ToolActionCode> = {
  ask_user_form: "form", ask_user_question: "answer", request_secret_input: "secret_request", request_takeover: "takeover", request_connection: "connection", use_secret_input: "secret_use",
  search_mcp_tools: "search_tools", search_history: "search_history", inspect_recent_runs: "inspect_runs", computer_help: "computer_help",
  send_chat_message: "send_message", say: "send_message", complete_scheduled_task: "complete_schedule", set_bot_profile: "profile",
};

/** What an observed tool event is doing, as a code; callers word it. */
function toolActionCode(activity: Pick<ToolActivity, "name" | "arguments">): ToolActionCode {
  const name = activity.name.toLowerCase();
  if (Object.hasOwn(namedToolActions, name)) return namedToolActions[name];
  if (name === "call_mcp_tool") {
    try {
      const args = JSON.parse(activity.arguments) as { name?: unknown };
      if (typeof args.name === "string") {
        // Only the connected service can name the role (a "web" connector browses). The
        // remote tool's own verb must not: Notion's "fetch" is not browsing the web.
        const service = args.name.split("__").slice(0, -1).join("__").toLowerCase();
        if (service && /^mcp[_-]*(browser|web|crawl)/.test(service)) return "browse";
      }
    } catch { /* An unparseable call is not evidence of the remote action. */ }
    return "connected_tool";
  }
  if (name === "computer_action") {
    try {
      const args = JSON.parse(activity.arguments) as { action?: unknown };
      const action = typeof args.action === "string" ? args.action.toLowerCase() : "";
      if (/browser|web|navigate|page|tab/.test(action)) return "browse";
      if (/file|read|document/.test(action)) return "documents";
      if (/shell|exec|terminal/.test(action)) return "command";
    } catch { /* A malformed argument is not evidence of an action. */ }
    return "computer";
  }
  if (/browser|web|fetch|crawl/.test(name)) return "browse";
  if (/read|file|document|attachment|pdf/.test(name)) return "documents";
  if (/shell|terminal|exec|command/.test(name)) return "command";
  if (/computer|desktop|screen/.test(name)) return "computer";
  if (/message|handoff/.test(name)) return "contact";
  return "call";
}

/** Labels reflect observed tool events, never an inferred task the Bot has not started. */
export function toolActionLabel(activity: Pick<ToolActivity, "name" | "arguments">, locale?: Language): string {
  return i18n.getFixedT(locale ?? null, "tasks")(`tool.action.${toolActionCode(activity)}`, { name: activity.name });
}

/**
 * Which provider served a web_search / web_fetch call: the server puts it first in the result (`{"source":"tavily",...}`),
 * so it is readable even from a shortened preview. Returns "via Tavily"-style text, or undefined when the result names none.
 */
export function toolSourceLabel(activity: Pick<ToolActivity, "name" | "result">, locale?: Language): string | undefined {
  if (activity.name !== "web_search" && activity.name !== "web_fetch") return undefined;
  const id = /^\s*\{"source":"([a-z]{2,20})"/.exec(activity.result ?? "")?.[1];
  if (!id) return undefined;
  const t = i18n.getFixedT(locale ?? null, "tasks");
  const known = ["tavily", "brave", "searxng", "firecrawl", "brightdata", "builtin", "computer"].includes(id);
  return t("tool.via", { source: known ? t(`tool.source.${id as "tavily"}`) : id });
}

/**
 * Consecutive finished calls of the same tool and action fold into one group (a row with a count). Anything that is
 * running, queued or has a problem stays its own entry, so a failure is never hidden inside a fold.
 */
export function foldToolRuns<T extends Pick<ToolActivity, "name" | "arguments" | "status" | "outcome">>(items: T[]): { key: string; items: T[] }[] {
  const groups: { key: string; items: T[]; code: string }[] = [];
  for (const [index, item] of items.entries()) {
    const foldable = toolDisplayState(item) === "completed";
    const code = foldable ? `${item.name}|${toolActionCode(item)}` : "";
    const last = groups[groups.length - 1];
    if (foldable && last && last.code === code) last.items.push(item);
    else groups.push({ key: `${index}`, items: [item], code });
  }
  return groups.map(({ key, items: grouped }) => ({ key, items: grouped }));
}

/** A step's human title for records: the action without the live progress wording. */
export function toolStepTitle(activity: Pick<ToolActivity, "name" | "arguments">, locale?: Language): string {
  return i18n.getFixedT(locale ?? null, "tasks")(`tool.title.${toolActionCode(activity)}`, { name: activity.name });
}

/** Seconds a finished step took; undefined while running or when times are missing. */
export function toolStepSeconds(activity: Pick<ToolActivity, "status" | "started_at" | "updated_at">): number | undefined {
  if (activity.status === "running" || activity.status === "queued") return undefined;
  const started = Date.parse(activity.started_at), ended = Date.parse(activity.updated_at);
  return Number.isFinite(started) && Number.isFinite(ended) && ended >= started ? (ended - started) / 1000 : undefined;
}

export function formatStepSeconds(seconds: number): string {
  if (seconds < 10) return `${seconds.toFixed(1)}s`;
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${Math.round(seconds - minutes * 60)}s`;
}

export function activeToolForRun(activities: ToolActivity[], runId: string): ToolActivity | undefined {
  return orderToolActivities(activities.filter(activity => activity.run_id === runId && activity.status === "running")).at(-1);
}

export function elapsedToolSeconds(activity: ToolActivity, now: number): number | undefined {
  const started = Date.parse(activity.started_at);
  const end = activity.status === "running" ? now : Date.parse(activity.updated_at);
  return Number.isFinite(started) && Number.isFinite(end) && end >= started ? Math.floor((end - started) / 1000) : undefined;
}

/** Compact, intentionally allowlisted context; never surface raw commands or URL secrets in the timeline. */
export function toolArgumentPreview(activity: Pick<ToolActivity, "arguments">): string | undefined {
  try {
    const args = JSON.parse(activity.arguments) as Record<string, unknown>;
    if (!args || Array.isArray(args) || typeof args !== "object") return undefined;
    if (typeof args.action === "string" && /^[a-z][a-z0-9._-]{0,48}$/i.test(args.action)) return args.action;
    if (typeof args.url === "string") {
      const url = new URL(args.url);
      if (url.protocol === "https:" || url.protocol === "http:") return `${url.hostname}${url.pathname}`.slice(0, 72);
    }
    if (typeof args.path === "string") return args.path.split(/[\\/]/).at(-1)?.slice(0, 64);
  } catch { /* Raw arguments stay in the expanded detail only. */ }
  return undefined;
}

const stepActionWords = {
  "desktop.capture": "desktop_capture", "desktop.click": "desktop_click", "desktop.type": "desktop_type", "desktop.key": "desktop_key", "desktop.scroll": "desktop_scroll", "desktop.start": "desktop_start", "desktop.stop": "desktop_stop",
  "browser.snapshot": "browser_snapshot", "browser.action": "browser_action", "files.read": "files_read", "files.write": "files_write", "files.list": "files_list",
} as const;

/** The short detail beside a step: the page or file it touched, the remote tool's own name,
 * or a plain word for a desktop action. Raw commands and argument bodies stay in the detail. */
export function toolStepDetail(activity: Pick<ToolActivity, "name" | "arguments">): string | undefined {
  try {
    const args = JSON.parse(activity.arguments) as Record<string, unknown>;
    if (args && typeof args === "object" && !Array.isArray(args)) {
      if (activity.name === "call_mcp_tool" && typeof args.name === "string") {
        const remote = args.name.split("__").at(-1);
        if (remote && /^[a-z0-9._-]{1,48}$/i.test(remote)) return remote;
      }
      if (typeof args.url === "string") {
        const url = new URL(args.url);
        if (url.protocol === "https:" || url.protocol === "http:") return `${url.hostname}${url.pathname === "/" ? "" : url.pathname}`.slice(0, 48);
      }
      if (typeof args.action === "string" && Object.hasOwn(stepActionWords, args.action)) return i18n.t(`tasks:tool.step_action.${stepActionWords[args.action as keyof typeof stepActionWords]}`);
    }
  } catch { /* Fall back to the allowlisted preview. */ }
  const preview = toolArgumentPreview(activity);
  return preview && !/^[a-z]+\.[a-z_]+$/i.test(preview) ? preview : undefined;
}

export function buildToolRunAnchors<T extends { run_id: string }>(messages: Message[], values: T[], runs: Run[] = []): ToolRunAnchors<T> {
  const beforeMessageId = new Map<string, T[]>();
  const afterMessageId = new Map<string, T[]>();
  const finalByRunID = new Map<string, Message>();
  const progressByRunID = new Map<string, Message>();
  const messageIDs = new Set(messages.map(message => message.id));
  const runByID = new Map(runs.map(run => [run.id, run]));
  for (const message of [...messages].sort((a, b) => a.seq - b.seq)) {
    if (message.role === "assistant" && message.kind !== "notice" && !isMidRunKind(message.kind) && message.run_id && !finalByRunID.has(message.run_id)) finalByRunID.set(message.run_id, message);
    if (message.role === "assistant" && isMidRunKind(message.kind) && message.run_id) progressByRunID.set(message.run_id, message);
  }
  for (const value of values) {
    const reply = finalByRunID.get(value.run_id);
    if (reply) {
      beforeMessageId.set(reply.id, [...(beforeMessageId.get(reply.id) ?? []), value]);
      continue;
    }
    const progress = progressByRunID.get(value.run_id);
    if (progress) {
      afterMessageId.set(progress.id, [...(afterMessageId.get(progress.id) ?? []), value]);
      continue;
    }
    const triggerMessageID = runByID.get(value.run_id)?.trigger_message_id;
    if (triggerMessageID && messageIDs.has(triggerMessageID)) afterMessageId.set(triggerMessageID, [...(afterMessageId.get(triggerMessageID) ?? []), value]);
  }
  return { beforeMessageId, afterMessageId };
}

/**
 * A summary is enough to render an accurate collapsed disclosure while the
 * detail page remains unloaded. Exact detail rows stay in the same aggregate
 * disclosure so a terminal run is never split across multiple progress notes.
 */
export function buildToolSummaryAnchors(messages: Message[], summaries: ToolActivityRunSummary[], _activities: ToolActivity[], runs: Run[] = []) {
  return buildToolRunAnchors(messages, summaries.filter(summary => summary.tool_count > 0), runs);
}

/** One disclosure per run. Never turn unanchored historical tools into new activity. */
export function buildToolTimeline(messages: Message[], activities: ToolActivity[], runs: Run[] = []): ToolTimeline {
  const beforeMessageId = new Map<string, ToolActivity[]>();
  const afterMessageId = new Map<string, ToolActivity[]>();
  const fallback: ToolActivity[] = [];
  const runById = new Map(runs.map(run => [run.id, run]));
  const messageIds = new Set(messages.map(message => message.id));
  const firstReply = new Map<string, Message>();
  for (const message of [...messages].sort((a, b) => a.seq - b.seq)) {
    if (message.role === "assistant" && message.kind !== "notice" && message.run_id && !firstReply.has(message.run_id)) firstReply.set(message.run_id, message);
  }
  const grouped = new Map<string, Map<string, ToolActivity>>();
  for (const activity of activities) {
    const calls = grouped.get(activity.run_id) ?? new Map<string, ToolActivity>();
    const previous = calls.get(activity.call_id);
    if (!previous || compareTime(preciseTime(previous.updated_at), preciseTime(activity.updated_at)) <= 0) calls.set(activity.call_id, activity);
    grouped.set(activity.run_id, calls);
  }
  for (const [runId, calls] of grouped) {
    const items = orderToolActivities([...calls.values()]);
    const reply = firstReply.get(runId);
    const run = runById.get(runId);
    const reports = messages.filter(message => message.run_id === runId && message.role === "assistant" && isMidRunKind(message.kind)).sort((a, b) => a.seq - b.seq);
    if (reports.length) {
      for (const activity of items) {
        const report = [...reports].reverse().find(message => compareTime(preciseTime(message.created_at), preciseTime(activity.started_at)) <= 0);
        const destination = report ? afterMessageId : beforeMessageId;
        const anchor = report?.id ?? reports[0].id;
        destination.set(anchor, [...(destination.get(anchor) ?? []), activity]);
      }
      continue;
    }
    if (reply) {
      beforeMessageId.set(reply.id, items);
    } else if (run?.trigger_message_id && messageIds.has(run.trigger_message_id)) {
      const anchor = run.trigger_message_id;
      afterMessageId.set(anchor, [...(afterMessageId.get(anchor) ?? []), ...items]);
    } else if (run?.status === "running" || run?.status === "queued") {
      // A live run can precede its trigger's snapshot; completed historical runs cannot.
      fallback.push(...items);
    }
  }
  return { beforeMessageId, afterMessageId, fallback: orderToolActivities(fallback) };
}

export function toolActivityKey(activity: Pick<ToolActivity, "run_id" | "call_id">) {
  return `${activity.run_id}\u0000${activity.call_id}`;
}

/** Something drawn in the conversation that closes the activity segment above it. `anchor` is the rendered message a later segment sits under; a boundary that draws nothing of its own (a progress note kept inside Activity) has none, and the segment follows the last anchor before it. */
export interface ActivityBoundary { at: string; anchor?: string }
/** Consecutive tool calls with nothing else between them. `anchor` is the message the segment is drawn under; undefined is the run's own place (the head segment). */
export interface ActivitySegment { tools: ToolActivity[]; anchor?: string }

/**
 * Cuts a run's tool calls (ordered by start) into the stretches the conversation shows them in. Whenever a boundary (a person's
 * message, another run's ticket or notice, the Bot's own say or progress message) falls between two calls, the second starts a new
 * segment after it. Calls with no boundary between them and `headAt` (the time of the item the run's block is anchored to; none:
 * the first call) stay in the head segment. Boundaries must be ordered by time. One pass over both lists: O(calls + boundaries).
 */
export function splitActivitySegments(tools: ToolActivity[], boundaries: ActivityBoundary[], headAt?: string): ActivitySegment[] {
  if (!tools.length) return [];
  const times = boundaries.map(boundary => preciseTime(boundary.at));
  const lastBefore = (at: PreciseTime | null, from: number) => { let index = from; while (index + 1 < times.length && compareTime(times[index + 1], at) <= 0) index += 1; return index; };
  const starts = tools.map(tool => preciseTime(tool.started_at));
  const headKey = headAt ? lastBefore(preciseTime(headAt), -1) : lastBefore(starts[0], -1);
  const segments: ActivitySegment[] = [];
  let key = -1, anchorAt = -1, segmentKey = -2;
  for (const [index, tool] of tools.entries()) {
    key = lastBefore(starts[index], key);
    const inHead = key <= headKey;
    if (!segments.length || (inHead ? -1 : key) !== segmentKey) {
      segmentKey = inHead ? -1 : key;
      let anchor: string | undefined;
      if (!inHead) { for (anchorAt = key; anchorAt >= 0 && !boundaries[anchorAt].anchor; anchorAt -= 1); anchor = anchorAt >= 0 ? boundaries[anchorAt].anchor : undefined; }
      segments.push({ tools: [], anchor });
    }
    segments[segments.length - 1].tools.push(tool);
  }
  return segments;
}
