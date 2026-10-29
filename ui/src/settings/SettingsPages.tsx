import {lazy,Suspense,type ReactNode} from "react";
import {AutoReviewSettings} from "../AutoReviewSettings";
import {AutomationSettings} from "../AutomationSettings";
import {DebugSettings} from "../BotInspector";
import {ComputerCredentials} from "../ComputerCredentials";
import {ComputerPanel} from "../ComputerPanel";
import {ComputerContents} from "../ComputerContents";
import {ComputerOptions} from "../ComputerOptions";
import {useHasComputer} from "../computerCapability";
import {ComputerResources} from "../ComputerResources";
import {ConnectionInfo} from "../ConnectionInfo";
import {DictationSettings} from "../DictationSettings";
import {ExtensionPanel} from "../ExtensionPanel";
import {FollowupsSettings} from "../FollowupsSettings";
import {PrimaryAssistantSetting} from "../PrimaryAssistantSetting";
import {AppearancePicker} from "../InteractionSystem";
import {LanguageSetting} from "../LanguageSetting";
import {ModelDefaults} from "../ModelSettings";
import {OwnerAccount} from "../OwnerSession";
import {PortabilitySettings} from "../PortabilitySettings";
import {ModelProviders} from "../ProviderSettings";
import {WebToolsSettings} from "../WebToolsSettings";
import type {SettingsTab} from "../SettingsShell";
import {ShowActivitySetting,TimezoneSetting} from "../UserTimezone";
import {UsagePanel} from "../UsagePanel";
import {VersionCard,useIsUpdateAdmin} from "../UpdateNotice";
import {WorkspacePurgeSettings} from "../WorkspacePurgeSettings";
import {useTranslation} from "../i18n";
import type {Bot, Conversation} from "../types";
import type {ThemePreference} from "../InteractionSystem";
import {Banner, DangerZone, SettingsCard, SettingsSection} from "./components";
import {RunHealthCard} from "./RunHealthCard";
import {StandingAllowances} from "../StandingAllowances";

/** Pieces that live in App.tsx and are handed in, so this folder never imports App. */
export type SettingsSlots={codex:ReactNode;notifications:ReactNode;providersRefresh:number;onProvidersConfigured:()=>void;legacyArchive:ReactNode};
export type SettingsPagesProps={
 page:SettingsTab;bots:Bot[];conversation?:Conversation;timezone:string;usageBotId:string;
 portabilityBotID:string;portabilityFile?:File;onPortabilityFileConsumed:()=>void;
 appearance:{preference:ThemePreference;choose:(theme:ThemePreference)=>void};
 extensionRefresh:number;slots:SettingsSlots;openTab:(tab:SettingsTab)=>void;
 /** Called after a Settings change that alters the Bot list (the Primary Assistant); the app reloads its Bots. */
 onBotsChanged?:()=>void|Promise<void>;
};
/** Only admins of a multi-account install reach this page, so its code (accounts list, detail, delete flows) loads on demand. */
const AdminAccounts=lazy(()=>import("../AdminAccounts").then(module=>({default:module.AdminAccounts})));
const unreachable=(value:never):never=>{throw new Error(`unhandled settings page: ${String(value)}`)};

/** One explicit case per tab; `never` makes a missing case a compile error. */
export function SettingsPages(props:SettingsPagesProps){
 const {page,bots}=props;
 const activeBots=bots.filter(bot=>!bot.archived);
 switch(page){
  case "general": return <GeneralPage {...props}/>;
  case "connections": return <ExtensionPanel bots={bots} kind="mcp" refreshToken={props.extensionRefresh}/>;
  case "skills": return <ExtensionPanel bots={bots} kind="skills" refreshToken={props.extensionRefresh}/>;
  case "permissions": return <PermissionsPage openTab={props.openTab}/>;
  case "followups": return <FollowupsSettings/>;
  case "models": return <ModelsPage {...props}/>;
  case "automation": return <AutomationSettings onOpenPermissions={()=>props.openTab("permissions")}/>;
  case "computer": return <ComputerPage openTab={props.openTab}/>;
  case "keys": return <KeysPage bots={activeBots}/>;
  case "usage": return <div className="settings-stack"><RunHealthCard/><UsagePanel preferredBotId={props.usageBotId} timezone={props.timezone}/></div>;
  case "advanced": return <AdvancedPage {...props} bots={bots} activeBots={activeBots}/>;
  case "admin": return <Suspense fallback={null}><AdminAccounts/></Suspense>;
  default: return unreachable(page);
 }
}

