import type { CSSProperties, ReactNode } from "react";
import { cx } from "../cx";
import "./Hint.css";

export type HintProps = {
  tone?: "quiet";
  style?: CSSProperties;
  className?: string;
  children?: ReactNode;
};

/** A one-line name for an icon-only control, on the tooltip tokens. The product draws it from `data-hint` (ActionHints in
 *  src/InteractionSystem.tsx), which positions it; this is the element it renders. `quiet` is the neutral chip for a longer
 *  line such as a group's read-by names. The tooltip is never Radix Tooltip (AGENTS). */
export function Hint({ tone, style, className, children }: HintProps) {
  return <div className={cx("tb-hint", tone && `is-${tone}`, className)} role="tooltip" style={style}>{children}</div>;
}
