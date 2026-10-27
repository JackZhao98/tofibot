import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { isDesktop } from "./desktop";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import { MessageMarkdown, ViewerLinkContext } from "./MessageMarkdown";
import {
  attachmentUrl, closeFilePreview, extensionOf, fileKind, fileSize, isPreviewable, parseDelimited, popFilePreview,
  readTextHead, tileLabel, tileTone, useFilePreviewStack, type FileKind, type PreviewTarget,
} from "./filePreviewModel";
import "./file-preview.css";

/** Type tile: folded corner, format in mono, one colour per format family. */
export function FileTile({ name, kind }: { name: string; kind: FileKind }) {
  return <span className="file-tile" data-file-tone={tileTone(kind)} data-file-kind={kind} aria-hidden="true">{tileLabel(name, kind)}</span>;
}

type Body =
  | { state: "loading" }
  | { state: "error" }
  | { state: "text"; text: string; truncated: boolean }
  | { state: "csv"; rows: string[][]; truncated: boolean }
  | { state: "none" };

function Lines({ text, label }: { text: string; label: string }) {
  const lines = useMemo(() => (text.endsWith("\n") ? text.slice(0, -1) : text).split("\n"), [text]);
  const gutter = useMemo(() => lines.map((_, i) => i + 1).join("\n"), [lines]);
  return <div className="fp-code" tabIndex={0} role="region" aria-label={label}>
    <pre className="fp-gutter" aria-hidden="true">{gutter}</pre>
    <pre className="fp-text"><code>{text}</code></pre>
  </div>;
}

