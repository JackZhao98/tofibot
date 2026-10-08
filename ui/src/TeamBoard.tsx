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

const columns: { status: WorkStatus; label: string }[] = [
  { status: "todo", label: "待办" },
  { status: "in_progress", label: "进行中" },
  { status: "blocked", label: "等待 / 阻塞" },
  { status: "review", label: "核查中" },
  { status: "done", label: "完成" },
];

const errorText = (cause: unknown) => cause instanceof Error ? cause.message : "操作失败，请重试";

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

  return <section className="team-board-screen" aria-label={`${conversation.name} 看板`}>
    <div className="team-board-shell">
      <header className="team-board-header">
        <div><h2>{conversation.name}</h2><span>团队看板</span></div>
        <button type="button" className="team-board-close" aria-label="返回对话" onClick={onClose}><TofiIcon name="close" size={20} /></button>
      </header>
      <div className="team-board-filter">
        <span>成员</span>
        <button type="button" className={!member ? "is-active" : ""} aria-pressed={!member} onClick={() => setMember("")}>全部</button>
        {availableBots.map(bot => <button type="button" key={bot.id} className={`team-board-member${member === bot.id ? " is-active" : ""}`} aria-label={`筛选 ${bot.name}`} aria-pressed={member === bot.id} title={bot.name} onClick={() => setMember(member === bot.id ? "" : bot.id)}><BotAvatar id={bot.id} mini /></button>)}
        <label className="team-board-goal-filter">目标 <select aria-label="筛选目标" value={goal} onChange={event => setGoal(event.target.value)}><option value="">全部目标</option>{goals.map(item => <option key={item.id} value={item.id}>{item.title}</option>)}</select></label>
        <span className="team-board-total">{visible.length} 张</span>
      </div>
      {error && <div className="team-board-error" role="alert">{error}<button type="button" onClick={() => void reload()}>重试</button></div>}
      {operationError && <div className="team-board-error" role="alert">{operationError.text}<button type="button" disabled={pending || conversation.archived} onClick={() => void mutate("retry", operationError.retry)}>重试操作</button></div>}
      {loading ? <p className="team-board-loading" role="status">正在载入看板…</p> : <div className="team-board-scroll"><div className="team-board-columns">
        {columns.map(column => {
          const list = visible.filter(item => item.status === column.status);
          return <section className="team-board-column" key={column.status} aria-label={`${column.label}，${list.length} 张`}>
            <h3><span className={`team-board-dot status-${column.status}`} />{column.label}<span className="team-board-count">{list.length}</span></h3>
            {list.map(item => <button type="button" key={item.id} className={`team-ticket${selectedId === item.id ? " is-selected" : ""}${item.status === "done" ? " is-done" : ""}`} aria-pressed={selectedId === item.id} onClick={() => setSelectedId(item.id)}>
              <span className="team-ticket-id">#{item.id.slice(0, 6)}{item.kind === "goal" && <span className="team-ticket-kind">目标</span>}</span>
              <strong>{item.title}</strong>
              {item.parent_goal_id && <span className="team-ticket-goal">{goalById.get(item.parent_goal_id)?.title ?? "关联目标"}</span>}
              <span className="team-ticket-footer"><BotAvatar id={item.bot_id} mini /><span>{botName(item.bot_id, item.bot_name)}</span><time dateTime={item.updated_at}>{new Intl.DateTimeFormat(intlLocale(), { timeZone:timezone, month:"numeric", day:"numeric" }).format(new Date(item.updated_at))}</time></span>
            </button>)}
            {column.status === "todo" && !conversation.archived && <button type="button" className="team-board-add" onClick={() => setCreating(true)}><TofiIcon name="plus" size={16} style={{ verticalAlign: "middle" }} /> 新建</button>}
          </section>;
        })}
      </div></div>}
      {creating && !conversation.archived && <form className="team-board-create" onSubmit={event => void create(event)}>
        <h3>新建待办</h3><label>标题<input autoFocus value={newTitle} maxLength={240} onChange={event => setNewTitle(event.target.value)} placeholder="要完成什么？" /></label>
        <label>负责成员<select value={newAssignee} onChange={event => setNewAssignee(event.target.value)}>{availableBots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>
        <div><button type="button" onClick={() => setCreating(false)}>取消</button><button type="submit" disabled={pending || !newTitle.trim() || !newAssignee}>{pending ? "创建中…" : "创建"}</button></div>
      </form>}
      {selected && <aside className="team-board-detail" aria-label="待办详情">
        <div className="team-board-detail-top"><span>#{selected.id.slice(0, 6)} · {selected.kind === "goal" ? "目标" : "待办"}</span><button type="button" aria-label="关闭待办详情" onClick={() => setSelectedId(null)}><TofiIcon name="close" size={20} /></button></div>
        <h3>{selected.title}</h3>
        {selected.description && <p>{selected.description}</p>}
        {selected.parent_goal_id && <p>关联目标：{goalById.get(selected.parent_goal_id)?.title ?? "关联目标"}</p>}
        <WorkExecution key={selected.id} item={selected} readOnly={!!selectedReadOnly} pending={pending} mutate={mutate} onExecuted={accept} />
        <label>状态<select value={selected.status} disabled={pending || selectedReadOnly || workExecutionActive(selected)} onChange={event => void update(selected, { status: event.target.value as WorkStatus })}>{columns.map(column => <option key={column.status} value={column.status}>{column.label}</option>)}</select></label>
        <label>负责成员<select value={selected.bot_id} disabled={pending || selectedReadOnly || workExecutionActive(selected)} onChange={event => void update(selected, { bot_id: event.target.value })}>{availableBots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>
        <button type="button" className="team-board-detail-work" onClick={onOpenWork}>打开目标与待办</button>
      </aside>}
    </div>
  </section>;
}
