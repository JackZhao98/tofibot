import type { ReactNode } from "react";
import "./approval-card.css";
import { TofiIcon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import { requestFullToolResult } from "./toolResultLoader";
import { ToolStatusIcon } from "./ToolStatusIcon";
import { toolDisplayLabel, foldToolRuns, toolDisplayState, toolStepTitle, toolStepSeconds, formatStepSeconds, toolStepDetail } from "./toolTimeline";
import { taskCertaintyText, taskOutcomeText, taskStatusText, type TaskOwner } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import { openPermissionsDeepLink } from "./settings/deepLinks";
import { formatClock } from "./i18n/format";
import { approvalFactRows } from "./extensionFacts";
import { REMEMBERED_APPROVAL_ACTOR, STANDING_ALLOWANCE_ACTOR, type Question } from "./questionTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Message, ToolActivity, ToolActivityRunSummary } from "./types";
import { problemStates, toggleDisclosure, type TaskDetailState } from "./TaskRunBlock";

/**
 * The opened Activity list of a task block. It draws only after a person opens Activity, so it is its own chunk
 * (`taskActivity` in lazyPanels.tsx), warmed in idle time and on hover or focus of the toggle.
 */
/** One step: marker, human action, short argument, then state and duration. */
const stepDiagnostics = (tool: ToolActivity) => JSON.stringify({ tool: tool.name, run_id: tool.run_id, call_id: tool.call_id, status: tool.status, outcome_status: tool.outcome?.status, code: tool.outcome?.code, certainty: tool.outcome?.execution_certainty, started_at: tool.started_at, updated_at: tool.updated_at }, null, 2);

function ToolStepRecord({ tool, locale, now, onOpenTools }: { tool: ToolActivity; locale?: Language; now?: number; onOpenTools?: () => void }) {
  const { t } = useTranslation("tasks", { lng: locale });
  const state = toolDisplayState(tool), preview = toolStepDetail(tool);
  const live = tool.status === "running" && now !== undefined ? Math.max(0, (now - Date.parse(tool.started_at)) / 1000) : undefined;
  const seconds = toolStepSeconds(tool) ?? (Number.isFinite(live) ? live : undefined);
  const started = Date.parse(tool.started_at);
  const problem = problemStates.includes(state);
  // A problem step explains itself in words first; codes stay in technical details.
  const reason = tool.outcome?.code || tool.outcome?.status ? taskOutcomeText(tool.outcome, locale) : state === "interrupted" ? t("step.interrupted") : t("step.failed");
  return <li className={`task-step is-${state}${problem ? " is-problem" : ""}`} data-step={`${tool.run_id}:${tool.call_id}`}>
    <details onToggle={event => { if (event.currentTarget.open) requestFullToolResult(tool); }}><summary onClick={toggleDisclosure}><ToolStatusIcon state={state} label={toolDisplayLabel(tool, locale)} /><span className="task-step-title">{state === "reviewed" ? t("step.reviewed_title") : toolStepTitle(tool)}{state !== "reviewed" && preview && <span className="task-step-argument"> · {preview}</span>}{problem && state === "not_executed" && tool.outcome?.code && <span className="task-step-argument task-step-reason"> · {reason}</span>}</span>{problem ? <span className="task-step-state">{toolDisplayLabel(tool, locale)}{seconds !== undefined && seconds >= 1 && <time> · {formatStepSeconds(seconds)}</time>}</span> : seconds !== undefined && seconds >= 1 && <time className="task-step-time-quiet">{formatStepSeconds(seconds)}</time>}</summary>
      <div className="task-step-body">{problem && <div className="task-step-problem" role="group" aria-label={t("step.problem")}>
        <p><strong>{reason}</strong>{tool.outcome?.execution_certainty && <> · {taskCertaintyText(tool.outcome.execution_certainty, locale)}</>}</p>
        {tool.outcome?.code === "mcp_review_setup_missing" && onOpenTools && <div className="task-step-actions"><button type="button" className="secondary-button" onClick={onOpenTools}>{t("step.open_tool_settings")}</button></div>}
        <details className="task-step-technical"><summary>{t("step.technical_details")}</summary>{tool.outcome?.message && <p>{tool.outcome.message}</p>}<pre tabIndex={0}>{stepDiagnostics(tool)}</pre></details>
      </div>}
      <div className="tool-activity-details">{Number.isFinite(started) && <span className="task-step-time">{t("step.started", { time: formatClock(started, { seconds: true, language: locale }) })}</span>}<span>{t("step.arguments")}</span><pre tabIndex={0}>{tool.arguments || t("step.none")}</pre>{!problem && tool.outcome?.message && <p className="tool-outcome">{tool.outcome.message}</p>}<span>{t("step.result")}</span><pre tabIndex={0}>{tool.result || t("step.no_result")}</pre></div></div></details>
  </li>;
}

/** A run of identical finished calls as one row with a count; opening it lists the calls. */
function ToolStepGroup({ items, locale, onOpenTools }: { items: ToolActivity[]; locale?: Language; onOpenTools?: () => void }) {
  const { t } = useTranslation("tasks", { lng: locale });
  const total = items.reduce((sum, tool) => sum + (toolStepSeconds(tool) ?? 0), 0);
  return <li className="task-step task-step-group is-completed">
    <details><summary onClick={toggleDisclosure}><ToolStatusIcon state="completed" label={toolDisplayLabel(items[0], locale)} /><span className="task-step-title">{toolStepTitle(items[0])}</span><span className="task-step-count" aria-label={t("step.group_count", { count: items.length })}>{items.length}</span><TofiIcon className="task-step-chevron" name="chevron-right" size={12} aria-hidden="true" />{total >= 1 && <time className="task-step-time-quiet">{formatStepSeconds(total)}</time>}</summary>
      <div className="task-step-body"><ol className="task-step-nested">{items.map(tool => <ToolStepRecord key={`${tool.run_id}:${tool.call_id}`} tool={tool} locale={locale} onOpenTools={onOpenTools} />)}</ol></div></details>
  </li>;
}

export type TaskActivityBodyProps = { owner: TaskOwner; /** Not the first stretch of a run: its earlier attempts and drafts were drawn above. */ head?: boolean; /** Not the last stretch of a run: the empty line and the "load more" page belong below. */ tail?: boolean; /** Every loaded call of the run when `tools` is only one stretch of it (the "load more" count). */ allTools?: ToolActivity[]; tools: ToolActivity[]; drafts: MailDraft[]; messages: Message[]; summaries: ToolActivityRunSummary[]; details: Record<string, TaskDetailState>; live: boolean; now: number; locale?: Language; recordedQuestions: Question[]; rememberedDecisions: Question[]; notes: Message[]; onOpenTools: () => void; onLoadDetails?: (runId: string, offset: number) => Promise<void>; renderMessage?: (message: Message) => ReactNode; renderQuestion: (question: Question) => ReactNode; renderDraft: (draft: MailDraft) => ReactNode };

export function TaskActivityBody({ owner, head = true, tail = true, allTools, tools, drafts, messages, summaries, details, live, now, locale, recordedQuestions, rememberedDecisions, notes, onOpenTools, onLoadDetails, renderMessage, renderQuestion, renderDraft }: TaskActivityBodyProps) {
  const { t } = useTranslation("tasks", { lng: locale });
  const attemptStatus = (status: string) => ({ done: t("activity.attempt.done"), failed: t("activity.attempt.failed"), cancelled: t("activity.attempt.cancelled"), interrupted: t("activity.attempt.interrupted") } as Record<string, string>)[status] ?? t("activity.attempt.ended");
  return <>
      {head && owner.family.previous.map((attempt, index) => <p key={attempt.id} className="task-record-meta">{t("activity.previous_attempt", { number: index + 1, status: attemptStatus(attempt.status) })}</p>)}
      {rememberedDecisions.map(question => <p key={question.question_id} className="task-record-meta task-remembered-record" data-question-record-id={question.question_id}>{t(question.answered_by === STANDING_ALLOWANCE_ACTOR ? "record.standing_decision" : "record.remembered_decision")}{question.answered_by === STANDING_ALLOWANCE_ACTOR && <> <button type="button" className="text-button approval-manage" aria-label={t("approval.manage_allowances")} onClick={openPermissionsDeepLink}>{t("approval.manage")}<TofiIcon name="chevron-right" size={12} aria-hidden="true" /></button></>}</p>)}
      {recordedQuestions.map(question => <section className="task-question-record" key={question.question_id} data-question-record-id={question.question_id} tabIndex={-1}>
        <p>{question.question_type === "approval" ? question.approval?.review_only || question.approval?.review?.status.startsWith("shadow") ? t("record.observation_only") : question.status === "answered" && question.answer === true ? t("record.approved_unconfirmed") : question.status === "expired" ? t("record.approval_expired") : t("record.decision") : t("record.answer")}</p>
        <dl><div><dt>{t("record.status")}</dt><dd>{taskStatusText(question.approval?.review?.status ?? question.status, locale)}</dd></div>{question.outcome && <div><dt>{t("record.execution")}</dt><dd>{taskCertaintyText(question.outcome.execution_certainty, locale)}</dd></div>}</dl>
        {question.question_type === "approval" && <details><summary>{t("record.view_proposal")}</summary><MessageMarkdown content={question.question} />{question.approval?.facts && <dl className="approval-verified-record">{approvalFactRows(question.approval.facts, t).map((row, index) => <div key={index}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>}{question.approval && <dl><div><dt>{t("record.action")}</dt><dd>{question.approval.action}</dd></div><div><dt>{t("record.target")}</dt><dd>{question.approval.target}</dd></div><div><dt>{t("record.impact")}</dt><dd>{question.approval.impact}</dd></div></dl>}{question.approval?.payload && <details><summary>{t("record.view_full_arguments")}</summary><pre tabIndex={0}>{question.approval.payload}</pre></details>}</details>}
        {question.question_type !== "approval" && renderQuestion(question)}
      </section>)}
      <ol className={`task-tool-records${live ? " is-live" : ""}`}>{foldToolRuns(tools).map(group => group.items.length > 1 ? <ToolStepGroup key={`${group.items[0].run_id}:${group.items[0].call_id}`} items={group.items} locale={locale} onOpenTools={onOpenTools} /> : <ToolStepRecord key={`${group.items[0].run_id}:${group.items[0].call_id}`} tool={group.items[0]} locale={locale} now={live ? now : undefined} onOpenTools={onOpenTools} />)}</ol>
      {head && drafts.filter(draft => draft.run_id !== owner.family.latest.id || draft.status === "unknown").map(renderDraft)}
      {notes.map(note => <details className="task-progress-record" key={note.id}><summary>{t("activity.progress_report")}</summary><MessageMarkdown content={note.content} /></details>)}
      {head && owner.family.previous.flatMap(attempt => messages.filter(message => message.run_id === attempt.id && message.role === "assistant" && message.kind !== "progress").map(message => <div key={message.id}>{renderMessage ? renderMessage(message) : <MessageMarkdown content={message.content} />}</div>))}
      {tail && head && !tools.length && !recordedQuestions.length && !notes.length && <p className="task-record-meta">{owner.family.attempts.every(attempt => summaries.find(item => item.run_id === attempt.id)?.tool_count === 0) ? t("activity.no_tool_steps") : t("activity.no_records")}</p>}
      {tail && owner.family.attempts.map(attempt => {
        const detail = details[attempt.id], count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? detail?.toolCount;
        const loaded = (allTools ?? tools).filter(tool => tool.run_id === attempt.id).length;
        return <div key={attempt.id} className="task-record-page">
          {detail?.error && <p>{t("activity.records_unavailable")}</p>}
          {count !== 0 && (detail?.hasMore !== false || (count !== undefined && loaded < count)) && <button type="button" className="secondary-button" disabled={detail?.loading} onClick={() => void onLoadDetails?.(attempt.id, detail?.loaded ?? 0)}>{detail?.loading ? t("activity.loading") : t("activity.load")}{count !== undefined ? ` (${loaded}/${count})` : ""}</button>}
        </div>;
      })}
  </>;
}

/** The body of a silent run's note row: the notes it left, as ordinary Markdown. */
export function SilentNoteBody({ notes }: { notes: { id: string; content: string }[] }) {
  return <div className="task-silent-note-body">{notes.map(note => <MessageMarkdown key={note.id} content={note.content} />)}</div>;
}
