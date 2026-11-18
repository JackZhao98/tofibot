import { cx } from "../cx";
import "./Mention.css";

/** A Bot's hue, or `you` for the person (blue is theirs alone). */
export type MentionTone = "mustard" | "tomato" | "teal" | "grape" | "lime" | "you";

export type MentionProps = {
  /** The name, without the @. */
  name: string;
  tone?: MentionTone;
  /** The message has just arrived: the highlighter swipes across once. Never on history load. */
  fresh?: boolean;
  className?: string;
};

/** An @mention inside message text: a highlighter pass over the lower part of the line, in the named party's colour. No box. */
export function Mention({ name, tone = "mustard", fresh, className }: MentionProps) {
  return <span className={cx("tb-mention", `tb-hl--${tone}`, fresh && "is-fresh", className)}>@{name}</span>;
}
