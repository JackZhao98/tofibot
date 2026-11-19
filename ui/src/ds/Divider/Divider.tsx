import type { ReactNode } from "react";
import { cx } from "../cx";
import "./Divider.css";

export type DividerProps = {
  /** date: a quiet centred label. unread: the tomato perforation above the first unread message. */
  tone?: "date" | "unread";
  /** The accessible name, when the visible text is not enough (the unread line). */
  label?: string;
  className?: string;
  children?: ReactNode;
};

/** A marker in the message list. Never stack two, and draw no rule beside a date. */
export function Divider({ tone = "date", label, className, children }: DividerProps) {
  return <div className={cx("tb-divider", tone === "unread" && "tb-divider--unread", className)} role="separator" aria-label={label}><span>{children}</span></div>;
}
