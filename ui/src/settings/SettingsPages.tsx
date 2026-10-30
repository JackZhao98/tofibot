import {lazy,Suspense,useState,type ReactNode} from "react";
import {ApprovalModeSetting} from "../ApprovalModeSetting";
import {AutoReviewSettings} from "../AutoReviewSettings";
import {ChatsSettings} from "../ChatsSettings";
import {AutomationSettings} from "../AutomationSettings";
import {DebugSettings} from "../BotInspector";
import {ComputerCredentials} from "../ComputerCredentials";
import {ComputerPanel} from "../ComputerPanel";
import {ComputerContents} from "../ComputerContents";
import {ComputerOptions} from "../ComputerOptions";
import {useHasComputer} from "../computerCapability";
import {ComputerResources} from "../ComputerResources";
import {ConnectionInfo} from "../ConnectionInfo";
import {DictationSettings,useDictationStatus} from "../DictationSettings";
import {ExtensionPanel} from "../ExtensionPanel";
import {FollowupsSettings} from "../FollowupsSettings";
import {PrimaryAssistantSetting} from "../PrimaryAssistantSetting";
import {AppearancePicker} from "../InteractionSystem";
import {LanguageSetting} from "../LanguageSetting";
import {ModelDefaults} from "../ModelSettings";
import {OwnerAccount} from "../OwnerAccount";
import {PortabilitySettings} from "../PortabilitySettings";
import {ModelProviders} from "../ProviderSettings";
import {WebToolsSettings} from "../WebToolsSettings";
import type {SettingsTab} from "../SettingsShell";
import {TimezoneSetting} from "../TimezoneSettings";
import {UsagePanel} from "../UsagePanel";
import {VersionCard,useIsUpdateAdmin} from "../UpdateNotice";
import {WorkspacePurgeSettings} from "../WorkspacePurgeSettings";
import {useTranslation} from "../i18n";
import type {Bot, Conversation} from "../types";
import type {ThemePreference} from "../InteractionSystem";
import {Banner, DangerZone, SectionAnchor, SettingsCard, SettingsSection} from "./components";
import {RunHealthCard} from "./RunHealthCard";
import {SettingsChanges} from "./SettingsChanges";
import {StandingAllowances} from "../StandingAllowances";

/** Pieces that live in App.tsx and are handed in, so this folder never imports App. */
export type SettingsSlots={codexRefresh:number;onCodexConfigured:()=>void;providersRefresh:number;onProvidersConfigured:()=>void;legacyArchive:ReactNode};
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
/** The storage card loads on demand; it is not part of the first paint of Settings. */
const StorageUsage=lazy(()=>import("../StorageUsage").then(module=>({default:module.StorageUsage})));
const unreachable=(value:never):never=>{throw new Error(`unhandled settings page: ${String(value)}`)};

