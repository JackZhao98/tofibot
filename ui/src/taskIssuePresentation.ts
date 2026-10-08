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
export type TaskIssueKind = "provider_busy" | "model_unconfigured" | "model_auth" | "model_quota" | "tool_setup" | "review_context" | "review_unavailable" | "expired" | "human_denied" | "policy_denied" | "uncertain_effect" | "connection_status" | "runtime_connection" | "unknown_failure";
export type TaskAction = "open_tools" | "open_codex" | "copy_diagnostics" | "view_activity" | "verify_steps" | "refresh_status";
export type ExecutionState = "not_executed" | "in_progress" | "completed" | "unknown";
export type TaskEvidence = { source: "run" | "tool" | "question" | "draft" | "connection"; id: string; code?: string; status?: string; certainty?: string; model?: string; updatedAt?: string; contextCode?: string };
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
  if (code === "mcp_result_unknown" || status === "uncertain_effect") return "uncertain_effect";
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
  mcp_config_changed: "outcome.config_changed",
} as const;
const known = <T extends object>(map: T, code: string | undefined): code is Extract<keyof T, string> => Boolean(code && Object.hasOwn(map, code));

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

export type TaskPresentationInput = { run: Run; family?: TaskFamily; tools?: ToolActivity[]; questions?: Question[]; drafts?: MailDraft[]; summary?: ToolActivityRunSummary; recordsComplete?: boolean; connected?: boolean; locale?: Language };
export function presentTaskIssue({ run, family, tools = [], questions = [], drafts = [], summary, recordsComplete = false, connected = true, locale }: TaskPresentationInput): TaskIssueView | undefined {
  const t = tasksT(locale);
  // Defend against unrelated records even if the caller hands us a mixed page.
  const sameObjectScope = (item: {run_id: string; conversation_id: string; bot_id: string}) => item.conversation_id === run.conversation_id && item.bot_id === run.bot_id && (item.run_id === run.id || family?.attempts.some(attempt => attempt.id === item.run_id && attempt.conversation_id === item.conversation_id && attempt.bot_id === item.bot_id));
  const questionUnknown = (question: Question) => !question.approval?.review_only && !question.approval?.review?.status.startsWith("shadow") && (question.outcome?.execution_certainty === "unknown" || question.outcome?.status === "uncertain_effect");
  // A newer attempt cannot settle an earlier object's external effect. Keep only
  // still-unknown earlier records, never revive resolved historical failures.
  tools = tools.filter(item => sameObjectScope(item) && (item.run_id === run.id || toolExecutionState(item) === "unknown"));
  questions = questions.filter(item => sameObjectScope(item) && (item.run_id === run.id || questionUnknown(item)));
  drafts = drafts.filter(item => sameObjectScope(item) && (item.run_id === run.id || item.status === "unknown"));
  const priorUnknown = [...tools, ...questions, ...drafts].some(item => item.run_id !== run.id);
  const causes: TaskIssueKind[] = [];
  const add = (kind?: TaskIssueKind) => { if (kind && !causes.includes(kind)) causes.push(kind); };
  const evidence: TaskEvidence[] = [];
  for (const tool of tools) {
    const state = toolExecutionState(tool);
    if (tool.run_id === run.id) add(category(tool.outcome?.code, tool.outcome?.status));
    if (state === "unknown") add("uncertain_effect");
    evidence.push({ source: "tool", id: tool.call_id, code: tool.outcome?.code, status: tool.outcome?.status ?? tool.status, certainty: state, updatedAt: tool.updated_at });
  }
  for (const question of questions) {
    const review = question.approval?.review;
    // Observations are read-only activity, never hard denials or permission.
    if (!question.approval?.review_only && !review?.status.startsWith("shadow")) {
      if (question.run_id === run.id) {
        add(category(undefined, review?.status));
        add(category(undefined, question.outcome?.status));
        if (question.status === "expired") add("expired");
        if (question.question_type === "approval" && question.status === "answered" && question.answer === false && question.answered_by !== "auto-review") add("human_denied");
      }
      if (questionUnknown(question)) add("uncertain_effect");
    }
    evidence.push({ source: "question", id: question.question_id, status: review?.status ?? question.status, certainty: question.outcome?.execution_certainty, model: review?.model, updatedAt: question.updated_at, contextCode: review?.context_failure?.code });
  }
  for (const draft of drafts) {
    if (draft.status === "unknown") add("uncertain_effect");
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
  const priority: TaskIssueKind[] = ["uncertain_effect", "expired", "human_denied", "policy_denied", "tool_setup", "review_context", "review_unavailable", "connection_status", "model_unconfigured", "model_auth", "model_quota", "provider_busy", "runtime_connection", "unknown_failure"];
  const kind = priority.find(value => causes.includes(value))!;
  const action: TaskAction = kind === "uncertain_effect" ? "verify_steps" : kind === "tool_setup" ? "open_tools" : ["model_unconfigured", "model_auth", "model_quota"].includes(kind) ? "open_codex" : ["review_context", "review_unavailable"].includes(kind) ? "copy_diagnostics" : kind === "connection_status" ? "refresh_status" : "view_activity";
  // A zero tool count is a recorded fact: there is nothing more to load or verify.
  const noToolSteps = isTerminalRun(run) && summary?.tool_count === 0 && !tools.some(tool => tool.run_id === run.id);
  const phase = run.finishing_reason ? "finishing" : isTerminalRun(run) ? "terminal" : "active";
  const facts: string[] = [];
  const links: (TaskFactLink | undefined)[] = [];
  const pushToolFact = (tool: ToolActivity, text: string) => {
    const label = taskToolLabel(tool, locale);
    links[facts.length] = { runId: tool.run_id, callId: tool.call_id, label };
    facts.push(t("fact.labelled", { label, text }));
  };
  if (kind === "uncertain_effect") {
    for (const tool of tools.filter(tool => toolExecutionState(tool) === "unknown")) pushToolFact(tool, t("fact.execution_needs_checking"));
    for (const question of questions.filter(questionUnknown)) facts.push(t("fact.labelled", { label: taskQuestionLabel(question, locale), text: t("fact.execution_needs_checking") }));
    for (const draft of drafts.filter(draft => draft.status === "unknown")) facts.push(t("fact.labelled", { label: taskDraftLabel(draft, locale), text: t("fact.sending_needs_checking") }));
    facts.push(t("fact.may_have_taken_effect"));
  }
  // Steps that did not run are marked on the steps themselves.
  if (drafts.some(draft => draft.status === "unknown")) facts.push(t("fact.check_sent_mail"));
  else if (kind === "uncertain_effect") facts.push(t("fact.check_target_service"));
  if (kind === "connection_status") facts.push(t("fact.may_still_be_running"));
  if (kind === "unknown_failure" && !facts.length) facts.push(noToolSteps ? t("fact.no_cause_recorded") : t("fact.cause_unconfirmed"));
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
  const complete = noToolSteps || (recordsComplete && (!summary || tools.filter(tool => tool.run_id === run.id).length >= summary.tool_count));
  const secondary = causes.filter(cause => cause !== kind && cause !== "unknown_failure").map(cause => cause === "provider_busy" ? t("secondary.then_provider_busy") : t(`issue.title.${cause}`));
  if (priorUnknown) secondary.unshift(run.status === "done" ? t("secondary.later_attempt_ended") : t("secondary.later_attempt", { phase: taskPhaseLabel({run, tools:tools.filter(tool => tool.run_id === run.id), questions:questions.filter(question => question.run_id === run.id), drafts:drafts.filter(draft => draft.run_id === run.id), locale}) }));
  if (!complete) secondary.push(t("secondary.records_incomplete"));
  evidence.push({ source: "run", id: run.id, status: run.status, code: overloaded ? "server_is_overloaded" : run.failure?.code, model: run.model, updatedAt: run.updated_at });
  if (!connected) evidence.push({ source: "connection", id: run.id, status: "disconnected" });
  return { kind, phase, title: t(`issue.title.${kind}`), facts, links, secondary, action, actionLabel: action === "open_codex" && provider !== "codex" ? t("issue.action.open_model_connections") : t(`issue.action.${action}`), evidence, recordsComplete: complete };
}

/** Diagnostics never contain arguments, results, prose errors, mail or reasoning. */
export function taskDiagnostics(issue: TaskIssueView) {
  const token = (value?: string) => value && /^[a-zA-Z0-9_.:/-]{1,160}$/.test(value) ? value : undefined;
  const timestamp = (value?: string) => value && /^\d{4}-\d{2}-\d{2}T[\d:.+-]+Z?$/.test(value) && Number.isFinite(Date.parse(value)) ? value : undefined;
  return JSON.stringify({ records_complete: issue.recordsComplete, evidence: issue.evidence.map(item => ({ source: item.source, id: token(item.id), code: token(item.code), status: token(item.status), certainty: token(item.certainty), model: token(item.model), updated_at: timestamp(item.updatedAt), context_code: token(item.contextCode) })) }, null, 2);
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
