import type { ReactNode } from "react";
import * as Collapsible from "@radix-ui/react-collapsible";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../fold.css";
import "../shared.css";
import "./ScheduleCard.css";

export type ScheduleCardProps = {
  state?: "done" | "run" | "error";
  /** The state in words and time: "12:31", "Running · 14s", "Failed". */
  statusLabel: string;
  /** The time, large: "09:00". */
  time: string;
  /** The day or the rule's short form: "Daily", "Mon", "Once". */
  day?: string;
  title: ReactNode;
  /** The rule in words, not cron: "Daily 09:00 · run 12". */
  meta?: string;
  /** The task text; opens under the card. */
  children?: ReactNode;
  /** Start open. */
  open?: boolean;
  bodyLabel?: string;
  /** What happened, for state error. */
  error?: ReactNode;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
};

/** A scheduled run, drawn as a page torn from a desk calendar. Say the rule in words. A schedule's tool calls live in the work log, not here. */
export function ScheduleCard({ state = "done", statusLabel, time, day, title, meta, children, open, bodyLabel, error, onRetry, retryLabel, className }: ScheduleCardProps) {
  const { t } = useTranslation();
  const status = <span className={cx("tb-sched-st", `is-${state}`)}><i>{state === "error" ? "!" : null}</i>{statusLabel}</span>;
  const row = (chevron: boolean) => <>
    <span className="tb-sched-title">{title}</span>{status}
    {chevron ? <Icon name="chevron-right" size={14} className="tb-sched-chev" /> : null}
  </>;
  return <section className={cx("tb-sched", `is-${state}`, className)} aria-label={typeof title === "string" ? title : undefined}>
    <span className="tb-sched-leaf" aria-hidden="true"><span className="tb-sched-rings" /><b>{time}</b>{day ? <small>{day}</small> : null}</span>
    {children ? <Collapsible.Root asChild defaultOpen={!!open}>
      <div className="tb-sched-main">
        <Collapsible.Trigger className="tb-sched-row">{row(true)}</Collapsible.Trigger>
        {meta ? <span className="tb-sched-meta">{meta}</span> : null}
        <Collapsible.Content asChild><div className="tb-fold"><div className="tb-sched-body">{bodyLabel ? <small>{bodyLabel}</small> : null}{children}</div></div></Collapsible.Content>
        {error ? <div className="tb-sched-err"><span>{error}</span>{onRetry ? <button type="button" onClick={onRetry}>{retryLabel ?? t("ds.schedule.retry")}</button> : null}</div> : null}
      </div>
    </Collapsible.Root> : <div className="tb-sched-main">
      <div className="tb-sched-row">{row(false)}</div>
      {meta ? <span className="tb-sched-meta">{meta}</span> : null}
      {error ? <div className="tb-sched-err"><span>{error}</span>{onRetry ? <button type="button" onClick={onRetry}>{retryLabel ?? t("ds.schedule.retry")}</button> : null}</div> : null}
    </div>}
  </section>;
}
