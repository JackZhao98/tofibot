import { currentLanguage, type Language } from "./index";

/**
 * Locale-aware formatting. Always pass the user's timezone (useUserTimezone)
 * for wall-clock values; never hardcode "zh-CN" or "en-US" for display.
 * Machine formats (date keys, sorting) may keep a fixed locale such as "en-CA".
 */
export function intlLocale(language: Language = currentLanguage()): string {
  return language;
}

const valid = (value: string | number | Date) => {
  const date = value instanceof Date ? value : new Date(value);
  return Number.isFinite(date.getTime()) ? date : undefined;
};

export function formatDateTime(value: string | number | Date, options: Intl.DateTimeFormatOptions = { dateStyle: "medium", timeStyle: "short" }, language?: Language): string {
  const date = valid(value);
  return date ? new Intl.DateTimeFormat(intlLocale(language), options).format(date) : "";
}

/** 24-hour clock time, e.g. 14:05 or 14:05:09. */
export function formatClock(value: string | number | Date, { timeZone, seconds = false, language }: { timeZone?: string; seconds?: boolean; language?: Language } = {}): string {
  return formatDateTime(value, { hour: "2-digit", minute: "2-digit", ...(seconds ? { second: "2-digit" } : {}), hourCycle: "h23", ...(timeZone ? { timeZone } : {}) }, language);
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions, language?: Language): string {
  return new Intl.NumberFormat(intlLocale(language), options).format(value);
}

const relativeUnits: [Intl.RelativeTimeFormatUnit, number][] = [["year", 31536000], ["month", 2592000], ["week", 604800], ["day", 86400], ["hour", 3600], ["minute", 60], ["second", 1]];
/** "3 minutes ago" / "3 分钟前"; the largest whole unit wins. */
export function formatRelativeTime(value: string | number | Date, now: number = Date.now(), language?: Language): string {
  const date = valid(value);
  if (!date) return "";
  const seconds = Math.round((date.getTime() - now) / 1000);
  const [unit, size] = relativeUnits.find(([, size]) => Math.abs(seconds) >= size) ?? ["second", 1];
  return new Intl.RelativeTimeFormat(intlLocale(language), { numeric: "auto" }).format(Math.round(seconds / size), unit);
}

/** "A, B and C" in the active language. */
export function formatList(items: string[], type: "conjunction" | "disjunction" = "conjunction", language?: Language): string {
  return new Intl.ListFormat(intlLocale(language), { style: "long", type }).format(items);
}
