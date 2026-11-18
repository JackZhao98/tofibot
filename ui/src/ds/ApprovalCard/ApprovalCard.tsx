import { Fragment, type ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Button } from "../Button/Button";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "./ApprovalCard.css";

export type ApprovalResolved = {
  /** The decision, as a stamp. */
  label: ReactNode;
  /** A few words of why. */
  reason?: ReactNode;
  declined?: boolean;
  /** Just decided: the stamp presses in once. */
  fresh?: boolean;
};

export type ApprovalCardProps = {
  title: string;
  /** Defaults to "Needs you". */
  eyebrow?: ReactNode;
  /** Server-checked facts as [label, value, mono]. They come from the server, not from the Bot's words. */
  facts?: Array<[string, ReactNode, boolean?]>;
  /** The Bot's note. */
  children?: ReactNode;
  approveLabel?: string;
  /** Offer Remember only for actions that are safe to repeat. */
  rememberLabel?: string;
  declineLabel?: string;
  onApprove?: () => void;
  onRemember?: () => void;
  onDecline?: () => void;
  /** After a decision: the actions give way to one stamped line. */
  resolved?: ApprovalResolved;
  className?: string;
};

/** A request that needs the person: the facts the server built in a sunk block, the Bot's words under them, then the actions.
 *  Open, it carries the ink edge and the hard shadow of what waits for you (S2); resolved, it settles to a plain note. */
export function ApprovalCard({ title, eyebrow, facts, children, approveLabel, rememberLabel, declineLabel, onApprove, onRemember, onDecline, resolved, className }: ApprovalCardProps) {
  const { t } = useTranslation();
  return <section className={cx("tb-approval", resolved && "is-resolved", className)} aria-label={title}>
    <header className="tb-approval-head">
      <span className="tb-approval-mark"><Icon name="shield-check" size={20} /></span>
      <div><div className="tb-eyebrow">{eyebrow ?? t("ds.needsYou")}</div><h3 className="tb-approval-title">{title}</h3></div>
    </header>
    {facts ? <dl className="tb-facts">{facts.map(([label, value, mono], index) => <Fragment key={index}><dt>{label}</dt><dd className={mono ? "tb-mono" : undefined}>{value}</dd></Fragment>)}</dl> : null}
    {children ? <div className="tb-approval-note">{children}</div> : null}
    {resolved
      ? <footer className={cx("tb-approval-done", resolved.declined && "is-declined", resolved.fresh && "is-fresh")}><b className="tb-stamp">{resolved.label}</b>{resolved.reason ? <span>{resolved.reason}</span> : null}</footer>
      : <footer className="tb-approval-actions">
        <Button variant="primary" icon="check" onClick={onApprove}>{approveLabel ?? t("ds.approve")}</Button>
        {rememberLabel ? <Button variant="secondary" onClick={onRemember}>{rememberLabel}</Button> : null}
        <Button variant="ghost" onClick={onDecline}>{declineLabel ?? t("ds.decline")}</Button>
      </footer>}
  </section>;
}
