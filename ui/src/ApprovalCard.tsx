import { useId, type ReactNode } from "react";
import { TofiIcon } from "./icons";
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
export function ApprovalCard({ title, avatar, time, exactTime, badge = "需要你批准", facts = [], payload, acceptLabel = "批准", declineLabel = "暂不批准", onAnswer, resolution, busy, disabled, error, note, secondaryAction }: ApprovalCardProps) {
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
        {payload && <details className="approval-payload"><summary>查看完整参数</summary><pre tabIndex={0} aria-label="完整工具参数，纯文本">{payload}</pre></details>}
        <div className="approval-actions">
          <button type="button" className="approval-decline" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(false)}>{busy === "decline" ? "提交中…" : declineLabel}</button>
          <button type="button" className="approval-accept" disabled={disabled || Boolean(busy) || !pending} onClick={() => onAnswer(true)}>{busy === "accept" ? "提交中…" : acceptLabel}</button>
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
