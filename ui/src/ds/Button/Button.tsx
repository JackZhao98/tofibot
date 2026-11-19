import type { ButtonHTMLAttributes, ReactNode, Ref } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Button.css";

export type ButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  /** primary: the one main action of a view. danger: destructive, always naming the target. */
  variant?: "primary" | "secondary" | "ghost" | "danger";
  /** md is 40px, sm is 32px. */
  size?: "md" | "sm";
  icon?: IconName;
  iconAfter?: IconName;
  busy?: boolean;
  /** The button element, for a caller that moves focus to it. */
  ref?: Ref<HTMLButtonElement>;
  children?: ReactNode;
};

/** A short verb label that names the result. Keep one primary per view. */
export function Button({ variant = "secondary", size = "md", icon, iconAfter, busy, children, className, type = "button", ...rest }: ButtonProps) {
  const iconSize = size === "sm" ? 16 : 18;
  return <button {...rest} type={type} className={cx("tb-btn", `tb-btn--${variant}`, `tb-btn--${size}`, busy && "is-busy", className)} aria-busy={busy ? true : undefined}>
    {icon && <Icon name={icon} size={iconSize} />}
    {children != null && <span className="tb-btn-label">{children}</span>}
    {iconAfter && <Icon name={iconAfter} size={iconSize} />}
  </button>;
}
