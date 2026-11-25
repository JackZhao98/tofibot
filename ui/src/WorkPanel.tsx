import { TextArea } from "./ds/TextArea/TextArea";
import { TextInput } from "./ds/TextInput/TextInput";
import { Button } from "./ds/Button/Button";
import { IconButton } from "./ds/IconButton/IconButton";
import { Segmented } from "./ds/Segmented/Segmented";
import { Badge, type BadgeTone } from "./ds/Badge/Badge";
import { Banner } from "./ds/Banner/Banner";
import { Card } from "./ds/Card/Card";
import { Checkbox } from "./ds/Checkbox/Checkbox";
import { Field } from "./ds/Field/Field";
import { ScheduleCard } from "./ds/ScheduleCard/ScheduleCard";
import { Tag } from "./ds/Tag/Tag";
import * as Tabs from "@radix-ui/react-tabs";
import { scheduleDisplay } from "./displayMetadata";
import { CATS, EmptyState } from "./EmptyCat";
import { ScheduleEditor } from "./ScheduleEditor";
import { ScheduleDeleteButton, ScheduleIconButton } from "./ScheduleActions";
import { ScheduleTimingFields, defaultTimingDraft, timingCreateFields, type TimingDraft } from "./ScheduleTimingFields";
import { DelayedFeedback } from "./DelayedFeedback";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { TofiIcon } from "./icons";
import { BotAvatar } from "./BotAvatar";
import { ConfirmAction } from "./ConfirmAction";
import { Disclosure } from "./InteractionPrimitives";
import { useUserTimezone } from "./UserTimezone";
import { dateTimeFormat, formatZonedTime } from "./timezone";
import { formatClock, intlLocale } from "./i18n/format";
import { WorkExecution } from "./WorkExecution";
import { WorkTaskFacts } from "./WorkTaskFacts";
import { workExecutionActive, workExecutionStatusKeys } from "./workExecutionState";
import { useTranslation } from "./i18n";
import { type Bot, type Conversation, type Schedule, type WorkItem, type WorkStatus } from "./types";
import { weeklyCadence } from "./scheduleCadence";
import { message, useAgenda, type Scope } from "./workAgenda";
import "./work-panel.css";
import { Select } from "./Select";

