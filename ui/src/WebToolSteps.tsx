import { TofiIcon as Icon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import { requestFullToolResult } from "./toolResultLoader";
import { ToolStatusIcon } from "./ToolStatusIcon";
import { toolDisplayLabel, toolDisplayState, toolActionLabel, toolArgumentPreview, toolSourceLabel, toolActivityKey, foldToolRuns, formatStepSeconds } from "./toolTimeline";
import { useTranslation } from "./i18n";
import { formatExactTime } from "./timezone";
import { toggleDisclosure } from "./TaskRunBlock";
import type { Message, ToolActivity } from "./types";

/**
 * The step list inside a web tool row's drawer. It draws only after a person opens the drawer, so it is its own
 * chunk (`toolSteps` in lazyPanels.tsx), warmed in idle time and on hover or focus of the row's toggle.
 */
export type WebToolEntry = { type: "tool"; at: string; activity: ToolActivity } | { type: "note"; at: string; note: Message };

function toolStepLabel(activity: ToolActivity) {
  // Drop the Chinese progressive prefix 正在 ("in the middle of") from a step label.
  return toolActionLabel(activity).replace(/^\u6b63\u5728/, "");
}

function webToolSeconds(activity: ToolActivity, now: number): number | undefined {
  const started = Date.parse(activity.started_at);
  const ended = activity.status === "running" ? now : Date.parse(activity.updated_at);
  return Number.isFinite(started) && Number.isFinite(ended) && ended >= started ? (ended - started) / 1000 : undefined;
}

export function WebToolSteps({ entries, live, now, timezone }: { entries: WebToolEntry[]; live: boolean; now: number; timezone: string }) {
  const { t } = useTranslation(["chat", "common"]);
  const renderStep = (activity: ToolActivity, liveRun: boolean) => {
    const duration = webToolSeconds(activity, now);
    const active = liveRun && (activity.status === "running" || activity.status === "queued");
    const state = toolDisplayState(activity);
    const problem = ["failed", "not_executed", "interrupted"].includes(state);
    return <li className={`web-tool-step is-${state}${problem ? " is-problem" : ""}`} key={toolActivityKey(activity)}>
      <ToolStatusIcon state={active ? "running" : state} label={toolDisplayLabel(activity)} />
      <details className="web-tool-step-detail" onToggle={event => { if (event.currentTarget.open) requestFullToolResult(activity); }}><summary onClick={toggleDisclosure}><span className="web-tool-step-label">{toolStepLabel(activity)}{toolArgumentPreview(activity) ? ` · ${toolArgumentPreview(activity)}` : ""}{toolSourceLabel(activity) ? ` · ${toolSourceLabel(activity)}` : ""}</span></summary>
        <div className="tool-activity-details"><small>{Number.isFinite(Date.parse(activity.started_at)) ? formatExactTime(activity.started_at, timezone) : ""} · {timezone}</small><div><span>{t("tools.arguments")}</span><pre>{activity.arguments || t("tools.none")}</pre></div><div><span>{t("tools.result")}</span>{activity.outcome && <p className="tool-outcome" role="note">{activity.outcome.message}</p>}<pre>{activity.result || (active ? t("tools.waiting_result") : t("tools.none"))}</pre></div>{activity.truncated && <small>{t("tools.truncated")}</small>}</div>
      </details>
      <span className="web-tool-step-meta">{problem && <span className="web-tool-step-state">{toolDisplayLabel(activity)}</span>}{duration !== undefined && duration >= 1 && <span className="web-tool-quiet">{formatStepSeconds(duration)}</span>}</span>
    </li>;
  };
  const stepList = (() => {
    const result: ({ type: "note"; at: string; note: Message } | { type: "tool"; at: string; activity: ToolActivity; group?: ToolActivity[] })[] = [];
    let buffer: ToolActivity[] = [];
    const flush = () => { for (const fold of foldToolRuns(buffer)) result.push({ type: "tool", at: fold.items[0].started_at, activity: fold.items[0], group: fold.items.length > 1 ? fold.items : undefined }); buffer = []; };
    for (const entry of entries) { if (entry.type === "tool") buffer.push(entry.activity); else { flush(); result.push(entry); } }
    flush();
    return result;
  })();
  return <>{stepList.map(entry => {
        if (entry.type === "note") return <li className="web-tool-step is-note" key={entry.note.id}><Icon name="file-text" size={16} /><div className="web-tool-step-copy"><span>{t("tools.progress_note")}</span><div className="web-tool-note"><MessageMarkdown content={entry.note.content} /></div></div><time>{formatExactTime(entry.note.created_at, timezone)}</time></li>;
        const activity = entry.activity;
        if (entry.group) return <li className="web-tool-step web-tool-group is-completed" key={`group-${toolActivityKey(activity)}`}>
          <ToolStatusIcon state="completed" label={toolDisplayLabel(activity)} />
          <details className="web-tool-step-detail"><summary onClick={toggleDisclosure}><span className="web-tool-step-label">{toolStepLabel(activity)}<span className="web-tool-count" aria-label={t("tools.call_count", { count: entry.group.length })}> ×{entry.group.length}</span><Icon className="web-tool-group-chevron" name="chevron-right" size={12} aria-hidden="true" /></span></summary>
            <ol className="web-tool-group-steps">{entry.group.map(item => renderStep(item, false))}</ol></details>
          <span className="web-tool-step-meta">{(() => { const total = entry.group.reduce((sum, item) => sum + (webToolSeconds(item, now) ?? 0), 0); return total >= 1 ? formatStepSeconds(total) : ""; })()}</span>
        </li>;
        return renderStep(activity, live);
      })}</>;
}
