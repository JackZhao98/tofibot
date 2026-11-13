import '../src/cascade.css';import '../src/fonts.css';import '../src/styles.css';import '../src/interaction-system.css';import '../src/settings-system.css';import '../src/desktop-system.css';import '../src/conversation-workspace.css';import './foundations.css';import '../src/v2-app.css';import '../src/sender-groups.css';import '../src/settings/settings-components.css';import '../src/settings/settings-polish.css';import '../src/web-tool-steps.css';import '../src/web-mention.css';import '../src/web-file-drop.css';import '../src/web-sidebar-list.css';import '../src/sidebar-pins.css';import '../src/sidebar-rail.css';import '../src/chat-header.css';import '../src/progressive-blur.css';import '../src/context-card.css';import '../src/focus-ring.css';
import React,{useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {setComputerCapability} from '../src/computerCapability';
import {setCapabilityPolicy} from '../src/capabilityPolicy';
const query=new URLSearchParams(location.search);
const host=query.get('host')==='none'?'none':'kvm';
setComputerCapability({backend:host,available:host==='kvm',host_backend:host,capabilities:['shell','files'],personal_computers:true} as any);
if(query.get('policy'))setCapabilityPolicy(JSON.parse(query.get('policy')!));
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as any)||'page'});
 const openSettings=(next:SettingsTab,view:'home'|'page'='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={{preference:'light',choose:()=>{}}} extensionRefresh={0} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}} openTab={openSettings}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Settings/></OwnerSessionGate></TimezoneProvider>));
