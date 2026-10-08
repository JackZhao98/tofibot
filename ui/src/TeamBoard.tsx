import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "./api";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import { browserTimezone } from "./timezone";
import { WorkExecution } from "./WorkExecution";
import { workExecutionActive } from "./workExecutionState";
import type { Bot, Conversation, WorkItem, WorkStatus } from "./types";
import "./team-board.css";
import { intlLocale } from "./i18n/format";
import { i18n, useTranslation } from "./i18n";

const columns = ["todo", "in_progress", "blocked", "review", "done"] as const satisfies readonly WorkStatus[];

const errorText = (cause: unknown) => cause instanceof Error ? cause.message : i18n.t("work:board.action_failed");

type Props = {
  conversation: Conversation;
  bots: Bot[];
  onClose: () => void;
  onOpenWork: () => void;
  timezone?: string;
};

export function TeamBoard(props: Props) {
  return <TeamBoardView key={props.conversation.id} {...props} />;
}

function TeamBoardView({ conversation, bots, onClose, onOpenWork, timezone = browserTimezone() }: Props) {
  const { t } = useTranslation("work");
  const [items, setItems] = useState<WorkItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [operationError, setOperationError] = useState<{ text: string; retry: () => Promise<unknown> } | null>(null);
  const mounted = useRef(false);
  const writing = useRef(false);
  const version = useRef(0);
  const latest = useRef({ conversation, bots, items });
  latest.current = { conversation, bots, items };
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [member, setMember] = useState("");
  const [goal, setGoal] = useState("");
  const [creating, setCreating] = useState(false);
  const [newTitle, setNewTitle] = useState("");
  const [newAssignee, setNewAssignee] = useState(conversation.bot_ids[0] ?? "");
  const operationView = useMemo(() => ({ active: true }), [selectedId, conversation.archived]);
  useEffect(() => { operationView.active = true; return () => { operationView.active = false; }; }, [operationView]);

  const reload = useCallback(async () => {
    if (!mounted.current) return;
    const current = ++version.current;
    try {
      const [open, history] = await Promise.all([
        api.workItems(conversation.id, "conversations", false),
        api.workItems(conversation.id, "conversations", true),
      ]);
      if (!mounted.current || current !== version.current) return;
      setItems([...(open.work_items ?? []), ...(history.work_items ?? [])].filter(item => item.status !== "cancelled"));
      setError("");
    } catch (cause) { if (mounted.current && current === version.current) setError(errorText(cause)); }
    finally { if (mounted.current && current === version.current) setLoading(false); }
  }, [conversation.id]);

  useEffect(() => {
    mounted.current = true;
    void reload();
    const refresh = () => { if (!document.hidden && !writing.current) void reload(); };
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => { mounted.current = false; version.current++; window.clearInterval(timer); window.removeEventListener("focus", refresh); document.removeEventListener("visibilitychange", refresh); };
  }, [reload]);
  useEffect(() => { setOperationError(null); }, [selectedId, conversation.archived]);

  const availableBots = useMemo(() => conversation.bot_ids.map(id => bots.find(bot => bot.id === id)).filter((bot): bot is Bot => Boolean(bot && !bot.archived)), [bots, conversation.bot_ids]);
  useEffect(() => { if (!availableBots.some(bot => bot.id === newAssignee)) setNewAssignee(availableBots[0]?.id ?? ""); }, [availableBots, newAssignee]);
  const goals = items.filter(item => item.kind === "goal");
  const goalById = new Map(goals.map(item => [item.id, item]));
  const visible = items.filter(item => (!member || item.bot_id === member) && (!goal || item.id === goal || item.parent_goal_id === goal));
  const selected = items.find(item => item.id === selectedId) ?? null;
  const selectedReadOnly = conversation.archived || selected?.conversation_archived || selected?.bot_archived || bots.some(bot => bot.id === selected?.bot_id && bot.archived);
  const botName = (id: string, fallback?: string) => bots.find(bot => bot.id === id)?.name ?? fallback ?? "Bot";

  function readOnly(item?: WorkItem) {
    return latest.current.conversation.archived || item?.conversation_archived || item?.bot_archived || latest.current.bots.some(bot => bot.id === item?.bot_id && bot.archived);
  }
  function accept(item: WorkItem) {
    if (!mounted.current || item.conversation_id !== conversation.id) return;
    version.current++;
    setItems(current => current.map(value => value.id === item.id ? item : value));
  }
  async function mutate(_id: string, action: () => Promise<unknown>) {
    if (writing.current || !mounted.current || latest.current.conversation.archived) return;
    writing.current = true; version.current++;
    setPending(true); setError("");
    setOperationError(null);
    try { await action(); await reload(); }
    catch (cause) { if (mounted.current && operationView.active && !latest.current.conversation.archived) setOperationError({ text: errorText(cause), retry: action }); }
    finally { writing.current = false; if (mounted.current) setPending(false); }
  }
  async function update(item: WorkItem, patch: Partial<Pick<WorkItem, "status" | "bot_id">>) {
    await mutate(item.id, async () => {
      const current = latest.current.items.find(value => value.id === item.id);
      if (!current || readOnly(current) || workExecutionActive(current)) return;
      accept(await api.updateWorkItem(item.id, patch));
    });
  }

  async function create(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!newTitle.trim() || !newAssignee || conversation.archived) return;
    await mutate("create", async () => {
      if (readOnly() || !latest.current.bots.some(bot => bot.id === newAssignee && !bot.archived)) return;
      const item = await api.createWorkItem(conversation.id, {
        kind: "task", title: newTitle.trim(), bot_id: newAssignee,
        ...(goal ? { parent_goal_id: goal } : {}),
      });
      if (!mounted.current || latest.current.conversation.archived) return;
      setSelectedId(item.id); setNewTitle(""); setCreating(false);
    });
  }

  return <section className="team-board-screen" aria-label={t("board.aria", { name: conversation.name })}>
    <div className="team-board-shell">
      <header className="team-board-header">
        <div><h2>{conversation.name}</h2><span>{t("board.title")}</span></div>
        <button type="button" className="team-board-close" aria-label={t("board.back")} onClick={onClose}><TofiIcon name="close" size={20} /></button>
      </header>
      <div className="team-board-filter">
        <span>{t("board.members")}</span>
        <button type="button" className={!member ? "is-active" : ""} aria-pressed={!member} onClick={() => setMember("")}>{t("board.all")}</button>
        {availableBots.map(bot => <button type="button" key={bot.id} className={`team-board-member${member === bot.id ? " is-active" : ""}`} aria-label={t("board.filter_member", { name: bot.name })} aria-pressed={member === bot.id} title={bot.name} onClick={() => setMember(member === bot.id ? "" : bot.id)}><BotAvatar id={bot.id} mini /></button>)}
        <label className="team-board-goal-filter">{t("board.goal")} <select aria-label={t("board.goal_filter")} value={goal} onChange={event => setGoal(event.target.value)}><option value="">{t("board.all_goals")}</option>{goals.map(item => <option key={item.id} value={item.id}>{item.title}</option>)}</select></label>
        <span className="team-board-total">{t("board.card_count", { count: visible.length })}</span>
      </div>
      {error && <div className="team-board-error" role="alert">{error}<button type="button" onClick={() => void reload()}>{t("board.retry")}</button></div>}
      {operationError && <div className="team-board-error" role="alert">{operationError.text}<button type="button" disabled={pending || conversation.archived} onClick={() => void mutate("retry", operationError.retry)}>{t("board.retry_action")}</button></div>}
      {loading ? <p className="team-board-loading" role="status">{t("board.loading")}</p> : <div className="team-board-scroll"><div className="team-board-columns">
        {columns.map(status => {
          const list = visible.filter(item => item.status === status);
          const label = t(`board.column.${status}`);
          return <section className="team-board-column" key={status} aria-label={t("board.column_aria", { label, count: list.length })}>
            <h3><span className={`team-board-dot status-${status}`} />{label}<span className="team-board-count">{list.length}</span></h3>
            {list.map(item => <button type="button" key={item.id} className={`team-ticket${selectedId === item.id ? " is-selected" : ""}${item.status === "done" ? " is-done" : ""}`} aria-pressed={selectedId === item.id} onClick={() => setSelectedId(item.id)}>
              <span className="team-ticket-id">#{item.id.slice(0, 6)}{item.kind === "goal" && <span className="team-ticket-kind">{t("board.kind_goal")}</span>}</span>
              <strong>{item.title}</strong>
              {item.parent_goal_id && <span className="team-ticket-goal">{goalById.get(item.parent_goal_id)?.title ?? t("board.linked_goal")}</span>}
              <span className="team-ticket-footer"><BotAvatar id={item.bot_id} mini /><span>{botName(item.bot_id, item.bot_name)}</span><time dateTime={item.updated_at}>{new Intl.DateTimeFormat(intlLocale(), { timeZone:timezone, month:"numeric", day:"numeric" }).format(new Date(item.updated_at))}</time></span>
            </button>)}
            {status === "todo" && !conversation.archived && <button type="button" className="team-board-add" onClick={() => setCreating(true)}><TofiIcon name="plus" size={16} style={{ verticalAlign: "middle" }} /> {t("board.new")}</button>}
          </section>;
        })}
      </div></div>}
      {creating && !conversation.archived && <form className="team-board-create" onSubmit={event => void create(event)}>
        <h3>{t("board.create_title")}</h3><label>{t("board.title_label")}<input autoFocus value={newTitle} maxLength={240} onChange={event => setNewTitle(event.target.value)} placeholder={t("board.title_placeholder")} /></label>
        <label>{t("board.assignee")}<select value={newAssignee} onChange={event => setNewAssignee(event.target.value)}>{availableBots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>
        <div><button type="button" onClick={() => setCreating(false)}>{t("board.cancel")}</button><button type="submit" disabled={pending || !newTitle.trim() || !newAssignee}>{pending ? t("board.creating") : t("board.create")}</button></div>
      </form>}
      {selected && <aside className="team-board-detail" aria-label={t("board.detail")}>
        <div className="team-board-detail-top"><span>#{selected.id.slice(0, 6)} · {selected.kind === "goal" ? t("board.kind_goal") : t("board.kind_task")}</span><button type="button" aria-label={t("board.close_detail")} onClick={() => setSelectedId(null)}><TofiIcon name="close" size={20} /></button></div>
        <h3>{selected.title}</h3>
        {selected.description && <p>{selected.description}</p>}
        {selected.parent_goal_id && <p>{t("board.linked_goal_detail", { title: goalById.get(selected.parent_goal_id)?.title ?? t("board.linked_goal") })}</p>}
        <WorkExecution key={selected.id} item={selected} readOnly={!!selectedReadOnly} pending={pending} mutate={mutate} onExecuted={accept} />
        <label>{t("board.status")}<select value={selected.status} disabled={pending || selectedReadOnly || workExecutionActive(selected)} onChange={event => void update(selected, { status: event.target.value as WorkStatus })}>{columns.map(status => <option key={status} value={status}>{t(`board.column.${status}`)}</option>)}</select></label>
        <label>{t("board.assignee")}<select value={selected.bot_id} disabled={pending || selectedReadOnly || workExecutionActive(selected)} onChange={event => void update(selected, { bot_id: event.target.value })}>{availableBots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>
        <button type="button" className="team-board-detail-work" onClick={onOpenWork}>{t("board.open_work")}</button>
      </aside>}
    </div>
  </section>;
}
