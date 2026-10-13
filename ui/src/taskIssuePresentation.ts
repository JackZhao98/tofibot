import { toolExecutionState, toolStepDetail, toolStepTitle } from "./toolTimeline";
export { toolExecutionState } from "./toolTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Question } from "./questionTimeline";
import { buildRetryFamilies, retryFamilyAnchor, isTerminalRun } from "./runFamily";
import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "./types";
import { providerForModel } from "./modelCatalog";
import { i18n, type Language } from "./i18n";
import { formatClock } from "./i18n/format";

/** Text for one language, or the active UI language when none is given (tests pin one). */
const tasksT = (locale?: Language) => i18n.getFixedT(locale ?? null, "tasks");
export type TaskIssueKind = "provider_busy" | "model_unconfigured" | "model_auth" | "model_quota" | "tool_setup" | "review_context" | "review_unavailable" | "expired" | "human_denied" | "policy_denied" | "connection_status" | "runtime_connection" | "unknown_failure";
export type TaskAction = "open_tools" | "open_codex" | "copy_diagnostics" | "view_activity" | "refresh_status";
export type ExecutionState = "not_executed" | "in_progress" | "completed" | "unknown";
export type TaskEvidence = { source: "run" | "tool" | "question" | "draft" | "connection"; id: string; code?: string; status?: string; certainty?: string; model?: string; updatedAt?: string; contextCode?: string; failureCategory?: string };
/** A fact that names one tool step can open that step in the activity record. */
export type TaskFactLink = { runId: string; callId: string; label: string };
export type TaskIssueView = { kind: TaskIssueKind; phase: "active" | "finishing" | "terminal"; title: string; facts: string[]; links: (TaskFactLink | undefined)[]; secondary: string[]; action: TaskAction; actionLabel: string; evidence: TaskEvidence[]; recordsComplete: boolean };
export type TaskFamily = ReturnType<typeof buildRetryFamilies>[number];
export type TaskOwner = { key: string; family: TaskFamily; anchor?: { kind: "message" | "question" | "draft"; id: string } };

/** Identity comes only from durable run links. No name/time/content matching. */
export function buildTaskOwners(runs: Run[], messages: Message[], questions: Question[], drafts: MailDraft[]): TaskOwner[] {
  return buildRetryFamilies(runs).filter(family => family.latest.kind !== "triage").flatMap(family => {
    const run = family.latest;
    const matching = <T extends { run_id: string; conversation_id: string; bot_id: string }>(item: T) => family.attempts.some(attempt => attempt.id === item.run_id && attempt.conversation_id === item.conversation_id && attempt.bot_id === item.bot_id);
    // Scheduled occurrences retain their own run ID and loaded scheduled message.
    const durableMessages = messages.filter(message => message.kind !== "progress");
    const messageId = retryFamilyAnchor(family, durableMessages) ?? durableMessages.find(message => message.run_id === run.id && message.conversation_id === run.conversation_id && message.kind !== "progress")?.id;
    const question = questions.find(matching), draft = drafts.find(matching);
    const anchor = messageId ? { kind: "message" as const, id: messageId } : question ? { kind: "question" as const, id: question.question_id } : draft ? { kind: "draft" as const, id: draft.draft_id } : undefined;
    // An off-page historical run must not manufacture a new chat item.
    return anchor || !isTerminalRun(run) ? [{ key: `${run.conversation_id}:${family.rootId}`, family, anchor }] : [];
  });
}

/** Outcome codes that close an approval proposal; review states arrive as codes, not statuses. */
const closedProposalCodes = ["mcp_review_setup_missing", "mcp_review_context_missing", "mcp_review_unavailable", "mcp_review_policy_denied", "approval_window_expired", "approval_denied"];
/** Keep the incumbent answer guards. Advice alone never creates permission. */
export function canAnswerQuestion(question: Question, archived = false) {
  if (archived || question.status !== "pending") return false;
  if (question.question_type !== "approval") return true;
  if (question.outcome?.execution_certainty === "unknown" || ["approval_expired", "uncertain_effect"].includes(question.outcome?.status ?? "") || closedProposalCodes.includes(question.outcome?.code ?? "")) return false;
  const approval = question.approval, review = approval?.review;
  if (approval?.review_only) return false;
  if (!review) return true; // Legacy backend pending manual proposal.
  return ["human_required", "invalidated", "not_eligible", "not_reviewed", "shadow", "shadow_needs_human"].includes(review.status);
}


