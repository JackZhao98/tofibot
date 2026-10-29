import { useState } from "react";
import { chooseLanguage } from "./saveLanguage";
import { isLanguagePreference, matchLanguage, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
import { languageChoices, selectedLanguageChoice } from "./languageChoices";
import "./timezone-settings.css";

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
    <div><h3>{t("language.title")}{language !== "en" && <span lang="en"> · Language</span>}</h3></div>
    <label className="timezone-select">{t("language.label")}
      <select value={selectedLanguageChoice(preference, browser)} disabled={busy} onChange={event => { if (isLanguagePreference(event.target.value)) void choose(event.target.value); }}>
        {languageChoices(browser).map(choice => <option key={choice.value} value={choice.value} lang={choice.lang}>{choice.tag ? `${choice.label} · ${t("language.default")}` : choice.label}</option>)}
      </select>
    </label>
    {error && <p className="timezone-error" role="alert">{error}</p>}
  </section>;
}
