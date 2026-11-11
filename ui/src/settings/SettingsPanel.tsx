import {SettingsShell,type SettingsEntry,type SettingsTab} from "../SettingsShell";
import {SettingsPages,type SettingsPagesProps} from "./SettingsPages";

/** The whole Settings surface behind one lazy boundary (see lazyPanels.tsx): shell, rail and every page. */
export type SettingsPanelProps={
 tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;entry:SettingsEntry;refreshToken:number;
 codexRefresh:number;onCodexConfigured:()=>void;
 pages:Omit<SettingsPagesProps,"page"|"slots">&{providersRefresh:number;onProvidersConfigured:()=>void};
};
export function SettingsPanel({tab,onTab,onClose,entry,refreshToken,codexRefresh,onCodexConfigured,pages}:SettingsPanelProps){
 const {providersRefresh,onProvidersConfigured,...rest}=pages;
 const slots:SettingsPagesProps["slots"]={
  codexRefresh,onCodexConfigured,
  providersRefresh,onProvidersConfigured
 };
 return <SettingsShell tab={tab} onTab={onTab} onClose={onClose} entry={entry} refreshToken={refreshToken} renderPage={page=><SettingsPages {...rest} slots={slots} page={page}/>}/>;
}
