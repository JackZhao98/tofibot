import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { api } from "./api";
import { BotAvatar } from "./BotAvatar";
import { useSettingsActive } from "./settingsActivity";
import { isDesktop } from "./desktop";
import type { AgentUsageTotal, ContextUsage, UsageCall, UsagePeriod } from "./types";
import "./usage-panel.css";
import { intlLocale } from "./i18n/format";
import { i18n, Trans, useTranslation } from "./i18n";

// Built per call so a language switch applies without a reload.
const number = { format: (value: number) => new Intl.NumberFormat(intlLocale()).format(value) };
const count = (value: number) => number.format(value);
const money = (value: number) => value < 0.01 && value > 0 ? `<$0.01` : `$${value.toFixed(2)}`;
const ranges = [["24h", "usage.range.24h"], ["7d", "usage.range.7d"], ["30d", "usage.range.30d"]] as const;
const fail = (cause: unknown) => cause instanceof Error ? cause.message : i18n.t("settings:usage.load_failed");

function UsageReel({text,rolled}:{text:string;rolled:boolean}) {
 if(isDesktop)return <>{text}</>;
 return <span className="web-usage-number" aria-label={text}>{Array.from(text).map((char,index)=>/\d/.test(char)?<span className="web-usage-col" aria-hidden="true" key={index}><span className="web-usage-reel" style={{"--d":rolled?Number(char)+10:0,"--delay":`${(text.length-index)*45}ms`} as CSSProperties}>{Array.from("01234567890123456789").map((face,at)=><span key={at}>{face}</span>)}</span></span>:<span aria-hidden="true" key={index}>{char}</span>)}</span>;
}

