import { scheduleDisplay } from "./displayMetadata";
import { CATS, EmptyState } from "./EmptyCat";
import { ScheduleEditor } from "./ScheduleEditor";
import { DelayedFeedback } from "./DelayedFeedback";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, ApiError } from "./api";
import { TofiIcon } from "./icons";
import { BotAvatar } from "./BotAvatar";
import { ConfirmAction, Disclosure } from "./InteractionSystem";
import { TimezoneSelect, useUserTimezone } from "./UserTimezone";
import { formatZonedTime } from "./timezone";
import { WorkExecution } from "./WorkExecution";
import { workExecutionActive, workExecutionStatusKeys } from "./workExecutionState";
import { i18n, useTranslation } from "./i18n";
import type { Bot, Conversation, Schedule, ScheduleKind, WorkItem, WorkStatus } from "./types";
import "./work-panel.css";

type Scope = { id: string; type: "bots" | "conversations" };
const workStatusKeys = { todo: "status.todo", in_progress: "status.in_progress", blocked: "status.blocked", review: "status.review", done: "status.done", cancelled: "status.cancelled" } as const satisfies Record<WorkStatus, string>;
const message = (cause: unknown) => cause instanceof Error ? cause.message : i18n.t("work:panel.load_failed");

function useAgenda(scope: Scope, history: boolean, refreshToken: number) {
  const [items, setItems] = useState<WorkItem[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const version = useRef(0);
  // Retired callbacks cannot restart a fetch and supersede the current view,
  // even when an earlier mutation finishes after the user changes tabs.
  const source = useMemo(() => ({ active: false }), [scope.id, scope.type, history]);
  const lastRefresh = useRef(refreshToken);
  const reload = useCallback(async () => {
    if (!source.active) return;
    const current = ++version.current;
    try {
      const [work, plans] = await Promise.all([api.workItems(scope.id, scope.type, history), api.scheduleAgenda(scope.id, scope.type, history)]);
      if (!source.active || current !== version.current) return;
      setItems(work.work_items ?? []); setSchedules(plans.schedules ?? []); setError("");
    } catch (cause) { if (source.active && current === version.current) setError(message(cause)); }
    finally { if (source.active && current === version.current) setLoading(false); }
  }, [scope.id, scope.type, history, source]);
  useEffect(() => {
    source.active = true; setLoading(true); setItems([]); setSchedules([]); setError(""); void reload();
    const timer = window.setInterval(() => { if (!document.hidden) void reload(); }, 5000);
    const focus = () => { if (!document.hidden) void reload(); };
    window.addEventListener("focus", focus); document.addEventListener("visibilitychange", focus);
    return () => { source.active = false; version.current++; clearInterval(timer); window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); };
  }, [reload, source]);
  useEffect(() => {
    if (lastRefresh.current === refreshToken) return;
    lastRefresh.current = refreshToken;
    void reload();
  }, [refreshToken, reload]);
  const accept = (item: WorkItem) => {
    if (!source.active) return;
    version.current++;
    setItems(current => current.map(value => value.id === item.id ? item : value));
  };
  return { items, schedules, loading, error, reload, accept };
}

export function WorkPreview({ scope, refreshToken, onOpen, disabled = false, leading }: { disabled?: boolean; scope: Scope; refreshToken: number; onOpen: () => void; leading?: ReactNode }) {
  const { t } = useTranslation("work");
  const { items, schedules, loading, error } = useAgenda(scope, false, refreshToken);
  const total = items.length + schedules.length;
  const titles = [...items.map(item => item.title), ...schedules.map(item => scheduleDisplay(item).title)].slice(0, 2);
  return <button className="work-preview" disabled={disabled} onClick={onOpen}>{leading}<span className="work-preview-copy"><strong>{t("preview.title")} <span>{loading ? "" : total}</span></strong><small>{error ? t("preview.sync_failed") : loading ? "" : titles.length ? titles.join(" · ") : t("preview.empty")}</small></span><span aria-hidden="true">›</span></button>;
}

type Props = { conversation: Conversation; conversations: Conversation[]; bots: Bot[]; refreshToken: number; onClose: () => void; onNavigate: (id: string) => void };

export function WorkPanel(props: Props) {
  return <WorkPanelView key={props.conversation.id} {...props} />;
}

