import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { i18n } from "./i18n";
import { sameData } from "./sameData";
import type { Question } from "./questionTimeline";

/** A separate read failure must not prevent ordinary conversation history loading. */
export function useQuestions(conversationId: string | null) {
  const [snapshot, setSnapshot] = useState<{ id: string; items: Question[] }>({ id: "", items: [] });
  const [failure, setFailure] = useState<{ id: string; text: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const requestSequence = useRef(0);
  // Background polls must not flip `loading` or replace an unchanged list: either would re-render the whole chat.
  const loadedFor = useRef("");
  const controller = useRef<AbortController | null>(null);
  const refresh = useCallback(async (resolved?: Question) => {
    if (!conversationId) return;
    if (resolved?.conversation_id === conversationId) setSnapshot(current => ({ id: conversationId, items: [...(current.id === conversationId ? current.items : []).filter(q => q.question_id !== resolved.question_id), resolved] }));
    const version = generation.current;
    const sequence = ++requestSequence.current;
    controller.current?.abort();
    const pending = new AbortController(); controller.current = pending;
    if (loadedFor.current !== conversationId) setLoading(true);
    try {
      const result = await request<{ questions: Question[] }>(`/api/questions?conversation_id=${encodeURIComponent(conversationId)}`, { signal: pending.signal });
      if (version !== generation.current || sequence !== requestSequence.current) return;
      const items = (result.questions ?? []).filter(q => q.conversation_id === conversationId);
      loadedFor.current = conversationId;
      setSnapshot(current => current.id === conversationId && sameData(current.items, items) ? current : { id: conversationId, items });
      setFailure(null);
    } catch (cause) {
      if (!pending.signal.aborted && version === generation.current && sequence === requestSequence.current) setFailure({ id: conversationId, text: cause instanceof Error ? cause.message : i18n.t("tasks:question.load_failed") });
    } finally {
      if (version === generation.current && sequence === requestSequence.current) setLoading(false);
    }
  }, [conversationId]);
  useEffect(() => {
    generation.current++;
    setFailure(null);
    let stopped = false;
    let timer: number | undefined;
    async function poll() {
      if (!document.hidden) await refresh();
      if (!stopped) timer = window.setTimeout(poll, 3000);
    }
    void poll();
    const focused = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focused); document.addEventListener("visibilitychange", focused);
    return () => { stopped = true; generation.current++; clearTimeout(timer); controller.current?.abort(); window.removeEventListener("focus", focused); document.removeEventListener("visibilitychange", focused); };
  }, [refresh]);
  return { items: snapshot.id === conversationId ? snapshot.items : [], error: failure?.id === conversationId ? failure.text : "", loading, refresh };
}
