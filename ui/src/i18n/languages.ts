/**
 * Pure language data and matching. No i18next, no DOM: the checker script and
 * tests can import this file directly.
 */
export const LANGUAGES = ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"] as const;
export type Language = (typeof LANGUAGES)[number];
/** "auto" follows the browser. The server stores it as an empty string. */
export type LanguagePreference = Language | "auto";
export const SOURCE_LANGUAGE: Language = "en";
/**
 * Languages whose catalogs must hold every key (scripts/check-i18n.mjs enforces
 * it). They load without the English fallback chunk. Add a language once its
 * translation is complete.
 */
export const COMPLETE_LANGUAGES = ["en", "zh-CN"] as const;

/**
 * One namespace per product area. Parallel workers own whole namespaces, so a
 * namespace file is never edited by two batches at once. Adding a namespace
 * means adding it here, to i18next.d.ts and to every locale directory.
 */
export const NAMESPACES = ["common", "auth", "chat", "tasks", "schedules", "work", "settings", "extensions", "computer", "bots", "mail"] as const;
export type Namespace = (typeof NAMESPACES)[number];
export const DEFAULT_NAMESPACE: Namespace = "common";

/** Each language names itself in its own script; these are data, never translated. */
export const LANGUAGE_ENDONYMS: Record<Language, string> = {
  en: "English",
  "zh-CN": "简体中文",
  "zh-TW": "繁體中文",
  ja: "日本語",
  ko: "한국어",
  de: "Deutsch",
  fr: "Français",
};

export function isLanguage(value: unknown): value is Language {
  return typeof value === "string" && (LANGUAGES as readonly string[]).includes(value);
}

export function isLanguagePreference(value: unknown): value is LanguagePreference {
  return value === "auto" || isLanguage(value);
}

/**
 * Best supported match for a list of BCP 47 tags, in the caller's order.
 * Traditional Chinese (Hant script, or TW/HK/MO without an explicit Hans
 * script) maps to zh-TW; every other Chinese tag maps to zh-CN.
 */
export function matchLanguage(tags: readonly string[]): Language | undefined {
  for (const raw of tags) {
    const parts = raw.trim().toLowerCase().split(/[-_]/).filter(Boolean);
    const base = parts[0];
    if (!base) continue;
    if (base === "zh") {
      if (parts.includes("hans")) return "zh-CN";
      if (parts.some(part => part === "hant" || part === "tw" || part === "hk" || part === "mo")) return "zh-TW";
      return "zh-CN";
    }
    if (base === "en") return "en";
    if (base === "ja" || base === "ko" || base === "de" || base === "fr") return base;
  }
  return undefined;
}

/** Saved choice first, then the browser's ordered list, then English. */
export function resolveLanguage(preference: LanguagePreference | undefined, browserTags: readonly string[]): Language {
  if (preference && preference !== "auto") return preference;
  return matchLanguage(browserTags) ?? SOURCE_LANGUAGE;
}
