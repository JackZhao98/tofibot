import type { ReactNode } from "react";
import { Icon, type IconName } from "../ds/Icon/Icon";
import "../ds/Button/Button.css";

/** A link drawn as a kit secondary Button (the kit Button is a button element; these go to a page). */
export function LinkButton({ href, icon, className, target, rel, children }: { href: string; icon?: IconName; className?: string; target?: string; rel?: string; children: ReactNode }) {
  return <a className={`tb-btn tb-btn--secondary tb-btn--md${className ? ` ${className}` : ""}`} href={href} target={target} rel={rel}>{icon && <Icon name={icon} size={18} />}<span className="tb-btn-label">{children}</span></a>;
}
