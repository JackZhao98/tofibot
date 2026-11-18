import type { MouseEventHandler, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./MemberChip.css";

export type MemberChipProps = {
  name: string;
  /** An Avatar or a BotAvatar. */
  avatar?: ReactNode;
  /** A word on the chip (Owner, Admin). Change a role from the member's menu, not here. */
  role?: string;
  /** Shows the remove button. */
  onRemove?: MouseEventHandler<HTMLButtonElement>;
  /** The remove button's name; defaults to "Remove <name>". */
  removeLabel?: string;
  className?: string;
};

/** A member as an edged chip: avatar, name, an optional role word and a remove button. Put chips in a `tb-members` wrap. */
export function MemberChip({ name, avatar, role, onRemove, removeLabel, className }: MemberChipProps) {
  const { t } = useTranslation();
  const label = removeLabel ?? t("ds.removeNamed", { name });
  return <span className={cx("tb-member", className)}>
    {avatar}
    <span className="tb-member-name">{name}</span>
    {role ? <span className="tb-member-role">{role}</span> : null}
    {onRemove && <button type="button" className="tb-member-x" aria-label={label} title={label} onClick={onRemove}><Icon name="close" size={12} /></button>}
  </span>;
}
