import { i18n } from "./i18n";
import { formatList, intlLocale } from "./i18n/format";
import { WEEKDAYS, type Weekday } from "./types";

// 2024-01-01 is a Monday, so index i of WEEKDAYS is day i + 1 of that month.
const weekdayOffset = (day: Weekday) => WEEKDAYS.indexOf(day);

/** Localized weekday name via Intl; the catalog picks short or long per language. */
export function weekdayName(day: Weekday): string {
  const style = i18n.t("schedules:run.cadence.weekday_style") === "long" ? "long" : "short";
  return new Intl.DateTimeFormat(intlLocale(), { weekday: style, timeZone: "UTC" }).format(new Date(Date.UTC(2024, 0, 1 + weekdayOffset(day))));
}

/** "Every Sun at 00:43" / "Every Mon, Wed and Fri at 08:00", in week order. */
export function weeklyCadence(weekdays: readonly Weekday[] | undefined, time: string | undefined): string {
  const days = WEEKDAYS.filter(day => weekdays?.includes(day));
  if (!days.length) return i18n.t("schedules:run.cadence.weekly");
  const list = formatList(days.map(weekdayName));
  return time ? i18n.t("schedules:run.cadence.weekly_at", { days: list, time }) : i18n.t("schedules:run.cadence.weekly_on", { days: list });
}
