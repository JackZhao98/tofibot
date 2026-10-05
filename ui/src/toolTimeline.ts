import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "./types";

/** A failed request is not evidence that its external effect did not occur. */
export function toolExecutionState(tool: Pick<ToolActivity, "status" | "outcome">): "not_executed" | "in_progress" | "completed" | "unknown" {
  const outcome = tool.outcome;
  if (outcome?.status === "uncertain_effect" || outcome?.code === "mcp_result_unknown" || outcome?.execution_certainty === "unknown") return "unknown";
  if (outcome?.execution_certainty === "not_executed") return tool.status === "completed" ? "unknown" : "not_executed";
  if (tool.status === "completed") return "completed";
  if (tool.status === "running" || tool.status === "queued") return "in_progress";
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
  const failures = items.filter(item => item.status === "failed" && ["failed", "unknown"].includes(toolDisplayState(item))).length;
  const interrupted = items.filter(item => item.status === "interrupted").length;
  const pending = items.filter(item => item.status === "queued" || item.status === "running").length;
  return [failures ? `${failures} 次失败` : "", interrupted ? `${interrupted} 次中断` : "", pending ? `${pending} 次待结束` : ""].filter(Boolean).join(" · ");
}

export function toolDisplayState(activity: Pick<ToolActivity,"status"|"outcome">): string {
  if (toolExecutionState(activity) === "unknown") return "unknown";
  if (activity.status === "failed" && activity.outcome?.status === "approval_expired") return "expired";
  if (activity.status === "failed" && activity.outcome?.code === "batch_skipped" && activity.outcome.execution_certainty === "not_executed") return "skipped";
  const certainty = toolExecutionState(activity);
  if (certainty === "unknown") return "unknown";
  if (certainty === "not_executed") return "not_executed";
  return activity.status;
}

export function toolDisplayLabel(activity: Pick<ToolActivity,"status"|"outcome">, locale: "zh-CN" | "en" = typeof document !== "undefined" && document.documentElement.lang.startsWith("en") ? "en" : "zh-CN"): string {
  const labels: Record<string, [string, string]> = {queued:["排队中","Queued"],running:["执行中","Running"],completed:["已完成","Completed"],failed:["失败","Failed"],interrupted:["已中断","Interrupted"],expired:["已过期","Expired"],skipped:["未执行","Not executed"],not_executed:["未执行","Not executed"],unknown:["结果待核实","Result needs checking"]};
  return (labels[toolDisplayState(activity)] ?? ["状态待确认", "Status unconfirmed"])[locale === "en" ? 1 : 0];
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
  const before = toolExecutionState(previous), after = toolExecutionState(next);
  if (before === after) return next;
  // Equal-version contradictory snapshots cannot confirm an external effect.
  // Retain any completed result as a business record while projecting unknown.
  const record = previous.status === "completed" ? previous : next;
  return { ...record, outcome: { ...record.outcome, status: "uncertain_effect", code: record.outcome?.code ?? "", execution_certainty: "unknown", message: "", next_action: "" } };
}

/** Keep tool order tied to the immutable queued timestamp, not completion time. */
export function orderToolActivities(activities: ToolActivity[]) {
  return [...activities].sort(compareToolActivities);
}

export function compareToolActivities(a: ToolActivity, b: ToolActivity) {
  return compareTime(preciseTime(a.started_at), preciseTime(b.started_at)) || a.call_id.localeCompare(b.call_id);
}

/** Labels reflect observed tool events, never an inferred task the Bot has not started. */
export function toolActionLabel(activity: Pick<ToolActivity, "name" | "arguments">): string {
  const name = activity.name.toLowerCase();
  if (name === "ask_user_form") return "等待你填写表单";
  if (name === "ask_user_question") return "等待你的回答";
  if (name === "request_secret_input") return "等待私密输入";
  if (name === "use_secret_input") return "正在使用私密输入";
  if (name === "search_mcp_tools") return "正在查找可用工具";
  if (name === "search_history") return "正在检查对话记录";
  if (name === "inspect_recent_runs") return "正在检查执行记录";
  if (name === "call_mcp_tool") {
    try {
      const args = JSON.parse(activity.arguments) as { name?: unknown };
      if (typeof args.name === "string") return toolActionLabel({ name: args.name, arguments: "" });
    } catch { /* An unparseable call is not evidence of the remote action. */ }
    return "正在使用连接的工具";
  }
  if (name === "computer_action") {
    try {
      const args = JSON.parse(activity.arguments) as { action?: unknown };
      const action = typeof args.action === "string" ? args.action.toLowerCase() : "";
      if (/browser|web|navigate|page|tab/.test(action)) return "正在浏览网页";
      if (/file|read|document/.test(action)) return "正在检查文档";
      if (/shell|exec|terminal/.test(action)) return "正在运行命令";
    } catch { /* A malformed argument is not evidence of an action. */ }
    return "正在操作电脑";
  }
  if (/browser|web|fetch|crawl/.test(name)) return "正在浏览网页";
  if (/read|file|document|attachment|pdf/.test(name)) return "正在检查文档";
  if (/shell|terminal|exec|command/.test(name)) return "正在运行命令";
  if (/computer|desktop|screen/.test(name)) return "正在操作电脑";
  if (/message|handoff/.test(name)) return "正在联系成员";
  return `正在调用 ${activity.name}`;
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

export function buildToolRunAnchors<T extends { run_id: string }>(messages: Message[], values: T[], runs: Run[] = []): ToolRunAnchors<T> {
  const beforeMessageId = new Map<string, T[]>();
  const afterMessageId = new Map<string, T[]>();
  const finalByRunID = new Map<string, Message>();
  const progressByRunID = new Map<string, Message>();
  const messageIDs = new Set(messages.map(message => message.id));
  const runByID = new Map(runs.map(run => [run.id, run]));
  for (const message of [...messages].sort((a, b) => a.seq - b.seq)) {
    if (message.role === "assistant" && message.kind !== "notice" && message.kind !== "progress" && message.run_id && !finalByRunID.has(message.run_id)) finalByRunID.set(message.run_id, message);
    if (message.role === "assistant" && message.kind === "progress" && message.run_id) progressByRunID.set(message.run_id, message);
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
    const reports = messages.filter(message => message.run_id === runId && message.role === "assistant" && message.kind === "progress").sort((a, b) => a.seq - b.seq);
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
