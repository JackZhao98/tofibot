import {lazy,Suspense,useEffect,useState,type ReactNode} from "react";
import {ApprovalModeSetting,ReviewerModel} from "../ApprovalModeSetting";
import {ObserveModeRow} from "../AutoReviewSettings";
import {ChatsSettings} from "../ChatsSettings";
import {AutomationRows,BehaviorPage} from "../AutomationSettings";
import {DebugSettings} from "../BotInspector";
import {ComputerCredentials} from "../ComputerCredentials";
import {EnvironmentsPage} from "../EnvironmentsPage";
import {useHasComputer} from "../computerCapability";
import {ConnectionInfo} from "../ConnectionInfo";
import {DictationSettings,useDictationStatus} from "../DictationSettings";
import {ExtensionPanel} from "../ExtensionPanel";
import {FollowupsSettings} from "../FollowupsSettings";
import {AppearancePicker} from "../InteractionSystem";
import {LanguageSetting} from "../LanguageSetting";
import {ModelDefaults} from "../ModelSettings";
import {HelpAndFeedback} from "./HelpAndFeedback";
import {OwnerAccount} from "../OwnerAccount";
import {PortabilitySettings} from "../PortabilitySettings";
import {ModelProviders} from "../ProviderSettings";
import {WebToolsSettings} from "../WebToolsSettings";
import type {SettingsTab} from "../SettingsShell";
import {TimezoneSetting} from "../TimezoneSettings";
import {UsagePanel} from "../UsagePanel";
import {SpendCap} from "../SpendCap";
import {VersionCard,useIsUpdateAdmin} from "../UpdateNotice";
import {useOwnerSession} from "../OwnerSession";
import {WorkspacePurgeSettings} from "../WorkspacePurgeSettings";
import {useTranslation} from "../i18n";
import type {Bot, Conversation} from "../types";
import type {ThemePreference} from "../InteractionSystem";
import {DangerZone, SectionAnchor, SettingsCard, SettingsRow, SettingsSection, SettingsSubpage} from "./components";
import {FoldGroup, useSubpage} from "./subpages";
import {RunHealthCard} from "./RunHealthCard";
import {SettingsChanges} from "./SettingsChanges";
import {StandingAllowances} from "../StandingAllowances";

/** Pieces that live in App.tsx and are handed in, so this folder never imports App. */
export type SettingsSlots={codexRefresh:number;onCodexConfigured:()=>void;providersRefresh:number;onProvidersConfigured:()=>void};
export type SettingsPagesProps={
 page:SettingsTab;bots:Bot[];conversation?:Conversation;timezone:string;usageBotId:string;
 portabilityBotID:string;portabilityFile?:File;onPortabilityFileConsumed:()=>void;
 appearance:{preference:ThemePreference;choose:(theme:ThemePreference)=>void};
 extensionRefresh:number;slots:SettingsSlots;openTab:(tab:SettingsTab)=>void;
};
/** Only admins of a multi-account install reach this page, so its code (accounts list, detail, delete flows) loads on demand. */
const AdminAccounts=lazy(()=>import("../AdminAccounts").then(module=>({default:module.AdminAccounts})));
/** The other two Administration pages load on demand for the same reason. */
const AdminGroups=lazy(()=>import("../AdminGroups").then(module=>({default:module.AdminGroups})));
const AdminResources=lazy(()=>import("../AdminResources").then(module=>({default:module.AdminResources})));
/** The storage card loads on demand; it is not part of the first paint of Settings. */
const StorageUsage=lazy(()=>import("../StorageUsage").then(module=>({default:module.StorageUsage})));
const unreachable=(value:never):never=>{throw new Error(`unhandled settings page: ${String(value)}`)};

