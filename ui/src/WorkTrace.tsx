import { lazy, Suspense, useRef, useState } from "react";
import { request } from "./api";
import { TofiIcon } from "./icons";
import { attachmentUrl } from "./filePreviewModel";
import { useTranslation } from "./i18n";
import type { Attachment, Run } from "./types";
import { traceCellAt, traceCells, traceThumbStyle } from "./workTraceLayout";
import "./work-trace.css";

const Lightbox = lazy(() => import("./AttachmentLightbox"));

type Capture = { seq: number; attachment_id?: string; label: string; redacted: boolean };
const THUMBS = 6, THUMB_WIDTH = 64;

/**
 * The work trace of a run: one folded line at the end of its Activity list. It shows the first steps as small cuts of the strip
 * and opens the strip in the image viewer; a click on a step in the viewer shows that step's own picture. The strip and the
 * pictures are made by the server when the run ends (nothing here decides what is kept).
 */
export default function WorkTrace({ run }: { run: Run }) {
  const { t } = useTranslation("tasks");
  const trace = run.trace;
  const button = useRef<HTMLButtonElement>(null);
  const [captures, setCaptures] = useState<Capture[] | null>(null);
  const [index, setIndex] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [asking, setAsking] = useState(false);
  const [gone, setGone] = useState(false);
  if (!trace || gone) return gone ? <p className="task-record-meta work-trace-note" role="status">{t("trace.deleted")}</p> : null;
  const cells = traceCells(trace.tiles, trace.steps);
  const url = attachmentUrl(trace.attachment_id);
  const first = trace.steps > trace.tiles ? 1 : 0;
  const thumbs = Array.from({ length: Math.min(THUMBS, trace.tiles) }, (_, i) => i + first);
  // The steps the strip shows are the last `tiles` captures of the run.
  const shown = captures ? captures.slice(Math.max(0, captures.length - trace.tiles)) : [];
  const withPicture = shown.filter(capture => capture.attachment_id && !capture.redacted);
  const file = (id: string, name: string): Attachment => ({ id, conversation_id: run.conversation_id, name, mime: "image/jpeg", size: 0, created_at: trace.created_at });
  const images = [file(trace.attachment_id, t("trace.title")), ...withPicture.map(capture => file(capture.attachment_id!, t("trace.step", { seq: capture.seq, label: capture.label })))];

  const stepButtons = images.map((image, to) => ({ label: image.name, to }));

  async function open() {
    setError("");
    try {
      if (!captures) setCaptures((await request<{ captures: Capture[] }>(`/api/runs/${encodeURIComponent(run.id)}/captures`)).captures);
      setIndex(0);
    } catch {
      setError(t("trace.load_failed"));
    }
  }
  // A click on a step of the strip shows that step; a click on a step's picture goes back to the strip.
  function cellAt(current: number, fx: number, fy: number): number | null {
    if (current !== 0) return 0;
    const cell = traceCellAt(fx, fy, cells);
    if (cell === null || cell < first) return null;
    const capture = shown[cell - first];
    const position = capture ? withPicture.indexOf(capture) : -1;
    return position < 0 ? null : position + 1;
  }
  async function remove() {
    try {
      await request(`/api/runs/${encodeURIComponent(run.id)}/captures`, { method: "DELETE" });
      setIndex(null);
      setGone(true);
    } catch {
      setError(t("trace.delete_failed"));
      setAsking(false);
    }
  }
  return <div className="work-trace" data-run-id={run.id}>
    <button ref={button} type="button" className="process-line work-trace-line" aria-label={`${t("trace.open")} · ${t("trace.steps", { count: trace.steps })}`} onPointerEnter={() => void import("./AttachmentLightbox")} onClick={() => void open()}>
      <span className="work-trace-thumbs" aria-hidden="true">{thumbs.map(i => <span key={i} className="work-trace-thumb" style={traceThumbStyle(url, i, cells, THUMB_WIDTH)} />)}</span>
      <span className="process-line-text">{t("trace.title")} · {t("trace.steps", { count: trace.steps })}</span>
      <TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" />
    </button>
    {asking
      ? <span className="work-trace-confirm" role="group" aria-label={t("trace.delete_confirm")}>
        <span>{t("trace.delete_confirm")}</span>
        <button type="button" className="text-button" onClick={() => void remove()}>{t("trace.delete_yes")}</button>
        <button type="button" className="text-button" onClick={() => setAsking(false)}>{t("trace.delete_no")}</button>
      </span>
      : <button type="button" className="text-button work-trace-delete" onClick={() => setAsking(true)}>{t("trace.delete")}</button>}
    {error && <small className="error-text" role="alert">{error}</small>}
    {index !== null && <Suspense fallback={null}><Lightbox images={images} index={index} onIndex={setIndex} onClose={() => setIndex(null)} origin={() => button.current} cellAt={cellAt} stepButtons={stepButtons} stepsLabel={t("trace.title")} /></Suspense>}
  </div>;
}
