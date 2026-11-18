import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "./Badge.css";

export type BadgeTone = "neutral" | "working" | "needs" | "done" | "error" | "unread" | "info" | "paused";
const TONE_ICON: Record<BadgeTone, IconName | null> = { working: "loading", done: "check", unread: "bell", needs: "mention", error: "error", info: "info", neutral: null, paused: "pause" };

export type BadgeProps = {
  /** The five states are neutral, working, needs, done and error. */
  tone?: BadgeTone;
  /** Overrides the mark of the tone; null for none. */
  icon?: IconName | null;
  className?: string;
  children?: ReactNode;
};

/** A status stamp: square-cornered, a mark and one or two mono words. Stamp only a state that is not the normal one. */
export function Badge({ tone = "neutral", icon, className, children }: BadgeProps) {
  const mark = icon === undefined ? TONE_ICON[tone] : icon;
  return <span className={cx("tb-badge", `tb-badge--${tone}`, className)}>
    {tone === "working" && icon === undefined ? <span className="tb-breath" aria-hidden="true" /> : mark ? <Icon name={mark} size={12} /> : null}
    <span>{children}</span>
  </span>;
}
