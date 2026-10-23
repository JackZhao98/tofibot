import type { Message } from "./types";
import type { MailDraft } from "./MailDraftCard";
import { i18n } from "./i18n";

export interface QuestionField {
  id: string;
  label: string;
  type: "text" | "email" | "password" | "textarea";
  required: boolean;
  placeholder?: string;
  /** The Bot's proposed variable name for "Save for this Bot"; the person decides. */
  suggested_name?: string;
}
export type FormAnswer = Record<string, string | { secret_ref: string; value_hidden: true; saved_as?: string }>;
export type OtherAnswer = { values: string[]; other_text: string };
/** Why an "unavailable" review produced no judgment; a finite union so every member has catalog text. */
export type ReviewFailureCategory = "reviewer_not_configured" | "input_invalid" | "input_too_large" | "provider_error" | "timeout" | "malformed_response" | "empty_response";
/** Server-built description of an extension change; never written by the Bot. */
export type ApprovalFacts = { kind: "skill_install" | "skill_publish_version" | "skill_delete" | "mcp_create" | "mcp_update" | "mcp_delete" | "mcp_test"; name: string; change_id?: string; source?: string; previous_source?: string; transport?: string; allow_tools?: string[]; deny_tools?: string[]; header_names?: string[]; oauth_scopes?: string[]; oauth_metadata?: string; skill?: { description?: string; excerpt?: string; allowed_tools?: string[]; files?: string[]; has_scripts?: boolean; new_version?: number; added?: string[]; removed?: string[]; changed?: string[]; enabled_bots?: number } };
export type ApprovalDetails = { action: string; target: string; impact: string; payload?: string; review_only?: boolean; review?: {source: "auto-review" | "tool_tier" | "remembered_approval" | "standing_allowance"; status: "reviewing" | "approved" | "shadow" | "human_required" | "invalidated" | "not_eligible" | "not_reviewed" | "setup_required" | "context_required" | "unavailable" | "policy_denied" | "terminal" | "human_decided" | "shadow_reviewing" | "shadow_allow" | "shadow_deny" | "shadow_needs_human" | "shadow_context_required" | "shadow_setup_required" | "shadow_unavailable" | "shadow_invalidated"; reason: string; model: string; risk_level?: "low" | "medium" | "high" | "unknown"; confirmation_required?: boolean; policy_version?: string; context_failure?: {code: string; observed?: number; limit?: number; observed_at_least?: boolean}; failure_category?: ReviewFailureCategory; decision_source?: "tool_tier" | "remembered_approval" | "standing_allowance"; grant_id?: string}; approve_label?: string; deny_label?: string; draft_id?: string; remember?: {kind: "identifier" | "exact"; target?: string}; standing?: {server: string; tool: string}; facts?: ApprovalFacts };
export type QuestionAnswer = string | boolean | string[] | FormAnswer | OtherAnswer;
export interface Question {
  question_id: string;
  conversation_id: string;
  bot_id: string;
  run_id: string;
  type: "question";
  question_type: "text" | "yes_no" | "approval" | "single_choice" | "multi_choice" | "form" | "takeover" | "connection";
  question: string;
  options?: { id: string; label: string }[];
  allow_other?: boolean;
  fields?: QuestionField[];
  source_url?: string;
  /** The MCP service a connection card is about. */
  target?: string;
  approval?: ApprovalDetails;
  min_selections?: number;
  max_selections?: number;
  status: "pending" | "answered" | "cancelled" | "run_done" | "expired";
  answer?: QuestionAnswer;
  answered_by?: string;
  created_at: string;
  updated_at?: string;
  expires_at?: string;
  /** True on a compact list row: approval payload and facts are fetched with `useQuestionDetail`. */
  detail_omitted?: boolean;
  outcome?: { status: string; code?: string; execution_certainty: string; message: string; next_action: string };
}
export type ConversationItem = { kind: "message"; message: Message } | { kind: "question"; question: Question } | { kind: "mail_draft"; draft: MailDraft };

