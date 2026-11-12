import { useEffect, useRef, useState } from "react";
import { request } from "./api";
import { useTranslation, type Language } from "./i18n";
import { TofiIcon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import AttachmentLightbox from "./AttachmentLightbox";
import { attachmentUrl } from "./filePreviewModel";
import { ToolStatusIcon } from "./ToolStatusIcon";
import { toolDisplayLabel, toolDisplayState, toolStepDetail, toolStepTitle } from "./toolTimeline";
import { groupPictures, workEntries, workGroupKind, type WorkCapture } from "./workTimelineModel";
import type { Attachment, Message, Run, ToolActivity } from "./types";
import "./work-trace.css";
import "./message-attachment.css";

export function CaptureStack({ images }: { images: Attachment[] }) {
  const { t } = useTranslation("tasks");
  const [index, setIndex] = useState<number | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  if (!images.length) return null;
  return <><button ref={button} type="button" className="work-capture-stack" aria-label={t("trace.open")} onClick={() => setIndex(0)}>
    {images.slice(0, 3).map(image => <img key={image.id} src={attachmentUrl(image.id)} alt="" loading="lazy" width="56" height="36" />)}
    {images.length > 3 && <span>+{images.length - 3}</span>}
  </button>{index !== null && <AttachmentLightbox images={images} index={index} onIndex={setIndex} onClose={() => setIndex(null)} origin={() => button.current} />}</>;
}

/** Public narration and collapsed batches. Detailed audit records remain available through View activity. */
export default function WorkTimeline({ tools, notes, runs, locale }: { tools: ToolActivity[]; notes: Message[]; runs: Run[]; locale?: Language }) {
  const { t } = useTranslation("tasks", { lng: locale });
  const [captures, setCaptures] = useState<Record<string, WorkCapture[]>>({});
  const [error, setError] = useState(false);
  const [revision, setRevision] = useState(0);
  // Refresh only after capture-capable work changes; no timers or background polling.
  const captureRuns = runs.filter(run => run.trace || tools.some(tool => tool.run_id === run.id && ["computer_browser", "computer_desktop", "computer_action"].includes(tool.name)));
  const signature = captureRuns.map(run => `${run.id}:${run.status}:${run.trace?.attachment_id ?? ""}:${tools.filter(tool => tool.run_id === run.id).map(tool => `${tool.call_id}:${tool.status}`).join(",")}`).join("|");
  useEffect(() => {
    const controller = new AbortController();
    setError(false);
    void Promise.all(captureRuns.map(async run => {
      const result = await request<{ captures: WorkCapture[] }>(`/api/runs/${encodeURIComponent(run.id)}/captures`, { signal: controller.signal });
      return [run.id, result.captures] as const;
    })).then(rows => { if (!controller.signal.aborted) setCaptures(Object.fromEntries(rows)); }, () => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [signature, revision]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const deleted = (event: Event) => {
      const runId = (event as CustomEvent<string>).detail;
      setCaptures(current => ({ ...current, [runId]: [] }));
    };
    window.addEventListener("tofi:trace-deleted", deleted);
    return () => window.removeEventListener("tofi:trace-deleted", deleted);
  }, []);
  return <div className="work-timeline">
    {workEntries(tools, notes).map(entry => entry.kind === "narration"
      ? <div className="work-narration" key={entry.note.id}><MessageMarkdown content={entry.note.content} /></div>
      : <div className="work-batch" key={entry.key}>
        <details className="work-command-group"><summary><TofiIcon name="chevron-right" size={12} /><span>{t(`trace.${entry.tools.some(tool => tool.status === "running" || tool.status === "queued") ? "working" : workGroupKind(entry.tools)}`, { count: entry.tools.length })}</span></summary>
          <ol>{entry.tools.map(tool => <li key={tool.call_id} data-step={`${tool.run_id}:${tool.call_id}`}><ToolStatusIcon state={toolDisplayState(tool)} label={toolDisplayLabel(tool, locale)} /><span>{toolStepTitle(tool)}{toolStepDetail(tool) && ` · ${toolStepDetail(tool)}`}</span></li>)}</ol>
        </details>
        <CaptureStack images={groupPictures(entry.tools, captures[entry.tools[0].run_id] ?? [])} />
      </div>)}
    {error && <button type="button" className="text-button" onClick={() => setRevision(value => value + 1)}>{t("trace.load_failed")}</button>}
  </div>;
}
