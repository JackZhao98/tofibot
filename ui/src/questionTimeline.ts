import type { Message } from "./types";
import type { MailDraft } from "./MailDraftCard";

export interface QuestionField {
  id: string;
  label: string;
  type: "text" | "email" | "password" | "textarea";
  required: boolean;
  placeholder?: string;
}
export type FormAnswer = Record<string, string | { secret_ref: string; value_hidden: true }>;
export type OtherAnswer = { values: string[]; other_text: string };
export type ApprovalDetails = { action: string; target: string; impact: string; payload?: string; review_only?: boolean; review?: {source: "auto-review"; status: "reviewing" | "approved" | "shadow" | "human_required" | "invalidated" | "not_eligible" | "not_reviewed" | "setup_required" | "context_required" | "unavailable" | "policy_denied" | "terminal" | "human_decided" | "shadow_reviewing" | "shadow_allow" | "shadow_deny" | "shadow_needs_human" | "shadow_context_required" | "shadow_setup_required" | "shadow_unavailable" | "shadow_invalidated"; reason: string; model: string; risk_level?: "low" | "medium" | "high" | "unknown"; confirmation_required?: boolean; policy_version?: string}; approve_label?: string; deny_label?: string; draft_id?: string };
export type QuestionAnswer = string | boolean | string[] | FormAnswer | OtherAnswer;
export interface Question {
  question_id: string;
  conversation_id: string;
  bot_id: string;
  run_id: string;
  type: "question";
  question_type: "text" | "yes_no" | "approval" | "single_choice" | "multi_choice" | "form";
  question: string;
  options?: { id: string; label: string }[];
  allow_other?: boolean;
  fields?: QuestionField[];
  source_url?: string;
  approval?: ApprovalDetails;
  min_selections?: number;
  max_selections?: number;
  status: "pending" | "answered" | "cancelled" | "run_done" | "expired";
  answer?: QuestionAnswer;
  answered_by?: string;
  created_at: string;
  updated_at?: string;
  expires_at?: string;
  outcome?: { status: string; execution_certainty: string; message: string; next_action: string };
}
export type ConversationItem = { kind: "message"; message: Message } | { kind: "question"; question: Question } | { kind: "mail_draft"; draft: MailDraft };

/** Readiness, terminal validity and policy advice are different server facts. */
export function autoReviewPresentation(question: Question): {badge: string; label: string} | undefined {
 const review = question.approval?.review;
 if (!review) return undefined;
 if (review.status.startsWith("shadow_")) {
  const labels: Record<string, string> = {shadow_reviewing:"观察审查中", shadow_allow:"观察建议允许", shadow_deny:"观察建议拒绝", shadow_needs_human:"观察建议人工决定", shadow_context_required:"观察上下文缺口", shadow_setup_required:"观察配置缺口", shadow_unavailable:"观察审查不可用", shadow_invalidated:"观察建议已失效"};
  return {badge:question.status === "answered" && question.answered_by && question.answered_by !== "auto-review" ? "人工已决定 · 观察建议" : "观察模式 · 保留原有执行策略", label:labels[review.status] ?? "观察建议"};
 }
 if (review.policy_version === "mcp-all-external-v5") {
  const blockedLabels: Record<string, string> = {setup_required:"配置缺口",context_required:"上下文缺口",unavailable:"审查不可用",policy_denied:"策略判决拒绝",terminal:question.status === "expired" ? "提案已失效" : "不可执行"};
  if (blockedLabels[review.status]) return {badge:"提案不可执行",label:blockedLabels[review.status]};
 }
 if (question.status === "answered" && question.answered_by && question.answered_by !== "auto-review") return {badge: "人工已决定", label: "人工决定有效"};
 switch (review.status) {
  case "setup_required": return {badge: "配置未就绪", label: "配置缺口"};
  case "context_required": return {badge: "审查上下文不完整", label: "上下文缺口"};
  case "unavailable": return {badge: "审查未完成", label: "审查不可用"};
  case "policy_denied": return {badge: "审查未允许", label: "策略判决拒绝"};
 }
 if (question.status === "expired") return {badge: "提案已过期", label: "提案已失效"};
 if (question.status === "cancelled" || question.status === "run_done" || review.status === "terminal") return {badge: "提案已结束", label: "不可执行"};
 switch (review.status) {
  case "approved": return question.status === "answered" && question.answered_by === "auto-review" && question.answer === true ? {badge: "AutoReview 自动批准", label: "自动批准"} : {badge: "审查建议允许", label: "允许建议"};
  case "reviewing": return {badge: review.policy_version === "mcp-all-external-v5" ? "AutoReview 审查中" : "AutoReview 审查中 · 可人工决定", label: "审查中"};
  case "shadow": return {badge: "观察模式 · 人工执行策略", label: "观察建议"};
  case "human_required": return {badge: "需要你决定", label: "策略需要人工决定"};
  case "invalidated": return {badge: "自动决定已失效", label: "自动决定失效"};
  case "not_eligible": return {badge: "执行策略需人工批准", label: "未获自动执行资格"};
  case "not_reviewed": return {badge: "本提案已有审查记录", label: "未重复审查"};
  case "human_decided": return {badge: "人工已决定", label: "人工决定有效"};
 }
}

