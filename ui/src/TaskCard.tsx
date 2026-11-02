import { memo, useState } from "react";
import { api, request } from "./api";
import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import { minutesLeft, type FoldedResult, type TaskCardModel } from "./taskCards";
import type { WorkChecklistItem, WorkStatus } from "./types";
import "./task-card.css";

const statusKeys = {
  todo: "status.todo", in_progress: "status.in_progress", blocked: "status.blocked", review: "status.review",
  done: "status.done", cancelled: "status.cancelled", input_required: "status.input_required", failed: "status.failed",
} as const satisfies Record<WorkStatus, string>;
const checkKeys = { open: "facts.check.open", done: "facts.check.done", skipped: "facts.check.skipped", blocked: "facts.check.blocked" } as const satisfies Record<WorkChecklistItem["status"], string>;
const reasonKeys = { failed: "card.reason.failed", cancelled: "card.reason.cancelled", "timed out": "card.reason.timed_out" } as const;

/** The one line of a task: who holds it, how far its checklist is, its state, and the member it waits on. */
export function useTaskLine(card: Pick<TaskCardModel, "holderName" | "done" | "total" | "status"> & Partial<Pick<TaskCardModel, "waiting" | "progressLine">> & { paused?: boolean }): string {
  const { t } = useTranslation("work");
  const names = (card.waiting ?? []).map(wait => wait.name).filter(Boolean).join(", ");
  const left = minutesLeft(card.waiting?.[0]?.deadlineAt, Date.now());
  const waiting = names ? t("card.waiting_on", { names }) + (Number.isNaN(left) ? "" : ` (${left >= 0 ? t("card.minutes_left", { minutes: left }) : t("card.overdue")})`) : "";
  return [card.holderName, card.total ? t("facts.progress", { done: card.done, total: card.total }) : "", card.paused ? t("card.paused") : t(statusKeys[card.status]), waiting, card.progressLine].filter(Boolean).join(" · ");
}

function PartResult({ result }: { result: FoldedResult }) {
  const { t } = useTranslation("work");
  return <Disclosure title={<span className="task-card-result-line">{t("card.result_of", { name: result.sender })}</span>}><p className="task-card-result-text">{result.text}</p></Disclosure>;
}

/** One checklist item. A plan item (it stands for a task the lead gave) adds the state of that task, its deadline, its one line and its folded results. */
function CardItem({ item, results }: { item: WorkChecklistItem; results: readonly FoldedResult[] }) {
  const { t } = useTranslation("work");
  const part = Boolean(item.task_id);
  const open = item.status === "open";
  const mark = part && open && item.task_status ? t(statusKeys[item.task_status]) : t(checkKeys[item.status]);
  const left = open && part ? minutesLeft(item.deadline_at, Date.now()) : NaN;
  const reason = item.reason ? (item.reason in reasonKeys ? t(reasonKeys[item.reason as keyof typeof reasonKeys]) : item.reason) : "";
  const meta = [Number.isNaN(left) ? "" : left >= 0 ? t("card.minutes_left", { minutes: left }) : t("card.overdue"), reason, open ? item.progress_line : ""].filter(Boolean).join(" · ");
  return <li data-status={item.status} data-part={part || undefined}>
    <span className="task-card-mark">{mark}</span>
    <span className="task-card-item-body"><span>{item.text}</span>{meta && <span className="task-card-meta">{meta}</span>}
      {results.map(result => <PartResult key={result.id} result={result} />)}
    </span>
  </li>;
}

/**
 * One card per task, collapsed to a single line. Task events update the model in place; the card adds no message and
 * is skipped by the row unless its own model changed (decision 0060). The card of a lead is its plan: each part is an
 * item, and the words of the parts' holders fold under their item.
 */
export const TaskCard = memo(function TaskCard({ card }: { card: TaskCardModel }) {
  const { t } = useTranslation("work");
  const line = useTaskLine(card);
  // A paused task waits for the Bot (it resumes after the current reply) or for the person: Resume starts it now, Drop ends it.
  const [busy, setBusy] = useState(false);
  const act = (run: () => Promise<unknown>) => { setBusy(true); void run().catch(() => undefined).finally(() => setBusy(false)); };
  const results = card.results ?? [];
  const planned = new Set(card.items.map(item => item.task_id).filter(Boolean));
  const loose = results.filter(result => !planned.has(result.taskId));
  return <section className="task-card" data-task-id={card.id} data-status={card.status} data-paused={card.paused || undefined} data-claimed={card.claimed || undefined} aria-label={t("card.label", { title: card.title })}>
    <Disclosure title={<span className="task-card-line">{line}</span>} defaultOpen={card.paused || undefined}>
      <p className="task-card-title">{card.title}</p>
      {card.paused && <div className="task-card-actions">
        {card.autoResume && <span className="task-card-note">{t("card.resumes_after")}</span>}
        <button type="button" className="secondary-button" disabled={busy} onClick={() => act(() => request(`/api/work-items/${encodeURIComponent(card.id)}/resume`, { method: "POST" }))}>{t("card.resume")}</button>
        <button type="button" className="secondary-button" disabled={busy} onClick={() => act(() => api.updateWorkItem(card.id, { status: "cancelled" }))}>{t("card.drop")}</button>
      </div>}
      {card.items.length > 0 && <ul className="task-card-items">
        {card.items.map(item => <CardItem key={item.id} item={item} results={item.task_id ? results.filter(result => result.taskId === item.task_id) : []} />)}
      </ul>}
      {loose.length > 0 && <div className="task-card-results">{loose.map(result => <PartResult key={result.id} result={result} />)}</div>}
    </Disclosure>
  </section>;
});

/** The member list's line for a task a member holds: claimed or only assigned, the title, progress and state. */
export const MemberTask = memo(function MemberTask({ card }: { card: TaskCardModel }) {
  const { t } = useTranslation("work");
  const progress = card.total ? ` · ${t("facts.progress", { done: card.done, total: card.total })}` : "";
  return <li className="member-task" data-claimed={card.claimed || undefined}>
    <span className="member-task-state">{card.claimed ? t("card.claimed") : t("card.assigned")}</span>
    <span className="member-task-title">{card.title}</span>
    <span className="member-task-meta">{progress} · {t(statusKeys[card.status])}</span>
  </li>;
});
