import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, api } from "./api";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { formatNumber } from "./i18n/format";
import { i18n, useTranslation } from "./i18n";
import type { RunContextPart, RunContextSnapshot } from "./types";
import "./run-context.css";

/**
 * "Context" inside a reply's View activity: what that run's first model call received, part by part, with sizes
 * and the exact text (D53). Lazy chunk: the entry bundle only carries the toggle. Owner-only on the server.
 */
export type RunContextState =
  | { status: "loading" }
  | { status: "unavailable" }
  | { status: "error"; message: string }
  | { status: "ready"; snapshot: RunContextSnapshot };

const KNOWN_KINDS = new Set(["system_guidance", "bot_instructions", "agent_report_prompt", "tool_evidence_policy", "durable_guidance", "computer_environment", "capability_guidance", "extension_discovery_policy", "voice_guidance", "extension_directory", "tools", "tools_on_demand", "memory", "summary", "history_message", "earlier_turns_note", "capability_refs", "schedule_states", "run_context", "request_note", "scheduled_task", "scheduled_order", "open_work", "tool_carryover", "form_answers", "group_profiles", "co_addressed", "coordination", "mentions_since_trigger", "handoff_task", "colleague_result", "colleague_failure", "waiting_on", "task_record", "task_updates", "wait_result", "group_mention", "other"]);

function useKindLabel() {
  const { t } = useTranslation("chat");
  return (part: Pick<RunContextPart, "kind" | "label">) => KNOWN_KINDS.has(part.kind) && part.kind !== "other"
    ? t(`runContext.kind.${part.kind}` as never)
    : part.label || part.kind;
}

export function RunContextView({ runId, timezone }: { runId: string; timezone: string }) {
  const [state, setState] = useState<RunContextState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const abort = new AbortController();
    setState({ status: "loading" });
    api.runContext(runId, abort.signal).then(
      snapshot => {
        if (abort.signal.aborted) return;
        // An answer that is not a snapshot is a failure, never an empty success.
        if (!Array.isArray(snapshot?.parts) || !snapshot.totals) setState({ status: "error", message: i18n.t("common:apiError.request_failed", { status: 200 }) });
        else setState({ status: "ready", snapshot });
      },
      (error: unknown) => {
        if (abort.signal.aborted) return;
        if (error instanceof ApiError && error.code === "context_snapshot_unavailable") setState({ status: "unavailable" });
        else setState({ status: "error", message: error instanceof Error ? error.message : String(error) });
      },
    );
    return () => abort.abort();
  }, [runId, attempt]);
  const retry = useCallback(() => setAttempt(value => value + 1), []);
  return <RunContextBody state={state} timezone={timezone} onRetry={retry} />;
}

export function RunContextBody({ state, timezone, onRetry }: { state: RunContextState; timezone: string; onRetry?: () => void }) {
  const { t } = useTranslation(["chat", "common"]);
  if (state.status === "loading") return <p className="run-context-note" role="status">{t("chat:runContext.loading")}</p>;
  if (state.status === "unavailable") return <p className="run-context-note" role="status">{t("chat:runContext.unavailable", { keep: 50 })}</p>;
  if (state.status === "error") return <p className="run-context-note" role="alert">{t("chat:runContext.failed", { error: state.message })} {onRetry && <button type="button" className="secondary-button" onClick={onRetry}>{t("common:action.retry")}</button>}</p>;
  return <RunContextReady snapshot={state.snapshot} timezone={timezone} />;
}

const SHARE_TONES = [88, 66, 48, 34, 22, 14];

