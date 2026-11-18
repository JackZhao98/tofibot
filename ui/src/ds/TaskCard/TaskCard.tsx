import type { ReactNode } from "react";
import { Badge, type BadgeTone } from "../Badge/Badge";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "./TaskCard.css";

export type TaskItem = {
  text: ReactNode;
  state?: "open" | "done" | "waiting" | "failed";
  /** The item has just been done: the marker draws, then the line strikes through. Never on history load. */
  fresh?: boolean;
};

export type TaskCardProps = {
  title: ReactNode;
  eyebrow?: ReactNode;
  /** The stamp in the head, a Badge tone and its words. */
  status?: { tone: BadgeTone; label: string };
  items?: TaskItem[];
  meta?: ReactNode;
  /** One or two actions. */
  actions?: ReactNode;
  /** A paused task is a plain teal note: no edge, no hard shadow (S3). */
  paused?: boolean;
  className?: string;
};

/** A task that needs the person, inline in the conversation. Every other task lives in the Tasks sidebar. A task waiting for you
 *  takes the ink edge and the hard shadow; a paused one does not. The checklist boxes use the A marker set (S19). */
export function TaskCard({ title, eyebrow, status, items, meta, actions, paused, className }: TaskCardProps) {
  return <section className={cx("tb-task", paused && "is-paused", className)} aria-label={typeof title === "string" ? title : undefined}>
    <header className="tb-task-head">
      <span className="tb-task-mark" aria-hidden="true"><Icon name="checklist" size={16} /></span>
      <div className="tb-task-titles">
        {eyebrow ? <div className="tb-eyebrow">{eyebrow}</div> : null}
        <div className="tb-task-title">{title}</div>
      </div>
      {status ? <Badge tone={status.tone}>{status.label}</Badge> : null}
    </header>
    {items ? <ol className="tb-task-items">
      {items.map((item, index) => <li key={index} className={cx("tb-task-item", `is-${item.state ?? "open"}`, item.fresh && "is-fresh")}>
        <span className="tb-task-box" aria-hidden="true" />
        <span className="tb-task-body"><span className="tb-task-text">{item.text}</span></span>
      </li>)}
    </ol> : null}
    {meta ? <div className="tb-task-meta">{meta}</div> : null}
    {actions ? <div className="tb-task-actions">{actions}</div> : null}
  </section>;
}
