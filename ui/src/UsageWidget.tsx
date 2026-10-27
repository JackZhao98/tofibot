import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, request } from "./api";
import { useTranslation } from "./i18n";
import { formatNumber } from "./i18n/format";
import { TofiIcon as Icon } from "./icons";
import { modelName } from "./modelCatalog";
import { useModelCatalog } from "./useModelCatalog";
import "./usage-widget.css";

/** The shape of GET /api/conversations/{id}/usage (internal/app/conversation_usage.go; AGENTS.md "Conversation usage"). */
export type ConversationUsage = {
  conversation_id: string;
  totals: { runs: number; runs_with_call_detail: number; requests: number; input_tokens: number; output_tokens: number; equivalent_usd: number; unpriced_requests: number };
  per_model: { model: string; requests: number; input_tokens: number; output_tokens: number; equivalent_usd: number; unpriced_requests: number }[];
  latest_context: null | {
    run_id: string; model: string; used_tokens: number; used_measured: boolean; window_tokens: number; window_known: boolean;
    parts: { kind: string; tokens: number; estimated: boolean }[]; parts_tokens: number; parts_code?: string;
  };
  tool_split: { available: true; tool_tokens: number; conversation_tokens: number; review_tokens: number; tool_model_calls: number; conversation_model_calls: number; tool_calls: number; basis?: string } | { available: false; code: string };
};

const PART_KINDS = ["instructions", "history", "tool_definitions", "tool_results", "memory_notes", "attachments", "other"] as const;
/** Re-reads after a run finishes at most this often. */
const THROTTLE_MS = 4000;

const partKind = (kind: string) => (PART_KINDS as readonly string[]).includes(kind) ? kind as typeof PART_KINDS[number] : "other";
const compact = (value: number) => formatNumber(value, { notation: "compact", maximumFractionDigits: value < 1000 ? 0 : 1 });
const money = (value: number) => value > 0 && value < 0.01 ? `<${formatNumber(0.01, { style: "currency", currency: "USD" })}` : formatNumber(value, { style: "currency", currency: "USD" });
/** A part's share of the window: under one percent reads "<1%", never a rounded zero. */
const share = (tokens: number, window: number) => window > 0 && tokens > 0 ? (tokens / window < 0.005 ? `<${percent(0.01)}` : percent(tokens / window)) : "";
const percent = (value: number) => formatNumber(value, { style: "percent", maximumFractionDigits: 0 });

/** Reads the usage of one conversation: once on open, and again (throttled) when a run in it finishes. No timer runs while it is idle. */
function useConversationUsage(conversationId: string, running: boolean) {
  const [data, setData] = useState<ConversationUsage | null>(null);
  const [failed, setFailed] = useState<false | "error" | "unsupported">(false);
  const [retry, setRetry] = useState(0);
  const last = useRef(0);
  const timer = useRef(0);
  const abort = useRef<AbortController | null>(null);
  const read = useCallback(() => {
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    last.current = Date.now();
    request<ConversationUsage>(`/api/conversations/${encodeURIComponent(conversationId)}/usage`, { signal: controller.signal }).then(
      body => { setData(body); setFailed(false); },
      error => { if (!controller.signal.aborted) setFailed((error instanceof ApiError && (error.status === 404 || error.status === 405)) ? "unsupported" : "error"); },
    );
  }, [conversationId]);
  useEffect(() => { read(); return () => { abort.current?.abort(); window.clearTimeout(timer.current); }; }, [read, retry]);
  const was = useRef(running);
  useEffect(() => {
    const finished = was.current && !running;
    was.current = running;
    if (!finished) return;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(read, Math.max(0, THROTTLE_MS - (Date.now() - last.current)));
  }, [running, read]);
  return { data, failed, reload: () => setRetry(n => n + 1) };
}

