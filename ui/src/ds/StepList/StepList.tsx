import type { MouseEventHandler, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { cx } from "../cx";
import "../bot-context.css";
import "./StepList.css";

/** skipped, blocked and stopped steps are `failed` too, with the reason in `error`. */
export type StepState = "pending" | "active" | "waiting" | "done" | "failed";

export type StepError = {
  /** The reason in product words. Provider text goes only under `details`. */
  reason: ReactNode;
  onRetry?: MouseEventHandler<HTMLButtonElement>;
  retryLabel?: string;
  details?: string;
  detailsLabel?: string;
};

export type Step = {
  text: ReactNode;
  state?: StepState;
  /** Up to three pictures. */
  thumbs?: string[];
  error?: StepError;
  /** The step has just finished: the stamp draws once. Never on history load. */
  fresh?: boolean;
};

export type StepListProps = {
  /** The Bot's hue: the tape, the date stamp and the current step take it. */
  botColor?: "tomato" | "mustard" | "teal" | "grape" | "lime";
  title: ReactNode;
  steps: Step[];
  /** The date stamp before the title, like `11.17 Mon`. */
  stamp?: string;
  /** Show the head only. */
  folded?: boolean;
  className?: string;
};

/** The plan a Bot keeps while it works, as a bullet-journal page (S20): washi tape in the Bot's colour, a tilted date stamp, the
 *  count on the right, and one signifier per step. Done is the B stamp tick, failed the stamp cross with its reason and a Retry link,
 *  current a pulsing dot under a highlighter band, to-do a small dot. No checkboxes and no strike-through. The same card shows a
 *  one-step failure when a run had no plan. */
export function StepList({ botColor, title, steps, stamp, folded, className }: StepListProps) {
  const { t } = useTranslation();
  const done = steps.filter(step => step.state === "done").length;
  const waiting = steps.some(step => step.state === "waiting");
  return <section className={cx("tb-plan", botColor && `tb-bot--${botColor}`, waiting && "is-waiting", folded && "is-folded", className)} aria-label={typeof title === "string" ? title : undefined}>
    <span className="tb-plan-tape" aria-hidden="true" />
    <header className="tb-plan-head">
      {stamp ? <span className="tb-plan-stamp">{stamp}</span> : null}
      <span className="tb-plan-title">{title}</span>
      <span className="tb-plan-count">{done} / {steps.length}</span>
    </header>
    {folded ? null : <ol className="tb-plan-list">
      {steps.map((step, index) => {
        const state = step.state ?? "pending";
        const error = state === "failed" ? step.error : undefined;
        return <li key={index} className={cx("tb-plan-step", `is-${state}`, step.fresh && "is-fresh")}>
          <span className="tb-plan-sig" role="img" aria-label={t(`ds.step.${state}`)}><i /></span>
          <span className="tb-plan-body">
            <span className="tb-plan-text">{step.text}</span>
            {error ? <span className="tb-plan-err">
              <span>{error.reason}</span>
              {error.onRetry ? <button type="button" className="tb-plan-retry" onClick={error.onRetry}>{error.retryLabel ?? t("ds.retry")}</button> : null}
              {error.details ? <details className="tb-plan-details"><summary>{error.detailsLabel ?? t("ds.details")}</summary><pre>{error.details}</pre></details> : null}
            </span> : null}
          </span>
          {step.thumbs && step.thumbs.length ? <span className="tb-plan-thumbs">{step.thumbs.slice(0, 3).map((src, i) => <img key={i} src={src} alt="" />)}</span> : null}
        </li>;
      })}
    </ol>}
  </section>;
}
