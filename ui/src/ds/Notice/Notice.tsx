import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Notice.css";

export type NoticeProps = {
  icon?: IconName;
  /** At most one action. */
  action?: ReactNode;
  className?: string;
  children?: ReactNode;
};

/** A centred system line in the conversation: a handover, a spend reminder, a command that ended. */
export function Notice({ icon, action, className, children }: NoticeProps) {
  return <div className={cx("tb-notice", className)} role="note">
    {icon && <span className="tb-notice-icon"><Icon name={icon} size={15} /></span>}
    <span className="tb-notice-text">{children}</span>
    {action ? <span className="tb-notice-action">{action}</span> : null}
  </div>;
}
