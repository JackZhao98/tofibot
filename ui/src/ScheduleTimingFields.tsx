import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import { weekdayName } from "./scheduleCadence";
import { dateTimeFormat } from "./timezone";
import { TimezoneSelect } from "./UserTimezone";
import { WEEKDAYS, type Schedule, type ScheduleKind, type SchedulePatchInput, type Weekday } from "./types";
import { Select } from "./Select";

/** The repeat / time / weekday / interval / zone controls of a schedule, shared by the
 * create form (WorkPanel) and the schedule editor. `zone` is the person's override;
 * the caller resolves the zone it shows and passes it as `timezone`. */
export type TimingDraft = { kind: ScheduleKind; runAt: string; dailyTime: string; weekdays: Weekday[]; interval: string; zone: string };

export const defaultTimingDraft: TimingDraft = { kind: "once", runAt: "", dailyTime: "09:00", weekdays: ["mon"], interval: "60", zone: "" };

/** The `YYYY-MM-DDTHH:mm` wall clock of an instant in a zone (what datetime-local shows). */
export function localInputValue(iso: string, timeZone: string): string {
  if (!iso || !Number.isFinite(Date.parse(iso))) return "";
  const parts = Object.fromEntries(dateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(new Date(iso)).map(part => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
}

/** The draft an editor opens with: the stored timing, in the schedule's own zone. */
export function timingDraftOf(schedule: Schedule): TimingDraft {
  return {
    kind: schedule.kind,
    runAt: schedule.kind === "once" || schedule.kind === "interval" ? localInputValue(schedule.next_at_utc, schedule.timezone) : "",
    dailyTime: schedule.daily_time || "09:00",
    weekdays: WEEKDAYS.filter(day => schedule.weekdays?.includes(day)),
    interval: String(Math.max(1, Math.round((schedule.interval_seconds || 3600) / 60))),
    zone: schedule.timezone,
  };
}

/** The timing fields of a create request for a draft. */
export function timingCreateFields(draft: TimingDraft, timezone: string) {
  const { kind } = draft;
  return {
    kind, timezone,
    ...(kind === "daily" ? { daily_time: draft.dailyTime } : kind === "weekly" ? { daily_time: draft.dailyTime, weekdays: WEEKDAYS.filter(day => draft.weekdays.includes(day)) } : { run_at: draft.runAt }),
    ...(kind === "interval" ? { interval_seconds: Math.round(Number(draft.interval) * 60) } : {}),
  };
}

/** Only what changed against the draft the editor opened with, plus the baseline that
 * guards it; undefined when the timing is unchanged. */
export function timingEditPatch(schedule: Schedule, baseline: TimingDraft, draft: TimingDraft): Pick<SchedulePatchInput, "kind" | "run_at" | "interval_seconds" | "daily_time" | "weekdays" | "timezone" | "expected_timing"> | undefined {
  const zone = draft.zone || schedule.timezone;
  const days = WEEKDAYS.filter(day => draft.weekdays.includes(day));
  const kindChanged = draft.kind !== baseline.kind;
  const zoneChanged = zone !== (baseline.zone || schedule.timezone);
  const clock = draft.kind === "daily" || draft.kind === "weekly";
  const changed = kindChanged || zoneChanged ||
    (clock && (draft.dailyTime !== baseline.dailyTime || (draft.kind === "weekly" && days.join() !== baseline.weekdays.join()))) ||
    (draft.kind === "once" && draft.runAt !== baseline.runAt) ||
    (draft.kind === "interval" && (draft.interval !== baseline.interval || draft.runAt !== baseline.runAt));
  if (!changed) return undefined;
  const patch: NonNullable<ReturnType<typeof timingEditPatch>> = {
    expected_timing: { kind: schedule.kind, timezone: schedule.timezone, interval_seconds: schedule.interval_seconds || 0, daily_time: schedule.daily_time || "", weekdays: schedule.weekdays || [], ...(schedule.kind === "once" ? { next_at_utc: schedule.next_at_utc } : {}) },
  };
  if (kindChanged) patch.kind = draft.kind;
  if (zoneChanged) patch.timezone = zone;
  if (clock) {
    if (kindChanged || draft.dailyTime !== baseline.dailyTime) patch.daily_time = draft.dailyTime;
    if (draft.kind === "weekly" && (kindChanged || days.join() !== baseline.weekdays.join())) patch.weekdays = days;
  }
  // A one-time run keeps its wall clock when only the zone moves; an interval without a
  // new first run starts one interval from now (the server's rule).
  if (draft.kind === "once" && (kindChanged || zoneChanged || draft.runAt !== baseline.runAt)) patch.run_at = draft.runAt;
  if (draft.kind === "interval") {
    if (kindChanged || draft.interval !== baseline.interval) patch.interval_seconds = Math.round(Number(draft.interval) * 60);
    if (draft.runAt && draft.runAt !== baseline.runAt) patch.run_at = draft.runAt;
  }
  return patch;
}

export function ScheduleTimingFields({ draft, timezone, onChange, disabled }: { draft: TimingDraft; timezone: string; onChange: (change: Partial<TimingDraft>) => void; disabled?: boolean }) {
  const { t } = useTranslation("work");
  const { kind } = draft;
  const clock = kind === "daily" || kind === "weekly";
  return <>
    <label>{t("create.repeat")}<Select aria-label={t("create.repeat_aria")} value={kind} disabled={disabled} onValueChange={value => onChange({ kind: value as ScheduleKind })}><option value="once">{t("create.repeat_once")}</option><option value="daily">{t("create.repeat_daily")}</option><option value="weekly">{t("create.repeat_weekly")}</option><option value="interval">{t("create.repeat_interval")}</option></Select></label>
    {kind === "weekly" && <fieldset className="work-weekdays" aria-label={t("create.weekdays")} disabled={disabled}><legend>{t("create.weekdays")}</legend>{WEEKDAYS.map(day => <button key={day} type="button" aria-pressed={draft.weekdays.includes(day)} onClick={() => onChange({ weekdays: draft.weekdays.includes(day) ? draft.weekdays.filter(value => value !== day) : [...draft.weekdays, day] })}>{weekdayName(day)}</button>)}</fieldset>}
    <label>{clock ? t("create.daily_time") : t("create.run_time")}{clock ? <input type="time" required disabled={disabled} value={draft.dailyTime} onChange={event => onChange({ dailyTime: event.target.value })} /> : <input type="datetime-local" required disabled={disabled} value={draft.runAt} onChange={event => onChange({ runAt: event.target.value })} />}</label>
    {kind === "interval" && <label>{t("create.interval_minutes")}<input type="number" min="1" max="525600" value={draft.interval} disabled={disabled} onChange={event => onChange({ interval: event.target.value })} required /></label>}
    <Disclosure title={t("create.timezone", { zone: timezone })}><TimezoneSelect value={timezone} disabled={disabled} onChange={zone => onChange({ zone })} /></Disclosure>
  </>;
}
