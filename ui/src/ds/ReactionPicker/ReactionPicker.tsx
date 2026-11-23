import { useTranslation } from "../../i18n";
import { cx } from "../cx";
import "../shared.css";
import "../pop.css";
import "./ReactionPicker.css";

/** The product's eight common reactions. There is no "more": a reaction outside the eight still shows as a chip and can be toggled off. */
export const REACTION_EMOJI = ["👍", "❤️", "😂", "🎉", "👀", "🙌", "🤔", "🐱"] as const;

export type ReactionPickerProps = {
  /** Defaults to the eight. */
  emoji?: readonly string[];
  /** The ones you already reacted with: pressed, tinted. */
  selected?: readonly string[];
  /** Emoji that cannot be picked right now (a request for it is still in flight). */
  disabled?: readonly string[];
  onPick?: (emoji: string) => void;
  /** The group's name; defaults to "Common reactions". */
  label?: string;
  /** Draw no surface of its own: inside a popover that already has one. */
  bare?: boolean;
  className?: string;
};

/** The reaction menu: the eight common emoji in one row. It floats above the bubble from the ☺ in the message actions, and above the menu on a long press. */
export function ReactionPicker({ emoji = REACTION_EMOJI, selected = [], disabled = [], onPick, label, bare, className }: ReactionPickerProps) {
  const { t } = useTranslation();
  return <div className={cx("tb-reactpick", !bare && "tb-pop tb-glass", className)} role="group" aria-label={label ?? t("ds.reactionPickerLabel")}>
    {emoji.map(item => <button key={item} type="button" className="tb-reactpick-item" aria-pressed={selected.includes(item)} disabled={disabled.includes(item)} aria-label={t("ds.reactionPickerReact", { emoji: item })} onClick={() => onPick?.(item)}>{item}</button>)}
  </div>;
}
