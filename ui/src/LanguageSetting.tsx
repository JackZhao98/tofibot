import { useState } from "react";
import { chooseLanguage } from "./saveLanguage";
import { isLanguagePreference, matchLanguage, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
import { languageChoices, selectedLanguageChoice } from "./languageChoices";
import { SettingsRow } from "./ds/SettingsRow/SettingsRow";
import { Select } from "./Select";
import { SavedNote, useSavedFlash } from "./settings/components";
import "./settings/general.css";

/**
 * Settings › General › Language. A choice applies at once (like the theme), then saves to
 * the account so other devices follow. Each language is named in its own script.
 */
export function LanguageSetting() {
  const { t } = useTranslation("settings");
  const { language, preference } = useLanguage();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, flash] = useSavedFlash();
  const browser = matchLanguage(typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language]) ?? "en";
  async function choose(next: LanguagePreference) {
    setBusy(true); setError("");
    const failure = await chooseLanguage(next, t("language.save_failed"));
    setError(failure);
    setBusy(false);
    if (!failure) flash();
  }
  return <>
    <SettingsRow fill label={<>{t("language.title")}{language !== "en" && <span lang="en"> · Language</span>}</>} data-setting="language">
      <span className="gen-saved-ctl"><SavedNote show={saved} />
        <span className="gen-ctl"><Select aria-label={t("language.label")} value={selectedLanguageChoice(preference, browser)} disabled={busy} onValueChange={value => { if (isLanguagePreference(value)) void choose(value); }}
          options={languageChoices(browser).map(choice => ({ value: choice.value, label: choice.label, tag: choice.tag ? t("language.default") : undefined }))} /></span>
      </span>
    </SettingsRow>
    {error && <p className="gen-note gen-note--danger" role="alert">{error}</p>}
  </>;
}
