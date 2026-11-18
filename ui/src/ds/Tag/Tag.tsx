import type { MouseEventHandler, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Tag.css";

export type TagProps = {
  icon?: IconName;
  /** Shows the remove button. */
  onRemove?: MouseEventHandler<HTMLButtonElement>;
  /** The remove button's name; defaults to "Remove". */
  removeLabel?: string;
  className?: string;
  children?: ReactNode;
};

/** A small square-cornered label for scopes, filters and attributes. Not for status: use Badge. */
export function Tag({ icon, onRemove, removeLabel, className, children }: TagProps) {
  const { t } = useTranslation();
  return <span className={cx("tb-tag", className)}>
    {icon && <Icon name={icon} size={14} />}
    <span>{children}</span>
    {onRemove && <button type="button" className="tb-tag-x" aria-label={removeLabel ?? t("ds.remove")} onClick={onRemove}><Icon name="close" size={12} /></button>}
  </span>;
}
