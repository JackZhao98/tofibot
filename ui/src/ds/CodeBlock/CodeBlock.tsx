import { useEffect, useRef, useState } from "react";
import * as Collapsible from "@radix-ui/react-collapsible";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import { highlightLine } from "./highlight";
import "../fold.css";
import "./CodeBlock.css";

export type CodeBlockProps = {
  /** The code, one string per line; or `code` as one string. */
  lines?: string[];
  code?: string;
  filename?: string;
  lang?: string;
  /** Long code folds after this many lines (default 16). */
  maxLines?: number;
  /** Start unfolded. */
  expanded?: boolean;
  /** Called on copy. Without it the block writes the code to the clipboard itself. */
  onCopy?: () => void;
  /** Controls the tick; without it the block shows it for a moment after a press. */
  copied?: boolean;
  onDownload?: () => void;
  /** Pins the hover state (download shown), for previews. */
  showActions?: boolean;
  copyLabel?: string;
  copiedLabel?: string;
  downloadLabel?: string;
  /** Pass already translated, with the number as {{count}}. Defaults come from i18n. */
  linesLabel?: string;
  moreLabel?: string;
  lessLabel?: string;
  className?: string;
  /** Names the code region: it takes focus so the keyboard can scroll long lines sideways. */
  regionLabel?: string;
};

const fill = (text: string, n: number) => text.replace("{{count}}", String(n));
const COPIED_MS = 1600;

function Line({ text, n }: { text: string; n: number }) {
  return <span className="tb-code-line"><i aria-hidden="true">{n}</i><code>{highlightLine(text).map((token, i) => token.kind ? <span key={i} className={`tb-syn-${token.kind}`}>{token.text}</span> : token.text)}</code></span>;
}

/** Code a Bot sent, on the terminal plate: a head, copy that turns into a tick, line numbers and the palette's five syntax colours.
 *  Long lines scroll sideways; they never wrap. */
export function CodeBlock({ lines, code, filename, lang, maxLines = 16, expanded, onCopy, copied, onDownload, showActions, copyLabel, copiedLabel, downloadLabel, linesLabel, moreLabel, lessLabel, className, regionLabel }: CodeBlockProps) {
  const { t } = useTranslation();
  const all = lines ?? String(code ?? "").split("\n");
  const folded = all.length > maxLines;
  const [open, setOpen] = useState(!!expanded);
  const [flash, setFlash] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const done = copied ?? flash;
  const press = () => {
    if (onCopy) onCopy(); else void navigator.clipboard?.writeText(all.join("\n")).catch(() => undefined);
    setFlash(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setFlash(false), COPIED_MS);
  };
  const head = folded ? all.slice(0, maxLines) : all;
  const rest = folded ? all.slice(maxLines) : [];
  const toggleLabel = open ? (lessLabel ?? t("ds.code.less")) : fill(moreLabel ?? t("ds.code.more", { count: rest.length }), rest.length);
  const body = <>
    <figcaption className="tb-code-head">
      {filename ? <b>{filename}</b> : null}{lang ? <span>{lang}</span> : null}<span>{linesLabel ? fill(linesLabel, all.length) : t("ds.code.lines", { count: all.length })}</span>
      <span className="tb-code-acts">
        {onDownload ? <button type="button" className="tb-code-act tb-code-dl" onClick={onDownload}><Icon name="download" size={13} />{downloadLabel ?? t("ds.file.download")}</button> : null}
        <button type="button" className={cx("tb-code-act", done && "is-done")} onClick={press}><Icon name={done ? "check" : "copy"} size={13} />{done ? (copiedLabel ?? t("ds.code.copied")) : (copyLabel ?? t("ds.code.copy"))}</button>
      </span>
    </figcaption>
    <pre className="tb-code-body" tabIndex={regionLabel ? 0 : undefined} role={regionLabel ? "region" : undefined} aria-label={regionLabel}>
      {head.map((text, i) => <Line key={i} text={text} n={i + 1} />)}
      {folded ? <Collapsible.Content asChild><span className="tb-fold"><span className="tb-code-rest">{rest.map((text, i) => <Line key={i} text={text} n={maxLines + i + 1} />)}</span></span></Collapsible.Content> : null}
    </pre>
    {folded ? <Collapsible.Trigger asChild><button type="button" className="tb-code-more">{toggleLabel}</button></Collapsible.Trigger> : null}
  </>;
  const cls = cx("tb-code", showActions && "is-hover", className);
  return folded
    ? <Collapsible.Root asChild open={open} onOpenChange={setOpen}><figure className={cls}>{body}</figure></Collapsible.Root>
    : <figure className={cls}>{body}</figure>;
}
