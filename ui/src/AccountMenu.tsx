import { useState, type RefObject } from "react";
import { useAppearance } from "./InteractionSystem";
import { OverlayMenu, OverlaySubmenu } from "./overlays/OverlayHost";
import { TofiIcon } from "./icons";
import { matchLanguage, useLanguage, useTranslation, type LanguagePreference } from "./i18n";
import { languageChoices, selectedLanguageChoice } from "./languageChoices";
import { chooseLanguage } from "./saveLanguage";
import type { OwnerSession } from "./ownerAuthApi";

/** Project links in the account menu. They open in a new tab; discussions are off on the repository, so feedback goes to issues. */
const REPO_URL = "https://github.com/JackZhao98/tofibot";
const projectLinks = [
  ["report_issue", "/issues/new", "alert"],
  ["send_feedback", "/issues/new?title=Feedback%3A%20", "chat"],
  ["report_security", "/security/advisories/new", "shield-check"],
  ["github", "", "link"],
] as const;

/** The sidebar account menu body; kept out of the entry chunk. */
export function AccountMenu({ setOpen, name, host, trigger, menu, session, logout, onSettings, onUsage, onConnection, onArtifacts }: { setOpen: (open: boolean) => void; name: string; host: string; trigger: RefObject<HTMLButtonElement | null>; menu: RefObject<HTMLDivElement | null>; session: OwnerSession | null; logout: () => Promise<void>; onSettings: () => void; onUsage: () => void; onConnection: () => void; onArtifacts: () => void }) {
  const appearance = useAppearance();
  const { preference: language } = useLanguage();
  const browserLanguage = matchLanguage(typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language]) ?? "en";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation(["auth", "common", "settings"]);
  const pickLanguage = (next: LanguagePreference) => { setError(""); void chooseLanguage(next, t("settings:language.save_failed")).then(setError); };
  const navigate = (action: () => void) => { setOpen(false); action(); };
  return <OverlayMenu open onOpenChange={setOpen} className="sidebar-account-menu" label={name} trigger={trigger} anchor={menu} sideOffset={10} {...(menu.current?.closest(".sidebar-collapsed") ? { side: "right", align: "end" } : { side: "top" })}>
      <div className="sidebar-account-menu-heading"><strong>{name}</strong><small>{host}</small></div>
      <button onClick={() => navigate(onSettings)}><TofiIcon name="settings" size={16}/>{t("common:nav.settings")}</button>
      <button onClick={() => navigate(onUsage)}><TofiIcon name="activity" size={16}/>{t("common:nav.usage")}</button>
      <div className="sidebar-account-themes" role="group" aria-label={t("common:theme.label")}>{(["system","light","dark"] as const).map(theme => <button key={theme} role="menuitemradio" aria-checked={appearance.preference === theme} data-keep-open="" onClick={() => appearance.choose(theme)}>{t(`common:theme.${theme}`)}</button>)}</div>
      <OverlaySubmenu label={t("common:language.label")} icon={<TofiIcon name="globe" size={16}/>} className="sidebar-account-menu sidebar-account-submenu">
        {languageChoices(browserLanguage).map(choice => <button key={choice.value} role="menuitemradio" lang={choice.lang} aria-checked={selectedLanguageChoice(language, browserLanguage) === choice.value} data-keep-open="" onClick={() => pickLanguage(choice.value)}>{choice.label}{choice.tag && <span className="menu-default-tag">{t("settings:language.default")}</span>}</button>)}
      </OverlaySubmenu>
      <button onClick={() => navigate(onConnection)}><TofiIcon name="server" size={16}/>{t("common:nav.connection")}</button>
      <button onClick={() => navigate(onArtifacts)}><TofiIcon name="folder" size={16}/>{t("common:nav.artifacts")}</button>
      <div className="sidebar-account-menu-rule"/>
      {projectLinks.map(([key, path, icon]) => <button key={key} className="sidebar-account-link" onClick={() => { setOpen(false); window.open(REPO_URL + path, "_blank", "noopener,noreferrer"); }}><TofiIcon name={icon} size={16}/><span>{t(`auth:account.${key}`)}</span><TofiIcon name="external-link" size={14} aria-hidden="true"/></button>)}
      {session?.enabled && <><div className="sidebar-account-menu-rule"/><button className="sidebar-account-logout" disabled={busy} onClick={() => { setBusy(true); setError(""); void logout().catch(cause => setError(cause instanceof Error ? cause.message : t("account.sign_out_failed"))).finally(() => setBusy(false)); }}>{busy ? t("account.signing_out") : t("account.sign_out")}</button></>}
      {error && <p className="error-text" role="alert">{error}</p>}
  </OverlayMenu>;
}
