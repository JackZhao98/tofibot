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
  /** A gauge of a quantity, not of work in flight (spend against a limit, context against its window): role=meter on its own scale. */
  meter?: { max: number; now: number; label: string };
  /** Stacked parts of one total, each a share of the track from 0 to 100 (a storage bar). They replace the single fill. */
  segments?: { key: string; value: number; className?: string }[];
  /** aria-valuetext: the quantity in words. */
  valueText?: string;
  /** No known total: the track carries no aria-valuenow. */
  unscaled?: boolean;
  /** The name when there is no visible label. */
  ariaLabel?: string;
  /** Goes on the track, the element that carries the role. */
  trackClassName?: string;
  /** data-state on the track. */
  state?: string;
};

/** A thick striped progress bar. Not for under a second of work. */
export function Progress({ value, label, showValue, tone = "default", className, meter, segments, valueText, unscaled, ariaLabel, trackClassName, state }: ProgressProps) {
  const v = Math.max(0, Math.min(100, value || 0));
  const name = meter?.label ?? ariaLabel ?? (typeof label === "string" ? label : undefined);
  return <div className={cx("tb-progress", className)}>
    {(label || showValue) && <div className="tb-progress-head"><span>{label}</span>{showValue && <span className="tb-mono">{Math.round(v)}%</span>}</div>}
    <div className={cx("tb-progress-track", tone !== "default" && `tb-progress--${tone}`, segments && "tb-progress--stacked", trackClassName)} role={meter ? "meter" : "progressbar"}
      aria-valuenow={unscaled ? undefined : meter ? meter.now : Math.round(v)} aria-valuemin={0} aria-valuemax={meter ? meter.max : 100} aria-valuetext={valueText} aria-label={name} data-state={state}>
      {segments ? segments.map(part => <i key={part.key} className={cx("tb-progress-seg", part.className)} style={{ width: `${Math.max(0, Math.min(100, part.value))}%` }} />) : <div className="tb-progress-fill" style={{ width: `${v}%` }} />}
    </div>
  </div>;
}