/** Preserve the server's sub-millisecond ordering at question/message boundaries. */
export function compareQuestionTime(a: string, b: string) {
  const parts = (value: string) => {
    const utc = /^(.*T\d{2}:\d{2}:\d{2})(?:\.(\d+))?Z$/.exec(value);
    return utc ? [Date.parse(`${utc[1]}Z`), Number((utc[2] ?? "").padEnd(9, "0").slice(0, 9))] : [Date.parse(value), 0];
  };
  const left = parts(a), right = parts(b);
  return left[0] - right[0] || left[1] - right[1];
}

/** Prefer the newest authoritative version while an answer response is local. */
export function reconcileQuestion(item: Question, local: Question | null): Question {
 if (!local || item.question_id !== local.question_id) return item;
 const serverTime = item.updated_at ?? item.created_at;
 const localTime = local.updated_at ?? local.created_at;
 const order = compareQuestionTime(serverTime, localTime);
 if (order > 0) return item;
 if (order < 0) return local;
 // Older servers omit versions. A backend expiry must still replace a decision.
 if (item.status === "expired" || item.status === "run_done" || item.status === "cancelled") return item;
 return local;
}

/** Messages retain sequence order; answering a card never moves its original position. */
export function buildQuestionTimeline(messages: Message[], questions: Question[], hasMore: boolean, mailDrafts: MailDraft[] = []): ConversationItem[] {
  const lowerBound = hasMore ? messages[0]?.created_at : undefined;
  const ordered: Exclude<ConversationItem, { kind: "message" }>[] = [
    ...questions.map(question => ({ kind: "question" as const, question })),
    ...mailDrafts.map(draft => ({ kind: "mail_draft" as const, draft })),
  ].filter(item => !lowerBound || (item.kind === "question" ? (item.question.status === "pending" || item.question.outcome?.next_action === "renew_approval") : item.draft.status === "pending") || compareQuestionTime(item.kind === "question" ? item.question.created_at : item.draft.created_at, lowerBound) >= 0)
    .sort((a, b) => compareQuestionTime(a.kind === "question" ? a.question.created_at : a.draft.created_at, b.kind === "question" ? b.question.created_at : b.draft.created_at)
      || (a.kind === "question" ? a.question.question_id : a.draft.draft_id).localeCompare(b.kind === "question" ? b.question.question_id : b.draft.draft_id));
  const output: ConversationItem[] = [];
  let cursor = 0;
  for (const message of messages) {
    while (cursor < ordered.length) {
      const next = ordered[cursor];
      if (compareQuestionTime(next.kind === "question" ? next.question.created_at : next.draft.created_at, message.created_at) >= 0) break;
      output.push(ordered[cursor++]);
    }
    output.push({ kind: "message", message });
  }
  while (cursor < ordered.length) output.push(ordered[cursor++]);
  return output;
}
