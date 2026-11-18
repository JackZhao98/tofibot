import type { ReactNode } from "react";
import { cx } from "../cx";
import "./LiveLine.css";

export type LiveLineProps = {
  /** The Bot's avatar, in groups. */
  avatar?: ReactNode;
  elapsed?: ReactNode;
  className?: string;
  /** The state, kept short: Thinking, Waiting to start, Finishing. */
  children?: ReactNode;
};

/** The live line under the conversation while a Bot thinks or works. Only when no plan card is up. */
export function LiveLine({ avatar, elapsed, className, children }: LiveLineProps) {
  return <div className={cx("tb-liveline", className)} role="status">
    {avatar ? <span className="tb-liveline-avatar">{avatar}</span> : null}
    <span className="tb-livemark" aria-hidden="true" />
    <span className="tb-liveline-text">{children}</span>
    {elapsed ? <span className="tb-liveline-time">{elapsed}</span> : null}
  </div>;
}
