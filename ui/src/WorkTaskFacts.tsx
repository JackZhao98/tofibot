import { memo } from "react";
import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import { formatZonedTime } from "./timezone";
import type { WorkItem, WorkWait } from "./types";
import "./work-panel.css";

const waitKeys = { bot_post: "facts.wait.bot_post", task_result: "facts.wait.task_result", human_answer: "facts.wait.human_answer", command_exit: "facts.wait.command_exit", timer: "facts.wait.timer" } as const satisfies Record<WorkWait["kind"], string>;
const checkKeys = { open: "facts.check.open", done: "facts.check.done", skipped: "facts.check.skipped", blocked: "facts.check.blocked" } as const satisfies Record<NonNullable<WorkItem["checklist"]>[number]["status"], string>;

/** Delegation, checklist, wait, and result lines of a task. Renders nothing for a plain task. `compact` stays inline-only so a board card (itself a button) holds no nested controls. */
export const WorkTaskFacts = memo(function WorkTaskFacts({ item, timezone, compact = false }: { item: WorkItem; timezone: string; compact?: boolean }) {
  const { t } = useTranslation("work");
  const checklist = item.checklist ?? [];
  const waits = item.waiting_on ?? [];
  const finished = checklist.filter(entry => entry.status === "done" || entry.status === "skipped").length;
  const from = item.assigner_kind === "bot" && item.assigner_bot_name ? item.assigner_bot_name : "";
  const version = (item.spec_version ?? 1) > 1 ? item.spec_version : 0;
  const closed = item.status === "done" || item.status === "failed" || item.status === "cancelled";
  const summary = closed ? item.result_summary : "";
  if (!from && !version && !checklist.length && !waits.length && !summary) return null;
  return <div className={`work-facts${compact ? " is-compact" : ""}`}>
    {(from || version || checklist.length > 0) && <div className="work-facts-chips">
      {from && <span className="work-chip">{t("facts.from", { name: from })}</span>}
      {!!version && <span className="work-chip" title={t("facts.version_title")}>{t("facts.version", { version })}</span>}
      {checklist.length > 0 && <span className="work-chip" title={t("facts.checklist_title")}>{t("facts.progress", { done: finished, total: checklist.length })}</span>}
    </div>}
    {summary && <p className="work-facts-result">{summary}</p>}
    {!compact && waits.length > 0 && <ul className="work-facts-waits" aria-label={t("facts.waits_aria")}>
      {waits.map(wait => <li key={wait.id}>{t(waitKeys[wait.kind])}{(wait.target_name || wait.note) && <> · {wait.target_name || wait.note}</>} <small>{t("facts.wait_until", { time: formatZonedTime(wait.deadline_at, timezone) })}</small></li>)}
    </ul>}
    {!compact && checklist.length > 0 && <Disclosure title={t("facts.checklist", { done: finished, total: checklist.length })}>
      <ul className="work-facts-checklist">
        {checklist.map(entry => <li key={entry.id} data-status={entry.status}><span className="work-check-mark">{t(checkKeys[entry.status])}</span><span>{entry.text}{entry.reason && <small>{entry.reason}</small>}</span></li>)}
      </ul>
    </Disclosure>}
  </div>;
});
