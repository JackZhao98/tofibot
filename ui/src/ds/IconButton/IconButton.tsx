import type { ButtonHTMLAttributes } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./IconButton.css";

export type IconButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children" | "aria-label"> & {
  icon: IconName;
  /** The accessible name and the hover hint. Required. */
  label: string;
  variant?: "ghost" | "solid";
  size?: "md" | "sm";
  /** Draw the filled icon. */
  filled?: boolean;
};

/** A square button that holds one icon. Not for the main action of a screen. */
export function IconButton({ icon, label, variant = "ghost", size = "md", filled, className, type = "button", ...rest }: IconButtonProps) {
  return <button {...rest} type={type} className={cx("tb-iconbtn", `tb-iconbtn--${variant}`, size === "sm" && "tb-iconbtn--sm", className)} aria-label={label} data-hint={label}>
    <Icon name={icon} size={size === "sm" ? 16 : 20} variant={filled ? "filled" : "outline"} />
  </button>;
}
