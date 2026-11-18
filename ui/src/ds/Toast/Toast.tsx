import type { MouseEventHandler, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "./Toast.css";

export type ToastProps = {
  tone?: "neutral" | "done" | "error";
  icon?: IconName;
  /** One action label, with its handler. */
  action?: string;
  onAction?: MouseEventHandler<HTMLButtonElement>;
  onClose?: MouseEventHandler<HTMLButtonElement>;
  /** The close button's name; defaults to "Dismiss". */
  closeLabel?: string;
  className?: string;
  children?: ReactNode;
};

/** A short glass notice at the edge of the screen. A plain role="alert" or "status" element, not Radix Toast (AGENTS: it
 *  would take Escape from a dialog under it). Wired in PR 2.3. */
export function Toast({ tone = "neutral", icon, action, onAction, onClose, closeLabel, className, children }: ToastProps) {
  const { t } = useTranslation();
  return <div className={cx("tb-toast", "tb-glass", `tb-toast--${tone}`, className)} role={tone === "error" ? "alert" : "status"}>
    <Icon name={icon ?? (tone === "error" ? "error" : tone === "done" ? "check-circle" : "info")} size={18} />
    <span className="tb-toast-text">{children}</span>
    {action ? <button type="button" className="tb-toast-action" onClick={onAction}>{action}</button> : null}
    {onClose ? <button type="button" className="tb-toast-x" aria-label={closeLabel ?? t("ds.dismiss")} onClick={onClose}><Icon name="close" size={14} /></button> : null}
  </div>;
}