export function UsageWidget({ conversationId, running, onAll }: { conversationId: string; running: boolean; onAll: () => void }) {
  const { t } = useTranslation("chat");
  const { data, failed, reload } = useConversationUsage(conversationId, running);
  const { catalog } = useModelCatalog();
  const name = (id: string) => catalog ? modelName(catalog.models, id) : id;

  let body;
  if (!data) {
    body = failed === "unsupported"
      ? <p className="usage-widget-note" role="status">{t("usageWidget.needs_newer_server")}</p>
      : failed
      ? <div className="usage-widget-note" role="alert">{t("usageWidget.error")} <button type="button" className="usage-widget-retry" onClick={reload}>{t("usageWidget.retry")}</button></div>
      : <div className="usage-widget-skeleton" aria-busy="true" aria-label={t("usageWidget.loading")}><i /><i /><i /></div>;
  } else if (!data.latest_context && !data.totals.input_tokens && !data.totals.output_tokens) {
    body = <p className="usage-widget-note">{t("usageWidget.empty")}</p>;
  } else {
    const ctx = data.latest_context;
    const fraction = ctx && ctx.window_tokens > 0 ? ctx.used_tokens / ctx.window_tokens : 0;
    const tone = fraction >= 0.92 ? "full" : fraction >= 0.75 ? "warn" : "ok";
    const split = data.tool_split;
    const splitTotal = split.available ? split.tool_tokens + split.conversation_tokens : 0;
    const toolShare = split.available && splitTotal > 0 ? split.tool_tokens / splitTotal : 0;
    const totals = data.totals;
    body = <>
      {ctx && <section className="usage-widget-section" aria-labelledby={`${conversationId}-uw-context`}>
        <h3 id={`${conversationId}-uw-context`}>{t("usageWidget.context")}</h3>
        <div className="usage-now">
          <strong className={`usage-used is-${tone}`}>{ctx.used_measured ? "" : "≈"}{t("usageWidget.used_percent", { percent: percent(fraction) })}</strong>
          <span className="usage-now-figures">{ctx.used_measured ? "" : "≈"}{compact(ctx.used_tokens)} / {ctx.window_known ? "" : "≈"}{compact(ctx.window_tokens)}</span>
        </div>
        <small className="usage-now-meta">{name(ctx.model)} · {ctx.used_measured ? t("usageWidget.measured") : t("usageWidget.estimated")}{ctx.window_known ? "" : ` · ${t("usageWidget.window_estimated")}`}</small>
        {/* The bar is the window: a segment per context part (or one for what is used when there is no snapshot), the rest empty. */}
        <div className="usage-parts-bar" role="img" aria-label={t("usageWidget.ring_label", { used: compact(ctx.used_tokens), window: compact(ctx.window_tokens), percent: percent(fraction) })}>
          {ctx.parts.length ? ctx.parts.map(part => <i key={part.kind} className={`usage-part k-${part.kind}`} style={{ flexGrow: part.tokens }} />) : <i className={`usage-part k-used is-${tone}`} style={{ flexGrow: ctx.used_tokens }} />}
          {ctx.window_tokens > ctx.used_tokens && <i className="usage-rest" style={{ flexGrow: ctx.window_tokens - (ctx.parts.length ? ctx.parts_tokens || ctx.used_tokens : ctx.used_tokens) }} />}
        </div>
        {ctx.parts.length ? <ul className="usage-legend">
          {ctx.parts.map(part => <li key={part.kind}><i className={`usage-swatch k-${part.kind}`} aria-hidden="true" /><span>{t(`usageWidget.part.${partKind(part.kind)}`)}</span><b>{part.estimated ? "≈" : ""}{compact(part.tokens)}</b><em>{share(part.tokens, ctx.window_tokens)}</em></li>)}
        </ul> : <p className="usage-widget-line">{t("usageWidget.parts_unavailable")}</p>}
      </section>}

      <section className="usage-widget-section" aria-labelledby={`${conversationId}-uw-split`}>
        <h3 id={`${conversationId}-uw-split`}>{t("usageWidget.split")}</h3>
        {split.available && splitTotal > 0 ? <>
          <div className="usage-split-bar" role="img" aria-label={t("usageWidget.split_label", { tools: percent(toolShare), conversation: percent(1 - toolShare) })}>
            <i className="k-tools" style={{ flexGrow: split.tool_tokens }} /><i className="k-conversation" style={{ flexGrow: split.conversation_tokens }} />
          </div>
          <dl className="usage-split-legend">
            <div><dt><i className="usage-swatch k-tools" aria-hidden="true" />{t("usageWidget.tools")}</dt><dd>{compact(split.tool_tokens)} · {t("usageWidget.calls", { count: split.tool_model_calls })}{split.tool_calls ? ` · ${t("usageWidget.tool_runs", { count: split.tool_calls })}` : ""}</dd></div>
            <div><dt><i className="usage-swatch k-conversation" aria-hidden="true" />{t("usageWidget.conversation")}</dt><dd>{compact(split.conversation_tokens)} · {t("usageWidget.calls", { count: split.conversation_model_calls })}</dd></div>
            {split.review_tokens > 0 && <div><dt><i className="usage-swatch k-review" aria-hidden="true" />{t("usageWidget.review")}</dt><dd>{compact(split.review_tokens)}</dd></div>}
          </dl>
        </> : <p className="usage-widget-line">{split.available ? t("usageWidget.split_unavailable.no_usage") : t(`usageWidget.split_unavailable.${split.code === "call_detail_incomplete" ? "call_detail_incomplete" : "no_usage"}`)}</p>}
      </section>

      <section className="usage-widget-section" aria-labelledby={`${conversationId}-uw-totals`}>
        <h3 id={`${conversationId}-uw-totals`}>{t("usageWidget.totals")}</h3>
        <dl className="usage-totals">
          <div><dt>{t("usageWidget.input")}</dt><dd>{compact(totals.input_tokens)}</dd></div>
          <div><dt>{t("usageWidget.output")}</dt><dd>{compact(totals.output_tokens)}</dd></div>
          <div><dt>{t("usageWidget.requests")}</dt><dd>{totals.requests ? formatNumber(totals.requests) : "–"}</dd></div>
          <div><dt>{t("usageWidget.cost")}</dt><dd title={totals.unpriced_requests ? t("usageWidget.unpriced", { count: totals.unpriced_requests }) : undefined}>{totals.requests ? `≈ ${money(totals.equivalent_usd)}` : "–"}</dd></div>
        </dl>
        {data.per_model.length > 1 && <ul className="usage-models">{data.per_model.slice(0, 3).map(row => <li key={row.model}><span>{name(row.model)}</span><b>{compact(row.input_tokens + row.output_tokens)}</b></li>)}</ul>}
        {totals.runs > totals.runs_with_call_detail && <p className="usage-widget-line">{t("usageWidget.coverage", { covered: totals.runs_with_call_detail, runs: totals.runs })}</p>}
      </section>
    </>;
  }
  return <div className="usage-widget" data-running={running || undefined}>
    {body}
    <button type="button" className="usage-widget-all" onClick={onAll}>{t("usageWidget.all")}<Icon name="arrow-right" size={14} /></button>
  </div>;
}