/** One explicit case per tab; `never` makes a missing case a compile error. */
export function SettingsPages(props:SettingsPagesProps){
 const {page,bots}=props;
 const activeBots=bots.filter(bot=>!bot.archived);
 switch(page){
  case "general": return <GeneralPage {...props}/>;
  case "connections": return <ConnectionsPage bots={bots} refresh={props.extensionRefresh}/>;
  case "skills": return <SectionAnchor id="library"><ExtensionPanel bots={bots} kind="skills" refreshToken={props.extensionRefresh}/></SectionAnchor>;
  case "permissions": return <PermissionsPage/>;
  case "models": return <ModelsPage {...props}/>;
  // "devices" (Other devices) is part of Environments; the shell redirects its id, and a direct render still lands on the same page.
  case "devices": return <SectionAnchor id="devices"><EnvironmentsPage openTab={props.openTab}/></SectionAnchor>;
  case "computer": return <ComputerPage openTab={props.openTab} bots={activeBots}/>;
  case "usage": return <div className="settings-stack"><SectionAnchor id="spend"><SpendCap/></SectionAnchor><SectionAnchor id="usage" wide><UsagePanel preferredBotId={props.usageBotId} timezone={props.timezone}/></SectionAnchor><SectionAnchor id="storage"><Suspense fallback={null}><StorageUsage/></Suspense></SectionAnchor></div>;
  case "advanced": return <AdvancedPage {...props} bots={bots} activeBots={activeBots}/>;
  case "admin": return <Suspense fallback={null}><div className="settings-stack"><SectionAnchor id="accounts" wide><AdminAccounts/></SectionAnchor><AdminVersion/></div></Suspense>;
  case "admin-groups": return <Suspense fallback={null}><AdminGroups openTab={props.openTab}/></Suspense>;
  case "admin-resources": return <Suspense fallback={null}><AdminResources openTab={props.openTab}/></Suspense>;
  default: return unreachable(page);
 }
}

/** A page that has subpages: the page stays mounted (hidden) while one is open, so Back lands on the row you came from with its state intact. */
function Hidden({when,children}:{when:boolean;children:ReactNode}){return <div hidden={when}>{children}</div>}

/** Connections: the list, and a row for the web search and page-reading chains (a subpage). */
const connectionsSubpages={web:"web"} as const;
function ConnectionsPage({bots,refresh}:{bots:Bot[];refresh:number}){
 const {t}=useTranslation("settings");
 const [sub,setSub]=useSubpage("connections",connectionsSubpages);
 return <>
  <Hidden when={sub!==null}><div className="settings-stack">
   <SectionAnchor id="list"><ExtensionPanel bots={bots} kind="mcp" refreshToken={refresh}/></SectionAnchor>
   <SettingsCard><SettingsRow label={t("webtools.title")} link={{label:t("webtools.title"),onOpen:()=>setSub("web")}}/></SettingsCard>
  </div></Hidden>
  {sub==="web"&&<SettingsSubpage title={t("webtools.title")} onBack={()=>setSub(null)}><SectionAnchor id="web"><WebToolsSettings bare/></SectionAnchor></SettingsSubpage>}
 </>;
}

/** Environments, then the Bot computer's environment variables and SSH keys as a subpage (only with a computer). */
const computerSubpages={keys:"keys"} as const;
function ComputerPage({openTab,bots}:{openTab:(tab:SettingsTab)=>void;bots:Bot[]}){
 const {t}=useTranslation("settings");
 const hasComputer=useHasComputer();
 const [sub,setSub]=useSubpage("computer",computerSubpages);
 const keys=sub==="keys"&&hasComputer;
 return <>
  <Hidden when={keys}><div className="settings-stack">
   <SectionAnchor id="resources"><EnvironmentsPage openTab={openTab}/></SectionAnchor>
   {hasComputer&&<AutomationRows groups={["environment"]}/>}
   {hasComputer&&<SettingsCard><SettingsRow label={t("computer.keys_row")} link={{label:t("computer.keys_row"),onOpen:()=>setSub("keys")}}/></SettingsCard>}
  </div></Hidden>
  {keys&&<SettingsSubpage title={t("computer.keys_row")} onBack={()=>setSub(null)}><SectionAnchor id="keys"><ComputerCredentials bots={bots}/></SectionAnchor></SettingsSubpage>}
 </>;
}

