import { useState } from "react";
import { chooseLanguage } from "./saveLanguage";
import { LANGUAGES, LANGUAGE_ENDONYMS, isLanguagePreference, matchLanguage, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
import "./timezone-settings.css";
import { Select } from "./Select";

/**
 * Settings › General. A choice applies at once (like appearance), then saves to
 * the account so other devices follow. Each language is named in its own script.
 */
export function LanguageSetting() {
  const { t } = useTranslation("settings");
  const { language, preference } = useLanguage();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const browser = matchLanguage(typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language]) ?? "en";
  async function choose(next: LanguagePreference) {
    setBusy(true); setError("");
    setError(await chooseLanguage(next, t("language.save_failed")));
    setBusy(false);
  }
  return <section className="timezone-setting language-setting">
    <div><h3>{t("language.title")}{language !== "en" && <span lang="en"> · Language</span>}</h3><p className="field-note">{t("language.note")}</p></div>
    <label className="timezone-select">{t("language.label")}
      <Select value={preference} disabled={busy} onValueChange={value => { if (isLanguagePreference(value)) void choose(value); }}>
        <option value="auto">{t("language.auto", { language: LANGUAGE_ENDONYMS[browser] })}</option>
        {LANGUAGES.map(code => <option key={code} value={code} lang={code}>{LANGUAGE_ENDONYMS[code]}</option>)}
      </Select>
    </label>
    {error && <p className="timezone-error" role="alert">{error}</p>}
  </section>;
}