const shadowLabels = ["shadow_reviewing", "shadow_allow", "shadow_deny", "shadow_needs_human", "shadow_context_required", "shadow_setup_required", "shadow_unavailable", "shadow_invalidated"] as const;
type ReviewBadge = "remembered" | "standing" | "human_decided_shadow" | "shadow_keeps_policy" | "proposal_not_executable" | "human_decided" | "setup_not_ready" | "review_context_incomplete" | "review_incomplete" | "review_not_allowed" | "proposal_expired" | "proposal_ended" | "auto_approved" | "review_suggests_allow" | "reviewing" | "reviewing_manual" | "shadow_manual_policy" | "needs_you" | "high_risk_tool" | "auto_invalidated" | "policy_needs_approval" | "already_reviewed";
type ReviewLabel = typeof shadowLabels[number] | "remembered" | "standing" | "shadow_advice" | "setup_gap" | "context_gap" | "review_unavailable" | "policy_denied" | "proposal_invalid" | "not_executable" | "human_decision_valid" | "auto_approved" | "allow_advice" | "reviewing" | "policy_needs_human" | "high_risk_tool" | "auto_invalidated" | "not_eligible" | "not_rereviewed";

/** Readiness, terminal validity and policy advice are different server facts. */
/** Host actor of a card decided by a remembered approval ("Approve and remember in this chat"). */
export const REMEMBERED_APPROVAL_ACTOR = "remembered-approval";
/** Host actor of a card decided by a standing allowance ("Always allow" for this Bot and tool). */
export const STANDING_ALLOWANCE_ACTOR = "standing-allowance";

export function autoReviewPresentation(question: Question): {badge: string; label: string} | undefined {
 const review = question.approval?.review;
 if (!review) return undefined;
 const t = i18n.getFixedT(null, "tasks");
 const view = (badge: ReviewBadge, label: ReviewLabel) => ({badge: t(`review.badge.${badge}`), label: t(`review.label.${label}`)});
 if (review.status.startsWith("shadow_")) {
  const label: ReviewLabel = (shadowLabels as readonly string[]).includes(review.status) ? review.status as ReviewLabel : "shadow_advice";
  return view(question.status === "answered" && question.answered_by && question.answered_by !== "auto-review" ? "human_decided_shadow" : "shadow_keeps_policy", label);
 }
 if (review.policy_version?.startsWith("mcp-all-external-v")) {
  const blockedLabels: Record<string, ReviewLabel> = {setup_required:"setup_gap",context_required:"context_gap",unavailable:"review_unavailable",policy_denied:"policy_denied",terminal:question.status === "expired" ? "proposal_invalid" : "not_executable"};
  if (Object.hasOwn(blockedLabels, review.status)) return view("proposal_not_executable", blockedLabels[review.status]);
 }
 if ((question.status === "answered" || question.status === "run_done") && question.answered_by === REMEMBERED_APPROVAL_ACTOR) return view("remembered", "remembered");
 if ((question.status === "answered" || question.status === "run_done") && question.answered_by === STANDING_ALLOWANCE_ACTOR) return view("standing", "standing");
 if (question.status === "answered" && question.answered_by && question.answered_by !== "auto-review") return view("human_decided", "human_decision_valid");
 switch (review.status) {
  case "setup_required": return view("setup_not_ready", "setup_gap");
  case "context_required": return view("review_context_incomplete", "context_gap");
  case "unavailable": return view("review_incomplete", "review_unavailable");
  case "policy_denied": return view("review_not_allowed", "policy_denied");
 }
 if (question.status === "expired") return view("proposal_expired", "proposal_invalid");
 if (question.status === "cancelled" || question.status === "run_done" || review.status === "terminal") return view("proposal_ended", "not_executable");
 switch (review.status) {
  case "approved": return question.status === "answered" && question.answered_by === "auto-review" && question.answer === true ? view("auto_approved", "auto_approved") : view("review_suggests_allow", "allow_advice");
  case "reviewing": return view(review.policy_version?.startsWith("mcp-all-external-v") ? "reviewing" : "reviewing_manual", "reviewing");
  case "shadow": return view("shadow_manual_policy", "shadow_advice");
  case "human_required": return review.decision_source === "tool_tier" ? view("high_risk_tool", "high_risk_tool") : view("needs_you", "policy_needs_human");
  case "invalidated": return view("auto_invalidated", "auto_invalidated");
  case "not_eligible": return view("policy_needs_approval", "not_eligible");
  case "not_reviewed": return view("already_reviewed", "not_rereviewed");
  case "human_decided": return view("human_decided", "human_decision_valid");
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
