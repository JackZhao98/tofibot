import {LANGUAGES, LANGUAGE_ENDONYMS, type Language, type LanguagePreference} from "./i18n/languages";

/** One entry of a language list. `tag` marks the browser-derived default, drawn as a grey "Default" tag. */
export type LanguageChoice = {value: LanguagePreference; label: string; lang: Language; tag?: "default"};

/**
 * The list a language picker shows: the language the browser asks for comes first, named in its own script and tagged
 * "Default" (choosing it stores nothing, so the browser keeps deciding); every other language follows. There is no
 * separate "Auto" entry.
 */
export function languageChoices(browser: Language): LanguageChoice[] {
  return [
    {value: "auto", label: LANGUAGE_ENDONYMS[browser], lang: browser, tag: "default"},
    ...LANGUAGES.filter(code => code !== browser).map(code => ({value: code as LanguagePreference, label: LANGUAGE_ENDONYMS[code], lang: code})),
  ];
}

/** The entry a stored preference selects. Explicitly choosing the browser's own language is the same as the default. */
export function selectedLanguageChoice(preference: LanguagePreference, browser: Language): LanguagePreference {
  return preference === browser ? "auto" : preference;
}