const category = (code?: string, status?: string): TaskIssueKind | undefined => {
  if (code === "approval_window_expired" || status === "approval_expired") return "expired";
  if (code === "mcp_review_policy_denied" || status === "policy_denied") return "policy_denied";
  if (code === "approval_denied" || status === "approval_denied") return "human_denied";
  if (code === "mcp_review_setup_missing" || status === "setup_required") return "tool_setup";
  if (code === "mcp_review_context_missing" || status === "context_required") return "review_context";
  if (code === "mcp_review_unavailable" || status === "unavailable") return "review_unavailable";
};

const clock = (value: string, locale: Language | undefined, seconds = true) => formatClock(value, { seconds, language: locale });
/** Human labels, stable across retries, loading order and subject edits: an object's own
 * start time plus what it is. They never include recipients, subjects or bodies. */
export function taskToolLabel(tool: Pick<ToolActivity, "name" | "arguments" | "started_at">, locale?: Language) {
  const detail = toolStepDetail(tool);
  return `${clock(tool.started_at, locale)} ${toolStepTitle(tool)}${detail ? ` · ${detail}` : ""}`.trim();
}
export function taskDraftLabel(draft: Pick<MailDraft, "created_at">, locale?: Language) {
  return tasksT(locale)("label.draft", { time: clock(draft.created_at, locale, false) }).trim();
}
export function taskQuestionLabel(question: Pick<Question, "created_at">, locale?: Language) {
  return tasksT(locale)("label.proposal", { time: clock(question.created_at, locale) }).trim();
}

/** Legacy neutral fingerprint label; kept for diagnostics callers, not shown in chat. */
export function taskObjectLabel(source: "tool" | "question" | "draft", id: string, locale?: Language) {
  let fingerprint = 2166136261;
  for (const character of id) fingerprint = Math.imul(fingerprint ^ character.charCodeAt(0), 16777619);
  const label = tasksT(locale)(source === "draft" ? "label.object.draft" : source === "tool" ? "label.object.tool" : "label.object.question");
  return `${label} · ${(fingerprint >>> 0).toString(16).padStart(8, "0").toUpperCase()}`;
}

// Server codes map to catalog keys; several codes share one human phrase.
const reviewStatusKeys = {
  context_required: "status.context_missing", setup_required: "status.setup_incomplete",
  policy_denied: "status.policy_denied", unavailable: "status.review_unavailable",
  human_required: "status.awaiting_approval", not_reviewed: "status.awaiting_approval", invalidated: "status.awaiting_approval", not_eligible: "status.awaiting_approval",
  reviewing: "status.reviewing", allow: "status.passed", approved: "status.passed",
  pending: "status.pending", answered: "status.answered", expired: "status.expired", cancelled: "status.cancelled", run_done: "status.run_done",
  approval_expired: "status.approval_expired", approval_denied: "status.approval_denied", uncertain_effect: "status.needs_checking",
  need_information: "status.need_information", validation_error: "status.validation_error",
  transient_failure: "status.transient_failure", permanent_failure: "status.permanent_failure",
} as const;
const certaintyKeys = {
  not_executed: "certainty.not_executed", unknown: "certainty.unknown", no_side_effect: "certainty.no_side_effects", no_side_effects: "certainty.no_side_effects", executed: "certainty.executed", completed: "certainty.executed",
} as const;
const outcomeCodeKeys = {
  mcp_review_context_missing: "status.context_missing", mcp_review_setup_missing: "status.setup_incomplete",
  mcp_review_policy_denied: "status.policy_denied", mcp_review_unavailable: "status.review_unavailable",
  approval_window_expired: "status.approval_expired", batch_skipped: "outcome.batch_skipped",
  stale_schema: "outcome.stale_schema", invalid_arguments: "outcome.invalid_arguments",
  mcp_retry_exhausted: "outcome.retry_exhausted", mcp_result_unknown: "status.needs_checking",
  // Readiness checks run before anything is sent: "mcp_" + the method readiness state.
  mcp_auth_required: "outcome.auth_required", mcp_unavailable: "outcome.unavailable",
  mcp_not_configured: "outcome.not_configured", mcp_unknown: "outcome.unknown", mcp_ready: "outcome.ready",
  mcp_config_changed: "outcome.config_changed", tool_rejected: "outcome.tool_rejected",
  // A computer or tool that stopped answering; the step failed, the task can go on.
  tool_timeout: "outcome.tool_timeout", computer_restarting: "outcome.computer_restarting",
  computer_unresponsive: "outcome.computer_unresponsive",
} as const;
const known = <T extends object>(map: T, code: string | undefined): code is Extract<keyof T, string> => Boolean(code && Object.hasOwn(map, code));

