import { useEffect, useRef } from "react";
import { api } from "./api";
import { useTranslation } from "./i18n";
import { codeText } from "./i18n/codeText";
import { hasPendingWorkExecution, startWorkExecution, workExecutionActive, workExecutionClosed, workExecutionStatusKeys } from "./workExecutionState";
import type { RunStatus, WorkItem } from "./types";
import { Button } from "./ds/Button/Button";
import { Badge, type BadgeTone } from "./ds/Badge/Badge";
import { Banner } from "./ds/Banner/Banner";
import { Card } from "./ds/Card/Card";
import { Notice } from "./ds/Notice/Notice";
import "./work-execution.css";

const statusTone: Record<RunStatus, BadgeTone> = { queued: "paused", running: "working", waiting: "needs", done: "done", failed: "error", cancelled: "neutral", interrupted: "paused" };

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
  if (item.kind !== "task") return <Notice>{t("execution.goal_note")}</Notice>;

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

  const note = (text: string) => <Notice>{text}</Notice>;
  return <section className="work-execution" aria-label={t("execution.aria")}>
    <div className="work-execution-heading"><strong>{t("execution.heading")}</strong><span role="status"><Badge tone={execution ? statusTone[execution.status] : "neutral"}>{execution ? t(workExecutionStatusKeys[execution.status]) : t("execution.not_run")}</Badge></span></div>
    {execution?.error && <Banner tone="error">{codeText(execution.error_code, "work") ?? execution.error}</Banner>}
    {active && note(execution?.status === "failed" ? t("execution.active_failed_note") : t("execution.active_note"))}
    {execution && !active && execution.status === "done" && <>
      {execution.result?.trim() ? <><Card variant="quiet"><p className="work-execution-result">{codeText(execution.result_code, "work") ?? execution.result}</p></Card>{note(t("execution.check_result"))}</> : note(t("execution.no_result"))}
    </>}
    <div className="work-execution-actions">
      {active ? <Button size="sm" disabled={pending || readOnly} onClick={() => void mutate(item.id, stop)}>{t("execution.stop")}</Button>
        : <Button size="sm" disabled={pending || readOnly || closed} onClick={() => void mutate(item.id, start)}>{pending ? t("execution.working") : unresolved ? t("execution.retry_confirm") : execution ? t("execution.rerun") : t("execution.start")}</Button>}
      {active && unresolved && <Button size="sm" disabled={pending || readOnly} onClick={() => void mutate(item.id, start)}>{t("execution.retry_confirm")}</Button>}
    </div>
    {readOnly ? note(t("execution.archived")) : closed && note(t("execution.reopen_note"))}
  </section>;
}
