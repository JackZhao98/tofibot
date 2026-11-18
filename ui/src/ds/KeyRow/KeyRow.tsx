import type { ReactNode } from "react";
import { Badge, type BadgeTone } from "../Badge/Badge";
import { BrandMark } from "../BrandMark/BrandMark";
import { Icon, type IconName } from "../Icon/Icon";
import { Tag } from "../Tag/Tag";
import { cx } from "../cx";
import "../shared.css";
import "./KeyRow.css";

export type KeyRowProps = {
  name: ReactNode;
  /** The masked value, last four characters at most (`sk-ant-…3kQp`). Never pass the secret itself: the row has no reveal,
   * and Copy belongs only to a value the server can still read. */
  masked: string;
  /** Last use, or when it was added or revoked. */
  meta?: ReactNode;
  scope?: ReactNode;
  /** A stamp only when something is wrong (refused, expired). A key that works has none. */
  status?: { tone: BadgeTone; label: ReactNode };
  icon?: IconName;
  /** The official mark of an outside service, shown with BrandMark instead of the icon. */
  logo?: string;
  logoTile?: "dark";
  revoked?: boolean;
  /** Buttons for the row. Children fill the same slot, for markup that passes them inline. */
  actions?: ReactNode;
  className?: string;
  children?: ReactNode;
};

/** A key or secret in a list: icon or mark, name and scope, the masked value, last use, an exception stamp, actions. */
export function KeyRow({ name, masked, meta, scope, status, icon = "key", logo, logoTile, revoked, actions, className, children }: KeyRowProps) {
  const slot = actions ?? children;
  return <div className={cx("tb-keyrow", revoked && "is-revoked", className)}>
    {logo ? <BrandMark src={logo} tile={logoTile} /> : <span className="tb-keyrow-icon" aria-hidden="true"><Icon name={icon} size={18} /></span>}
    <div className="tb-keyrow-main">
      <div className="tb-keyrow-name">{name}{scope ? <Tag>{scope}</Tag> : null}</div>
      <div className="tb-keyrow-value tb-mono">{masked}</div>
    </div>
    <div className="tb-keyrow-meta">{meta}</div>
    {status ? <Badge tone={status.tone}>{status.label}</Badge> : null}
    {slot ? <div className="tb-keyrow-actions">{slot}</div> : null}
  </div>;
}
