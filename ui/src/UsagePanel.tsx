import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { api } from "./api";
import { GazeAvatar } from "./GazeAvatar";
import { useSettingsActive } from "./settingsActivity";
import { isDesktop } from "./desktop";
import type { AgentUsageTotal, ContextUsage, UsageCall, UsagePeriod } from "./types";
import "./usage-panel.css";

const number = new Intl.NumberFormat("zh-CN");
const count = (value: number) => number.format(value);
const money = (value: number) => value < 0.01 && value > 0 ? `<$0.01` : `$${value.toFixed(2)}`;
const ranges = [["24h", "过去 24 小时"], ["7d", "过去 7 天"], ["30d", "过去 30 天"]] as const;
const fail = (cause: unknown) => cause instanceof Error ? cause.message : "读取失败，请重试";

function UsageReel({text,rolled}:{text:string;rolled:boolean}) {
 if(isDesktop)return <>{text}</>;
 return <span className="web-usage-number" aria-label={text}>{Array.from(text).map((char,index)=>/\d/.test(char)?<span className="web-usage-col" aria-hidden="true" key={index}><span className="web-usage-reel" style={{"--d":rolled?Number(char)+10:0,"--delay":`${(text.length-index)*45}ms`} as CSSProperties}>{Array.from("01234567890123456789").map((face,at)=><span key={at}>{face}</span>)}</span></span>:<span aria-hidden="true" key={index}>{char}</span>)}</span>;
}

export function UsagePanel({ preferredBotId, timezone }: { preferredBotId?: string; timezone: string }) {
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
    return { key, label: new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, weekday: "short" }).format(date), requests: calls.filter(call => (selectedId === "all" || call.bot_id === selectedId) && dayKey(new Date(call.occurred_at)) === key).length };
  });
  const chartMax = Math.max(1, ...recentDays.map(day => day.requests));

  return <section className="usage-page" aria-label="Usage">
    <p className="usage-explanation">记录从功能上线后开始。参考等值按公开 API 单价估算，并非 Codex 实际收费或节省金额。缓存命中量不可得，输入全部按非缓存价计算；不含摘要请求与工具费用。</p>
    {error && <div className="usage-error" role="alert">{error}<button type="button" onClick={() => void load()}>重试</button></div>}
    {loading ? <p role="status">正在读取 Usage…</p> : agents.length === 0 ? <p className="usage-empty">还没有 Agent。</p> : <div className="usage-layout">
      <nav className="usage-agents" aria-label="选择 Agent"><button type="button" aria-current={selectedId === "all" ? "true" : undefined} onClick={() => setSelected("all")}><span><strong>全部 Bot</strong><small>合并用量</small></span></button>{agents.map(agent => <button type="button" key={agent.bot_id} aria-current={selectedId === agent.bot_id ? "true" : undefined} onClick={() => setSelected(agent.bot_id)}><GazeAvatar id={agent.bot_id} mini /><span><strong>{agent.bot_name}</strong><small>{agent.runs ? `${count(agent.runs)} 次运行` : "尚无记录"}</small></span></button>)}</nav>
      <div className={`usage-detail${rolled ? " is-rolled" : ""}`} ref={detailRef}>
        <div className="usage-summary"><h3>{selectedId === "all" ? "全部 Bot" : current?.bot_name ?? "Agent"}</h3><div className="usage-ranges" aria-label="统计时段">{ranges.map(([key, label]) => <button key={key} type="button" aria-pressed={range === key} onClick={() => setRange(key)}>{label}</button>)}</div><div><span><small>输入 token</small><strong><UsageReel text={count(total.input)} rolled={rolled}/></strong></span><span><small>输出 token</small><strong><UsageReel text={count(total.output)} rolled={rolled}/></strong></span><span><small>模型请求</small><strong><UsageReel text={count(total.requests)} rolled={rolled}/></strong></span><span><small>API 参考等值</small><strong><UsageReel text={total.unpriced === total.requests && total.requests ? "暂无报价" : money(total.usd)} rolled={rolled}/></strong></span></div>{total.unpriced > 0 && <p>有 {count(total.unpriced)} 次请求的模型缺少可核实报价，未计入金额。</p>}<p>按 2026-09-25 <a href="https://developers.openai.com/api/docs/pricing" target="_blank" rel="noreferrer">OpenAI 官方 API 报价</a>计算；30 天视图按小时汇总，边界可能相差不足一小时。</p></div>
        {!isDesktop && <div className="web-usage-chart"><h4>最近 7 天请求</h4><div className="web-usage-bars" aria-label="最近 7 天请求次数">{recentDays.map((day,index)=><span className="web-usage-bar" key={day.key}><i style={{"--h":`${day.requests/chartMax*100}%`,"--delay":`${200+index*60}ms`} as CSSProperties} title={`${day.label} ${day.requests} 次请求`}/><small>{day.label}</small></span>)}</div></div>}
        <h4>逐次请求 · 保留 7 天</h4>
        {!visibleCalls.length ? <p className="usage-empty">该时段没有可显示的请求。新请求完成后会记录到这里。</p> : <div className="usage-call-list">{visibleCalls.map(call => <details key={call.id} className="usage-call"><summary><time dateTime={call.occurred_at}>{new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, month:"numeric", day:"numeric", hour:"2-digit", minute:"2-digit" }).format(new Date(call.occurred_at))}</time><span>{call.bot_name} · {call.conversation_name}</span><strong>{call.price_known ? money(call.equivalent_usd) : "暂无报价"}</strong></summary><p>{call.model} · 输入 {count(call.input_tokens)} · 输出 {count(call.output_tokens)}</p><p>触发内容：{call.trigger_content || "系统任务或续接，无直接用户消息"}</p></details>)}</div>}
        {selectedId !== "all" && <>
        <h4>各对话的最近一次上下文</h4>
        {!rows.length ? <p className="usage-empty">还没有可显示的上下文记录。Agent 下一次运行后会开始记录。</p> : rows.map(row => {
          const remaining = Math.max(0, row.compact_at - row.estimated_input);
          const fraction = row.compact_at > 0 ? Math.min(100, row.estimated_input / row.compact_at * 100) : 0;
          return <article className="usage-context" key={`${row.conversation_id}:${row.bot_id}`}>
            <div className="usage-context-heading"><strong>{row.conversation_name}</strong><time dateTime={row.updated_at}>{new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, month:"numeric", day:"numeric", hour:"2-digit", minute:"2-digit" }).format(new Date(row.updated_at))}</time></div>
            <div className="usage-context-model">{row.model}{!row.window_known && " · 模型窗口为默认估算"}{row.run_status === "running" && " · 运行中"}</div>
            <div className="usage-context-meter" role="meter" aria-label="距自动压缩阈值的上下文占用" aria-valuemin={0} aria-valuemax={row.compact_at} aria-valuenow={Math.min(row.estimated_input, row.compact_at)}><span style={{ width: `${fraction}%` }} /></div>
            <div className="usage-context-numbers"><span>请求前估算 <strong>{count(row.estimated_input)}</strong></span><span>距压缩约 <strong>{count(remaining)}</strong></span></div>
            <p>上次模型实际输入 {row.last_input ? count(row.last_input) : "未报告"} · 本地窗口配置 {count(row.window_tokens)} · 80% 时尝试自动压缩{row.compact_count ? ` · 本次运行已压缩 ${row.compact_count} 次` : ""}</p>
          </article>;
        })}
        </>}
      </div>
    </div>}
  </section>;
}
