import type {ReactNode} from "react";
import {AdminAccounts} from "../AdminAccounts";
import {AutoReviewSettings} from "../AutoReviewSettings";
import {DebugSettings} from "../BotInspector";
import {ComputerCredentials} from "../ComputerCredentials";
import {ComputerPanel} from "../ComputerPanel";
import {ComputerResources} from "../ComputerResources";
import {ConnectionInfo} from "../ConnectionInfo";
import {DictationSettings} from "../DictationSettings";
import {ExtensionPanel} from "../ExtensionPanel";
import {AppearancePicker} from "../InteractionSystem";
import {LanguageSetting} from "../LanguageSetting";
import {ModelDefaults} from "../ModelSettings";
import {OwnerAccount} from "../OwnerSession";
import {PortabilitySettings} from "../PortabilitySettings";
import {ModelProviders} from "../ProviderSettings";
import type {SettingsTab} from "../SettingsShell";
import {TimezoneSetting} from "../UserTimezone";
import {UsagePanel} from "../UsagePanel";
import {WorkspacePurgeSettings} from "../WorkspacePurgeSettings";
import {useTranslation} from "../i18n";
import type {Bot, Conversation} from "../types";
import type {ThemePreference} from "../InteractionSystem";
import {Banner, DangerZone, SettingsCard, SettingsSection} from "./components";

/** Pieces that live in App.tsx and are handed in, so this folder never imports App. */
export type SettingsSlots={codex:ReactNode;notifications:ReactNode;providersRefresh:number;onProvidersConfigured:()=>void;legacyArchive:ReactNode};
export type SettingsPagesProps={
 page:SettingsTab;bots:Bot[];conversation?:Conversation;timezone:string;usageBotId:string;
 portabilityBotID:string;portabilityFile?:File;onPortabilityFileConsumed:()=>void;
 appearance:{preference:ThemePreference;choose:(theme:ThemePreference)=>void};
 extensionRefresh:number;slots:SettingsSlots;openTab:(tab:SettingsTab)=>void;
};
const unreachable=(value:never):never=>{throw new Error(`unhandled settings page: ${String(value)}`)};

/** One explicit case per tab; `never` makes a missing case a compile error. */
export function SettingsPages(props:SettingsPagesProps){
 const {page,bots}=props;
 const activeBots=bots.filter(bot=>!bot.archived);
 switch(page){
  case "general": return <GeneralPage {...props}/>;
  case "connections": return <ExtensionPanel bots={bots} kind="mcp" refreshToken={props.extensionRefresh}/>;
  case "skills": return <ExtensionPanel bots={bots} kind="skills" refreshToken={props.extensionRefresh}/>;
  case "approvals": return <ApprovalsPage/>;
  case "models": return <ModelsPage {...props}/>;
  case "computer": return <><ComputerResources/><ComputerPanel/></>;
  case "keys": return <ComputerCredentials bots={activeBots}/>;
  case "usage": return <UsagePanel preferredBotId={props.usageBotId} timezone={props.timezone}/>;
  case "advanced": return <AdvancedPage {...props} bots={bots} activeBots={activeBots}/>;
  case "admin": return <AdminAccounts/>;
  default: return unreachable(page);
 }
}

function GeneralPage({appearance,slots,openTab}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <OwnerAccount/>
  <AppearancePicker value={appearance.preference} onChange={appearance.choose}/>
  <SettingsSection title={t("general.region")}><SettingsCard className="settings-region"><LanguageSetting/><TimezoneSetting/></SettingsCard></SettingsSection>
  <SettingsSection><SettingsCard>{slots.notifications}</SettingsCard></SettingsSection>
  <Banner tone="info" title={t("general.moved_title")} action={{label:t("general.open_advanced"),onClick:()=>openTab("advanced")}}>{t("general.moved_body")}</Banner>
  {slots.legacyArchive}
 </div>;
}

function ModelsPage({slots}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 return <div className="settings-two">
  <div className="settings-stack">
   <ModelProviders refreshToken={slots.providersRefresh} onConfigured={slots.onProvidersConfigured} codex={slots.codex}/>
   <ModelDefaults/>
  </div>
  <div className="settings-stack"><SettingsSection title={t("dictation.title")}><DictationSettings/></SettingsSection></div>
 </div>;
}

function ApprovalsPage(){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <AutoReviewSettings/>
  <Banner tone="info" title={t("approvals.soon_title")}>{t("approvals.soon_body")}</Banner>
 </div>;
}

function AdvancedPage({portabilityBotID,portabilityFile,onPortabilityFileConsumed,bots,activeBots,conversation}:SettingsPagesProps&{activeBots:Bot[]}){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <SettingsSection title={t("advanced.server")}><SettingsCard className="settings-flush"><ConnectionInfo/></SettingsCard></SettingsSection>
  <SettingsSection title={t("advanced.data")}><SettingsCard className="settings-flush"><PortabilitySettings bots={bots} initialFile={portabilityFile} initialBotID={portabilityBotID} onInitialFileConsumed={onPortabilityFileConsumed}/></SettingsCard></SettingsSection>
  <SettingsSection title={t("advanced.debug")}><SettingsCard className="settings-flush"><DebugSettings bots={activeBots} conversation={conversation}/></SettingsCard></SettingsSection>
  <DangerZone title={t("advanced.danger")}><WorkspacePurgeSettings/></DangerZone>
 </div>;
}