/** One explicit case per tab; `never` makes a missing case a compile error. */
export function SettingsPages(props:SettingsPagesProps){
 const {page,bots}=props;
 const activeBots=bots.filter(bot=>!bot.archived);
 switch(page){
  case "general": return <GeneralPage {...props}/>;
  case "connections": return <SectionAnchor id="list"><ExtensionPanel bots={bots} kind="mcp" refreshToken={props.extensionRefresh}/></SectionAnchor>;
  case "skills": return <SectionAnchor id="library"><ExtensionPanel bots={bots} kind="skills" refreshToken={props.extensionRefresh}/></SectionAnchor>;
  case "permissions": return <PermissionsPage/>;
  case "allowances": return <AllowancesPage openTab={props.openTab}/>;
  case "followups": return <FollowupsSettings/>;
  case "models": return <ModelsPage {...props}/>;
  case "tools": return <div className="settings-stack"><SectionAnchor id="web"><WebToolsSettings/></SectionAnchor></div>;
  case "devices": return <SectionAnchor id="devices"><ComputerPanel part="devices"/></SectionAnchor>;
  case "automation": return <AutomationSettings onOpenPermissions={()=>props.openTab("permissions")}/>;
  case "computer": return <ComputerPage openTab={props.openTab}/>;
  case "keys": return <SectionAnchor id="keys"><KeysPage bots={activeBots}/></SectionAnchor>;
  case "usage": return <div className="settings-stack"><SectionAnchor id="health"><RunHealthCard/></SectionAnchor><SectionAnchor id="usage"><UsagePanel preferredBotId={props.usageBotId} timezone={props.timezone}/></SectionAnchor></div>;
  case "advanced": return <AdvancedPage {...props} bots={bots} activeBots={activeBots}/>;
  case "admin": return <Suspense fallback={null}><SectionAnchor id="accounts"><AdminAccounts/></SectionAnchor></Suspense>;
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
 return useHasComputer()?<><SectionAnchor id="resources"><ComputerResources onOpenAdmin={()=>openTab("admin")}/></SectionAnchor><SectionAnchor id="environment"><ComputerPanel part="environment"/></SectionAnchor><ComputerContents openTab={openTab}/></>:<ComputerOptions/>;
}

function GeneralPage({appearance,slots,bots,onBotsChanged}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <SectionAnchor id="account"><OwnerAccount/></SectionAnchor>
  <Suspense fallback={null}><StorageUsage/></Suspense>
  <SectionAnchor id="appearance"><AppearancePicker value={appearance.preference} onChange={appearance.choose}/></SectionAnchor>
  <SectionAnchor id="primary"><PrimaryAssistantSetting bots={bots} onChanged={onBotsChanged}/></SectionAnchor>
  <SettingsSection id="region" title={t("general.region")}><SettingsCard className="settings-region"><LanguageSetting/><TimezoneSetting/></SettingsCard></SettingsSection>
  <SettingsSection id="chats" title={t("general.chat")}><ChatsSettings/></SettingsSection>
  {slots.legacyArchive}
 </div>;
}

function ModelsPage({slots}:SettingsPagesProps){
 const [version,setVersion]=useState(0);
 const dictation=useDictationStatus(version);
 // A connected OpenAI method is what makes Dictation available, so the section follows the provider cards.
 const changed=()=>setVersion(current=>current+1);
 const showDictation=Boolean(dictation.value?.configured)||Boolean(dictation.error&&!dictation.value);
 return <div className={showDictation?"settings-two":"settings-stack"}>
  <div className="settings-stack">
   <SectionAnchor id="providers"><ModelProviders refreshToken={slots.providersRefresh} codexRefresh={slots.codexRefresh} onConfigured={()=>{slots.onProvidersConfigured();changed()}} onCodexConfigured={()=>{slots.onCodexConfigured();changed()}}/></SectionAnchor>
   <SectionAnchor id="defaults"><ModelDefaults/></SectionAnchor>
  </div>
  {showDictation&&<div className="settings-stack"><DictationSettings value={dictation.value} error={dictation.error}/></div>}
 </div>;
}

/** Permissions: one control, the approval mode for this account. Everything else lives under Advanced > Tool rules. */
export function PermissionsPage(){
 return <div className="settings-stack permissions-page"><SectionAnchor id="mode"><ApprovalModeSetting/></SectionAnchor></div>;
}

/** Advanced > Tool rules: who reviews calls in detail, how each kind of tool asks, and every allowance you have given (revocable here). */
export function AllowancesPage({openTab}:{openTab:(tab:SettingsTab)=>void}){
 const {t}=useTranslation(["settings","extensions"]);
 const rows=[["read_only","read"],["write","write"],["high_risk","high"]] as const;
 return <div className="settings-stack permissions-page">
  <SectionAnchor id="reviewer"><AutoReviewSettings/></SectionAnchor>
  <SettingsSection id="how" title={t("permissionsPage.how.title")} hint={<button type="button" className="text-button" onClick={()=>openTab("connections")}>{t("permissionsPage.how.link")}</button>}>
   <SettingsCard className="permissions-how">{rows.map(([tier,key])=><div key={tier} className="permissions-how-row" data-tier={tier}><strong>{t(`extensions:tools.tier.${tier}`)}</strong><p>{t(`permissionsPage.how.${key}`)}</p></div>)}</SettingsCard>
  </SettingsSection>
  <StandingAllowances/>
 </div>;
}

function AdvancedPage({portabilityBotID,portabilityFile,onPortabilityFileConsumed,bots,activeBots,conversation}:SettingsPagesProps&{activeBots:Bot[]}){
 const {t}=useTranslation("settings");
 const admin=useIsUpdateAdmin();
 return <div className="settings-stack">
  {admin&&<SettingsSection id="version" title={t("advanced.version")}><VersionCard/></SettingsSection>}
  <SettingsSection id="server" title={t("advanced.server")}><SettingsCard><ConnectionInfo/></SettingsCard></SettingsSection>
  <SettingsSection id="data" title={t("advanced.data")}><PortabilitySettings bots={bots} initialFile={portabilityFile} initialBotID={portabilityBotID} onInitialFileConsumed={onPortabilityFileConsumed}/></SettingsSection>
  <SettingsSection id="changes" title={t("changes.title")} description={t("changes.description")}><SettingsChanges/></SettingsSection>
  <SettingsSection id="debug" title={t("advanced.debug")}><SettingsCard><DebugSettings bots={activeBots} conversation={conversation}/></SettingsCard></SettingsSection>
  <SectionAnchor id="danger"><DangerZone title={t("advanced.danger")}><WorkspacePurgeSettings/></DangerZone></SectionAnchor>
 </div>;
}