function WorkPanelView({ conversation, conversations, bots, refreshToken, onClose, onNavigate }: Props) {
  const { t } = useTranslation("work");
  const botView = conversation.kind === "dm";
  const scope: Scope = { type: botView ? "bots" : "conversations", id: botView ? conversation.bot_id! : conversation.id };
  const [history, setHistory] = useState(false);
  const [member, setMember] = useState("");
  const [creating, setCreating] = useState(false);
  const [creatingBusy, setCreatingBusy] = useState(false);
  const [pending, setPending] = useState("");
  const [operationError, setOperationError] = useState<{ text: string; id: string; retry: () => Promise<unknown> } | null>(null);
  const operationView = useMemo(() => ({ active: false }), [conversation.id, conversation.archived, history, member]);
  const writing = useRef(false);
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    operationView.active = true; setOperationError(null);
    return () => { operationView.active = false; };
  }, [operationView]);
  const { items, schedules, loading, error, reload, accept } = useAgenda(scope, history, refreshToken);
  const latest = useRef({ conversation, conversations, bots, items });
  latest.current = { conversation, conversations, bots, items };
  const currentReload = useRef(reload);
  currentReload.current = reload;
  const preference = useUserTimezone();
  const visibleItems = items.filter(item => !member || item.bot_id === member);
  const visibleSchedules = schedules.filter(item => !member || item.bot_id === member);
  const goals = visibleItems.filter(item => item.kind === "goal");
  const tasks = visibleItems.filter(item => item.kind === "task");
  const lookupBot = (id: string, fallback?: string) => bots.find(bot => bot.id === id)?.name || fallback || "Bot";
  const lookupSource = (id: string, fallback?: string) => conversations.find(value => value.id === id)?.name || fallback || t("panel.original_conversation");
  async function mutate(id: string, action: () => Promise<unknown>) {
    if (writing.current || !operationView.active || latest.current.conversation.archived) return;
    writing.current = true;
    setPending(id); setOperationError(null);
    try { await action(); if (mounted.current) await currentReload.current(); }
    // The editor retains its draft and explains conflicts. Replaying that stale
    // PATCH cannot resolve a conflict; the user must reopen the current record.
    catch (cause) { if (operationView.active && !(cause instanceof ApiError && cause.code === "edit_conflict")) setOperationError({ text: message(cause), id, retry: action }); }
    finally { writing.current = false; if (mounted.current) setPending(""); }
  }
  function itemReadOnly(item: WorkItem) {
    const current = latest.current;
    return current.conversation.archived || item.conversation_archived || item.bot_archived || current.bots.some(bot => bot.id === item.bot_id && bot.archived) || current.conversations.some(source => source.id === item.conversation_id && source.archived);
  }
  function updateStatus(item: WorkItem, status: WorkStatus) {
    return mutate(item.id, async () => {
      const current = latest.current.items.find(value => value.id === item.id);
      if (!current || itemReadOnly(current) || workExecutionActive(current)) return;
      accept(await api.updateWorkItem(item.id, { status }));
    });
  }
  function provenance(item: WorkItem | Schedule) {
    return <div className="work-provenance">{botView ? <button onClick={() => onNavigate(item.conversation_id)}>{lookupSource(item.conversation_id, item.conversation_name)}</button> : <span><BotAvatar id={item.bot_id} mini />{lookupBot(item.bot_id, item.bot_name)}</span>}</div>;
  }
  function renderItem(item: WorkItem) {
    const readOnly = itemReadOnly(item);
    return <article className={`work-row work-${item.status}`} key={item.id}>
      <button className="work-complete" aria-label={item.status === "done" ? t("panel.reopen_item", { title: item.title }) : t("panel.complete_item", { title: item.title })} disabled={!!pending || item.status === "cancelled" || readOnly || workExecutionActive(item)} onClick={() => void updateStatus(item, item.status === "done" ? "todo" : "done")}><span>{item.status === "done" && <TofiIcon name="check" size={16} variant="filled" />}</span></button>
      <div className="work-row-main"><strong>{item.title}</strong><div className="work-row-meta">{provenance(item)}{item.status !== "todo" && <span className={`work-state state-${item.status}`}>{t(workStatusKeys[item.status])}</span>}</div>
        {item.parent_goal_id && <small className="work-parent">{items.find(goal => goal.id === item.parent_goal_id)?.title || t("panel.linked_group_goal")}</small>}
        <details className="work-item-details"><summary>{t("panel.details")}</summary>{item.description && <p>{item.description}</p>}<small>{formatZonedTime(item.updated_at, preference.timezone)}</small><WorkExecution item={item} readOnly={!!readOnly} pending={!!pending} mutate={mutate} onExecuted={accept} />{!readOnly && <label>{t("panel.status")}<select aria-label={t("panel.status_aria", { title: item.title })} value={item.status} disabled={!!pending || workExecutionActive(item)} onChange={event => { const status = event.currentTarget.value as WorkStatus; void updateStatus(item, status); }}>{Object.entries(workStatusKeys).map(([value, key]) => <option key={value} value={value}>{t(key)}</option>)}</select></label>}</details>
      </div>
    </article>;
  }
  return <div className="detail-content work-panel">
    <div className="detail-heading"><div><h2>{botView ? t("panel.bot_heading", { name: conversation.name }) : t("panel.group_heading")}</h2></div><button className="close-button" aria-label={t("panel.close")} disabled={creatingBusy} onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    <div className="work-toolbar"><div className="work-tabs" role="tablist" aria-label={t("panel.scope")}><button role="tab" aria-selected={!history} onClick={() => { setHistory(false); setOperationError(null); }}>{t("panel.tab_open")}</button><button role="tab" aria-selected={history} onClick={() => { setHistory(true); setOperationError(null); }}>{t("panel.tab_history")}</button></div><button className="ghost-button work-add" aria-label={creating ? t("panel.cancel_new") : t("panel.new")} disabled={conversation.archived || creatingBusy} onClick={() => setCreating(value => !value)}><TofiIcon name={creating ? "close" : "plus"} size={20} /></button></div>
    {!botView && <div className="work-member-select"><select className="work-member-filter" aria-label={t("panel.member_filter")} value={member} onChange={event => setMember(event.target.value)}><option value="">{t("panel.all_members")}</option>{conversation.bot_ids.map(id => <option key={id} value={id}>{lookupBot(id)}</option>)}</select><TofiIcon name="chevron-down" size={16} aria-hidden="true" /></div>}
    {creating && <WorkCreate onBusyChange={setCreatingBusy} conversation={conversation} conversations={conversations} bots={bots} goals={items.filter(item => item.kind === "goal")} onDone={() => { setCreating(false); setHistory(false); void reload(); }} onCancel={() => setCreating(false)} />}
    {operationError && <div className="error-banner" role="alert">{operationError.text}<button className="text-button" disabled={!!pending} onClick={() => void mutate(operationError.id, operationError.retry)}>{t("panel.retry_action")}</button></div>}
    {error && <div className="error-banner" role="alert">{error}<button className="text-button" disabled={!!pending} onClick={() => void reload()}>{t("panel.reload")}</button></div>}
    {loading && <DelayedFeedback><div className="inline-state">{t("panel.loading")}</div></DelayedFeedback>}
    {!loading && !error && !goals.length && !tasks.length && !visibleSchedules.length && <EmptyState name={history ? "work-history-empty" : "work-empty"} className="work-empty" look={history ? CATS.kiwi : CATS.taro} pose={history ? "asleep" : "curious"}><p>{history ? t("panel.empty_history") : t("panel.empty")}</p></EmptyState>}
    {!!goals.length && <section className="work-section"><h3>{t("panel.goals")} <span>{goals.length}</span></h3>{goals.map(renderItem)}</section>}
    {!!tasks.length && <section className="work-section"><h3>{t("panel.tasks")} <span>{tasks.length}</span></h3>{tasks.map(renderItem)}</section>}
    {!!visibleSchedules.length && <section className="work-section"><h3>{history ? t("panel.schedules_ended") : t("panel.schedules")}<span>{preference.timezone}</span></h3><div className="work-timeline">{visibleSchedules.map(item => {
      const attention = item.execution_status === "failed" || item.execution_status === "interrupted";
      const waiting = item.execution_status === "waiting";
      const label = waiting ? t(workExecutionStatusKeys.waiting) : item.status === "paused" ? t("panel.paused") : item.execution_status && (item.kind === "once" || attention || ["running", "queued"].includes(item.execution_status)) ? t(workExecutionStatusKeys[item.execution_status]) : item.kind === "daily" ? t("panel.daily_at", { time: item.daily_time }) : item.kind === "interval" ? t("panel.every_minutes", { count: Math.round((item.interval_seconds || 0) / 60) }) : t("panel.once");
      const display = scheduleDisplay(item);
      return <article className={`work-schedule${attention ? " needs-attention" : ""}`} key={item.id}><time dateTime={item.next_at_utc}>{formatZonedTime(item.next_at_utc, preference.timezone)}</time><strong>{display.title}</strong><p className="work-schedule-description">{display.description}</p><div className="work-row-meta">{provenance(item)}<span className="work-state">{label}</span></div><details className="work-item-details"><summary>{t("panel.manage")}</summary><div className="schedule-instructions"><strong>{t("panel.instructions")}</strong><p>{item.content}</p></div>{!(conversation.archived || item.conversation_archived || item.bot_archived) && <ScheduleEditor schedule={item} disabled={!!pending} onSave={action => mutate(item.id, action)} />}<div className="work-actions">{!(conversation.archived || item.conversation_archived || item.bot_archived) && <>{item.status === "active" && <button disabled={!!pending} onClick={() => void mutate(item.id, () => api.pauseSchedule(item.id))}>{t("panel.pause")}</button>}{item.status === "paused" && <button disabled={!!pending} onClick={() => void mutate(item.id, () => api.resumeSchedule(item.id))}>{t("panel.resume")}</button>}{waiting && item.last_run_id && <button disabled={!!pending} onClick={() => void mutate(item.id, () => api.cancelRun(item.last_run_id!))}>{t("panel.stop_turn")}</button>}{attention && item.last_run_id && <button disabled={!!pending} onClick={() => void mutate(item.id, () => api.retryRun(item.last_run_id!))}>{t("panel.rerun")}</button>}<ConfirmAction label={t("panel.delete")} question={t("panel.delete_confirm")} disabled={!!pending} onConfirm={() => mutate(item.id, () => api.deleteSchedule(item.id))} /></>}</div>{item.timezone !== preference.timezone && <small>{t("panel.schedule_zone", { zone: item.timezone })}</small>}</details></article>;
    })}</div></section>}
  </div>;
}

