/// <reference types="vite/client" />
import i18next from "i18next";
import { initReactI18next } from "react-i18next";
import { useSyncExternalStore } from "react";
import { COMPLETE_LANGUAGES, DEFAULT_NAMESPACE, LANGUAGES, NAMESPACES, SOURCE_LANGUAGE, isLanguage, isLanguagePreference, resolveLanguage, type Language, type LanguagePreference } from "./languages";

export * from "./languages";
export { useTranslation, Trans } from "react-i18next";

type Catalog = Record<string, unknown>;
type Bundle = Record<string, Catalog>;

/** "../locales/zh-CN/tasks.json" -> "tasks" */
function byNamespace(files: Record<string, Catalog>): Bundle {
  const bundle: Bundle = {};
  for (const [path, catalog] of Object.entries(files)) bundle[path.slice(path.lastIndexOf("/") + 1).replace(/\.json$/, "")] = catalog;
  return bundle;
}

// Every language, English included, is one lazy chunk (locales/<lang>/index.ts),
// so the main bundle carries no catalog text. English is also loaded as the
// fallback for languages that are not yet complete.
const bundles = import.meta.glob<Record<string, Catalog>>("../locales/*/index.ts", { import: "default" });

const STORAGE_KEY = "tofi.language";

function readStoredPreference(): LanguagePreference {
  try {
    const value = globalThis.localStorage?.getItem(STORAGE_KEY);
    return isLanguagePreference(value) ? value : "auto";
  } catch { return "auto"; }
}
function writeStoredPreference(preference: LanguagePreference) {
  try { globalThis.localStorage?.setItem(STORAGE_KEY, preference); } catch { /* private mode: the server copy still applies after sign-in */ }
}
function browserTags(): readonly string[] {
  if (typeof navigator === "undefined") return [];
  return navigator.languages?.length ? navigator.languages : navigator.language ? [navigator.language] : [];
}

void i18next.use(initReactI18next).init({
  resources: {},
  lng: SOURCE_LANGUAGE,
  fallbackLng: SOURCE_LANGUAGE,
  supportedLngs: [...LANGUAGES],
  load: "currentOnly",
  ns: [...NAMESPACES],
  defaultNS: DEFAULT_NAMESPACE,
  fallbackNS: false,
  partialBundledLanguages: true,
  initAsync: false,
  returnEmptyString: false,
  interpolation: { escapeValue: false },
  react: { useSuspense: false },
});

export const i18n = i18next;

const loading = new Map<Language, Promise<void>>();
function loadBundle(language: Language): Promise<void> {
  let pending = loading.get(language);
  if (!pending) {
    const loader = bundles[`../locales/${language}/index.ts`];
    pending = (loader ? loader() : Promise.resolve({})).then(files => {
      for (const [namespace, catalog] of Object.entries(byNamespace(files))) i18next.addResourceBundle(language, namespace, catalog, true, true);
    });
    // A failed chunk (offline, deploy in progress) may be retried later.
    pending.catch(() => loading.delete(language));
    loading.set(language, pending);
  }
  return pending;
}
/**
 * Load a language's catalogs (plus English when the language may have gaps)
 * without switching to it. Safe to call repeatedly.
 */
export function loadLanguage(language: Language): Promise<void> {
  const fallback = (COMPLETE_LANGUAGES as readonly Language[]).includes(language) ? [] : [SOURCE_LANGUAGE];
  return Promise.all([language, ...fallback].map(loadBundle)).then(() => undefined);
}

let preference: LanguagePreference = readStoredPreference();
const listeners = new Set<() => void>();
const notify = () => listeners.forEach(listener => listener());

function applyDocumentLanguage(language: Language) {
  if (typeof document !== "undefined") document.documentElement.lang = language;
}

/** Switch the UI language. English stays visible if a catalog fails to load. */
let switches = 0;
export async function setLanguage(language: Language): Promise<void> {
  const current = ++switches;
  try { await loadLanguage(language); }
  catch {
    language = SOURCE_LANGUAGE;
    await loadLanguage(language).catch(() => { /* keys show until the next reload */ });
  }
  // A later choice that loaded first wins; a slow chunk must not switch back.
  if (current !== switches) return;
  await i18next.changeLanguage(language);
  applyDocumentLanguage(language);
  notify();
}

/** Record and apply a choice. The caller persists it server-side. */
export function setLanguagePreference(next: LanguagePreference): Promise<void> {
  preference = next;
  writeStoredPreference(next);
  notify();
  return setLanguage(resolveLanguage(next, browserTags()));
}

/** The server stores "" for automatic. Unknown values are treated as automatic. */
export function preferenceFromServer(value: unknown): LanguagePreference {
  return isLanguage(value) ? value : "auto";
}
export function preferenceToServer(value: LanguagePreference): string {
  return value === "auto" ? "" : value;
}
/** Apply the account's stored choice after sign-in, if it differs from this device. */
export function syncServerLanguagePreference(value: unknown): Promise<void> | undefined {
  const next = preferenceFromServer(value);
  if (next === preference) return undefined;
  return setLanguagePreference(next);
}

/** The chosen language (also for Intl), even while its catalog still falls back to English. */
export function currentLanguage(): Language {
  const language = i18next.language;
  return isLanguage(language) ? language : SOURCE_LANGUAGE;
}
export function currentLanguagePreference(): LanguagePreference { return preference; }

/** Re-renders on language or preference change. */
export function useLanguage(): { language: Language; preference: LanguagePreference } {
  const language = useSyncExternalStore(subscribe, currentLanguage, currentLanguage);
  const saved = useSyncExternalStore(subscribe, currentLanguagePreference, currentLanguagePreference);
  return { language, preference: saved };
}
function subscribe(listener: () => void) {
  listeners.add(listener);
  i18next.on("languageChanged", listener);
  return () => { listeners.delete(listener); i18next.off("languageChanged", listener); };
}

/** Resolves once the initial language is ready; render after it to avoid a flash of English. */
export const i18nReady: Promise<void> = setLanguage(resolveLanguage(preference, browserTags()));
