import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Badge } from "../Badge/Badge";
import { cx } from "../cx";
import "./DraftCard.css";

export type DraftField = [label: string, value: string, changed?: boolean];
export type DraftCardProps = {
  state?: "pending" | "sent" | "canceled";
  /** A stamp before the title ("Draft", "Needs you"). */
  kind?: ReactNode;
  title?: ReactNode;
  /** Where it goes, named before the press. */
  dest?: ReactNode;
  /** A form: label, value and whether the Bot changed it. A message passes its body as children. */
  fields?: DraftField[];
  /** Pending foot. */
  actions?: ReactNode;
  hint?: ReactNode;
  primary?: ReactNode;
  /** After the send or the cancel: the one receipt line and what follows it (View, Restore). */
  receipt?: ReactNode;
  after?: ReactNode;
  sentLabel?: string;
  canceledLabel?: string;
  changedLabel?: string;
  label?: string;
  className?: string;
  children?: ReactNode;
};

/** Something the Bot wrote for you to send, waiting for your press. Once sent or canceled it shrinks to two lines and one stamped receipt. */
export function DraftCard({ state = "pending", kind, title, dest, fields, actions, hint, primary, receipt, after, sentLabel, canceledLabel, changedLabel, label, className, children }: DraftCardProps) {
  const { t } = useTranslation();
  const form = !!fields;
  // The stamp lands (one overshoot) only when the state changes while the card is on screen.
  const before = useRef(state);
  const [fresh, setFresh] = useState(false);
  useEffect(() => {
    if (before.current !== state) { before.current = state; setFresh(true); }
  }, [state]);
  return <section className={cx("tb-draft", `is-${state}`, form && "tb-draft--form", className)} aria-label={label}>
    {state === "pending" && (kind || title) ? <header className="tb-draft-head">
      {kind ? <Badge tone="needs">{kind}</Badge> : null}
      {title ? <b className="tb-draft-title">{title}</b> : null}
    </header> : null}
    {state === "pending" && dest ? <div className="tb-draft-dest">{dest}</div> : null}
    {form && state === "pending" ? <dl className="tb-draft-fields">
      {fields.map(([name, value, changed], i) => <div key={i} className={changed ? "is-changed" : undefined}>
        <dt>{name}{changed ? <i>{changedLabel ?? t("ds.draft.changed")}</i> : null}</dt><dd><span>{value}</span></dd>
      </div>)}
    </dl> : null}
    {children ? <div className="tb-draft-body">{children}</div> : null}
    <footer className="tb-draft-foot">
      {state === "sent" ? <b className={cx("tb-stamp", fresh && "is-fresh")}>{sentLabel ?? t("ds.draft.sent")}</b>
        : state === "canceled" ? <b className={cx("tb-stamp", "tb-stamp--quiet", fresh && "is-fresh")}>{canceledLabel ?? t("ds.draft.canceled")}</b> : null}
      {state === "pending" ? actions : null}
      {(state === "pending" ? hint : receipt) ? <span className="tb-draft-hint">{state === "pending" ? hint : receipt}</span> : null}
      <span className="tb-draft-end">{state === "pending" ? primary : after}</span>
    </footer>
  </section>;
}
