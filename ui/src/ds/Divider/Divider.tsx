import type { ReactNode } from "react";
import { cx } from "../cx";
import "./Divider.css";

export type DividerProps = {
  /** date: a quiet centred label. unread: the tomato perforation above the first unread message. */
  tone?: "date" | "unread";
  className?: string;
  children?: ReactNode;
};

/** A marker in the message list. Never stack two, and draw no rule beside a date. */
export function Divider({ tone = "date", className, children }: DividerProps) {
  return <div className={cx("tb-divider", tone === "unread" && "tb-divider--unread", className)} role="separator"><span>{children}</span></div>;
}
