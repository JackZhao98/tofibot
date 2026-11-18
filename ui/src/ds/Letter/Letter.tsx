import { Fragment, useLayoutEffect, useRef, type ReactNode } from "react";
import { animate } from "motion/mini";
import { useTranslation } from "../../i18n";
import { EASE, MOTION } from "../../motion";
import { prefersReducedMotion } from "../../motion/reduced";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./Letter.css";

export type LetterNote = { avatar?: ReactNode; title: ReactNode; text: ReactNode };
export type LetterProps = {
  /** in: a mail the Bot read for you. out: a draft waiting for your send. */
  direction?: "in" | "out";
  from?: string;
  /** The sender's initial in the round mark; defaults to the first letter of from. */
  monogram?: string;
  to?: ReactNode;
  cc?: ReactNode;
  subject?: ReactNode;
  /** Overrides the default "SUBJECT" label. */
  subjectLabel?: string;
  /** In: the postmark, one string per line (received, time, date). */
  postmark?: ReactNode | ReactNode[];
  /** Out: the Bot's cat on the stamp. */
  stamp?: ReactNode;
  sign?: ReactNode;
  /** Out: an attachment chip. */
  attachment?: ReactNode;
  /** The Bot's one-paragraph read, as a sticky note. */
  note?: LetterNote;
  /** Fold the body under a fade. The button `more` opens it. */
  folded?: boolean;
  more?: ReactNode;
  onMore?: () => void;
  actions?: ReactNode;
  hint?: ReactNode;
  /** The press that sends. Never send from the letter without it. */
  primary?: ReactNode;
  /** The accessible name of the letter. */
  label?: string;
  className?: string;
  children?: ReactNode;
};

/** The out curve of the motion tokens as the four numbers Motion takes. */
const CURVE = EASE.out.slice(13, -1).split(",").map(Number) as [number, number, number, number];
const lines = (value: ReactNode | ReactNode[]) => Array.isArray(value) ? value.map((line, i) => <Fragment key={i}>{i ? <br /> : null}{line}</Fragment>) : value;

/** An email as a real letter: airmail edge, FROM/TO head, postmark or stamp, the Bot's note, a ruled body and the actions.
 *  The paper stays light in both themes (skin scope letter). */
export function Letter({ direction = "in", from, monogram, to, cc, subject, subjectLabel, postmark, stamp, sign, attachment, note, folded, more, onMore, actions, hint, primary, label, className, children }: LetterProps) {
  const { t } = useTranslation();
  const inbound = direction !== "out";
  const subjectKey = subjectLabel ?? t("ds.letter.subject");
  const head: [string, ReactNode][] = inbound
    ? [[t("ds.letter.from"), <Fragment key="f"><span className="tb-letter-mono" aria-hidden="true">{monogram || String(from || "?").charAt(0)}</span>{from}</Fragment>], [t("ds.letter.to"), to], [subjectKey, subject]]
    : [[t("ds.letter.to"), to], ...(cc ? [[t("ds.letter.cc"), cc] as [string, ReactNode]] : []), [subjectKey, subject]];

  // When `folded` flips, the body grows or shrinks from its old height to its new one (Motion, base token).
  const body = useRef<HTMLDivElement>(null);
  const last = useRef<{ folded: boolean | undefined; height: number } | null>(null);
  useLayoutEffect(() => {
    const el = body.current;
    if (!el) return;
    const height = el.getBoundingClientRect().height;
    const before = last.current;
    last.current = { folded, height };
    if (!before || before.folded === folded || before.height === height || prefersReducedMotion()) return;
    const run = animate(el, { height: [`${before.height}px`, `${height}px`] }, { duration: MOTION.base / 1000, ease: CURVE });
    run.finished.then(() => { el.style.height = ""; }, () => { el.style.height = ""; });
    return () => run.stop();
  }, [folded]);

  return <article className={cx("tb-letter", inbound ? "is-in" : "is-out", className)} aria-label={label}>
    <header className="tb-letter-head">
      {head.map(([key, value], i) => <Fragment key={i}><span className="tb-letter-k">{key}</span><b>{value}</b></Fragment>)}
      {inbound ? (postmark ? <span className="tb-letter-postmark" aria-hidden="true">{lines(postmark)}</span> : null)
        : (stamp ? <span className="tb-letter-stamp" aria-hidden="true">{stamp}</span> : null)}
    </header>
    {note ? <div className="tb-letter-note">{note.avatar ? <span className="tb-letter-note-av">{note.avatar}</span> : null}<b>{note.title}</b>{note.text}</div> : null}
    <div ref={body} className={cx("tb-letter-body", folded && "is-folded")}>{children}</div>
    {more ? <button type="button" className="tb-letter-more" aria-expanded={folded === undefined ? undefined : !folded} onClick={onMore}>{more}</button> : null}
    {sign ? <div className="tb-letter-sign">{sign}</div> : null}
    {attachment ? <div className="tb-letter-att"><Icon name="attachment" size={13} />{attachment}</div> : null}
    {actions || hint || primary ? <footer className="tb-letter-foot">{actions}{hint ? <span className="tb-letter-hint">{hint}</span> : null}{primary ? <span className="tb-letter-primary">{primary}</span> : null}</footer> : null}
  </article>;
}
