import { toolExecutionState } from "./toolTimeline";
export { toolExecutionState } from "./toolTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Question } from "./questionTimeline";
import { buildRetryFamilies, retryFamilyAnchor, isTerminalRun } from "./runFamily";
import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "./types";

export type TaskLocale = "zh-CN" | "en";
export const taskLocale = (): TaskLocale => typeof document !== "undefined" && document.documentElement.lang.startsWith("en") ? "en" : "zh-CN";
export const taskText = (locale: TaskLocale, zh: string, en: string) => locale === "en" ? en : zh;
export type TaskIssueKind = "provider_busy" | "model_unconfigured" | "model_auth" | "model_quota" | "tool_setup" | "review_context" | "review_unavailable" | "expired" | "human_denied" | "policy_denied" | "uncertain_effect" | "connection_status" | "runtime_connection" | "unknown_failure";
export type TaskAction = "open_tools" | "open_codex" | "copy_diagnostics" | "view_activity" | "verify_steps" | "refresh_status";
export type ExecutionState = "not_executed" | "in_progress" | "completed" | "unknown";
export type TaskEvidence = { source: "run" | "tool" | "question" | "draft" | "connection"; id: string; code?: string; status?: string; certainty?: string; model?: string; updatedAt?: string; contextCode?: string };
export type TaskIssueView = { kind: TaskIssueKind; phase: "active" | "finishing" | "terminal"; title: string; facts: string[]; secondary: string[]; action: TaskAction; actionLabel: string; evidence: TaskEvidence[]; recordsComplete: boolean };
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

/** Keep the incumbent answer guards. Advice alone never creates permission. */
export function canAnswerQuestion(question: Question, archived = false) {
  if (archived || question.status !== "pending") return false;
  if (question.question_type !== "approval") return true;
  if (question.outcome?.execution_certainty === "unknown" || ["approval_expired", "approval_denied", "uncertain_effect", "setup_required", "context_required", "unavailable", "policy_denied"].includes(question.outcome?.status ?? "")) return false;
  const approval = question.approval, review = approval?.review;
  if (approval?.review_only) return false;
  if (!review) return true; // Legacy backend pending manual proposal.
  return ["human_required", "invalidated", "not_eligible", "not_reviewed", "shadow", "shadow_needs_human"].includes(review.status);
}


const titles: Record<TaskIssueKind, [string, string]> = {
  provider_busy: ["模型服务暂时繁忙", "Model service is temporarily busy"],
  model_unconfigured: ["没有可用的 AI 提供方", "No AI provider is available"],
  model_auth: ["模型账户登录已失效", "Model account sign-in is no longer valid"],
  model_quota: ["模型账户额度已用尽", "Model account usage limit reached"],
  tool_setup: ["工具尚未就绪", "Tool setup is incomplete"],
  review_context: ["执行前检查缺少必要信息", "Required information is missing from the pre-execution check"],
  review_unavailable: ["执行前检查暂时不可用", "Pre-execution checking is unavailable"],
  expired: ["批准已过期", "Approval expired"],
  human_denied: ["你未批准此操作", "You declined this action"],
  policy_denied: ["此操作未通过安全检查", "This action did not pass the safety check"],
  uncertain_effect: ["执行结果待核实", "Execution result needs checking"],
  connection_status: ["连接中断，状态待确认", "Connection lost; status is unconfirmed"],
  runtime_connection: ["连接中断，任务未完成", "Connection interrupted; the task did not finish"],
  unknown_failure: ["任务未完成", "The task did not finish"],
};
const actions: Record<TaskAction, [string, string]> = {
  open_tools: ["查看工具设置", "Open tool settings"], open_codex: ["打开 Codex 设置", "Open Codex settings"], copy_diagnostics: ["复制诊断信息", "Copy diagnostics"], view_activity: ["查看执行记录", "View activity"], verify_steps: ["查看核实步骤", "See verification steps"], refresh_status: ["刷新状态", "Refresh status"],
};
const category = (code?: string, status?: string): TaskIssueKind | undefined => {
  if (code === "mcp_result_unknown" || status === "uncertain_effect") return "uncertain_effect";
  if (code === "approval_window_expired" || status === "approval_expired") return "expired";
  if (code === "mcp_review_policy_denied" || status === "policy_denied") return "policy_denied";
  if (code === "approval_denied" || status === "approval_denied") return "human_denied";
  if (code === "mcp_review_setup_missing" || status === "setup_required") return "tool_setup";
  if (code === "mcp_review_context_missing" || status === "context_required") return "review_context";
  if (code === "mcp_review_unavailable" || status === "unavailable") return "review_unavailable";
};

