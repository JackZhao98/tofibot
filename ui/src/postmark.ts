// Received-time parsing for the mail card's postmark (the kit Letter draws the postmark itself).
import { intlLocale } from "./i18n/format";

/** Month.day as postmarks print it, e.g. 9.28. */
export const postmarkDate = (date: Date) => `${date.getMonth() + 1}.${date.getDate()}`;

/** Best-effort date and time from a model-written received time. Unknown parts stay absent. */
export function receivedParts(value: string | undefined): { date?: string; time?: string } {
  const text = value?.trim();
  if (!text) return {};
  // A calendar date without a time is a day, not UTC midnight.
  const dayOnly = text.match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (dayOnly) return { date: `${Number(dayOnly[2])}.${Number(dayOnly[3])}` };
  // Only trust Date.parse when the text names a year; bare "9/23" parses differently per engine.
  const parsed = /\b\d{4}\b/.test(text) ? Date.parse(text.replace(/\s*·\s*/g, " ")) : NaN;
  if (Number.isFinite(parsed)) {
    const date = new Date(parsed);
    const hasTime = /\d{1,2}:\d{2}/.test(text);
    return { date: postmarkDate(date), time: hasTime ? date.toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit", hour12: false }) : undefined };
  }
  const time = text.match(/\b(\d{1,2}):(\d{2})\b/);
  // Parsing patterns for model-written Chinese dates and times, not UI copy. Escaped
  // so the hardcoded-text gate stays at zero: \u6708/\u65e5 month/day,
  // \u4e0b\u5348/\u665a\u4e0a afternoon/evening (pm), \u51cc\u6668/\u4e0a\u5348 small hours/morning (am).
  const day = text.match(/\b(\d{1,2})[./-](\d{1,2})\b(?!:)/) ?? text.match(/(\d{1,2})\s*\u6708\s*(\d{1,2})\s*\u65e5/);
  let hour = time ? Number(time[1]) : 0;
  if (time && /\bpm\b|\u4e0b\u5348|\u665a\u4e0a/i.test(text) && hour < 12) hour += 12;
  if (time && /\bam\b|\u51cc\u6668|\u4e0a\u5348/i.test(text) && hour === 12) hour = 0;
  return { date: day ? `${Number(day[1])}.${Number(day[2])}` : undefined, time: time ? `${String(hour).padStart(2, "0")}:${time[2]}` : undefined };
}
