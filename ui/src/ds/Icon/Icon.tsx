import { TofiIcon, type TofiIconName } from "../../icons";
import { cx } from "../cx";
import "./Icon.css";

export type IconName = TofiIconName;
export type IconProps = {
  name: IconName;
  /** 16, 20 or 24; 28 to 32 in empty states. */
  size?: number;
  /** Outline at rest; filled for the active, selected or emphasised state. */
  variant?: "outline" | "filled";
  /** Only when the icon carries meaning alone; otherwise it is hidden from screen readers. */
  title?: string;
  /** Icon motion (the one motion exception for the plus, V3 PLAN 8.3); off under reduced motion. */
  animated?: boolean;
  className?: string;
};

/** One icon of Tofi Rounded Icons, drawn inline so it takes the text colour. The stroke is fixed at 1.8. */
export function Icon({ name, size = 20, variant = "outline", title, animated, className }: IconProps) {
  return <TofiIcon name={name} size={size} variant={variant} title={title} animated={animated} className={cx("tb-icon", className)} />;
}
