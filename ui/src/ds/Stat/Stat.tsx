import type { CSSProperties, ReactNode } from "react";
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
  /** reel: each digit of a text value is a column that rolls to its face once `rolled` is true (the Usage numbers; the owner kept this motion). */
  variant?: "plain" | "reel";
  /** reel only: false holds the digits at zero, true rolls them. Set it when the number scrolls into view. */
  rolled?: boolean;
  className?: string;
};

const FACES = Array.from("01234567890123456789");

/** The digits of a number as columns on a reel. The whole text is the accessible name; the columns are decoration. */
function Reel({ text, rolled }: { text: string; rolled: boolean }) {
  return <span className={cx("tb-reel", rolled && "is-rolled")} aria-label={text}>{Array.from(text).map((char, index) => /\d/.test(char)
    ? <span className="tb-reel-col" aria-hidden="true" key={index}><span className="tb-reel-strip" style={{ "--d": rolled ? Number(char) + 10 : 0, "--delay": `${(text.length - index) * 45}ms` } as CSSProperties}>{FACES.map((face, at) => <span key={at}>{face}</span>)}</span></span>
    : <span aria-hidden="true" key={index}>{char}</span>)}</span>;
}

/** One number with its label, a delta and an optional spark of bars. Four in a row at most. */
export function Stat({ label, value, unit, delta, deltaTone, spark, variant = "plain", rolled = true, className }: StatProps) {
  const max = spark ? Math.max(...spark) || 1 : 1;
  const reel = variant === "reel" && typeof value === "string";
  return <div className={cx("tb-stat", reel && "tb-stat--reel", className)}>
    <div className="tb-eyebrow">{label}</div>
    <div className="tb-stat-value">{reel ? <Reel text={value as string} rolled={rolled} /> : value}{unit ? <span className="tb-stat-unit">{unit}</span> : null}</div>
    {delta ? <div className={cx("tb-stat-delta", deltaTone && `is-${deltaTone}`)}>{delta}</div> : null}
    {spark ? <div className="tb-stat-spark" aria-hidden="true">{spark.map((v, i) => <span key={i} style={{ height: `${Math.max(8, Math.round(v / max * 100))}%` }} />)}</div> : null}
  </div>;
}
