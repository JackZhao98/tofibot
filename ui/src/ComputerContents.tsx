import {useTranslation} from "./i18n";
import {TofiIcon, type TofiIconName} from "./icons";
import {useSettingsActive} from "./settingsActivity";
import {useComputerContents, type ComputerContents as Contents} from "./useComputerContents";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import "./computer-contents.css";

/**
 * Settings > Computer, "What is on this Bot computer" (docs/design/computer-decoupling.md section 7.5). The Bot's own data
 * (chats, memory, files it sent, connections) lives on the server; these four things live only on the computer and are
 * lost with it. Mounted by the Computer page only while a computer is attached.
 */
export function ComputerContents({openTab}: {openTab: (tab: SettingsTab) => void}) {
  const active = useSettingsActive();
  const {data, failed, retry} = useComputerContents(active);
  return <ComputerContentsView data={data} failed={failed} retry={retry} openTab={openTab}/>;
}

/** The rows without the request: `data` is null while the list loads. */
export function ComputerContentsView({data, failed, retry, openTab}: {data: Contents | null; failed: boolean; retry: () => void; openTab: (tab: SettingsTab) => void}) {
  const {t} = useTranslation("computer");
  const saved = data ? data.environment.length + data.ssh_keys.length : 0;
  const badge = (icon: TofiIconName) => <span className="computer-kept"><TofiIcon name={icon} size={14} aria-hidden="true"/>{t("contents.badge")}</span>;
  return <SettingsSection title={t("contents.title")} description={t("contents.description")} className="computer-contents">
    <SettingsCard>
      <SettingsRow label={t("contents.browser.name")} description={t("contents.browser.what")} control={badge("browser")}/>
      <SettingsRow label={t("contents.tools.name")} description={t("contents.tools.what")} control={badge("terminal")}/>
      <SettingsRow label={t("contents.files.name")} description={<>{t("contents.files.what")}{data && data.pending_file_copies > 0 && <span className="computer-row-note" role="status">{t("contents.files.pending", {count: data.pending_file_copies})}</span>}</>} control={badge("folder")}/>
      <SettingsRow label={t("contents.config.name")} description={<>{t("contents.config.what")}<span className="computer-row-note">{data ? (saved > 0 ? t("contents.config.saved", {count: saved}) : t("contents.config.none")) : " "}</span></>}
        control={<button type="button" className="secondary-button" onClick={() => openTab("keys")}>{t("contents.config.manage")}</button>}/>
    </SettingsCard>
    {failed && <p className="computer-loss-note is-error" role="alert">{t("contents.load_failed")} <button type="button" className="text-button" onClick={retry}>{t("contents.retry")}</button></p>}
  </SettingsSection>;
}
