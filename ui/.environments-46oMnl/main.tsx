import '../src/cascade.css';import '../src/skin/fonts.css';import '../src/styles.css';import '../src/interaction-system.css';import '../src/desktop-system.css';import '../src/conversation-workspace.css';import '../src/v2-foundations.css';import '../src/v2-app.css';import '../src/sender-groups.css';import '../src/layout/settings-page.css';import '../src/web-tool-steps.css';import '../src/web-mention.css';import '../src/web-file-drop.css';import '../src/workspace/sidebar.css';import '../src/context-card.css';import '../src/ds/legacy-aliases.css';import '../src/forced.css';
import React,{useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {subscribeSettingsDeepLinks} from '../src/settings/deepLinks';
import {setComputerCapability} from '../src/computerCapability';
const query=new URLSearchParams(location.search);
setComputerCapability((query.get('computer')==='detached'?{backend:'none',available:false,host_backend:'kvm',capabilities:[],personal_computers:true}:{backend:'kvm',available:true,capabilities:['shell','files','browser','desktop','terminal','resources'],personal_computers:true}) as any);
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as any)||'page'});
 const openSettings=(next:SettingsTab,view:'home'|'page'='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={{preference:'light',choose:()=>{}}} extensionRefresh={0} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}} openTab={openSettings}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Settings/></OwnerSessionGate></TimezoneProvider>));
