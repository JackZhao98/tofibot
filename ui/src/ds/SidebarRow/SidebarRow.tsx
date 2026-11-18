import type { AnchorHTMLAttributes, ReactNode } from "react";
import { Badge, type BadgeTone } from "../Badge/Badge";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./SidebarRow.css";

export type SidebarRowProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "children"> & {
  label: ReactNode;
  /** The last message or the time. */
  meta?: ReactNode;
  /** A lead avatar; wins over `icon`. */
  avatar?: ReactNode;
  icon?: IconName;
  active?: boolean;
  unread?: boolean;
  /** A count. Show a count or a status, never both. */
  count?: number;
  /** A Badge tone, shown as a dot-sized stamp. */
  status?: BadgeTone;
};

/** A conversation or place in the sidebar. Reads only the `--c-srow-*` roles, so the Mac material (a transparent sidebar) and the
 *  web ground both come from the skin. Dragging arrives in PR 5.2. */
export function SidebarRow({ label, meta, avatar, icon, active, unread, count, status, className, href = "#", ...rest }: SidebarRowProps) {
  return <a {...rest} href={href} className={cx("tb-srow", active && "is-active", unread && "is-unread", className)} aria-current={active ? "page" : undefined}>
    {avatar ? <span className="tb-srow-lead">{avatar}</span> : icon ? <span className="tb-srow-lead tb-srow-icon"><Icon name={icon} size={20} variant={active ? "filled" : "outline"} /></span> : null}
    <span className="tb-srow-main">
      <span className="tb-srow-label">{label}</span>
      {meta ? <span className="tb-srow-meta">{meta}</span> : null}
    </span>
    {status ? <Badge tone={status} /> : null}
    {count ? <span className="tb-count">{count}</span> : null}
  </a>;
}