function GeneralPage({appearance}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 return <div className="settings-stack">
  <SectionAnchor id="account"><OwnerAccount/></SectionAnchor>
  <SectionAnchor id="appearance"><AppearancePicker value={appearance.preference} onChange={appearance.choose}/></SectionAnchor>
  <SettingsSection id="region" title={t("general.region")}><SettingsCard className="settings-region"><LanguageSetting/><TimezoneSetting/></SettingsCard></SettingsSection>
  <SettingsSection id="chats" title={t("general.chat")}><ChatsSettings/></SettingsSection>
  <HelpAndFeedback/>
 </div>;
}

function ModelsPage({slots}:SettingsPagesProps){
 const {t}=useTranslation("settings");
 const [version,setVersion]=useState(0);
 const dictation=useDictationStatus(version);
 // A connected OpenAI method is what makes Dictation available, so the section follows the provider cards.
 const changed=()=>setVersion(current=>current+1);
 const showDictation=Boolean(dictation.value?.configured)||Boolean(dictation.error&&!dictation.value);
 // One column: the Settings window (at most 1180 px, --window-w) leaves the page too narrow for Dictation beside the providers.
 return <div className="settings-stack">
  <SectionAnchor id="providers"><ModelProviders refreshToken={slots.providersRefresh} codexRefresh={slots.codexRefresh} onConfigured={()=>{slots.onProvidersConfigured();changed()}} onCodexConfigured={()=>{slots.onCodexConfigured();changed()}}/></SectionAnchor>
  <SectionAnchor id="defaults"><ModelDefaults/></SectionAnchor>
  <SettingsCard className="st-folds"><FoldGroup tab="models" id="more" title={t("models.more")}>
   <AutomationRows groups={["models"]} flat/>
   {showDictation&&<DictationSettings value={dictation.value} error={dictation.error}/>}
  </FoldGroup></SettingsCard>
 </div>;
}

/** Permissions: the approval mode, what is allowed for good, what is remembered; Advanced (a subpage) holds the reviewer and Observe. */
const permissionsSubpages={advanced:"advanced"} as const;
export function PermissionsPage(){
 const {t}=useTranslation("settings");
 const [sub,setSub]=useSubpage("permissions",permissionsSubpages);
 return <>
  <Hidden when={sub!==null}><div className="settings-stack permissions-page">
   <SectionAnchor id="mode"><ApprovalModeSetting/></SectionAnchor>
   <StandingAllowances rememberedExtra={<AutomationRows groups={["remembered"]}/>}/>
   <SettingsCard><SettingsRow label={t("permissionsPage.advanced.title")} link={{label:t("permissionsPage.advanced.title"),onOpen:()=>setSub("advanced")}}/></SettingsCard>
  </div></Hidden>
  {sub==="advanced"&&<SettingsSubpage title={t("permissionsPage.advanced.title")} onBack={()=>setSub(null)}><div className="settings-stack permissions-page"><SectionAnchor id="advanced">
   <ReviewerModel/><ObserveModeRow/><AutomationRows groups={["permissions"]}/>
  </SectionAnchor></div></SettingsSubpage>}
 </>;
}

/**
 * System: version, server, Behavior (a subpage), data (Export, Import and the settings Bots changed, each a subpage), troubleshooting
 * (Run health, Debug) and Start over. `systemOwner` says which subpage holds a section a link can name.
 */
