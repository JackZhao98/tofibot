import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {computerHostCommand} from "./computerCapability";
import {Banner, SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import "./computer-options.css";

/**
 * Settings > Environments on a server with no computer backend. It explains what
 * works without one and lists how a computer can be added. The web app never
 * installs anything on the host: the KVM option is a command the owner runs
 * there, and this page picks the result up through the config poll.
 */
export function ComputerOptions({onAddE2B, onAddMac}: {onAddE2B?: () => void; onAddMac?: () => void} = {}) {
  const {t} = useTranslation("computer");
  return <div className="settings-stack computer-options">
    <Banner tone="info" title={t("none.title")}>{t("none.body")}</Banner>
    <SettingsSection title={t("none.options")}>
      <SettingsCard>
        <SettingsRow label={t("none.kvm.name")} description={<>{t("none.kvm.what")}<span className="computer-command-label">{t("none.kvm.run")}</span><code className="computer-command" data-computer-command="">{computerHostCommand}</code><span className="computer-command-note">{t("none.kvm.after")}</span></>}/>
        <SettingsRow label={t("none.own.name")} description={t("none.own.what")} control={onAddMac ? <button type="button" className="secondary-button" data-add="mac" aria-label={t("environments.add_menu.mac")} onClick={onAddMac}>{t("none.add")}</button> : <span className="computer-soon"><TofiIcon name="clock" size={14} aria-hidden="true"/>{t("none.soon")}</span>}/>
        <SettingsRow label={t("none.e2b.name")} description={t("none.e2b.what")} control={onAddE2B ? <button type="button" className="secondary-button" data-add="e2b" aria-label={t("none.e2b.add")} onClick={onAddE2B}>{t("none.add")}</button> : <span className="computer-soon"><TofiIcon name="clock" size={14} aria-hidden="true"/>{t("none.soon")}</span>}/>
      </SettingsCard>
    </SettingsSection>
  </div>;
}