/** Neutral labels are stable across retries, loading order and subject edits.
 * They identify records visually; authority still comes from the full IDs. */
export function taskObjectLabel(source: "tool" | "question" | "draft", id: string, locale: TaskLocale = taskLocale()) {
  let fingerprint = 2166136261;
  for (const character of id) fingerprint = Math.imul(fingerprint ^ character.charCodeAt(0), 16777619);
  const label = source === "draft" ? taskText(locale, "邮件", "Email") : source === "tool" ? taskText(locale, "工具调用", "Tool call") : taskText(locale, "提案", "Proposal");
  return `${label} · ${(fingerprint >>> 0).toString(16).padStart(8, "0").toUpperCase()}`;
}

const reviewStatusWords: Record<string, [string, string]> = {
  context_required: ["执行前检查缺少必要信息", "Pre-execution check is missing information"], setup_required: ["工具尚未就绪", "Tool setup is incomplete"],
  policy_denied: ["未通过安全检查", "Did not pass the safety check"], unavailable: ["执行前检查暂时不可用", "Pre-execution checking is unavailable"],
  human_required: ["等你批准", "Waiting for your approval"], not_reviewed: ["等你批准", "Waiting for your approval"], invalidated: ["等你批准", "Waiting for your approval"], not_eligible: ["等你批准", "Waiting for your approval"],
  reviewing: ["检查中", "Checking"], allow: ["已通过检查", "Passed the check"], approved: ["已通过检查", "Passed the check"],
  pending: ["待处理", "Pending"], answered: ["已回答", "Answered"], expired: ["已过期", "Expired"], cancelled: ["已取消", "Cancelled"], run_done: ["运行已结束", "Run ended"],
  approval_expired: ["批准已过期", "Approval expired"], approval_denied: ["未批准", "Declined"], uncertain_effect: ["结果待核实", "Result needs checking"],
};
const certaintyWords: Record<string, [string, string]> = {
  not_executed: ["未执行", "Not executed"], unknown: ["结果待核实", "Result needs checking"], no_side_effect: ["未产生改动", "No changes made"], executed: ["已执行", "Executed"], completed: ["已执行", "Executed"],
};
/** Human words for review/answer states; raw codes stay in technical diagnostics only. */
export function taskStatusText(status: string | undefined, locale: TaskLocale = taskLocale()) {
  if (status?.startsWith("shadow")) return taskText(locale, "观察记录", "Observation only");
  const words = status ? reviewStatusWords[status] : undefined;
  return words ? taskText(locale, ...words) : taskText(locale, "状态待确认", "Status unconfirmed");
}
export function taskCertaintyText(certainty: string | undefined, locale: TaskLocale = taskLocale()) {
  const words = certainty ? certaintyWords[certainty] : undefined;
  return words ? taskText(locale, ...words) : taskText(locale, "执行情况待确认", "Execution unconfirmed");
}

