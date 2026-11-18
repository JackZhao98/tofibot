import type { ReactNode } from "react";
import { cx } from "../cx";
import "./SettingsRow.css";

export type SettingsRowProps = {
  label: ReactNode;
  /** Only when it adds something the label does not say. */
  hint?: ReactNode;
  /** The id of the control, when there is one: the label then names it for screen readers. */
  htmlFor?: string;
  className?: string;
  /** The control. */
  children?: ReactNode;
};

/** One setting: a label and an optional hint on the left, its control on the right. Wraps under the label on a phone. */
export function SettingsRow({ label, hint, htmlFor, className, children }: SettingsRowProps) {
  return <div className={cx("tb-srow2", className)}>
    <div className="tb-srow2-text">
      {htmlFor ? <label className="tb-srow2-label" htmlFor={htmlFor}>{label}</label> : <div className="tb-srow2-label">{label}</div>}
      {hint ? <div className="tb-srow2-hint">{hint}</div> : null}
    </div>
    <div className="tb-srow2-control">{children}</div>
  </div>;
}
