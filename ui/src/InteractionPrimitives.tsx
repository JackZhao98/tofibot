// Controls on Radix for lazy chunks (Settings, onboarding, panels). The entry must not import this file (D31, test:dialogs).
import { useEffect, useRef, useState, type ReactNode } from "react";
import * as Collapsible from "@radix-ui/react-collapsible";
import * as RadioGroup from "@radix-ui/react-radio-group";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import type { ThemePreference } from "./InteractionSystem";
export function AppearancePicker({ value, onChange }: { value: ThemePreference; onChange: (theme: ThemePreference) => void }) {
  const { t } = useTranslation("settings");
  // A choice applies at once; "Saved" shows for 1.5 s (the same note as SettingsSwitch, kept local).
  const [saved, setSaved] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const choose = (theme: ThemePreference) => { onChange(theme); setSaved(true); window.clearTimeout(timer.current); timer.current = window.setTimeout(() => setSaved(false), 1500); };
  return <section className="appearance-setting"><div className="appearance-head"><h3>{t("appearance.title")}</h3><span className="settings-saved" aria-live="polite">{saved ? t("saved") : ""}</span></div><RadioGroup.Root value={value} onValueChange={theme => choose(theme as ThemePreference)} className="appearance-options" aria-label={t("appearance.label")}>
    {(["system", "light", "dark"] as const).map((theme) => <RadioGroup.Item key={theme} value={theme}><span className={`theme-swatch theme-swatch-${theme}`} aria-hidden="true"><i /><i /><i /></span><span>{t(`appearance.${theme}`)}</span></RadioGroup.Item>)}
  </RadioGroup.Root></section>;
}


export function Disclosure({ title, children, defaultOpen = false }: { title: ReactNode; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return <Collapsible.Root className="disclosure" open={open} onOpenChange={setOpen}>
    <Collapsible.Trigger className="disclosure-toggle"><span>{title}</span><TofiIcon className="disclosure-chevron" name="chevron-down" size={16} /></Collapsible.Trigger>
    <Collapsible.Content forceMount inert={!open || undefined} aria-hidden={!open} className="disclosure-region collapsible-content"><div className="disclosure-inner">{children}</div></Collapsible.Content>
  </Collapsible.Root>;
}
