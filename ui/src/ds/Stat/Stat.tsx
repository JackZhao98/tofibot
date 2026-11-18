import type { ReactNode } from "react";
import { cx } from "../cx";
import "../shared.css";
import "./Stat.css";

export type StatProps = {
  label: ReactNode;
  value: ReactNode;
  unit?: ReactNode;
  delta?: ReactNode;
  /** up is good news (lime), down is bad news (danger). Without it the delta is plain. */
  deltaTone?: "up" | "down";
  /** Bars, as numbers from 0 to 100; scaled to the tallest. Decorative: say the trend in `delta`. */
  spark?: number[];
  className?: string;
};

/** One number with its label, a delta and an optional spark of bars. Four in a row at most. */
export function Stat({ label, value, unit, delta, deltaTone, spark, className }: StatProps) {
  const max = spark ? Math.max(...spark) || 1 : 1;
  return <div className={cx("tb-stat", className)}>
    <div className="tb-eyebrow">{label}</div>
    <div className="tb-stat-value">{value}{unit ? <span className="tb-stat-unit">{unit}</span> : null}</div>
    {delta ? <div className={cx("tb-stat-delta", deltaTone && `is-${deltaTone}`)}>{delta}</div> : null}
    {spark ? <div className="tb-stat-spark" aria-hidden="true">{spark.map((v, i) => <span key={i} style={{ height: `${Math.max(8, Math.round(v / max * 100))}%` }} />)}</div> : null}
  </div>;
}
