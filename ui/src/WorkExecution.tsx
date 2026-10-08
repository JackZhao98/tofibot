import { useEffect, useRef } from "react";
import { api } from "./api";
import { useTranslation } from "./i18n";
import { hasPendingWorkExecution, startWorkExecution, workExecutionActive, workExecutionClosed, workExecutionStatusKeys } from "./workExecutionState";
import type { WorkItem } from "./types";
import "./work-execution.css";

type Props = {
  item: WorkItem;
  readOnly?: boolean;
  pending: boolean;
  mutate: (id: string, action: () => Promise<unknown>) => Promise<void>;
  onExecuted: (item: WorkItem) => void;
};

export function WorkExecution(props: Props) {
  const { item, readOnly, pending, mutate } = props;
  const { t } = useTranslation("work");
  const latest = useRef(props);
  latest.current = props;
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const execution = item.execution;
  const active = workExecutionActive(item);
  const closed = workExecutionClosed(item);
  const unresolved = hasPendingWorkExecution(item.id);
  if (item.kind !== "task") return <p className="work-execution-note">{t("execution.goal_note")}</p>;

  async function start() {
    const current = latest.current;
    if (!mounted.current || current.item.id !== item.id || current.readOnly) return;
    // A retry of an uncertain request is safe even if polling has since found the attempt.
    if (!hasPendingWorkExecution(item.id) && (workExecutionActive(current.item) || workExecutionClosed(current.item))) return;
    const result = await startWorkExecution(item.id);
    if (mounted.current && latest.current.item.id === item.id) latest.current.onExecuted(result.work_item);
  }
  async function stop() {
    const current = latest.current;
    if (!mounted.current || current.item.id !== item.id || current.readOnly || !workExecutionActive(current.item)) return;
    await api.cancelRun(current.item.execution!.root_run_id);
  }

  return <section className="work-execution" aria-label={t("execution.aria")}>
    <div className="work-execution-heading"><strong>{t("execution.heading")}</strong><span role="status">{execution ? t(workExecutionStatusKeys[execution.status]) : t("execution.not_run")}</span></div>
    {execution?.error && <p className="work-execution-error" role="alert">{execution.error}</p>}
    {active && <p className="work-execution-note">{execution?.status === "failed" ? t("execution.active_failed_note") : t("execution.active_note")}</p>}
    {execution && !active && execution.status === "done" && <>
      {execution.result?.trim() ? <><p className="work-execution-result">{execution.result}</p><p className="work-execution-note">{t("execution.check_result")}</p></> : <p className="work-execution-note">{t("execution.no_result")}</p>}
    </>}
    <div className="work-execution-actions">
      {active ? <button type="button" disabled={pending || readOnly} onClick={() => void mutate(item.id, stop)}>{t("execution.stop")}</button>
        : <button type="button" disabled={pending || readOnly || closed} onClick={() => void mutate(item.id, start)}>{pending ? t("execution.working") : unresolved ? t("execution.retry_confirm") : execution ? t("execution.rerun") : t("execution.start")}</button>}
      {active && unresolved && <button type="button" disabled={pending || readOnly} onClick={() => void mutate(item.id, start)}>{t("execution.retry_confirm")}</button>}
    </div>
    {readOnly ? <p className="work-execution-note">{t("execution.archived")}</p> : closed && <p className="work-execution-note">{t("execution.reopen_note")}</p>}
  </section>;
}