function WorkCreate({ conversation, conversations, bots, goals, onDone, onCancel, onBusyChange }: Pick<Props, "conversation" | "conversations" | "bots"> & { goals: WorkItem[]; onDone: () => void; onCancel: () => void; onBusyChange: (busy: boolean) => void }) {
  const { t } = useTranslation("work");
  const botView = conversation.kind === "dm";
  const sources = botView ? conversations.filter(value => !value.archived && (value.bot_id === conversation.bot_id || value.bot_ids.includes(conversation.bot_id!))) : [conversation];
  const [sourceID, setSourceID] = useState(conversation.id);
  const source = sources.find(value => value.id === sourceID) || conversation;
  const [type, setType] = useState<"task" | "goal" | "schedule">("task");
  const [title, setTitle] = useState("");
  const [scheduleTitle, setScheduleTitle] = useState("");
  const [description, setDescription] = useState("");
  const [parent, setParent] = useState("");
  const [botID, setBotID] = useState(conversation.bot_id || conversation.bot_ids[0] || "");
  const [kind, setKind] = useState<ScheduleKind>("once");
  const [runAt, setRunAt] = useState("");
  const [dailyTime, setDailyTime] = useState("09:00");
  const [interval, setInterval] = useState("60");
  const preference = useUserTimezone();
  const [zone, setZone] = useState("");
  const timezone = zone || preference.timezone;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const members = bots.filter(bot => !bot.archived && (source.kind === "dm" ? bot.id === source.bot_id : source.bot_ids.includes(bot.id)));
  const goalOptions = goals.filter(goal => goal.conversation_id === source.id && !["done", "cancelled"].includes(goal.status));
  async function create(event: React.FormEvent) {
    event.preventDefault(); if (busy || !title.trim()) return;
    setBusy(true); onBusyChange(true); setError("");
    try {
      if (type === "schedule") await api.createSchedule(sourceID, { bot_id: botID, title: scheduleTitle.trim(), description: description.trim(), content: title, kind, timezone, ...(kind === "daily" ? { daily_time: dailyTime } : { run_at: runAt }), ...(kind === "interval" ? { interval_seconds: Math.round(Number(interval) * 60) } : {}) });
      else await api.createWorkItem(sourceID, { bot_id: botID, kind: type, title: title.trim(), description: description.trim(), ...(type === "task" && parent ? { parent_goal_id: parent } : {}) });
      onDone();
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); onBusyChange(false); }
  }
  return <form aria-busy={busy} className="work-create" onSubmit={event => void create(event)}><fieldset disabled={busy} style={{ display: "contents", border: 0, padding: 0, margin: 0, minWidth: 0 }}><div className="work-create-kind" role="group" aria-label={t("create.type_aria")}>{(["task", "goal", "schedule"] as const).map(value => <button key={value} type="button" aria-pressed={type === value} onClick={() => setType(value)}>{t(`create.type.${value}`)}</button>)}</div>
    <label>{t(`create.content_label.${type}`)}<textarea autoFocus aria-label={t("create.content_aria")} maxLength={type === "schedule" ? 32000 : 240} value={title} onChange={event => setTitle(event.target.value)} rows={2} required /></label>
    {botView && <label>{t("create.source")}<select value={sourceID} onChange={event => { setSourceID(event.target.value); setParent(""); }} aria-label={t("create.source_aria")}>{sources.map(value => <option key={value.id} value={value.id}>{value.kind === "dm" ? t("create.direct") : value.name}</option>)}</select></label>}
    {!botView && <label>{t("create.owner")}<select aria-label={t("create.owner_aria")} value={botID} onChange={event => setBotID(event.target.value)}>{members.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>}
    {type === "schedule" ? <><label>{t("create.schedule_title")}<input aria-label={t("create.schedule_title")} required maxLength={120} value={scheduleTitle} onChange={event => setScheduleTitle(event.target.value)} placeholder={t("create.schedule_title_placeholder")} /></label><label>{t("create.schedule_description")}<textarea aria-label={t("create.schedule_description")} maxLength={280} required value={description} onChange={event => setDescription(event.target.value)} rows={2} /></label><label>{t("create.repeat")}<select aria-label={t("create.repeat_aria")} value={kind} onChange={event => setKind(event.target.value as ScheduleKind)}><option value="once">{t("create.repeat_once")}</option><option value="daily">{t("create.repeat_daily")}</option><option value="interval">{t("create.repeat_interval")}</option></select></label><label>{kind === "daily" ? t("create.daily_time") : t("create.run_time")}{kind === "daily" ? <input type="time" required value={dailyTime} onChange={event => setDailyTime(event.target.value)} /> : <input type="datetime-local" required value={runAt} onChange={event => setRunAt(event.target.value)} />}</label>{kind === "interval" && <label>{t("create.interval_minutes")}<input type="number" min="1" max="525600" value={interval} onChange={event => setInterval(event.target.value)} required /></label>}<Disclosure title={t("create.timezone", { zone: timezone })}><TimezoneSelect value={timezone} onChange={setZone} /></Disclosure></> : <Disclosure title={t("create.more")}><label>{t("create.description")}<textarea aria-label={t("create.description_aria")} rows={3} value={description} onChange={event => setDescription(event.target.value)} /></label>{type === "task" && !!goalOptions.length && <label>{t("create.parent_goal")}<select value={parent} onChange={event => setParent(event.target.value)}><option value="">{t("create.no_goal")}</option>{goalOptions.map(goal => <option key={goal.id} value={goal.id}>{goal.title}</option>)}</select></label>}</Disclosure>}
    {error && <p className="error-text" role="alert">{error}</p>}<div className="work-create-actions"><button className="text-button" type="button" onClick={onCancel}>{t("create.cancel")}</button><button className="primary-button" disabled={busy || !title.trim() || !botID || (type === "schedule" && (!preference.ready || !scheduleTitle.trim() || !description.trim()))}>{busy ? t("create.saving") : t("create.add")}</button></div>
  </fieldset></form>;
}
