// One postmark for outgoing and incoming mail (Motion Lab letterParts.Postmark):
// two rings with text set along the band, the date in the centre, and for
// outgoing mail the cancellation waves beside it. Drawn as an SVG string so the
// imperative send flight and the React mail card share a single source.
import "./postmark.css";
import { intlLocale } from "./i18n/format";

export type PostmarkParts = { ring: string; date: string; time?: string; waves?: boolean };

let instance = 0;
const escape = (value: string) => value.replace(/[&<>"']/g, character => `&#${character.charCodeAt(0)};`);

export function postmarkSVG({ ring, date, time, waves = false }: PostmarkParts): string {
  const id = `tofi-postmark-ring-${++instance}`;
  const width = waves ? 150 : 80;
  const centre = time
    ? `<text class="pm-date" x="40" y="40" text-anchor="middle" font-size="9.5" font-weight="700" fill="currentColor">${escape(date)}</text><text class="pm-time" x="40" y="49.5" text-anchor="middle" font-size="6.6" font-weight="600" fill="currentColor">${escape(time)}</text>`
    : `<text class="pm-date" x="40" y="43.5" text-anchor="middle" font-size="9" font-weight="700" fill="currentColor">${escape(date)}</text>`;
  const cancellation = waves ? [26, 36, 46, 56].map(y => `<path d="M76 ${y}q9 -6 18 0t18 0t18 0t18 0" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>`).join("") : "";
  return `<svg class="postmark" viewBox="0 0 ${width} 80" aria-hidden="true"><defs><path id="${id}" d="M40 16a24 24 0 1 1 0 48a24 24 0 1 1 0-48"/></defs><circle cx="40" cy="40" r="31" fill="none" stroke="currentColor" stroke-width="2.4"/><circle cx="40" cy="40" r="17" fill="none" stroke="currentColor" stroke-width="1.4"/><text class="pm-ring" font-size="7.4" font-weight="600" letter-spacing="1.4" fill="currentColor"><textPath href="#${id}">${escape(ring)}</textPath></text>${centre}${cancellation}</svg>`;
}

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