export type TaskPresentationInput = { run: Run; family?: TaskFamily; tools?: ToolActivity[]; questions?: Question[]; drafts?: MailDraft[]; summary?: ToolActivityRunSummary; recordsComplete?: boolean; connected?: boolean; locale?: TaskLocale };
export function presentTaskIssue({ run, family, tools = [], questions = [], drafts = [], summary, recordsComplete = false, connected = true, locale = "zh-CN" }: TaskPresentationInput): TaskIssueView | undefined {
  const t = (zh: string, en: string) => taskText(locale, zh, en);
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
  if (!causes.length) return undefined;
  const priority: TaskIssueKind[] = ["uncertain_effect", "expired", "human_denied", "policy_denied", "tool_setup", "review_context", "review_unavailable", "connection_status", "model_unconfigured", "model_auth", "model_quota", "provider_busy", "runtime_connection", "unknown_failure"];
  const kind = priority.find(value => causes.includes(value))!;
  const action: TaskAction = kind === "uncertain_effect" ? "verify_steps" : kind === "tool_setup" ? "open_tools" : ["model_unconfigured", "model_auth", "model_quota"].includes(kind) ? "open_codex" : ["review_context", "review_unavailable"].includes(kind) ? "copy_diagnostics" : kind === "connection_status" ? "refresh_status" : "view_activity";
  // A zero tool count is a recorded fact: there is nothing more to load or verify.
  const noToolSteps = isTerminalRun(run) && summary?.tool_count === 0 && !tools.some(tool => tool.run_id === run.id);
  const phase = run.finishing_reason ? "finishing" : isTerminalRun(run) ? "terminal" : "active";
  const facts: string[] = [];
  if (kind === "uncertain_effect") {
    for (const tool of tools.filter(tool => toolExecutionState(tool) === "unknown")) facts.push(`${taskObjectLabel("tool", `${tool.run_id}:${tool.call_id}`, locale)}: ${t("执行结果待核实。", "Execution result needs checking.")}`);
    for (const question of questions.filter(questionUnknown)) facts.push(`${taskObjectLabel("question", question.question_id, locale)}: ${t("执行结果待核实。", "Execution result needs checking.")}`);
    for (const draft of drafts.filter(draft => draft.status === "unknown")) facts.push(`${taskObjectLabel("draft", draft.draft_id, locale)}: ${t("发送结果待核实。", "Sending result needs checking.")}`);
    facts.push(t("以上待核实的操作可能已经生效；再次操作前请先核实。", "The unconfirmed actions above may have taken effect. Check before trying again."));
  }
  const notExecuted = tools.filter(tool => toolExecutionState(tool) === "not_executed");
  if (notExecuted.length) for (const tool of notExecuted) facts.push(`${taskObjectLabel("tool", `${tool.run_id}:${tool.call_id}`, locale)}: ${t("本次工具调用未执行。", "This tool call was not executed.")}`);
  else for (const question of questions.filter(question => !question.approval?.review_only && question.outcome?.execution_certainty === "not_executed")) facts.push(`${taskObjectLabel("question", question.question_id, locale)}: ${t("此提案对应的操作未执行。", "The action associated with this proposal was not executed.")}`);
  if (drafts.some(draft => draft.status === "unknown")) facts.push(t("请检查邮件服务中的已发送记录，核对时间、收件人与主题。", "Check sent-mail records in your email service and compare the time, recipient, and subject."));
  else if (kind === "uncertain_effect") facts.push(t("请在目标服务中核对这次操作的记录。无法确认时，请保持结果待核实。", "Check this action's records in the target service. If still unconfirmed, keep the result unconfirmed."));
  if (kind === "connection_status") facts.push(t("任务可能仍在运行。刷新只读取最新状态。", "The task may still be running. Refresh only reads the latest status."));
  if (kind === "unknown_failure" && !facts.length) facts.push(noToolSteps ? t("没有记录具体原因。", "No specific cause was recorded.") : t("目前无法确认原因和执行结果。", "The cause and execution result are not yet confirmed."));
  if (kind === "provider_busy") facts.push(t("任务未完成。", "The task did not finish."));
  if (kind === "model_unconfigured") facts.push(t("工作区没有连接 Codex 账户，模型无法调用。", "This workspace has no Codex account connected, so the model cannot be called."), t("在设置的「服务器与 Codex」页连接账户后重试。", "Connect an account under Settings › Server and Codex, then retry."));
  if (kind === "model_auth") facts.push(t("Codex 账户的登录已失效，模型拒绝了这次调用。", "The Codex account sign-in is no longer valid, and the model rejected this request."), t("在设置的「服务器与 Codex」页重新连接后重试。", "Reconnect under Settings › Server and Codex, then retry."));
  if (kind === "model_quota") facts.push(t("Codex 账户的用量额度已用尽，模型拒绝了这次调用。", "The Codex account has reached its usage limit, and the model rejected this request."), t("额度恢复或更换账户后重试。", "Retry after the limit resets or connect another account."));
  if (noToolSteps) facts.push(t("本次没有执行任何工具步骤。", "No tool steps ran in this attempt."));
  if (kind === "expired") facts.push(phase === "finishing" ? t("正在收尾，已完成结果保留。", "Finishing up; completed results are retained.") : t("本次工作已停止，已完成结果保留。", "This workflow stopped; completed results are retained."));
  if (tools.some(tool => toolExecutionState(tool) === "completed")) facts.push(t("已完成的工具步骤保留在工作过程。", "Completed tool steps are retained in activity."));
  const complete = noToolSteps || (recordsComplete && (!summary || tools.filter(tool => tool.run_id === run.id).length >= summary.tool_count));
  const secondary = causes.filter(cause => cause !== kind && cause !== "unknown_failure").map(cause => cause === "provider_busy" ? t("随后模型服务繁忙，任务未完成。", "The model service was then busy, and the task did not finish.") : t(...titles[cause]));
  if (priorUnknown) secondary.unshift(run.status === "done" ? t("后续尝试已结束；此前操作的结果仍待核实。", "The later attempt ended; earlier action results still need checking.") : `${t("后续尝试", "Later attempt")}: ${taskPhaseLabel({run, tools:tools.filter(tool => tool.run_id === run.id), questions:questions.filter(question => question.run_id === run.id), drafts:drafts.filter(draft => draft.run_id === run.id), locale})}`);
  if (!complete) secondary.push(t("执行记录尚未完整加载。", "Execution records are not fully loaded."));
  evidence.push({ source: "run", id: run.id, status: run.status, code: overloaded ? "server_is_overloaded" : run.failure?.code, model: run.model, updatedAt: run.updated_at });
  if (!connected) evidence.push({ source: "connection", id: run.id, status: "disconnected" });
  return { kind, phase, title: t(...titles[kind]), facts, secondary, action, actionLabel: t(...actions[action]), evidence, recordsComplete: complete };
}

