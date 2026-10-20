import {SettingsShell,type SettingsEntry,type SettingsTab} from "../SettingsShell";
import {useTranslation} from "../i18n";
import {CodexPanel,NotificationSetting} from "./AppSlots";
import {SettingsPages,type SettingsPagesProps} from "./SettingsPages";

/** The whole Settings surface behind one lazy boundary (see lazyPanels.tsx): shell, rail and every page. */
export type SettingsPanelProps={
 tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;entry:SettingsEntry;refreshToken:number;
 codexRefresh:number;onCodexConfigured:()=>void;legacyArchive:boolean;onOpenArchive:()=>void;
 pages:Omit<SettingsPagesProps,"page"|"slots">&{providersRefresh:number;onProvidersConfigured:()=>void};
};
export function SettingsPanel({tab,onTab,onClose,entry,refreshToken,codexRefresh,onCodexConfigured,legacyArchive,onOpenArchive,pages}:SettingsPanelProps){
 const {t}=useTranslation("chat");
 const {providersRefresh,onProvidersConfigured,...rest}=pages;
 const slots:SettingsPagesProps["slots"]={
  codex:<CodexPanel refreshToken={codexRefresh} onConfigured={onCodexConfigured}/>,
  notifications:<NotificationSetting/>,
  providersRefresh,onProvidersConfigured,
  legacyArchive:legacyArchive?<div className="legacy-archive-entry"><span>{t("detail.legacy_archive")}</span><button className="text-button" onClick={onOpenArchive}>{t("detail.manage")}</button></div>:null
 };
 return <SettingsShell tab={tab} onTab={onTab} onClose={onClose} entry={entry} refreshToken={refreshToken} renderPage={page=><SettingsPages {...rest} slots={slots} page={page}/>}/>;
}