// A refused or failed step names the cause of a run that ended without finishing. Codes map to
// catalog keys; the step is named by its own words, never by raw upstream detail.
const stepReasonKeys = {
  mcp_needs_human_unattended: "reason.step.needs_human_unattended", mcp_high_risk_unattended: "reason.step.high_risk_unattended",
  mcp_review_context_missing: "reason.step.context_missing", mcp_review_unavailable: "reason.step.review_unavailable",
  mcp_review_setup_missing: "reason.step.setup_missing", mcp_review_policy_denied: "reason.step.policy_denied",
  approval_denied: "reason.step.denied", approval_window_expired: "reason.step.approval_expired",
  mcp_auth_required: "reason.step.auth_required", mcp_unavailable: "reason.step.unreachable",
  tool_timeout: "reason.step.timeout", mcp_result_unknown: "reason.step.unconfirmed",
} as const;
const stepName = (tool: Pick<ToolActivity, "name" | "arguments">) => toolStepDetail(tool) ?? toolStepTitle(tool);
/** Latest step whose outcome explains the failure; any other failed step is the fallback. */
function decisiveStep(tools: ToolActivity[]): ToolActivity | undefined {
  const latest = (items: ToolActivity[]) => items.reduce<ToolActivity | undefined>((best, item) => !best || item.started_at >= best.started_at ? item : best, undefined);
  return latest(tools.filter(tool => known(stepReasonKeys, tool.outcome?.code))) ?? latest(tools.filter(tool => tool.status === "failed" || tool.status === "interrupted"));
}
function runReason(run: Run, tools: ToolActivity[], t: ReturnType<typeof tasksT>): string | undefined {
  const step = decisiveStep(tools), code = step?.outcome?.code;
  if (step) return known(stepReasonKeys, code) ? t(stepReasonKeys[code], { step: stepName(step) }) : t("reason.step.failed", { step: stepName(step) });
  const error = (run.error ?? "").toLowerCase();
  if (/api error \(http (?:429|5\d\d)\)|overloaded|service unavailable|bad gateway|gateway time-?out/.test(error)) return t("reason.run.model_unavailable");
  if (run.failure?.code === "budget_exhausted") return t("reason.run.budget_exhausted");
  if (run.failure?.code === "no_final_answer") return t("reason.run.no_final_answer");
  if (run.failure?.code === "tool_stuck") return t("reason.run.tool_stuck");
  if (error.includes("scheduled task returned without confirming a completed result")) return t("reason.run.receipt_missing");
}

/** A tool outcome in words: the specific code first, then its status. */
export function taskOutcomeText(outcome: { code?: string; status?: string } | undefined, locale?: Language) {
  const code = outcome?.code;
  return known(outcomeCodeKeys, code) ? tasksT(locale)(outcomeCodeKeys[code]) : taskStatusText(outcome?.status, locale);
}

/** Human words for review/answer states; raw codes stay in technical diagnostics only. */
export function taskStatusText(status: string | undefined, locale?: Language) {
  const t = tasksT(locale);
  if (status?.startsWith("shadow")) return t("status.observation");
  return known(reviewStatusKeys, status) ? t(reviewStatusKeys[status]) : t("status.unconfirmed");
}
export function taskCertaintyText(certainty: string | undefined, locale?: Language) {
  const t = tasksT(locale);
  return known(certaintyKeys, certainty) ? t(certaintyKeys[certainty]) : t("certainty.unconfirmed");
}