function RunContextReady({ snapshot, timezone }: { snapshot: RunContextSnapshot; timezone: string }) {
  const { t } = useTranslation("chat");
  const kindLabel = useKindLabel();
  const { totals, history } = snapshot;
  const labelOf = useMemo(() => {
    const labels = new Map<string, string>();
    for (const part of snapshot.parts) if (!labels.has(part.kind)) labels.set(part.kind, kindLabel(part));
    return labels;
  }, [snapshot.parts, kindLabel]);
  const kinds = useMemo(() => [...totals.by_kind].sort((a, b) => b.tokens - a.tokens || b.chars - a.chars), [totals.by_kind]);
  const sum = kinds.reduce((n, kind) => n + kind.tokens, 0) || 1;
  const redactions = snapshot.parts.reduce((n, part) => n + (part.redactions ?? 0), 0);
  const groups: Array<{ history: boolean; parts: RunContextPart[]; key: number }> = [];
  snapshot.parts.forEach((part, index) => {
    const isHistory = part.kind === "history_message";
    const last = groups[groups.length - 1];
    if (last && last.history === isHistory) last.parts.push(part);
    else groups.push({ history: isHistory, parts: [part], key: index });
  });
  return <div className="run-context-body">
    <p className="run-context-total"><strong>{t("runContext.total", { chars: formatNumber(totals.chars), tokens: formatNumber(totals.tokens) })}</strong> <span>{t("runContext.estimate")}</span></p>
    <div className="run-context-share" role="img" aria-label={t("runContext.share_label")}>
      {kinds.map((kind, index) => <span key={kind.kind} style={{ flexGrow: Math.max(kind.tokens, 1), ["--share-tone" as string]: `${SHARE_TONES[Math.min(index, SHARE_TONES.length - 1)]}%` }} />)}
    </div>
    <ul className="run-context-legend">
      {kinds.map((kind, index) => <li key={kind.kind}>
        <i aria-hidden="true" style={{ ["--share-tone" as string]: `${SHARE_TONES[Math.min(index, SHARE_TONES.length - 1)]}%` }} />
        <span className="run-context-legend-name">{labelOf.get(kind.kind) ?? kind.kind}{kind.parts > 1 ? ` ×${kind.parts}` : ""}</span>
        <span className="run-context-legend-size">{t("runContext.share", { percent: Math.round((kind.tokens / sum) * 100), tokens: formatNumber(kind.tokens) })}</span>
      </li>)}
    </ul>
    {totals.on_demand_tokens > 0 && <p className="run-context-note">{t("runContext.on_demand", { tokens: formatNumber(totals.on_demand_tokens) })}</p>}
    {history.skipped > 0 && <p className="run-context-note">{t("runContext.skipped", { number: history.skipped })}{history.skipped_by_summary > 0 ? ` ${t("runContext.skipped_by_summary", { number: history.skipped_by_summary })}` : ""}</p>}
    {history.earlier_unfetched && <p className="run-context-note">{t("runContext.earlier_unfetched")}</p>}
    {totals.dropped_texts > 0 && <p className="run-context-note">{t("runContext.dropped_texts", { number: totals.dropped_texts })}</p>}
    {redactions > 0 && <p className="run-context-note">{t("runContext.redacted", { number: redactions })}</p>}
    <div className="run-context-parts">
      {groups.map(group => group.history
        ? <section key={group.key} className="run-context-history" aria-label={t("runContext.history_label", { number: group.parts.length })}>
            <h4>{t("runContext.history_label", { number: group.parts.length })}</h4>
            <ol>{group.parts.map((part, index) => <li key={part.message?.id ?? index}><PartRow part={part} label={labelOf.get(part.kind) ?? part.kind} timezone={timezone} compact /></li>)}</ol>
          </section>
        : group.parts.map((part, index) => <PartRow key={`${group.key}-${index}`} part={part} label={labelOf.get(part.kind) ?? part.kind} timezone={timezone} />))}
    </div>
  </div>;
}

function formatClock(value: string | undefined, timezone: string) {
  if (!value) return "";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "";
  try {
    return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZone: timezone }).format(date);
  } catch {
    return date.toISOString();
  }
}

function PartRow({ part, label, timezone, compact = false }: { part: RunContextPart; label: string; timezone: string; compact?: boolean }) {
  const { t } = useTranslation("chat");
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const timer = useRef(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const message = part.message;
  const size = t("runContext.size", { chars: formatNumber(part.chars), tokens: formatNumber(part.tokens) });
  const copy = () => {
    if (part.text === undefined) return;
    void navigator.clipboard?.writeText(part.text).then(() => {
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1500);
    }, () => undefined);
  };
  const meta = compact && message
    ? [t(`runContext.role.${part.role === "assistant" ? "assistant" : "user"}` as never), formatClock(message.created_at, timezone), message.gisted ? t("runContext.gist") : t("runContext.verbatim"), message.trimmed ? t("runContext.trimmed") : "", message.request ? t("runContext.request") : "", message.images ? t("runContext.images", { number: message.images }) : ""].filter(Boolean).join(" · ")
    : "";
  return <details className={`run-context-part${compact ? " is-compact" : ""}${part.on_demand ? " is-on-demand" : ""}`} onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>
      <span className="run-context-part-name">{compact ? meta : label}{!compact && part.on_demand ? ` · ${t("runContext.not_sent")}` : ""}</span>
      <span className="run-context-part-size">{size}</span>
    </summary>
    {open && <div className="run-context-part-text">
      {part.items && part.items.length > 0 && <ul className="run-context-items">{part.items.map(item => <li key={item.name}><span>{item.name}</span><span>{t("runContext.size", { chars: formatNumber(item.chars), tokens: formatNumber(item.tokens) })}</span></li>)}</ul>}
      {part.text_dropped || part.text === undefined
        ? <p className="run-context-note">{t("runContext.text_dropped")}</p>
        : <>
            <div className="run-context-actions"><button type="button" className="run-context-copy" aria-label={t("runContext.copy")} onClick={copy}><CopyFeedbackIcon copied={copied} size={14} /><span>{copied ? t("runContext.copied") : t("runContext.copy")}</span></button></div>
            <pre>{part.text}</pre>
          </>}
    </div>}
  </details>;
}
