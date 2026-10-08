import { useId, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import "./approval-card.css";

export type ApprovalResolution = { label: string; answer?: string; accepted?: boolean };
export interface ApprovalCardProps {
  title: ReactNode;
  avatar?: ReactNode;
  time?: string;
  exactTime?: string;
  badge?: string;
  facts?: { label: string; value: ReactNode }[];
  /** Exact external-tool arguments, rendered only as inert text on demand. */
  payload?: string;
  acceptLabel?: string;
  declineLabel?: string;
  onAnswer: (accepted: boolean) => void;
  resolution?: ApprovalResolution;
  busy?: "accept" | "decline" | "cancel";
  disabled?: boolean;
  error?: string;
  note?: string;
  secondaryAction?: ReactNode;
}

/** Shared visual card. Only the caller's confirmed result can resolve it. */
export function ApprovalCard({ title, avatar, time, exactTime, badge: badgeText, facts = [], payload, acceptLabel: acceptText, declineLabel: declineText, onAnswer, resolution, busy, disabled, error, note, secondaryAction }: ApprovalCardProps) {
  const { t } = useTranslation("tasks");
  const badge = badgeText ?? t("phase.needs_approval");
  const acceptLabel = acceptText ?? t("approval.accept");
  const declineLabel = declineText ?? t("approval.decline");
  const heading = useId();
  const pending = !resolution;
  return <section className={`approval-card ${pending ? "is-pending" : "is-resolved"}${resolution?.accepted === false ? " is-declined" : ""}`} aria-labelledby={heading} aria-busy={Boolean(busy)}>
    <div className="approval-fold approval-heading-fold" aria-hidden={!pending} inert={!pending}>
      <div className="approval-fold-inner"><header className="approval-head">
        {avatar && <span className="approval-avatar">{avatar}</span>}
        <span className="approval-badge">{badge}</span>
        {time && <time title={exactTime}>{time}</time>}
      </header></div>
    </div>
    <div id={heading} className="approval-title">{title}</div>
    <div className="approval-fold approval-content-fold" aria-hidden={!pending} inert={!pending}>
      <div className="approval-fold-inner">
        {facts.length > 0 && <dl className="approval-facts">{facts.map((fact, index) => <div key={index}><dt>{fact.label}</dt><dd>{fact.value}</dd></div>)}</dl>}
        {payload && <details className="approval-payload"><summary>{t("record.view_full_arguments")}</summary><pre tabIndex={0} aria-label={t("approval.payload_label")}>{payload}</pre></details>}
        <div className="approval-actions">
          <button type="button" className="approval-decline" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(false)}>{busy === "decline" ? t("approval.submitting") : declineLabel}</button>
          <button type="button" className="approval-accept" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(true)}>{busy === "accept" ? t("approval.submitting") : acceptLabel}</button>
          {secondaryAction && <span className="approval-secondary">{secondaryAction}</span>}
        </div>
        {note && <p className="approval-note">{note}</p>}
      </div>
    </div>
    {resolution && <div className="approval-result">
      <TofiIcon name={resolution.accepted ? "check" : "close"} size={16} aria-hidden="true" />
      <span>{resolution.label}</span>{resolution.answer && <strong>{resolution.answer}</strong>}
    </div>}
    {error && <p className="approval-error" role="alert">{error}</p>}
  </section>;
}