export function UsagePanel({ preferredBotId, timezone }: { preferredBotId?: string; timezone: string }) {
  const { t } = useTranslation("settings");
  const active = useSettingsActive();
  const detailRef = useRef<HTMLDivElement>(null);
  const [rolled, setRolled] = useState(false);
  const [agents, setAgents] = useState<AgentUsageTotal[]>([]);
  const [contexts, setContexts] = useState<ContextUsage[]>([]);
  const [periods, setPeriods] = useState<Record<"24h" | "7d" | "30d", UsagePeriod[]>>({ "24h": [], "7d": [], "30d": [] });
  const [calls, setCalls] = useState<UsageCall[]>([]);
  const [range, setRange] = useState<"24h" | "7d" | "30d">("7d");
  const [selected, setSelected] = useState("");
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    if (isDesktop || !active || loading || rolled) return;
    const element = detailRef.current;
    if (!element) return;
    const observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { observer.disconnect(); requestAnimationFrame(() => requestAnimationFrame(() => setRolled(true))); } }, { threshold: .2 });
    observer.observe(element);
    return () => observer.disconnect();
  }, [active, loading, rolled, agents.length]);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    try {
      const result = await api.usage();
      setAgents(result.agents ?? []);
      setContexts(result.contexts ?? []);
      setPeriods(result.periods ?? { "24h": [], "7d": [], "30d": [] });
      setCalls(result.calls ?? []);
      setError("");
    } catch (cause) { setError(fail(cause)); }
    finally { setLoading(false); }
  }, []);
  useEffect(() => {
    if (!active) return;
    void load();
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, 10000);
    return () => window.clearInterval(timer);
  }, [active, load]);
  useEffect(() => { setSelected(preferredBotId ?? ""); }, [preferredBotId]);
  const selectedId = selected === "all" ? "all" : selected && agents.some(agent => agent.bot_id === selected) ? selected : preferredBotId && agents.some(agent => agent.bot_id === preferredBotId) ? preferredBotId : "all";
  const current = agents.find(agent => agent.bot_id === selectedId);
  const rows = useMemo(() => contexts.filter(row => row.bot_id === selectedId), [contexts, selectedId]);
  const periodRows = (periods[range] ?? []).filter(row => selectedId === "all" || row.bot_id === selectedId);
  const total = periodRows.reduce((acc, row) => ({ input: acc.input + row.input_tokens, output: acc.output + row.output_tokens, requests: acc.requests + row.requests, usd: acc.usd + row.equivalent_usd, unpriced: acc.unpriced + row.unpriced_requests }), { input: 0, output: 0, requests: 0, usd: 0, unpriced: 0 });
  const cutoff = Date.now() - (range === "24h" ? 1 : range === "7d" ? 7 : 30) * 86400000;
  const visibleCalls = calls.filter(call => (selectedId === "all" || call.bot_id === selectedId) && new Date(call.occurred_at).getTime() >= cutoff);
  const dayKey = (date: Date) => new Intl.DateTimeFormat("en-CA", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit" }).format(date);
  const recentDays = Array.from({ length: 7 }, (_, index) => {
    const date = new Date(Date.now() - (6 - index) * 86400000);
    const key = dayKey(date);
    return { key, label: new Intl.DateTimeFormat(intlLocale(), { timeZone: timezone, weekday: "short" }).format(date), requests: calls.filter(call => (selectedId === "all" || call.bot_id === selectedId) && dayKey(new Date(call.occurred_at)) === key).length };
  });
  const chartMax = Math.max(1, ...recentDays.map(day => day.requests));

  return <section className="usage-page" aria-label={t("usage.label")}>
    <p className="usage-explanation">{t("usage.explanation")}</p>
    {error && <div className="usage-error" role="alert">{error}<button type="button" onClick={() => void load()}>{t("action.retry")}</button></div>}
    {loading ? <p role="status">{t("usage.loading")}</p> : agents.length === 0 ? <p className="usage-empty">{t("usage.no_agents")}</p> : <div className="usage-layout">
      <nav className="usage-agents" aria-label={t("usage.choose_agent")}><button type="button" aria-current={selectedId === "all" ? "true" : undefined} onClick={() => setSelected("all")}><span><strong>{t("usage.all_bots")}</strong><small>{t("usage.combined")}</small></span></button>{agents.map(agent => <button type="button" key={agent.bot_id} aria-current={selectedId === agent.bot_id ? "true" : undefined} onClick={() => setSelected(agent.bot_id)}><BotAvatar id={agent.bot_id} mini /><span><strong>{agent.bot_name}</strong><small>{agent.runs ? t("usage.runs", { count: agent.runs, runs: count(agent.runs) }) : t("usage.no_records")}</small></span></button>)}</nav>
      <div className={`usage-detail${rolled ? " is-rolled" : ""}`} ref={detailRef}>
        <div className="usage-summary"><h3>{selectedId === "all" ? t("usage.all_bots") : current?.bot_name ?? t("usage.agent_fallback")}</h3><div className="usage-ranges" aria-label={t("usage.ranges")}>{ranges.map(([key, label]) => <button key={key} type="button" aria-pressed={range === key} onClick={() => setRange(key)}>{t(label)}</button>)}</div><div><span><small>{t("usage.input_tokens")}</small><strong><UsageReel text={count(total.input)} rolled={rolled}/></strong></span><span><small>{t("usage.output_tokens")}</small><strong><UsageReel text={count(total.output)} rolled={rolled}/></strong></span><span><small>{t("usage.requests")}</small><strong><UsageReel text={count(total.requests)} rolled={rolled}/></strong></span><span><small>{t("usage.api_equivalent")}</small><strong><UsageReel text={total.unpriced === total.requests && total.requests ? t("usage.no_price") : money(total.usd)} rolled={rolled}/></strong></span></div>{total.unpriced > 0 && <p>{t("usage.unpriced", { count: total.unpriced, requests: count(total.unpriced) })}</p>}<p><Trans t={t} i18nKey="usage.pricing_source" components={{ link: <a href="https://developers.openai.com/api/docs/pricing" target="_blank" rel="noreferrer" /> }} /></p></div>
        {!isDesktop && <div className="web-usage-chart"><h4>{t("usage.chart_title")}</h4><div className="web-usage-bars" aria-label={t("usage.chart_label")}>{recentDays.map((day,index)=><span className="web-usage-bar" key={day.key}><i style={{"--h":`${day.requests/chartMax*100}%`,"--delay":`${200+index*60}ms`} as CSSProperties} title={t("usage.chart_bar", { day: day.label, count: day.requests })}/><small>{day.label}</small></span>)}</div></div>}
        <h4>{t("usage.calls_title")}</h4>
        {!visibleCalls.length ? <p className="usage-empty">{t("usage.no_calls")}</p> : <div className="usage-call-list">{visibleCalls.map(call => <details key={call.id} className="usage-call"><summary><time dateTime={call.occurred_at}>{new Intl.DateTimeFormat(intlLocale(), { timeZone: timezone, month:"numeric", day:"numeric", hour:"2-digit", minute:"2-digit" }).format(new Date(call.occurred_at))}</time><span>{call.bot_name} · {call.conversation_name}</span><strong>{call.price_known ? money(call.equivalent_usd) : t("usage.no_price")}</strong></summary><p>{call.model} · {t("usage.call_input", { value: count(call.input_tokens) })} · {t("usage.call_output", { value: count(call.output_tokens) })}</p><p>{t("usage.trigger", { content: call.trigger_content || t("usage.trigger_none") })}</p></details>)}</div>}
        {selectedId !== "all" && <>
        <h4>{t("usage.contexts_title")}</h4>
        {!rows.length ? <p className="usage-empty">{t("usage.no_contexts")}</p> : rows.map(row => {
          const remaining = Math.max(0, row.compact_at - row.estimated_input);
          const fraction = row.compact_at > 0 ? Math.min(100, row.estimated_input / row.compact_at * 100) : 0;
          return <article className="usage-context" key={`${row.conversation_id}:${row.bot_id}`}>
            <div className="usage-context-heading"><strong>{row.conversation_name}</strong><time dateTime={row.updated_at}>{new Intl.DateTimeFormat(intlLocale(), { timeZone: timezone, month:"numeric", day:"numeric", hour:"2-digit", minute:"2-digit" }).format(new Date(row.updated_at))}</time></div>
            <div className="usage-context-model">{row.model}{!row.window_known && ` · ${t("usage.window_estimated")}`}{row.run_status === "running" && ` · ${t("usage.running")}`}</div>
            <div className="usage-context-meter" role="meter" aria-label={t("usage.meter_label")} aria-valuemin={0} aria-valuemax={row.compact_at} aria-valuenow={Math.min(row.estimated_input, row.compact_at)}><span style={{ width: `${fraction}%` }} /></div>
            <div className="usage-context-numbers"><span><Trans t={t} i18nKey="usage.estimated" values={{ value: count(row.estimated_input) }} components={{ strong: <strong /> }} /></span><span><Trans t={t} i18nKey="usage.remaining" values={{ value: count(remaining) }} components={{ strong: <strong /> }} /></span></div>
            <p>{[t("usage.last_input", { value: row.last_input ? count(row.last_input) : t("usage.not_reported") }), t("usage.window", { value: count(row.window_tokens) }), t("usage.auto_compact"), ...(row.compact_count ? [t("usage.compacted", { count: row.compact_count })] : [])].join(" · ")}</p>
          </article>;
        })}
        </>}
      </div>
    </div>}
  </section>;
}
