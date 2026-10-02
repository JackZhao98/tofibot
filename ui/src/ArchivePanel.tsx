import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "./api";
import { TofiIcon } from "./icons";
import { GazeAvatar } from "./GazeAvatar";
import type { Bot, Conversation } from "./types";

type Props = {
  onClose: () => void;
  onOpen: (id: string) => void;
  onLoaded: (conversations: Conversation[], bots: Bot[]) => void;
  onChanged: () => Promise<void>;
  onDelete: (conversation: Conversation) => void;
};

function archiveError(cause: unknown) {
  if (cause instanceof ApiError && cause.code === "archive_busy") return "还有未完成的任务，请等它结束或先停止后再恢复。";
  if (cause instanceof ApiError && cause.code === "no_active_members") return "这个群没有可用成员，恢复后请先添加至少一位 Bot。";
  if (cause instanceof ApiError && cause.code === "archive_blocked") return "这个会话当前不可用，请先恢复后再继续。";
  return cause instanceof Error ? cause.message : "归档操作失败，请稍后重试。";
}

export function ArchivePanel({ onClose, onOpen, onLoaded, onChanged, onDelete }: Props) {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [bots, setBots] = useState<Bot[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [conversationResult, botResult] = await Promise.all([api.conversations(true), api.bots(true)]);
      const archivedConversations = conversationResult.conversations.filter((item) => item.archived);
      const archivedBots = botResult.bots.filter((item) => item.archived);
      setConversations(archivedConversations);
      setBots(archivedBots);
      onLoaded(archivedConversations, archivedBots);
      setError("");
    } catch (cause) {
      setError(archiveError(cause));
    } finally {
      setLoading(false);
    }
  }, [onLoaded]);

  useEffect(() => { void load(); }, [load]);

  async function restore(conversation: Conversation) {
    if (busyId) return;
    setBusyId(conversation.id);
    setError("");
    try {
      if (conversation.kind === "dm") {
        const bot = bots.find((item) => item.id === conversation.bot_id || item.dm_conversation_id === conversation.id);
        if (!bot) throw new Error("找不到这个 Bot 的身份资料，无法恢复。");
        await api.setBotArchived(bot.id, false);
      } else {
        await api.setConversationArchived(conversation.id, false);
      }
      await onChanged();
      await load();
    } catch (cause) {
      setError(archiveError(cause));
    } finally {
      setBusyId(null);
    }
  }

  return <div className="detail-content archive-panel">
    <div className="detail-heading"><div><h2>旧归档</h2></div><button className="close-button" aria-label="关闭归档" onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    {conversations.length > 0 && <p className="field-note">恢复后，定时任务需手动启用。</p>}
    {error && <p className="error-text" role="alert">{error} <button type="button" className="text-button" disabled={loading || busyId !== null} onClick={() => void load()}>重新载入</button></p>}
    {loading ? <div className="inline-state"><div className="spinner" />读取归档…</div> : !conversations.length ? !error && <div className="panel-empty">暂无归档。</div> : <div className="archive-list">{conversations.map((conversation) => {
      const bot = conversation.kind === "dm" ? bots.find((item) => item.id === conversation.bot_id || item.dm_conversation_id === conversation.id) : undefined;
      return <article className="archive-card" key={conversation.id}>
        {bot ? <GazeAvatar id={bot.id} mini /> : <span className="archive-group-mark" aria-hidden="true"><TofiIcon name="group" size={20} /></span>}
        <div className="archive-card-copy"><button type="button" className="archive-open" onClick={() => onOpen(conversation.id)}>{conversation.name}</button><small>{conversation.kind === "group" ? `${conversation.bot_ids.length} 位成员 · 群` : "Bot · 历史私信"}</small></div>
        <div className="archive-card-actions"><button type="button" className="danger-link" disabled={busyId !== null} onClick={() => onDelete(conversation)}>{bot ? "删除 Bot" : "删除群聊"}</button><button type="button" className="secondary-button" disabled={busyId !== null} onClick={() => void restore(conversation)}>{busyId === conversation.id ? "恢复中…" : "恢复"}</button></div>
      </article>;
    })}</div>}
  </div>;
}
