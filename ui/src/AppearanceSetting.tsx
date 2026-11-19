import { SettingsRow } from "./ds/SettingsRow/SettingsRow";
import { useTranslation } from "./i18n";
import type { ThemePreference } from "./InteractionSystem";
import { SavedNote, Segmented, useSavedFlash } from "./settings/components";
import "./settings/general.css";

/** Settings > General > Theme: a choice applies at once, and "Saved" shows for 1.5 s beside it. */
export function ThemeRow({ value, onChange }: { value: ThemePreference; onChange: (theme: ThemePreference) => void }) {
  const { t } = useTranslation("settings");
  const [saved, flash] = useSavedFlash();
  const choose = (theme: ThemePreference) => { onChange(theme); flash(); };
  return <SettingsRow label={t("appearance.title")} data-setting="theme">
    <span className="gen-saved-ctl"><SavedNote show={saved} />
      <Segmented value={value} onChange={choose} label={t("appearance.label")} options={(["system", "light", "dark"] as const).map(theme => ({ value: theme, label: t(`appearance.${theme}`) }))} />
    </span>
  </SettingsRow>;
}
