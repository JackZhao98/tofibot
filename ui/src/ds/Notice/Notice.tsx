import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Notice.css";

export type NoticeProps = {
  icon?: IconName;
  /** danger: a failed read or send, spoken as an alert. Not for a run's errors: those sit on the failed step. */
  tone?: "danger";
  /** At most one action. */
  action?: ReactNode;
  className?: string;
  children?: ReactNode;
};

/** A centred system line in the conversation: a handover, a spend reminder, a command that ended. */
export function Notice({ icon, tone, action, className, children }: NoticeProps) {
  return <div className={cx("tb-notice", tone === "danger" && "tb-notice--danger", className)} role={tone === "danger" ? "alert" : "note"}>
    {icon && <span className="tb-notice-icon"><Icon name={icon} size={15} /></span>}
    {children != null && <span className="tb-notice-text">{children}</span>}
    {action ? <span className="tb-notice-action">{action}</span> : null}
  </div>;
}
