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
  facts?: { label: string; value: ReactNode }[];
  /** Facts the server built from the exact payload; shown above everything the Bot wrote. */
  verified?: { label: string; value: ReactNode }[];
  verifiedTitle?: string;
  /** Names the Bot-written rows in `facts` when `verified` is shown. */
  factsCaption?: string;
  /** Exact external-tool arguments, rendered only as inert text on demand. */
  payload?: string;
  acceptLabel?: string;
  declineLabel?: string;
  onAnswer: (accepted: boolean) => void;
  /** "Approve and remember in this chat": offered only when the caller says a memory could later decide. */
  onRemember?: () => void;
  rememberHint?: string;
  /** "Always allow": offered only for a write-tier call a standing allowance could later decide. */
  onAlwaysAllow?: () => void;
  alwaysAllowHint?: string;
  /** "Manage ›": the link from this card to where the allowance lives (the tool's row in Connections, or Permissions once an allowance decided). */
  manage?: { label: string; onClick: () => void };
  resolution?: ApprovalResolution;
  busy?: "accept" | "decline" | "cancel" | "remember" | "always";
  disabled?: boolean;
  error?: string;
  note?: string;
  secondaryAction?: ReactNode;
}

/** Shared visual card. Only the caller's confirmed result can resolve it. */
export function ApprovalCard({ title, avatar, time, exactTime, facts = [], verified = [], verifiedTitle, factsCaption, payload, acceptLabel: acceptText, declineLabel: declineText, onAnswer, onRemember, rememberHint, onAlwaysAllow, alwaysAllowHint, manage, resolution, busy, disabled, error, note, secondaryAction }: ApprovalCardProps) {
  const { t } = useTranslation("tasks");
  const acceptLabel = acceptText ?? t("approval.accept");
  const declineLabel = declineText ?? t("approval.decline");
  const heading = useId();
  const pending = !resolution;
  return <section className={`approval-card ${pending ? "is-pending" : "is-resolved"}${resolution?.accepted === false ? " is-declined" : ""}`} aria-labelledby={heading} aria-busy={Boolean(busy)}>
    <div className="approval-fold approval-heading-fold" aria-hidden={!pending} inert={!pending}>
      <div className="approval-fold-inner">{(avatar || time) && <header className="approval-head">
        {avatar && <span className="approval-avatar">{avatar}</span>}
        {time && <time title={exactTime}>{time}</time>}
      </header>}</div>
    </div>
    <div id={heading} className="approval-title">{title}</div>
    <div className="approval-fold approval-content-fold" aria-hidden={!pending} inert={!pending}>
      <div className="approval-fold-inner">
        {verified.length > 0 && <section className="approval-verified" aria-label={verifiedTitle} data-testid="approval-verified"><h4>{verifiedTitle}</h4><dl>{verified.map((fact, index) => <div key={index}><dt>{fact.label}</dt><dd>{fact.value}</dd></div>)}</dl></section>}
        {verified.length > 0 && facts.length > 0 && factsCaption && <p className="approval-facts-caption">{factsCaption}</p>}
        {facts.length > 0 && <dl className={`approval-facts${verified.length > 0 ? " is-secondary" : ""}`}>{facts.map((fact, index) => <div key={index}><dt>{fact.label}</dt><dd>{fact.value}</dd></div>)}</dl>}
        {payload && <details className="approval-payload"><summary>{t("record.view_full_arguments")}</summary><pre tabIndex={0} aria-label={t("approval.payload_label")}>{payload}</pre></details>}
        <div className="approval-actions">
          <button type="button" className="approval-decline" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(false)}>{busy === "decline" ? t("approval.submitting") : declineLabel}</button>
          <button type="button" className="approval-accept" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(true)}>{busy === "accept" ? t("approval.submitting") : acceptLabel}</button>
          {onAlwaysAllow && <button type="button" className="approval-always" disabled={disabled || Boolean(busy) || !pending} onClick={onAlwaysAllow}>{busy === "always" ? t("approval.submitting") : t("approval.always_allow")}</button>}
          {secondaryAction && <span className="approval-secondary">{secondaryAction}</span>}
        </div>
        {onRemember && <div className="approval-remember">
          <button type="button" className="approval-remember-button" disabled={disabled || Boolean(busy) || !pending} onClick={onRemember}>{busy === "remember" ? t("approval.submitting") : t("approval.remember")}</button>
          {rememberHint && <span className="approval-remember-hint">{rememberHint}</span>}
        </div>}
        {onAlwaysAllow && alwaysAllowHint && <p className="approval-remember-hint approval-always-hint">{alwaysAllowHint} {manage && <button type="button" className="text-button approval-manage" aria-label={manage.label} onClick={manage.onClick}>{t("approval.manage")}<TofiIcon name="chevron-right" size={12} aria-hidden="true" /></button>}</p>}
        {note && <p className="approval-note">{note}</p>}
      </div>
    </div>
    {resolution && <div className="approval-result">
      <TofiIcon name={resolution.accepted ? "check" : "close"} size={16} aria-hidden="true" />
      <span>{resolution.label}</span>{resolution.answer && <strong>{resolution.answer}</strong>}
      {manage && !onAlwaysAllow && <button type="button" className="text-button approval-manage" aria-label={manage.label} onClick={manage.onClick}>{t("approval.manage")}<TofiIcon name="chevron-right" size={12} aria-hidden="true" /></button>}
    </div>}
    {error && <p className="approval-error" role="alert">{error}</p>}
  </section>;
}
