import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api } from "./api";
import { scheduleDisplay } from "./displayMetadata";
import { i18n, useTranslation } from "./i18n";
import type { Schedule, WorkItem } from "./types";
import "./work-panel.css";

/** The agenda read and the one-line preview that Bot and member panels show. The full panel (WorkPanel.tsx) loads on demand. */
export type Scope = { id: string; type: "bots" | "conversations" };
export const message = (cause: unknown) => cause instanceof Error ? cause.message : i18n.t("work:panel.load_failed");

export function useAgenda(scope: Scope, history: boolean, refreshToken: number) {
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
