import type { ButtonHTMLAttributes, ReactNode, Ref } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./IconButton.css";

export type IconButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-label"> & {
  icon: IconName;
  /** The accessible name and the hover hint. Required. */
  label: string;
  variant?: "ghost" | "solid";
  size?: "md" | "sm";
  /** Draw the filled icon. */
  filled?: boolean;
  /** Icon motion on press (the plus of the sidebar). */
  animated?: boolean;
  /** The button element, for a caller that anchors a menu to it. */
  ref?: Ref<HTMLButtonElement>;
};

/** A square button that holds one icon. Not for the main action of a screen. */
export function IconButton({ icon, label, variant = "ghost", size = "md", filled, animated, className, type = "button", children, ...rest }: IconButtonProps) {
  return <button {...rest} type={type} className={cx("tb-iconbtn", `tb-iconbtn--${variant}`, size === "sm" && "tb-iconbtn--sm", className)} aria-label={label} data-hint={label}>
    <Icon name={icon} size={size === "sm" ? 16 : 20} variant={filled ? "filled" : "outline"} animated={animated} />{children}
  </button>;
}
