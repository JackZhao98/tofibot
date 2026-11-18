import type { MouseEventHandler } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "./JumpButton.css";

export type JumpButtonProps = {
  /** The accessible name, for example "Back to latest". The arrow is the whole visible button. */
  label: string;
  onClick?: MouseEventHandler<HTMLButtonElement>;
  className?: string;
};

/** Back to the latest message: a round glass button, centred over the message column above the composer. Show it only after the
 *  person has scrolled up. */
export function JumpButton({ label, onClick, className }: JumpButtonProps) {
  return <button type="button" className={cx("tb-jump", "tb-glass", className)} onClick={onClick} aria-label={label} title={label}>
    <Icon name="arrow-down" size={18} />
  </button>;
}
