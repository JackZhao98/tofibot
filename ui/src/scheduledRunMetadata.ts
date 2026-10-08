import { scheduleDisplay } from "./displayMetadata";
import type { Message, Run, Schedule } from "./types";
import type { ScheduleOccurrence } from "./scheduleOccurrences";
import { browserTimezone, validTimezone } from "./timezone";
import { i18n } from "./i18n";
import { intlLocale } from "./i18n/format";

const scheduleT = () => i18n.getFixedT(null, "schedules");

function timestamp(value: string | undefined) {
  return value && Number.isFinite(Date.parse(value)) ? value : undefined;
}

function cadence(schedule: Pick<Schedule, "kind" | "daily_time" | "interval_seconds">) {
  const t = scheduleT();
  if (schedule.kind === "once") return t("run.cadence.once");
  if (schedule.kind === "daily") return schedule.daily_time ? t("run.cadence.daily_at", { time: schedule.daily_time }) : t("run.cadence.daily");
  const seconds = schedule.interval_seconds;
  if (!seconds || seconds <= 0) return t("run.cadence.interval");
  if (seconds % 86400 === 0) return t("run.cadence.days", { count: seconds / 86400 });
  if (seconds % 3600 === 0) return t("run.cadence.hours", { count: seconds / 3600 });
  if (seconds % 60 === 0) return t("run.cadence.minutes", { count: seconds / 60 });
  return t("run.cadence.seconds", { count: seconds });
}

/** Presentation metadata must belong to this occurrence, never the latest run. */
export function scheduledRunMetadata(message: Message, run?: Run, occurrence?: ScheduleOccurrence, schedule?: Schedule, timezone?: string) {
  const t = scheduleT();
  const owningRun = message.run_id && run?.id === message.run_id ? run : undefined;
  const exactOccurrence = message.run_id && occurrence?.root_run_id === message.run_id ? occurrence : undefined;
  const matchedSchedule = exactOccurrence && schedule?.id === exactOccurrence.schedule_id && schedule.conversation_id === message.conversation_id ? schedule : undefined;
  const displayTimezone = timezone && validTimezone(timezone) ? timezone : browserTimezone();
  const time = (value: string) => new Intl.DateTimeFormat(intlLocale(), { timeZone: displayTimezone, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(value));
  const date = (value: string) => new Intl.DateTimeFormat(intlLocale(), { timeZone: displayTimezone, year: "numeric", month: "long", day: "numeric" }).format(new Date(value));
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
  const scheduleZone = exactOccurrence?.kind === "daily" && exactOccurrence.timezone && exactOccurrence.timezone !== displayTimezone ? exactOccurrence.timezone : "";
  const creator = exactOccurrence?.created_by ?? matchedSchedule?.created_by;
  const creatorLabel = creator === "user" ? t("run.created_by_user") : creator === "bot" ? t("run.created_by_bot") : "";
  const occurrenceLabel = exactOccurrence?.occurrence_number ? t("run.occurrence", { number: exactOccurrence.occurrence_number }) : "";

  // Never borrow edited live labels for a historical occurrence. Missing
  // snapshots get neutral labels, never a first line of the execution prompt.
  const display = scheduleDisplay(exactOccurrence);
  return {
    ...display,
    plannedAt,
    plannedTime: plannedAt ? time(plannedAt) : t("run.planned_unknown"),
    plannedTimeDescription: plannedAt ? t("run.planned_description", { date: date(plannedAt), time: time(plannedAt), zone: displayTimezone }) : t("run.planned_description_unknown"),
    metadata: [recurrence ? scheduleZone ? t("run.with_zone", { recurrence, zone: scheduleZone }) : recurrence : plannedAt ? t("run.triggered_on", { date: date(plannedAt) }) : t("run.metadata_fallback"), occurrenceLabel, creatorLabel, createdAt ? t("run.created_on", { date: date(createdAt) }) : ""].filter(Boolean).join(" · "),
    metadataDescription: recurrence ? t("run.metadata_snapshot") : t("run.metadata_no_snapshot"),
    statusAt: terminal ? statusAt : undefined,
    statusTime: terminal && statusAt ? time(statusAt) : undefined,
    statusTimeDescription: terminal && statusAt ? t("run.status_description", { date: date(statusAt), time: time(statusAt), zone: displayTimezone }) : undefined,
    runningSince: state === "running" ? statusAt : undefined,
  };
}
