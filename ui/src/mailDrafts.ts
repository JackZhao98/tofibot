import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import { i18n } from "./i18n";
import { sameData } from "./sameData";

// The draft list and its polling stay in the first bundle; the card that shows
// a draft loads on demand (MailDraftCard).
export type MailDraftAttachment = { id: string; name: string; mime: string; size: number };

export type MailDraft = {
  draft_id: string;
  conversation_id: string;
  bot_id: string;
  run_id: string;
  to: string;
  subject: string;
  body: string;
  demo: boolean;
  attachments?: MailDraftAttachment[];
  status: "pending" | "sending" | "sent" | "declined" | "unknown";
  revision: number;
  created_at: string;
  updated_at: string;
};

export function useMailDrafts(conversationId: string | null) {
  const [snapshot, setSnapshot] = useState<{ id: string; drafts: MailDraft[] }>({ id: "", drafts: [] });
  const [error, setError] = useState("");
  const refresh = useCallback(async () => {
    if (!conversationId) return;
    try {
      const result = await request<{ drafts: MailDraft[] }>(`/api/mail-drafts?conversation_id=${encodeURIComponent(conversationId)}`);
      const drafts = result.drafts ?? [];
      setSnapshot((current) => current.id === conversationId && sameData(current.drafts, drafts) ? current : { id: conversationId, drafts });
      setError("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : i18n.t("mail:draft.load_failed")); }
  }, [conversationId]);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => { if (!document.hidden) void refresh(); }, 3000);
    const focus = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focus);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", focus); };
  }, [refresh]);
  return { items: snapshot.id === conversationId ? snapshot.drafts : [], refresh, error };
}

