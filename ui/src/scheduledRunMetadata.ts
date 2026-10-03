import { scheduleDisplay } from "./displayMetadata";
import type { Message, Run, Schedule } from "./types";
import type { ScheduleOccurrence } from "./scheduleOccurrences";
import { browserTimezone, validTimezone } from "./timezone";

function timestamp(value: string | undefined) {
  return value && Number.isFinite(Date.parse(value)) ? value : undefined;
}

function cadence(schedule: Pick<Schedule, "kind" | "daily_time" | "interval_seconds">) {
  if (schedule.kind === "once") return "只一次";
  if (schedule.kind === "daily") return schedule.daily_time ? `每天 ${schedule.daily_time}` : "每天";
  const seconds = schedule.interval_seconds;
  if (!seconds || seconds <= 0) return "按间隔执行";
  if (seconds % 86400 === 0) return `每 ${seconds / 86400} 天`;
  if (seconds % 3600 === 0) return `每 ${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `每 ${seconds / 60} 分钟`;
  return `每 ${seconds} 秒`;
}

/** Presentation metadata must belong to this occurrence, never the latest run. */
export function scheduledRunMetadata(message: Message, run?: Run, occurrence?: ScheduleOccurrence, schedule?: Schedule, timezone?: string) {
  const owningRun = message.run_id && run?.id === message.run_id ? run : undefined;
  const exactOccurrence = message.run_id && occurrence?.root_run_id === message.run_id ? occurrence : undefined;
  const matchedSchedule = exactOccurrence && schedule?.id === exactOccurrence.schedule_id && schedule.conversation_id === message.conversation_id ? schedule : undefined;
  const displayTimezone = timezone && validTimezone(timezone) ? timezone : browserTimezone();
  const time = (value: string) => new Intl.DateTimeFormat("zh-CN", { timeZone: displayTimezone, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(value));
  const date = (value: string) => new Intl.DateTimeFormat("zh-CN", { timeZone: displayTimezone, year: "numeric", month: "long", day: "numeric" }).format(new Date(value));
  const plannedAt = timestamp(exactOccurrence?.scheduled_for_utc);
  const state = exactOccurrence?.execution_status ?? owningRun?.status;
  // The root run's update time is not the delegated family's completion time.
  const statusRun = owningRun?.status === state && (!exactOccurrence || exactOccurrence.status_run_id === owningRun?.id) ? owningRun : undefined;
  const statusAt = timestamp(statusRun?.updated_at);
  const terminal = state === "done" || state === "failed" || state === "interrupted" || state === "cancelled";
  const createdAt = timestamp(matchedSchedule?.created_at);
  // Snapshot fields are written when an occurrence is claimed. A changed
  // schedule must never rewrite the frequency shown for an old execution.
  const recurrence = exactOccurrence?.kind ? cadence({ kind: exactOccurrence.kind, daily_time: exactOccurrence.daily_time, interval_seconds: exactOccurrence.interval_seconds }) : undefined;
  const scheduleZone = exactOccurrence?.kind === "daily" && exactOccurrence.timezone && exactOccurrence.timezone !== displayTimezone ? `（${exactOccurrence.timezone}）` : "";
  const creator = exactOccurrence?.created_by ?? matchedSchedule?.created_by;
  const creatorLabel = creator === "user" ? "由你创建" : creator === "bot" ? "由助理创建" : "";
  const occurrenceLabel = exactOccurrence?.occurrence_number ? `第 ${exactOccurrence.occurrence_number} 次` : "";

  // Never borrow edited live labels for a historical occurrence. Missing
  // snapshots get neutral labels, never a first line of the execution prompt.
  const display = scheduleDisplay(exactOccurrence);
  return {
    ...display,
    plannedAt,
    plannedTime: plannedAt ? time(plannedAt) : "待确认",
    plannedTimeDescription: plannedAt ? `计划触发：${date(plannedAt)} ${time(plannedAt)} · ${displayTimezone}` : "计划触发时间待确认",
    metadata: [recurrence ? recurrence + scheduleZone : plannedAt ? `定时触发 · ${date(plannedAt)}` : "定时任务", occurrenceLabel, creatorLabel, createdAt ? `${date(createdAt)}创建` : ""].filter(Boolean).join(" · "),
    metadataDescription: recurrence ? "本次触发时保存的计划频率" : "本次触发记录；历史频率未保存",
    statusAt: terminal ? statusAt : undefined,
    statusTime: terminal && statusAt ? time(statusAt) : undefined,
    statusTimeDescription: terminal && statusAt ? `状态更新：${date(statusAt)} ${time(statusAt)} · ${displayTimezone}` : undefined,
    runningSince: state === "running" ? statusAt : undefined,
  };
}
