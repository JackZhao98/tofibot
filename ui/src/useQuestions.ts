import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { i18n } from "./i18n";
import { sameData } from "./sameData";
import type { Question } from "./questionTimeline";

export const QUESTION_POLL_MS = 3000;
/** An unchanged list backs off to this; a pending question or a change returns to QUESTION_POLL_MS. */
export const QUESTION_IDLE_POLL_MS = 15000;
export const questionPollDelay = (items: Question[], unchangedPolls: number) =>
  items.some(q => q.status === "pending") ? QUESTION_POLL_MS : Math.min(QUESTION_IDLE_POLL_MS, QUESTION_POLL_MS * 2 ** Math.min(unchangedPolls, 8));

const details = new Map<string, Promise<Question>>();
/** Full card of a settled question whose list row omitted approval payload and facts. Cached per version. */
export function fetchQuestionDetail(question: Question): Promise<Question> {
  const key = `${question.question_id}:${question.updated_at ?? ""}`;
  let known = details.get(key);
  if (!known) {
    known = request<{ question: Question }>(`/api/questions/${encodeURIComponent(question.question_id)}`).then(result => result.question);
    details.set(key, known);
    known.catch(() => details.delete(key));
  }
  return known;
}

/** The question with its omitted detail, fetched once `enabled` (e.g. the card was expanded). */
export function useQuestionDetail(question: Question, enabled: boolean): { question: Question; failed: boolean } {
  const [loaded, setLoaded] = useState<{ key: string; question: Question } | null>(null);
  const [failed, setFailed] = useState("");
  const key = `${question.question_id}:${question.updated_at ?? ""}`;
  useEffect(() => {
    if (!enabled || !question.detail_omitted) return;
    let live = true;
    fetchQuestionDetail(question).then(full => { if (live) { setLoaded({ key, question: full }); setFailed(""); } }, () => { if (live) setFailed(key); });
    return () => { live = false; };
  }, [enabled, question.detail_omitted, key]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!question.detail_omitted) return { question, failed: false };
  return { question: loaded?.key === key ? loaded.question : question, failed: failed === key };
}

/** A separate read failure must not prevent ordinary conversation history loading. `since` is the start of the loaded message window: settled questions older than it are not requested. */
export function useQuestions(conversationId: string | null, since?: string) {
  const [snapshot, setSnapshot] = useState<{ id: string; items: Question[] }>({ id: "", items: [] });
  const [failure, setFailure] = useState<{ id: string; text: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const requestSequence = useRef(0);
  // Background polls must not flip `loading` or replace an unchanged list: either would re-render the whole chat.
  const loadedFor = useRef("");
  const controller = useRef<AbortController | null>(null);
  const unchanged = useRef(0);
  const latest = useRef<Question[]>([]);
  /** Re-arms the poll timer from the newest list, so an event-driven change returns a backed-off poll to 3 s. */
  const rearm = useRef<(() => void) | null>(null);
  const refresh = useCallback(async (resolved?: Question) => {
    if (!conversationId) return;
    if (resolved?.conversation_id === conversationId) setSnapshot(current => ({ id: conversationId, items: [...(current.id === conversationId ? current.items : []).filter(q => q.question_id !== resolved.question_id), resolved] }));
    const version = generation.current;
    const sequence = ++requestSequence.current;
    controller.current?.abort();
    const pending = new AbortController(); controller.current = pending;
    if (loadedFor.current !== conversationId) setLoading(true);
    try {
      const result = await request<{ questions: Question[] }>(`/api/questions?conversation_id=${encodeURIComponent(conversationId)}${since ? `&since=${encodeURIComponent(since)}` : ""}`, { signal: pending.signal });
      if (version !== generation.current || sequence !== requestSequence.current) return;
      const items = (result.questions ?? []).filter(q => q.conversation_id === conversationId);
      const loadedBefore = loadedFor.current === conversationId;
      loadedFor.current = conversationId;
      const same = sameData(latest.current, items) && loadedBefore;
      unchanged.current = same ? unchanged.current + 1 : 0;
      latest.current = items;
      rearm.current?.();
      setSnapshot(current => current.id === conversationId && sameData(current.items, items) ? current : { id: conversationId, items });
      setFailure(null);
    } catch (cause) {
      if (!pending.signal.aborted && version === generation.current && sequence === requestSequence.current) setFailure({ id: conversationId, text: cause instanceof Error ? cause.message : i18n.t("tasks:question.load_failed") });
    } finally {
      if (version === generation.current && sequence === requestSequence.current) setLoading(false);
    }
  }, [conversationId, since]);
  useEffect(() => {
    generation.current++;
    unchanged.current = 0;
    latest.current = [];
    setFailure(null);
    let stopped = false;
    let timer: number | undefined;
    const arm = () => { if (stopped) return; clearTimeout(timer); timer = window.setTimeout(poll, questionPollDelay(latest.current, unchanged.current)); };
    rearm.current = arm;
    async function poll() {
      if (!document.hidden) await refresh();
      arm();
    }
    void poll();
    const focused = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focused); document.addEventListener("visibilitychange", focused);
    return () => { stopped = true; rearm.current = null; generation.current++; clearTimeout(timer); controller.current?.abort(); window.removeEventListener("focus", focused); document.removeEventListener("visibilitychange", focused); };
  }, [refresh]);
  return { items: snapshot.id === conversationId ? snapshot.items : [], error: failure?.id === conversationId ? failure.text : "", loading, refresh };
}
