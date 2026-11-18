import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./EmptyState.css";

export type EmptyStateProps = {
  title: ReactNode;
  /** The mustard block's icon; ignored when `art` is given. */
  icon?: IconName;
  /** Art in place of the icon block (an image, for example the sleeping calico). */
  art?: ReactNode;
  /** One next step. */
  action?: ReactNode;
  className?: string;
  /** One sentence. */
  children?: ReactNode;
};

/** An empty area: a mustard block or art, a title, one sentence and one action. */
export function EmptyState({ title, icon = "inbox", art, action, className, children }: EmptyStateProps) {
  return <div className={cx("tb-empty", className)}>
    {art ? <div className="tb-empty-art">{art}</div> : <div className="tb-empty-icon"><Icon name={icon} size={28} /></div>}
    <h3 className="tb-empty-title">{title}</h3>
    {children ? <p className="tb-empty-text">{children}</p> : null}
    {action ? <div className="tb-empty-action">{action}</div> : null}
  </div>;
}
