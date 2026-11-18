import type { ReactNode } from "react";
import { cx } from "../cx";
import "./Kbd.css";

/** A key cap with a thick bottom edge. One key per Kbd. */
export function Kbd({ className, children }: { className?: string; children?: ReactNode }) {
  return <kbd className={cx("tb-kbd", className)}>{children}</kbd>;
}
