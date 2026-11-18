import type { ReactNode } from "react";
import { cx } from "../cx";
import "../shared.css";
import "./Progress.css";

export type ProgressProps = {
  /** 0 to 100. */
  value: number;
  label?: ReactNode;
  showValue?: boolean;
  tone?: "default" | "warning" | "danger";
  className?: string;
};

/** A thick striped progress bar. Not for under a second of work. */
export function Progress({ value, label, showValue, tone = "default", className }: ProgressProps) {
  const v = Math.max(0, Math.min(100, value || 0));
  return <div className={cx("tb-progress", className)}>
    {(label || showValue) && <div className="tb-progress-head"><span>{label}</span>{showValue && <span className="tb-mono">{Math.round(v)}%</span>}</div>}
    <div className={cx("tb-progress-track", tone !== "default" && `tb-progress--${tone}`)} role="progressbar" aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100} aria-label={typeof label === "string" ? label : undefined}>
      <div className="tb-progress-fill" style={{ width: `${v}%` }} />
    </div>
  </div>;
}