export type TaskPresentationInput = { run: Run; family?: TaskFamily; tools?: ToolActivity[]; questions?: Question[]; drafts?: MailDraft[]; summary?: ToolActivityRunSummary; recordsComplete?: boolean; connected?: boolean; /** The run already posted a final reply in the conversation. */ replied?: boolean; locale?: Language };
export function presentTaskIssue({ run, family, tools = [], questions = [], drafts = [], summary, recordsComplete = false, connected = true, replied = false, locale }: TaskPresentationInput): TaskIssueView | undefined {
  const t = tasksT(locale);
  // Defend against unrelated records even if the caller hands us a mixed page.
  const sameObjectScope = (item: {run_id: string; conversation_id: string; bot_id: string}) => item.conversation_id === run.conversation_id && item.bot_id === run.bot_id && (item.run_id === run.id || family?.attempts.some(attempt => attempt.id === item.run_id && attempt.conversation_id === item.conversation_id && attempt.bot_id === item.bot_id));
  // An uncertain result never raises a chat card: it shows as the step's own status in
  // the work log, and the Bot explains it. Only this attempt's records can raise a card.
  tools = tools.filter(item => sameObjectScope(item) && item.run_id === run.id);
  questions = questions.filter(item => sameObjectScope(item) && item.run_id === run.id);
  drafts = drafts.filter(item => sameObjectScope(item) && item.run_id === run.id);
  const causes: TaskIssueKind[] = [];
  const add = (kind?: TaskIssueKind) => { if (kind && !causes.includes(kind)) causes.push(kind); };
  const evidence: TaskEvidence[] = [];
  for (const tool of tools) {
    add(category(tool.outcome?.code, tool.outcome?.status));
    evidence.push({ source: "tool", id: tool.call_id, code: tool.outcome?.code, status: tool.outcome?.status ?? tool.status, certainty: toolExecutionState(tool), updatedAt: tool.updated_at });
  }
  for (const question of questions) {
    const review = question.approval?.review;
    // Observations are read-only activity, never hard denials or permission.
    if (!question.approval?.review_only && !review?.status.startsWith("shadow")) {
      add(category(undefined, review?.status));
      add(category(undefined, question.outcome?.status));
      if (question.status === "expired") add("expired");
      if (question.question_type === "approval" && question.status === "answered" && question.answer === false && question.answered_by !== "auto-review") add("human_denied");
    }
    evidence.push({ source: "question", id: question.question_id, status: review?.status ?? question.status, certainty: question.outcome?.execution_certainty, model: review?.model, updatedAt: question.updated_at, contextCode: review?.context_failure?.code, failureCategory: review?.failure_category });
  }
  for (const draft of drafts) {
    evidence.push({ source: "draft", id: draft.draft_id, status: draft.status, certainty: draft.status === "unknown" ? "unknown" : undefined, updatedAt: draft.updated_at });
  }
  if (run.stop_reason === "approval_expired" || run.finishing_reason === "approval_expired" || run.failure?.code === "approval_expired") add("expired");
  // Exact independent token in the RUN error only. It affects copy, never replay.
  const overloaded = run.status === "failed" && /(?:^|[^a-zA-Z0-9_])server_is_overloaded(?:$|[^a-zA-Z0-9_])/.test(run.error ?? "");
  const modelAccount: Partial<Record<string, TaskIssueKind>> = { model_unconfigured: "model_unconfigured", model_auth_invalid: "model_auth", model_quota_exhausted: "model_quota" };
  const modelKind = run.status === "failed" ? modelAccount[run.failure?.code ?? ""] : undefined;
  if (overloaded) add("provider_busy");
  else if (modelKind) add(modelKind);
  else if (run.failure?.code === "connection_interrupted") add("runtime_connection");
  else if (["failed", "interrupted"].includes(run.status)) add("unknown_failure");
  if (!connected && !isTerminalRun(run)) add("connection_status");
  // A blocked or failed tool step is shown on that step (red, with its reason and
  // diagnostics), not as a separate card. Cards stay for run-level problems:
  // model account, results needing verification, expiry and interrupted runs.
  const stepLevel: TaskIssueKind[] = ["review_context", "review_unavailable", "tool_setup", "policy_denied", "human_denied"];
  for (let index = causes.length - 1; index >= 0; index--) if (stepLevel.includes(causes[index])) causes.splice(index, 1);
  if (!causes.length) return undefined;
  const priority: TaskIssueKind[] = ["expired", "human_denied", "policy_denied", "tool_setup", "review_context", "review_unavailable", "connection_status", "model_unconfigured", "model_auth", "model_quota", "provider_busy", "runtime_connection", "unknown_failure"];
  const kind = priority.find(value => causes.includes(value))!;
  const action: TaskAction = kind === "tool_setup" ? "open_tools" : ["model_unconfigured", "model_auth", "model_quota"].includes(kind) ? "open_codex" : ["review_context", "review_unavailable"].includes(kind) ? "copy_diagnostics" : kind === "connection_status" ? "refresh_status" : "view_activity";
  // A zero tool count is a recorded fact: there is nothing more to load or verify.
  const noToolSteps = isTerminalRun(run) && summary?.tool_count === 0 && !tools.some(tool => tool.run_id === run.id);
  const phase = run.finishing_reason ? "finishing" : isTerminalRun(run) ? "terminal" : "active";
  const facts: string[] = [];
  const links: (TaskFactLink | undefined)[] = [];
  // Steps that did not run are marked on the steps themselves.
  if (kind === "connection_status") facts.push(t("fact.may_still_be_running"));
  if (kind === "unknown_failure" && !facts.length) facts.push(runReason(run, tools, t) ?? (noToolSteps ? t("fact.no_cause_recorded") : t("fact.cause_unconfirmed")));
  if (kind === "provider_busy") facts.push(t("fact.task_did_not_finish"));
  // Model account copy names the provider of the model that failed; no model means the workspace default.
  const provider = run.model?.trim() ? providerForModel(run.model) : undefined;
  const keyLabel = provider === "anthropic" ? "Claude" : "OpenAI";
  const model = provider === "codex" ? "codex" : provider ? "api_key" : "any";
  if (kind === "model_unconfigured") facts.push(t(`fact.model_unconfigured.${model}`, { provider: keyLabel }), t(`fact.model_unconfigured.${model}_next`, { provider: keyLabel }));
  if (kind === "model_auth") facts.push(t(`fact.model_auth.${model}`, { provider: keyLabel }), t(`fact.model_auth.${model}_next`, { provider: keyLabel }));
  if (kind === "model_quota") facts.push(t(`fact.model_quota.${model}`, { provider: keyLabel }), t(`fact.model_quota.${model}_next`, { provider: keyLabel }));
  if (noToolSteps) facts.push(t("fact.no_tool_steps"));
  if (kind === "expired") facts.push(phase === "finishing" ? t("fact.finishing_results_kept") : t("fact.stopped_results_kept"));
  if (tools.some(tool => toolExecutionState(tool) === "completed")) facts.push(t("fact.completed_steps_kept"));
  if (replied && kind === "unknown_failure") facts.push(t("fact.reply_below"));
  // With a recorded step count, the loaded steps decide; without one, the page flag does.
  const loadedCount = tools.filter(tool => tool.run_id === run.id).length;
  const complete = noToolSteps || (summary ? loadedCount >= summary.tool_count : recordsComplete);
  const secondary = causes.filter(cause => cause !== kind && cause !== "unknown_failure").map(cause => cause === "provider_busy" ? t("secondary.then_provider_busy") : t(`issue.title.${cause}`));
  if (!complete) secondary.push(t("secondary.records_incomplete"));
  evidence.push({ source: "run", id: run.id, status: run.status, code: overloaded ? "server_is_overloaded" : run.failure?.code, model: run.model, updatedAt: run.updated_at });
  if (!connected) evidence.push({ source: "connection", id: run.id, status: "disconnected" });
  return { kind, phase, title: t(`issue.title.${kind}`), facts, links, secondary, action, actionLabel: action === "open_codex" && provider !== "codex" ? t("issue.action.open_model_connections") : t(`issue.action.${action}`), evidence, recordsComplete: complete };
}

