import { memo } from "react";
import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import type { TaskCardModel } from "./taskCards";
import type { WorkChecklistItem, WorkStatus } from "./types";
import "./task-card.css";

const statusKeys = {
  todo: "status.todo", in_progress: "status.in_progress", blocked: "status.blocked", review: "status.review",
  done: "status.done", cancelled: "status.cancelled", input_required: "status.input_required", failed: "status.failed",
} as const satisfies Record<WorkStatus, string>;
const checkKeys = { open: "facts.check.open", done: "facts.check.done", skipped: "facts.check.skipped", blocked: "facts.check.blocked" } as const satisfies Record<WorkChecklistItem["status"], string>;

/** The one line of a task: who holds it, how far its checklist is, and its state. */
export function useTaskLine(card: Pick<TaskCardModel, "holderName" | "done" | "total" | "status">): string {
  const { t } = useTranslation("work");
  return [card.holderName, card.total ? t("facts.progress", { done: card.done, total: card.total }) : "", t(statusKeys[card.status])].filter(Boolean).join(" · ");
}

/**
 * One card per task, collapsed to a single line. Task events update the model in place; the card adds no message and
 * is skipped by the row unless its own model changed (decision 0060).
 */
export const TaskCard = memo(function TaskCard({ card }: { card: TaskCardModel }) {
  const { t } = useTranslation("work");
  const line = useTaskLine(card);
  return <section className="task-card" data-task-id={card.id} data-status={card.status} data-claimed={card.claimed || undefined} aria-label={t("card.label", { title: card.title })}>
    <Disclosure title={<span className="task-card-line">{line}</span>}>
      <p className="task-card-title">{card.title}</p>
      {card.items.length > 0 && <ul className="task-card-items">
        {card.items.map(item => <li key={item.id} data-status={item.status}><span className="task-card-mark">{t(checkKeys[item.status])}</span><span>{item.text}</span></li>)}
      </ul>}
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