/** Diagnostics never contain arguments, results, prose errors, mail or reasoning. */
export function taskDiagnostics(issue: TaskIssueView) {
  const token = (value?: string) => value && /^[a-zA-Z0-9_.:/-]{1,160}$/.test(value) ? value : undefined;
  const timestamp = (value?: string) => value && /^\d{4}-\d{2}-\d{2}T[\d:.+-]+Z?$/.test(value) && Number.isFinite(Date.parse(value)) ? value : undefined;
  return JSON.stringify({ records_complete: issue.recordsComplete, evidence: issue.evidence.map(item => ({ source: item.source, id: token(item.id), code: token(item.code), status: token(item.status), certainty: token(item.certainty), model: token(item.model), updated_at: timestamp(item.updatedAt), context_code: token(item.contextCode) })) }, null, 2);
}

export function taskPhaseLabel(input: TaskPresentationInput) {
  const { run, questions = [], tools = [], drafts = [], locale = "zh-CN" } = input;
  const t = (zh: string, en: string) => taskText(locale, zh, en);
  if (run.finishing_reason) return t("正在收尾", "Finishing up");
  if (questions.some(question => canAnswerQuestion(question) && question.question_type === "approval") || drafts.some(draft => draft.status === "pending")) return t("需要你批准", "Your approval is needed");
  if (questions.some(question => canAnswerQuestion(question) && question.question_type !== "approval")) return t("需要你回答", "Your input is needed");
  if (isTerminalRun(run)) return run.status === "done" ? t("本轮已结束", "This turn ended") : t("本次工作已停止", "This workflow stopped");
  if (questions.some(question => question.approval?.review?.status === "reviewing")) return t("执行前检查中", "Checking before execution");
  if (tools.some(tool => tool.status === "running")) return t("正在使用工具", "Using a tool");
  if (tools.some(tool => tool.status === "queued")) return t("正在准备工具", "Preparing a tool");
  return run.status === "queued" ? t("已排队", "Queued") : t("正在处理", "Working");
}
