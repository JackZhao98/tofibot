import { useState } from "react";
import { request } from "./api";
import { errorText } from "./i18n/errors";
import { LANGUAGES, LANGUAGE_ENDONYMS, isLanguagePreference, matchLanguage, preferenceToServer, setLanguagePreference, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
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
    await setLanguagePreference(next);
    try { await request("/api/preferences", { method: "PUT", body: JSON.stringify({ language: preferenceToServer(next) }) }); }
    catch (cause) { setError(errorText(cause, "settings", t("language.save_failed"))); }
    finally { setBusy(false); }
  }
  return <section className="timezone-setting language-setting">
    <div><h3>{t("language.title")}{language !== "en" && <span lang="en"> · Language</span>}</h3><p className="field-note">{t("language.note")}</p></div>
    <label className="timezone-select">{t("language.label")}
      <select value={preference} disabled={busy} onChange={event => { if (isLanguagePreference(event.target.value)) void choose(event.target.value); }}>
        <option value="auto">{t("language.auto", { language: LANGUAGE_ENDONYMS[browser] })}</option>
        {LANGUAGES.map(code => <option key={code} value={code} lang={code}>{LANGUAGE_ENDONYMS[code]}</option>)}
      </select>
    </label>
    {error && <p className="timezone-error" role="alert">{error}</p>}
  </section>;
}
