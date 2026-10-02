import { useEffect, useRef } from "react";
import { api } from "./api";
import { hasPendingWorkExecution, startWorkExecution, workExecutionActive, workExecutionClosed, workExecutionStatus } from "./workExecutionState";
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
  const latest = useRef(props);
  latest.current = props;
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const execution = item.execution;
  const active = workExecutionActive(item);
  const closed = workExecutionClosed(item);
  const unresolved = hasPendingWorkExecution(item.id);
  if (item.kind !== "task") return <p className="work-execution-note">目标用于规划，请从待办开始执行。</p>;

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

  return <section className="work-execution" aria-label="任务执行">
    <div className="work-execution-heading"><strong>执行</strong><span role="status">{execution ? workExecutionStatus[execution.status] : "尚未执行"}</span></div>
    {execution?.error && <p className="work-execution-error" role="alert">{execution.error}</p>}
    {active && <p className="work-execution-note">{execution?.status === "failed" ? "仍有协作在执行，可停止本次执行。" : "执行期间暂不能修改或完成待办。"}</p>}
    {execution && !active && execution.status === "done" && <>
      {execution.result?.trim() ? <><p className="work-execution-result">{execution.result}</p><p className="work-execution-note">请核查结果，再确认待办是否完成。</p></> : <p className="work-execution-note">未收到可见结果，请检查原会话或重新执行。</p>}
    </>}
    <div className="work-execution-actions">
      {active ? <button type="button" disabled={pending || readOnly} onClick={() => void mutate(item.id, stop)}>停止执行</button>
        : <button type="button" disabled={pending || readOnly || closed} onClick={() => void mutate(item.id, start)}>{pending ? "处理中…" : unresolved ? "重试确认执行" : execution ? "重新执行" : "开始执行"}</button>}
      {active && unresolved && <button type="button" disabled={pending || readOnly} onClick={() => void mutate(item.id, start)}>重试确认执行</button>}
    </div>
    {readOnly ? <p className="work-execution-note">已归档，仅可查看执行记录。</p> : closed && <p className="work-execution-note">重新打开待办后可再次执行。</p>}
  </section>;
}
