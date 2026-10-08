import { useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { TofiIcon } from "./icons";
import { BotAvatar } from "./BotAvatar";
import type { Bot, Conversation } from "./types";
import { i18n, useTranslation } from "./i18n";
import { errorText } from "./i18n/errors";

type Props = {
  onClose: () => void;
  onOpen: (id: string) => void;
  onLoaded: (conversations: Conversation[], bots: Bot[]) => void;
  onChanged: () => Promise<void>;
  onDelete: (conversation: Conversation) => void;
};

// archive_busy, no_active_members and archive_blocked resolve to bots:apiError.<code>.
function archiveError(cause: unknown) {
  return errorText(cause, "bots", i18n.t("bots:archive.error.failed"));
}

export function ArchivePanel({ onClose, onOpen, onLoaded, onChanged, onDelete }: Props) {
  const { t } = useTranslation("bots");
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
        if (!bot) throw new Error(t("archive.error.bot_missing"));
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
    <div className="detail-heading"><div><h2>{t("archive.title")}</h2></div><button className="close-button" aria-label={t("archive.close_aria")} onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    {conversations.length > 0 && <p className="field-note">{t("archive.schedule_note")}</p>}
    {error && <p className="error-text" role="alert">{error} <button type="button" className="text-button" disabled={loading || busyId !== null} onClick={() => void load()}>{t("archive.reload")}</button></p>}
    {loading ? <div className="inline-state"><div className="spinner" />{t("archive.loading")}</div> : !conversations.length ? !error && <div className="panel-empty">{t("archive.empty")}</div> : <div className="archive-list">{conversations.map((conversation) => {
      const bot = conversation.kind === "dm" ? bots.find((item) => item.id === conversation.bot_id || item.dm_conversation_id === conversation.id) : undefined;
      return <article className="archive-card" key={conversation.id}>
        {bot ? <BotAvatar id={bot.id} mini /> : <span className="archive-group-mark" aria-hidden="true"><TofiIcon name="group" size={20} /></span>}
        <div className="archive-card-copy"><button type="button" className="archive-open" onClick={() => onOpen(conversation.id)}>{conversation.name}</button><small>{conversation.kind === "group" ? t("archive.group_meta", { count: conversation.bot_ids.length }) : t("archive.dm_meta")}</small></div>
        <div className="archive-card-actions"><button type="button" className="danger-link" disabled={busyId !== null} onClick={() => onDelete(conversation)}>{bot ? t("archive.delete_bot") : t("archive.delete_group")}</button><button type="button" className="secondary-button" disabled={busyId !== null} onClick={() => void restore(conversation)}>{busyId === conversation.id ? t("archive.restoring") : t("archive.restore")}</button></div>
      </article>;
    })}</div>}
  </div>;
}
