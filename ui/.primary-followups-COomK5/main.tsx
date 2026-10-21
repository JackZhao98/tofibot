import '../src/fonts.css';import '../src/styles.css';import '../src/interaction-system.css';import '../src/settings-system.css';import '../src/desktop-system.css';import '../src/conversation-workspace.css';import './foundations.css';import '../src/v2-app.css';import '../src/sender-groups.css';import '../src/settings/settings-components.css';import '../src/settings/settings-polish.css';import '../src/web-tool-steps.css';import '../src/web-mention.css';import '../src/web-file-drop.css';import '../src/web-sidebar-list.css';import '../src/sidebar-pins.css';import '../src/chat-header.css';import '../src/progressive-blur.css';import '../src/context-card.css';import '../src/focus-ring.css';
import React,{useCallback,useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {ApiError,api} from '../src/api';
import {TimezoneProvider} from '../src/UserTimezone';
import {DeleteConversationDialog} from '../src/DeleteConversationDialog';
import {PrimaryAssistantSetting,PrimaryBadge,ReassignPrimaryDialog} from '../src/PrimaryAssistant';
import {FollowupsSettings} from '../src/FollowupsSettings';
const query=new URLSearchParams(location.search);
const scene=query.get('scene')||'selector';
function useBots(){
 const [bots,setBots]=useState<any[]>([]);
 const reload=useCallback(async()=>{setBots((await api.bots()).bots)},[]);
 useEffect(()=>{void reload()},[reload]);
 return {bots,reload};
}
function Selector(){
 const {bots,reload}=useBots();
 return <div className="workspace"><div className="settings-page-body" data-page="general"><div className="settings-stack"><PrimaryAssistantSetting bots={bots} onChanged={reload}/></div></div></div>;
}
function Followups(){
 return <div className="workspace"><div className="settings-page-body" data-page="followups"><FollowupsSettings/></div></div>;
}
function Delete(){
 const {bots}=useBots();
 const target={id:'bot_research',conversationId:'c1',name:'Research Bot',kind:'bot' as const,isPrimary:query.get('primary')==='1'};
 return bots.length?<DeleteConversationDialog target={target} bots={query.get('alone')==='1'?bots.filter((b:any)=>b.id===target.id):bots} onClose={()=>{(window as any).__closed=true}} onDelete={async(t,successor)=>{(window as any).__deleted={id:t.id,successor};if(query.get('stale')==='1')throw new ApiError(409,'stale','primary_reassign_required')}}/>:null;
}
function Hide(){
 const {bots}=useBots();
 const bot=bots.find((b:any)=>b.id==='bot_research');
 return bot?<ReassignPrimaryDialog bot={bot} bots={bots} onClose={()=>{(window as any).__closed=true}} onConfirm={async id=>{(window as any).__hidden={successor:id}}}/>:null;
}
function Badge(){return <div className="workspace"><div className="contact-copy"><strong><span className="contact-name">A very long Bot name that has to be truncated before the tag</span><PrimaryBadge/></strong></div></div>}
const scenes:Record<string,()=>JSX.Element|null>={selector:Selector,followups:Followups,delete:Delete,hide:Hide,badge:Badge};
const Scene=scenes[scene];
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><Scene/></TimezoneProvider>));
