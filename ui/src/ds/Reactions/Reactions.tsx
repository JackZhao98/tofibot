import type { MouseEventHandler } from "react";
import { useTranslation } from "../../i18n";
import { cx } from "../cx";
import "./Reactions.css";

export type ReactionItem = {
  emoji: string;
  count: number;
  /** You reacted: the chip is tinted. */
  mine?: boolean;
  /** Who reacted, as the chip's tooltip. */
  who?: string;
  onToggle?: MouseEventHandler<HTMLButtonElement>;
};

export type ReactionsProps = {
  items: ReactionItem[];
  /** The group's name; defaults to "Reactions". */
  label?: string;
  className?: string;
};

/** Reaction chips resting on a bubble's lower edge (MessageBubble places them). Adding one goes through the message actions, so
 *  there is no add chip here. */
export function Reactions({ items, label, className }: ReactionsProps) {
  const { t } = useTranslation();
  return <div className={cx("tb-reactions", className)} role="group" aria-label={label ?? t("ds.reactionsLabel")}>
    {items.map((reaction, index) => <button key={`${reaction.emoji}${index}`} type="button" className={cx("tb-reaction", reaction.mine && "is-mine")} aria-pressed={!!reaction.mine} title={reaction.who} onClick={reaction.onToggle}>
      <span className="tb-reaction-emoji" aria-hidden="true">{reaction.emoji}</span>
      <span className="tb-reaction-count">{reaction.count}</span>
    </button>)}
  </div>;
}