/** Diagnostics never contain arguments, results, prose errors, mail or reasoning. */
export function taskDiagnostics(issue: TaskIssueView) {
  const token = (value?: string) => value && /^[a-zA-Z0-9_.:/-]{1,160}$/.test(value) ? value : undefined;
  const timestamp = (value?: string) => value && /^\d{4}-\d{2}-\d{2}T[\d:.+-]+Z?$/.test(value) && Number.isFinite(Date.parse(value)) ? value : undefined;
  return JSON.stringify({ records_complete: issue.recordsComplete, evidence: issue.evidence.map(item => ({ source: item.source, id: token(item.id), code: token(item.code), status: token(item.status), certainty: token(item.certainty), model: token(item.model), updated_at: timestamp(item.updatedAt), context_code: token(item.contextCode), failure_category: token(item.failureCategory) })) }, null, 2);
}

export function taskPhaseLabel(input: TaskPresentationInput) {
  const { run, questions = [], tools = [], drafts = [], locale } = input;
  const t = tasksT(locale);
  if (run.finishing_reason) return t("phase.finishing");
  if (questions.some(question => canAnswerQuestion(question) && question.question_type === "approval") || drafts.some(draft => draft.status === "pending")) return t("phase.needs_approval");
  if (questions.some(question => canAnswerQuestion(question) && question.question_type !== "approval")) return t("phase.needs_answer");
  if (isTerminalRun(run)) return run.status === "done" ? t("phase.turn_ended") : t("phase.stopped");
  if (questions.some(question => question.approval?.review?.status === "reviewing")) return t("phase.reviewing");
  if (tools.some(tool => tool.status === "running")) return t("phase.using_tool");
  if (tools.some(tool => tool.status === "queued")) return t("phase.preparing_tool");
  return run.status === "queued" ? t("phase.queued") : t("phase.working");
}
