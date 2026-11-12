import { useState, type RefObject } from "react";
import { useAppearance } from "./InteractionSystem";
import { OverlayMenu, OverlaySubmenu } from "./overlays/OverlayHost";
import { TofiIcon } from "./icons";
import { matchLanguage, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
import { languageChoices, selectedLanguageChoice } from "./languageChoices";
import { chooseLanguage } from "./saveLanguage";
import type { OwnerSession } from "./ownerAuthApi";

/** The sidebar account menu body; kept out of the entry chunk. */
export function AccountMenu({ setOpen, name, trigger, menu, session, logout, onSettings, onUsage, onArtifacts }: { setOpen: (open: boolean) => void; name: string; trigger: RefObject<HTMLButtonElement | null>; menu: RefObject<HTMLDivElement | null>; session: OwnerSession | null; logout: () => Promise<void>; onSettings: () => void; onUsage: () => void; onArtifacts: () => void; }) {
  const appearance = useAppearance();
  const { preference: language } = useLanguage();
  const browserLanguage = matchLanguage(typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language]) ?? "en";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation(["auth", "common", "settings"]);
  const pickLanguage = (next: LanguagePreference) => { setError(""); void chooseLanguage(next, t("settings:language.save_failed")).then(setError); };
  const navigate = (action: () => void) => { setOpen(false); action(); };
  return <OverlayMenu open onOpenChange={setOpen} className="sidebar-account-menu" label={name} trigger={trigger} anchor={menu} sideOffset={10} {...(menu.current?.closest(".sidebar-collapsed") ? { side: "right", align: "end" } : { side: "top" })}>
      <div className="sidebar-account-menu-heading"><strong>{name}</strong></div>
      <button onClick={() => navigate(onSettings)}><TofiIcon name="settings" size={16}/>{t("common:nav.settings")}</button>
      <button onClick={() => navigate(onUsage)}><TofiIcon name="activity" size={16}/>{t("common:nav.usage")}</button>
      <button onClick={() => navigate(onArtifacts)}><TofiIcon name="folder" size={16}/>{t("common:nav.artifacts")}</button>
      <OverlaySubmenu label={t("common:language.label")} icon={<TofiIcon name="globe" size={16}/>} className="sidebar-account-menu sidebar-account-submenu">
        {languageChoices(browserLanguage).map(choice => <button key={choice.value} role="menuitemradio" lang={choice.lang} aria-checked={selectedLanguageChoice(language, browserLanguage) === choice.value} data-keep-open="" onClick={() => pickLanguage(choice.value)}>{choice.label}{choice.tag && <span className="menu-default-tag">{t("settings:language.default")}</span>}</button>)}
      </OverlaySubmenu>
      <div className="sidebar-account-menu-rule"/>
      <div className="sidebar-account-menu-footer">
        {session?.enabled && <button className="sidebar-account-logout" disabled={busy} onClick={() => { setBusy(true); setError(""); void logout().catch(cause => setError(cause instanceof Error ? cause.message : t("account.sign_out_failed"))).finally(() => setBusy(false)); }}><TofiIcon name="logout" size={16}/>{busy ? t("account.signing_out") : t("account.sign_out")}</button>}
        <button className="sidebar-account-theme" data-keep-open="" aria-label={t(`common:theme.${appearance.preference}`)} data-hint={t(`common:theme.${appearance.preference}`)} title={t(`common:theme.${appearance.preference}`)} onClick={() => appearance.choose(appearance.preference === "system" ? "light" : appearance.preference === "light" ? "dark" : "system")}><TofiIcon name={appearance.preference === "system" ? "monitor" : appearance.preference === "light" ? "sun" : "moon"} size={16}/></button>
      </div>
      {error && <p className="error-text" role="alert">{error}</p>}
  </OverlayMenu>;
}