type SystemSub="behavior"|"followups"|"export"|"import"|"changes"|"health"|"debug";
const systemOwner:Record<string,SystemSub>={behavior:"behavior",groups:"behavior",replies:"behavior",reviews:"behavior",tasks:"behavior",computer:"behavior",followups:"followups"};
/** Administration's Version card: server version and updates, for the administrator of a multi-account server. */
function AdminVersion(){
 const {t}=useTranslation("settings");
 return useIsUpdateAdmin()?<SettingsSection id="version" title={t("advanced.version")}><VersionCard/></SettingsSection>:null;
}
function AdvancedPage({portabilityBotID,portabilityFile,onPortabilityFileConsumed,bots,activeBots,conversation}:SettingsPagesProps&{activeBots:Bot[]}){
 const {t}=useTranslation("settings");
 // On a multi-account server the Version card is in Administration; a single-account owner keeps it here.
 const updateAdmin=useIsUpdateAdmin(),session=useOwnerSession();
 const admin=updateAdmin&&!session?.multi_account;
 const [sub,setSub]=useSubpage<SystemSub>("advanced",systemOwner);
 // A file or a Bot handed over from elsewhere opens the half it is for.
 useEffect(()=>{if(portabilityFile)setSub("import");else if(portabilityBotID)setSub("export")},[portabilityFile,portabilityBotID,setSub]);
 const row=(label:string,to:SystemSub,description?:string)=><SettingsRow label={label} description={description} link={{label,onOpen:()=>setSub(to)}}/>;
 const back=()=>setSub(sub==="followups"?"behavior":null);
 const sheet=(title:string,id:SystemSub,body:ReactNode)=>sub===id&&<SettingsSubpage title={title} onBack={back}>{body}</SettingsSubpage>;
 return <>
  <Hidden when={sub!==null}><div className="settings-stack">
   {admin&&<SettingsSection id="version" title={t("advanced.version")}><VersionCard/></SettingsSection>}
   <SettingsSection id="server" title={t("advanced.server")}><SettingsCard><ConnectionInfo/></SettingsCard></SettingsSection>
   <SettingsSection><SettingsCard>{row(t("shell.page.automation.name"),"behavior",t("advanced.behavior_hint"))}</SettingsCard></SettingsSection>
   <SettingsSection id="data" title={t("advanced.data")}><SettingsCard>{row(t("portability.export"),"export")}{row(t("portability.import"),"import")}{row(t("changes.title"),"changes")}</SettingsCard></SettingsSection>
   <SettingsSection id="diagnostics" title={t("advanced.diagnostics")}><SettingsCard>{row(t("usage.health.title"),"health")}{row(t("advanced.debug"),"debug")}</SettingsCard></SettingsSection>
   <SectionAnchor id="danger"><DangerZone title={t("advanced.danger")}><WorkspacePurgeSettings/></DangerZone></SectionAnchor>
  </div></Hidden>
  <Hidden when={sub!=="behavior"}>{(sub==="behavior"||sub==="followups")&&<SettingsSubpage title={t("shell.page.automation.name")} onBack={back}><BehaviorPage onOpenFollowups={()=>setSub("followups")}/></SettingsSubpage>}</Hidden>
  {sheet(t("shell.page.followups.name"),"followups",<SectionAnchor id="followups"><FollowupsSettings/></SectionAnchor>)}
  {sheet(t("portability.export"),"export",<PortabilitySettings bots={bots} show="export" initialBotID={portabilityBotID}/>)}
  {sheet(t("portability.import"),"import",<PortabilitySettings bots={bots} show="import" initialFile={portabilityFile} onInitialFileConsumed={onPortabilityFileConsumed}/>)}
  {sheet(t("changes.title"),"changes",<div className="settings-stack"><AutomationRows groups={["changes"]}/><SettingsChanges/></div>)}
  {sheet(t("usage.health.title"),"health",<RunHealthCard/>)}
  {sheet(t("advanced.debug"),"debug",<SettingsCard><DebugSettings bots={activeBots} conversation={conversation}/></SettingsCard>)}
 </>;
}