/**
 * Environment variables and SSH keys are the computer's configuration: without a computer the page renders nothing
 * (the shell sends the person to Computer), so a saved link to it makes no /api/computer/credentials request.
 */
function KeysPage({bots}:{bots:Bot[]}){
 return useHasComputer()?<ComputerCredentials bots={bots}/>:null;
}

/**
 * With no computer backend the page explains the options; otherwise it is the computer's own settings, including what
 * lives only on it. That list (and the Keys page it links to) is the computer's configuration, so it exists only here.
 */
function ComputerPage({openTab}:{openTab:(tab:SettingsTab)=>void}){
 return useHasComputer()?<><ComputerResources/><ComputerPanel/><ComputerContents openTab={openTab}/></>:<ComputerOptions/>;
}

function GeneralPage({appearance,slots,openTab,bots,onBotsChanged}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <OwnerAccount/>
  <AppearancePicker value={appearance.preference} onChange={appearance.choose}/>
  <PrimaryAssistantSetting bots={bots} onChanged={onBotsChanged}/>
  <SettingsSection title={t("general.region")}><SettingsCard className="settings-region"><LanguageSetting/><TimezoneSetting/></SettingsCard></SettingsSection>
  <SettingsSection title={t("general.chat")}><SettingsCard><ShowActivitySetting/></SettingsCard></SettingsSection>
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
   <WebToolsSettings/>
  </div>
  <div className="settings-stack"><SettingsSection title={t("dictation.title")}><DictationSettings/></SettingsSection></div>
 </div>;
}

/** Permissions: who reviews calls, how each kind of tool asks, and every allowance you have given (revocable here). */
export function PermissionsPage({openTab}:{openTab:(tab:SettingsTab)=>void}){
 const {t}=useTranslation(["settings","extensions"]);
 const rows=[["read_only","read"],["write","write"],["high_risk","high"]] as const;
 return <div className="settings-stack permissions-page">
  <AutoReviewSettings/>
  <SettingsSection title={t("permissionsPage.how.title")} hint={<button type="button" className="text-button" onClick={()=>openTab("connections")}>{t("permissionsPage.how.link")}</button>}>
   <SettingsCard className="permissions-how">{rows.map(([tier,key])=><div key={tier} className="permissions-how-row" data-tier={tier}><strong>{t(`extensions:tools.tier.${tier}`)}</strong><p>{t(`permissionsPage.how.${key}`)}</p></div>)}</SettingsCard>
  </SettingsSection>
  <StandingAllowances/>
 </div>;
}

function AdvancedPage({portabilityBotID,portabilityFile,onPortabilityFileConsumed,bots,activeBots,conversation}:SettingsPagesProps&{activeBots:Bot[]}){
 const {t}=useTranslation("settings");
 const admin=useIsUpdateAdmin();
 return <div className="settings-stack">
  {admin&&<SettingsSection title={t("advanced.version")}><VersionCard/></SettingsSection>}
  <SettingsSection title={t("advanced.server")}><SettingsCard><ConnectionInfo/></SettingsCard></SettingsSection>
  <SettingsSection title={t("advanced.data")}><PortabilitySettings bots={bots} initialFile={portabilityFile} initialBotID={portabilityBotID} onInitialFileConsumed={onPortabilityFileConsumed}/></SettingsSection>
  <SettingsSection title={t("advanced.debug")}><SettingsCard><DebugSettings bots={activeBots} conversation={conversation}/></SettingsCard></SettingsSection>
  <DangerZone title={t("advanced.danger")}><WorkspacePurgeSettings/></DangerZone>
 </div>;
}
