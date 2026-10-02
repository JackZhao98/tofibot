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
export type ApprovalDetails = { action: string; target: string; impact: string; payload?: string; approve_label?: string; deny_label?: string; draft_id?: string };
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
  created_at: string;
  updated_at?: string;
  expires_at?: string;
  outcome?: { status: string; execution_certainty: string; message: string; next_action: string };
}
export type ConversationItem = { kind: "message"; message: Message } | { kind: "question"; question: Question } | { kind: "mail_draft"; draft: MailDraft };

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