const workStatusKeys = { todo: "status.todo", in_progress: "status.in_progress", blocked: "status.blocked", review: "status.review", input_required: "status.input_required", done: "status.done", cancelled: "status.cancelled", failed: "status.failed" } as const satisfies Record<WorkStatus, string>;
const statusTones = { todo: "neutral", in_progress: "working", blocked: "error", review: "needs", input_required: "needs", done: "done", cancelled: "neutral", failed: "error" } as const satisfies Record<WorkStatus, BadgeTone>;
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
  const [goalFilter, setGoalFilter] = useState("");
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
  // A group's goals filter its list as the team board's goal filter did: the goal and the to-dos under it.
  const allGoals = items.filter(item => item.kind === "goal");
  const goal = allGoals.some(item => item.id === goalFilter) ? goalFilter : "";
  const visibleItems = items.filter(item => (!member || item.bot_id === member) && (!goal || item.id === goal || item.parent_goal_id === goal));
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
  function updateAssignee(item: WorkItem, botID: string) {
    return mutate(item.id, async () => {
      const current = latest.current.items.find(value => value.id === item.id);
      if (!current || current.bot_id === botID || itemReadOnly(current) || workExecutionActive(current) || !latest.current.bots.some(bot => bot.id === botID && !bot.archived)) return;
      accept(await api.updateWorkItem(item.id, { bot_id: botID }));
    });
  }
  /** Members who can take a group's item over: the active Bots of the group, plus the current holder so the field never reads empty. */
  function assignees(item: WorkItem) {
    const ids = conversation.bot_ids.filter(id => bots.some(bot => bot.id === id && !bot.archived));
    return ids.includes(item.bot_id) ? ids : [item.bot_id, ...ids];
  }
  function provenance(item: WorkItem | Schedule) {
    return botView
      ? <Button variant="ghost" size="sm" className="work-provenance" onClick={() => onNavigate(item.conversation_id)}>{lookupSource(item.conversation_id, item.conversation_name)}</Button>
      : <Tag className="work-provenance"><BotAvatar id={item.bot_id} mini />{lookupBot(item.bot_id, item.bot_name)}</Tag>;
  }
  function renderItem(item: WorkItem) {
    const readOnly = itemReadOnly(item);
    const locked = !!pending || workExecutionActive(item);
    return <article className={`work-row work-${item.status}`} key={item.id}>
      <Checkbox className="work-complete" aria-label={item.status === "done" ? t("panel.reopen_item", { title: item.title }) : t("panel.complete_item", { title: item.title })} checked={item.status === "done"} disabled={!!pending || item.status === "cancelled" || item.status === "failed" || readOnly || workExecutionActive(item)} onChange={checked => void updateStatus(item, checked ? "done" : "todo")} />
      <div className="work-row-main"><strong>{item.status === "done" ? <s>{item.title}</s> : item.title}</strong><div className="work-row-meta">{provenance(item)}{item.status !== "todo" && <Badge tone={statusTones[item.status]}>{t(workStatusKeys[item.status])}</Badge>}</div>
        <WorkTaskFacts item={item} timezone={preference.timezone} />
        {item.parent_goal_id && <Tag className="work-parent">{items.find(goal => goal.id === item.parent_goal_id)?.title || t("panel.linked_group_goal")}</Tag>}
        <Disclosure title={t("panel.details")}><div className="work-item-details">{item.description && <p>{item.description}</p>}<small className="tb-mono">{formatZonedTime(item.updated_at, preference.timezone)}</small><WorkExecution item={item} readOnly={!!readOnly} pending={!!pending} mutate={mutate} onExecuted={accept} />
          {!readOnly && <Field label={t("panel.status")}><Select size="sm" aria-label={t("panel.status_aria", { title: item.title })} value={item.status} disabled={locked} onValueChange={value => { const status = value as WorkStatus; void updateStatus(item, status); }}>{Object.entries(workStatusKeys).filter(([value]) => value !== "input_required" && value !== "failed" || value === item.status).map(([value, key]) => <option key={value} value={value}>{t(key)}</option>)}</Select></Field>}
          {!readOnly && !botView && <Field label={t("panel.assignee")}><Select size="sm" aria-label={t("panel.assignee_aria", { title: item.title })} value={item.bot_id} disabled={locked} onValueChange={value => void updateAssignee(item, value)}>{assignees(item).map(id => <option key={id} value={id}>{lookupBot(id, id === item.bot_id ? item.bot_name : undefined)}</option>)}</Select></Field>}
        </div></Disclosure>
      </div>
    </article>;
  }
  return <Tabs.Root value={history ? "history" : "open"} onValueChange={value => { setHistory(value === "history"); setOperationError(null); }} asChild><div className="detail-content work-panel">
    <div className="detail-heading"><div><h2>{botView ? t("panel.bot_heading", { name: conversation.name }) : t("panel.group_heading")}</h2></div><button className="close-button" aria-label={t("panel.close")} disabled={creatingBusy} onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    <div className="work-toolbar"><Tabs.List className="tb-tablist work-tabs" aria-label={t("panel.scope")}><Tabs.Trigger value="open" className="tb-tab">{t("panel.tab_open")}</Tabs.Trigger><Tabs.Trigger value="history" className="tb-tab">{t("panel.tab_history")}</Tabs.Trigger></Tabs.List><IconButton icon={creating ? "close" : "plus"} label={creating ? t("panel.cancel_new") : t("panel.new")} disabled={conversation.archived || creatingBusy} onClick={() => setCreating(value => !value)} /></div>
    <Tabs.Content value={history ? "open" : "history"} forceMount hidden />
    <Tabs.Content value={history ? "history" : "open"}>
    {!botView && <div className="work-member-select"><Select size="sm" className="work-member-filter" aria-label={t("panel.member_filter")} value={member} onValueChange={value => setMember(value)}><option value="">{t("panel.all_members")}</option>{conversation.bot_ids.map(id => <option key={id} value={id}>{lookupBot(id)}</option>)}</Select>{!!allGoals.length && <Select size="sm" className="work-member-filter work-goal-filter" aria-label={t("panel.goal_filter")} value={goal} onValueChange={value => setGoalFilter(value)}><option value="">{t("panel.all_goals")}</option>{allGoals.map(item => <option key={item.id} value={item.id}>{item.title}</option>)}</Select>}</div>}
    {creating && <WorkCreate onBusyChange={setCreatingBusy} conversation={conversation} conversations={conversations} bots={bots} goals={items.filter(item => item.kind === "goal")} onDone={() => { setCreating(false); setHistory(false); void reload(); }} onCancel={() => setCreating(false)} />}
    {operationError && <Banner tone="error" className="work-banner" action={<Button variant="ghost" size="sm" disabled={!!pending} onClick={() => void mutate(operationError.id, operationError.retry)}>{t("panel.retry_action")}</Button>}>{operationError.text}</Banner>}
    {error && <Banner tone="error" className="work-banner" action={<Button variant="ghost" size="sm" disabled={!!pending} onClick={() => void reload()}>{t("panel.reload")}</Button>}>{error}</Banner>}
    {loading && <DelayedFeedback><div className="inline-state">{t("panel.loading")}</div></DelayedFeedback>}
    {!loading && !error && !goals.length && !tasks.length && !visibleSchedules.length && <EmptyState name={history ? "work-history-empty" : "work-empty"} className="work-empty" look={history ? CATS.kiwi : CATS.taro} pose={history ? "asleep" : "curious"}><p>{history ? t("panel.empty_history") : t("panel.empty")}</p></EmptyState>}
    {!!goals.length && <section className="work-section"><h3>{t("panel.goals")} <span>{goals.length}</span></h3>{goals.map(renderItem)}</section>}
    {!!tasks.length && <section className="work-section"><h3>{t("panel.tasks")} <span>{tasks.length}</span></h3>{tasks.map(renderItem)}</section>}
    {!!visibleSchedules.length && <section className="work-section"><h3>{history ? t("panel.schedules_ended") : t("panel.schedules")}<span>{preference.timezone}</span></h3><div className="work-timeline">{visibleSchedules.map(item => {
      const attention = item.execution_status === "failed" || item.execution_status === "interrupted";
      const waiting = item.execution_status === "waiting";
      const running = item.execution_status === "running" || item.execution_status === "queued";
      const label = waiting ? t(workExecutionStatusKeys.waiting) : item.status === "paused" ? t("panel.paused") : item.execution_status && (item.kind === "once" || attention || running) ? t(workExecutionStatusKeys[item.execution_status]) : item.kind === "daily" ? t("panel.daily_at", { time: item.daily_time }) : item.kind === "weekly" ? weeklyCadence(item.weekdays, item.daily_time) : item.kind === "interval" ? t("panel.every_minutes", { count: Math.round((item.interval_seconds || 0) / 60) }) : t("panel.once");
      const display = scheduleDisplay(item);
      const next = Number.isFinite(Date.parse(item.next_at_utc));
      const open = !(conversation.archived || item.conversation_archived || item.bot_archived);
      const owner = botView ? lookupSource(item.conversation_id, item.conversation_name) : lookupBot(item.bot_id, item.bot_name);
      const actions = <>{item.status === "active" && <ScheduleIconButton icon="pause" label={t("panel.pause")} disabled={!!pending} onClick={() => void mutate(item.id, () => api.pauseSchedule(item.id))} />}{item.status === "paused" && <ScheduleIconButton icon="play" label={t("panel.resume")} disabled={!!pending} onClick={() => void mutate(item.id, () => api.resumeSchedule(item.id))} />}{waiting && item.last_run_id && <ScheduleIconButton icon="stop" label={t("panel.stop_turn")} disabled={!!pending} onClick={() => void mutate(item.id, () => api.cancelRun(item.last_run_id!))} />}{attention && item.last_run_id && <ScheduleIconButton icon="retry" label={t("panel.rerun")} disabled={!!pending} onClick={() => void mutate(item.id, () => api.retryRun(item.last_run_id!))} />}<ScheduleDeleteButton label={t("panel.delete")} question={t("panel.delete_confirm")} disabled={!!pending} onConfirm={() => mutate(item.id, () => api.deleteSchedule(item.id))} /></>;
      return <ScheduleCard key={item.id} className="work-schedule" state={attention ? "error" : waiting || running ? "run" : "done"} statusLabel={label} time={next ? formatClock(item.next_at_utc, { timeZone: preference.timezone }) : "--:--"} day={next ? dateTimeFormat(intlLocale(), { timeZone: preference.timezone, month: "short", day: "numeric" }).format(new Date(item.next_at_utc)) : undefined} title={display.title} meta={[owner, display.description].filter(Boolean).join(" · ")} bodyLabel={t("panel.instructions")}>
        <p className="schedule-instructions">{item.content}</p>
        {botView && provenance(item)}
        {open && <ScheduleEditor schedule={item} disabled={!!pending} onSave={action => mutate(item.id, action)} actions={actions} />}
        {item.timezone !== preference.timezone && <small>{t("panel.schedule_zone", { zone: item.timezone })}</small>}
      </ScheduleCard>;
    })}</div></section>}
    </Tabs.Content>
  </div></Tabs.Root>;
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
  const [timing, setTiming] = useState<TimingDraft>(defaultTimingDraft);
  const { kind, weekdays } = timing;
  const preference = useUserTimezone();
  const timezone = timing.zone || preference.timezone;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const members = bots.filter(bot => !bot.archived && (source.kind === "dm" ? bot.id === source.bot_id : source.bot_ids.includes(bot.id)));
  const goalOptions = goals.filter(goal => goal.conversation_id === source.id && !["done", "cancelled", "failed"].includes(goal.status));
  async function create(event: React.FormEvent) {
    event.preventDefault(); if (busy || !title.trim()) return;
    setBusy(true); onBusyChange(true); setError("");
    try {
      if (type === "schedule") await api.createSchedule(sourceID, { bot_id: botID, title: scheduleTitle.trim(), description: description.trim(), content: title, ...timingCreateFields(timing, timezone) });
      else await api.createWorkItem(sourceID, { bot_id: botID, kind: type, title: title.trim(), description: description.trim(), ...(type === "task" && parent ? { parent_goal_id: parent } : {}) });
      onDone();
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); onBusyChange(false); }
  }
  return <Card variant="quiet" className="work-create-card"><form aria-busy={busy} className="work-create" onSubmit={event => void create(event)}><fieldset disabled={busy} style={{ display: "contents", border: 0, padding: 0, margin: 0, minWidth: 0 }}><Segmented className="work-create-kind" label={t("create.type_aria")} value={type} onChange={setType} options={(["task", "goal", "schedule"] as const).map(value => ({ value, label: t(`create.type.${value}`) }))} />
    <label>{t(`create.content_label.${type}`)}<TextArea autoFocus aria-label={t("create.content_aria")} maxLength={type === "schedule" ? 32000 : 240} value={title} onChange={event => setTitle(event.target.value)} rows={2} required /></label>
    {botView && <label>{t("create.source")}<Select value={sourceID} onValueChange={value => { setSourceID(value); setParent(""); }} aria-label={t("create.source_aria")}>{sources.map(value => <option key={value.id} value={value.id}>{value.kind === "dm" ? t("create.direct") : value.name}</option>)}</Select></label>}
    {!botView && <label>{t("create.owner")}<Select aria-label={t("create.owner_aria")} value={botID} onValueChange={value => setBotID(value)}>{members.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</Select></label>}
    {type === "schedule" ? <><label>{t("create.schedule_title")}<TextInput aria-label={t("create.schedule_title")} required maxLength={120} value={scheduleTitle} onChange={event => setScheduleTitle(event.target.value)} placeholder={t("create.schedule_title_placeholder")} /></label><label>{t("create.schedule_description")}<TextArea aria-label={t("create.schedule_description")} maxLength={280} required value={description} onChange={event => setDescription(event.target.value)} rows={2} /></label><ScheduleTimingFields draft={timing} timezone={timezone} onChange={change => setTiming(current => ({ ...current, ...change }))} /></> : <Disclosure title={t("create.more")}><label>{t("create.description")}<TextArea aria-label={t("create.description_aria")} rows={3} value={description} onChange={event => setDescription(event.target.value)} /></label>{type === "task" && !!goalOptions.length && <label>{t("create.parent_goal")}<Select value={parent} onValueChange={value => setParent(value)}><option value="">{t("create.no_goal")}</option>{goalOptions.map(goal => <option key={goal.id} value={goal.id}>{goal.title}</option>)}</Select></label>}</Disclosure>}
    {error && <p className="error-text" role="alert">{error}</p>}<div className="work-create-actions"><Button variant="ghost" onClick={onCancel}>{t("create.cancel")}</Button><Button variant="primary" type="submit" disabled={busy || !title.trim() || !botID || (type === "schedule" && (!preference.ready || !scheduleTitle.trim() || !description.trim() || (kind === "weekly" && !weekdays.length)))}>{busy ? t("create.saving") : t("create.add")}</Button></div>
  </fieldset></form></Card>;
}