/** `embedded`: the viewer fills the outside panel, whose own header closes it (no back or close button here). */
export function Viewer({ target, canBack = false, previous, embedded = false }: { target: PreviewTarget; canBack?: boolean; previous?: PreviewTarget; embedded?: boolean }) {
  const { t } = useTranslation("chat");
  const [meta, setMeta] = useState(target);
  const [body, setBody] = useState<Body>({ state: "loading" });
  const [attempt, setAttempt] = useState(0);
  const url = attachmentUrl(meta.id);
  const kind = fileKind(meta.name, meta.mime);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => { setMeta(target); setBody({ state: "loading" }); }, [target]);
  useEffect(() => {
    const abort = new AbortController();
    (async () => {
      let current = meta;
      if (!current.mime && !current.size) {
        // A link to a file the chat has not rendered: learn its name, type and size from the response.
        const probe = await fetch(url, { credentials: "same-origin", signal: abort.signal });
        if (!probe.ok) throw new Error(String(probe.status));
        const name = /filename="?([^";]+)"?/i.exec(probe.headers.get("Content-Disposition") ?? "")?.[1] ?? current.name;
        current = { ...current, name, mime: probe.headers.get("Content-Type") ?? "application/octet-stream", size: Number(probe.headers.get("Content-Length")) || 0 };
        void probe.body?.cancel();
        if (abort.signal.aborted) return;
        setMeta(current);
      }
      const k = fileKind(current.name, current.mime);
      if (k === "pdf" || k === "image" || !isPreviewable(k)) { setBody({ state: "none" }); return; }
      const { text, truncated } = await readTextHead(url, undefined, abort.signal);
      if (abort.signal.aborted) return;
      if (k === "csv" || k === "tsv") {
        const parsed = parseDelimited(text, k === "tsv" ? "\t" : ",");
        setBody({ state: "csv", rows: parsed.rows, truncated: truncated || parsed.more });
      } else if (k === "json" && !truncated && extensionOf(current.name) === "json") {
        let pretty = text;
        try { pretty = JSON.stringify(JSON.parse(text), null, 2); } catch { /* invalid JSON is shown as written */ }
        setBody({ state: "text", text: pretty, truncated: false });
      } else setBody({ state: "text", text, truncated });
    })().catch(cause => { if (!abort.signal.aborted && !(cause instanceof DOMException)) setBody({ state: "error" }); });
    return () => abort.abort();
  }, [meta.id, attempt, url]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { ref.current?.focus({ preventScroll: true }); }, [target.id]);

  async function saveNative() {
    if (!window.tofiDesktop) return;
    try { const file = await window.tofiDesktop.prepareAttachment(meta.id); await window.tofiDesktop.saveAttachment(file.handle); } catch { /* the desktop shell reports its own save errors */ }
  }

  const download = isDesktop
    ? <button type="button" className="fp-action" onClick={() => void saveNative()} aria-label={t("attachment.download", { name: meta.name })}><TofiIcon name="download" size={16} /><span>{t("attachment.download_action")}</span></button>
    : <a className="fp-action" href={url} download={meta.name} aria-label={t("attachment.download", { name: meta.name })}><TofiIcon name="download" size={16} /><span>{t("attachment.download_action")}</span></a>;

  let content;
  if (body.state === "loading") content = <p className="fp-status" role="status">{t("attachment.loading")}</p>;
  else if (body.state === "error") content = <div className="fp-status" role="alert"><p>{t("attachment.load_failed")}</p><button type="button" className="fp-action" onClick={() => { setBody({ state: "loading" }); setAttempt(n => n + 1); }}>{t("attachment.retry")}</button></div>;
  else if (kind === "pdf") content = <iframe className="fp-frame" src={`${url}?inline=1`} title={meta.name} />;
  else if (kind === "image") content = <div className="fp-image"><img src={url} alt={meta.name} /></div>;
  else if (body.state === "none") content = <div className="fp-unsupported"><TofiIcon name="file" size={28} /><strong>{t("attachment.unsupported")}</strong><p>{t("attachment.unsupported_hint")}</p>{download}</div>;
  else if (body.state === "csv") content = body.rows.length === 0 ? <p className="fp-status">{t("attachment.empty")}</p> : <>
    <div className="fp-table" tabIndex={0} role="region" aria-label={meta.name}><table><thead><tr>{body.rows[0].map((cell, i) => <th key={i} scope="col">{cell}</th>)}</tr></thead><tbody>{body.rows.slice(1).map((row, r) => <tr key={r}>{row.map((cell, i) => <td key={i}>{cell}</td>)}</tr>)}</tbody></table></div>
    {body.truncated && <p className="fp-note">{t("attachment.truncated")}</p>}</>;
  else if (body.state === "text") {
    content = body.text === "" ? <p className="fp-status">{t("attachment.empty")}</p> : <>
      {kind === "md"
        ? <ViewerLinkContext.Provider value={true}><div className="fp-md message-content" tabIndex={0}><MessageMarkdown content={body.text} /></div></ViewerLinkContext.Provider>
        : <Lines text={body.text} label={meta.name} />}
      {body.truncated && <p className="fp-note">{t("attachment.truncated")}</p>}</>;
  }

  return <div className={`fp-shell${embedded ? " is-embedded" : ""}`} data-file-kind={kind}>
    <header className="fp-head">
      {canBack && !embedded && <button type="button" className="fp-icon" onClick={popFilePreview} aria-label={t("attachment.viewer_back", { name: previous?.name ?? "" })}><TofiIcon name="arrow-left" size={18} /></button>}
      <FileTile name={meta.name} kind={kind} />
      <div className="fp-title">{embedded ? <strong className="fp-name">{meta.name}</strong> : <h2 id="fp-title">{meta.name}</h2>}<small>{meta.size ? fileSize(meta.size) : ""}</small></div>
      {download}
      {!embedded && <button type="button" className="fp-icon" onClick={closeFilePreview} aria-label={t("attachment.viewer_close")}><TofiIcon name="close" size={18} /></button>}
    </header>
    <div className="fp-body" ref={ref} tabIndex={-1}>{content}</div>
  </div>;
}

/** One file viewer for the whole app, driven by openFilePreview(). A native dialog gives Esc, focus trap and focus return. */
export function FilePreviewHost() {
  const { t } = useTranslation("chat");
  const stack = useFilePreviewStack();
  const current = stack.at(-1);
  const dialog = useRef<HTMLDialogElement>(null);
  const open = Boolean(current);
  useEffect(() => {
    const d = dialog.current;
    if (!d) return;
    // showModal() would move focus to the first control (Download) and ring it; land on the scrollable body instead.
    if (open && !d.open) { d.showModal(); (d.querySelector<HTMLElement>(".fp-body") ?? d).focus({ preventScroll: true }); }
    if (!open && d.open) d.close();
  }, [open]);
  const onClose = useCallback(() => { closeFilePreview(); }, []);
  return <dialog ref={dialog} className="file-preview" tabIndex={-1} aria-labelledby="fp-title" aria-label={t("attachment.viewer")} onCancel={event => { event.preventDefault(); onClose(); }} onClose={onClose} onClick={event => { if (event.target === dialog.current) onClose(); }}>
    {current && <Viewer key={current.id} target={current} canBack={stack.length > 1} previous={stack.at(-2)} />}
  </dialog>;
}
