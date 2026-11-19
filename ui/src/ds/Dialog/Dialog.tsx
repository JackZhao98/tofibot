import { useId, type HTMLAttributes, type ReactNode, type Ref } from "react";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { IconButton } from "../IconButton/IconButton";
import { cx } from "../cx";
import "../shared.css";
import "./Dialog.css";

/** The scrim class for the product: the whole window, behind the dialog, above everything but toasts and hints. Pass it to
 *  ModalDialog as `overlayClassName`. */
export const DIALOG_SCRIM = "tb-scrim tb-scrim--modal";

export type DialogProps = Omit<HTMLAttributes<HTMLDivElement>, "title"> & {
  /** A question or a plain statement of the action, naming the target. */
  title: ReactNode;
  /** danger carries the red mark and is always `role="alertdialog"`. A neutral dialog that must be answered passes `role="alertdialog"`. */
  tone?: "neutral" | "danger";
  /** The buttons, confirm last. */
  actions?: ReactNode;
  /** Draws a close button in the head. */
  onClose?: () => void;
  /** The close button's name; defaults to "Close". */
  closeLabel?: string;
  /** Work in flight: the dialog says so to assistive technology. */
  busy?: boolean;
  /** Without it the dialog is the bare panel; ModalDialog supplies the scrim in the product. */
  scrim?: boolean;
  ref?: Ref<HTMLDivElement>;
  children?: ReactNode;
};

/** A modal dialog on frosted glass with the largest soft shadow. Name the target and what is lost in the body; do not use
 *  a dialog for information only (use a Banner or Toast). In the product it is the child of `ModalDialog`
 *  (src/overlays/ModalDialog.tsx), which adds the scrim, the focus rules and the `data-state` motion. */
export function Dialog({ title, tone = "neutral", actions, onClose, closeLabel, busy, scrim = false, className, children, ref, ...rest }: DialogProps) {
  const { t } = useTranslation();
  const titleId = useId(), bodyId = useId();
  // The Dialog names itself from its title and describes itself from its body. Radix Dialog.Content passes ids of a Title and a
  // Description we do not render, so what arrives in `rest` is replaced here.
  const panel = <div {...rest} ref={ref} className={cx("tb-dialog", "tb-glass", `tb-dialog--${tone}`, className)} role={tone === "danger" ? "alertdialog" : rest.role ?? "dialog"}
    aria-labelledby={rest["aria-label"] === undefined ? titleId : undefined} aria-describedby={children ? bodyId : undefined} aria-busy={busy || undefined}>
    <header className="tb-dialog-head">
      {tone === "danger" ? <span className="tb-dialog-mark"><Icon name="alert" size={20} /></span> : null}
      <h2 id={titleId} className="tb-dialog-title">{title}</h2>
      {onClose ? <IconButton icon="close" label={closeLabel ?? t("ds.close")} size="sm" onClick={onClose} /> : null}
    </header>
    {children ? <div id={bodyId} className="tb-dialog-body">{children}</div> : null}
    {actions ? <footer className="tb-dialog-foot">{actions}</footer> : null}
  </div>;
  return scrim ? <div className="tb-scrim">{panel}</div> : panel;
}
