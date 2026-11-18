import type { HTMLAttributes, ReactNode } from "react";
import { cx } from "../cx";
import "../shared.css";
import "./Card.css";

export type CardProps = Omit<HTMLAttributes<HTMLElement>, "title"> & {
  /** solid and glass are notes in the final V3.0 cascade (bundle.css:772); raised is the one thing that waits for you. */
  variant?: "solid" | "raised" | "glass" | "quiet" | "block";
  title?: ReactNode;
  eyebrow?: ReactNode;
  actions?: ReactNode;
  footer?: ReactNode;
  /** Pad the body (default). */
  pad?: boolean;
};

/** The basic container: a sticky note with a faint glue band. Do not nest a raised card in a raised card. */
export function Card({ variant = "solid", title, eyebrow, actions, footer, pad = true, className, children, ...rest }: CardProps) {
  return <section {...rest} className={cx("tb-card", `tb-card--${variant}`, !pad && "tb-card--flush", className)}>
    {(title || eyebrow || actions) && <header className="tb-card-head">
      <div>{eyebrow ? <div className="tb-eyebrow">{eyebrow}</div> : null}{title ? <h3 className="tb-card-title">{title}</h3> : null}</div>
      {actions ? <div className="tb-card-actions">{actions}</div> : null}
    </header>}
    <div className="tb-card-body">{children}</div>
    {footer ? <footer className="tb-card-foot">{footer}</footer> : null}
  </section>;
}
