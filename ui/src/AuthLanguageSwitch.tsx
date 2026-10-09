import "./owner-setup.css";
import { LANGUAGES, LANGUAGE_ENDONYMS, isLanguagePreference, matchLanguage, setLanguagePreference, useLanguage, useTranslation } from "./i18n";

/**
 * Language picker for the sign-in and setup screens, before any account exists.
 * Same choices as Settings › General (LanguageSetting); the choice stays on this
 * device, and "Auto" follows the browser.
 */
export function AuthLanguageSwitch() {
  const { t } = useTranslation("auth");
  const { preference } = useLanguage();
  const browser = matchLanguage(typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language]) ?? "en";
  return <label className="auth-language">
    <span className="sr-only">{t("language.label")}</span>
    <select value={preference} aria-label={t("language.label")} onChange={event => { if (isLanguagePreference(event.target.value)) void setLanguagePreference(event.target.value); }}>
      <option value="auto">{t("language.auto", { language: LANGUAGE_ENDONYMS[browser] })}</option>
      {LANGUAGES.map(code => <option key={code} value={code} lang={code}>{LANGUAGE_ENDONYMS[code]}</option>)}
    </select>
  </label>;
}
