import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from "react";
import {request} from "./api";
import {useAccountIdentity, useOwnerSession} from "./OwnerSession";
import {TofiIcon, type TofiIconName} from "./icons";
import {SettingsActivityContext} from "./settingsActivity";
import {SettingsDraftContext, type SettingsDraft} from "./settingsDraft";
import {SettingsHeaderSlotContext} from "./settings/headerSlot";
import {useConnectionAttention} from "./settings/useConnectionAttention";
import {providerForModel, providerLabels} from "./modelCatalog";
import {useLanguage, useTranslation} from "./i18n";

export type SettingsTab="general"|"connections"|"skills"|"approvals"|"models"|"computer"|"keys"|"usage"|"advanced"|"admin";
export type SettingsView="home"|"page";
/** `seq` changes on every open request, so a deep link to the tab already selected still lands on its page on mobile. */
export type SettingsEntry={seq:number;view:SettingsView};
// Names and descriptions are catalog keys (settings:shell.*), translated at render.
type Page={id:SettingsTab;icon:TofiIconName;name:`shell.page.${SettingsTab}.name`;description:`shell.page.${SettingsTab}.description`};
type GroupId="you"|"bots"|"workspace"|"system";
type Group={id:GroupId;label:`shell.group.${GroupId}`;items:Page[]};
const page=(id:SettingsTab,icon:TofiIconName):Page=>({id,icon,name:`shell.page.${id}.name`,description:`shell.page.${id}.description`});
const group=(id:GroupId,items:Page[]):Group=>({id,label:`shell.group.${id}`,items});
const adminPage=page("admin","group");
const groups:Group[]=[
 group("you",[page("general","sliders")]),
 group("bots",[page("connections","plug"),page("skills","skill"),page("approvals","shield-check"),page("models","sparkles")]),
 group("workspace",[page("computer","monitor"),page("keys","key"),page("usage","progress")]),
 group("system",[page("advanced","settings")])
];
type Target=SettingsTab|"close"|"home";
const narrowQuery="(max-width: 700px)";
function useNarrow(){
 const [narrow,setNarrow]=useState(()=>typeof matchMedia==="function"&&matchMedia(narrowQuery).matches);
 useEffect(()=>{const query=matchMedia(narrowQuery);const update=()=>setNarrow(query.matches);update();query.addEventListener("change",update);return()=>query.removeEventListener("change",update)},[]);
 return narrow;
}
/** Right-side value hints for the mobile list. Fetched only while the list is showing, and none of them probes a service. */
function useMobileHints(enabled:boolean){
 const [hints,setHints]=useState<{model?:string;computer?:string;keys?:number}>({});
 useEffect(()=>{
  if(!enabled)return;
  const controller=new AbortController();const {signal}=controller;
  const merge=(patch:object)=>{if(!signal.aborted)setHints(current=>({...current,...patch}))};
  request<{model:string}>("/api/model-settings",{signal}).then(value=>merge({model:value.model?providerLabels[providerForModel(value.model)]:undefined})).catch(()=>{});
  request<{state:string}>("/api/computers/firecracker/info",{signal}).then(value=>merge({computer:value.state})).catch(()=>{});
  request<{credentials:{kind?:string}[]}>("/api/computer/credentials",{signal}).then(value=>merge({keys:(value.credentials??[]).length})).catch(()=>{});
  return()=>controller.abort();
 },[enabled]);
 return hints;
}
export function SettingsShell({tab,onTab,onClose,renderPage,entry,refreshToken=0}:{tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;renderPage:(tab:SettingsTab)=>ReactNode;entry?:SettingsEntry;refreshToken?:number}){
 const {t}=useTranslation("settings");
 const {language}=useLanguage();
 const session=useOwnerSession();
 const identity=useAccountIdentity();
 const isAdmin=Boolean(session?.multi_account&&session.owner?.role==="admin");
 const visibleGroups:Group[]=useMemo(()=>isAdmin?groups.map(item=>item.id==="system"?{...item,items:[...item.items,adminPage]}:item):groups,[isAdmin]);
 const allItems=visibleGroups.flatMap(item=>item.items);
 const attention=useConnectionAttention(refreshToken);
 const counts:Partial<Record<SettingsTab,{value:number;tone:"honey"|"iris"}>>={...(attention>0?{connections:{value:attention,tone:"honey"}}:{})};
 const narrow=useNarrow();
 const [mobileView,setMobileView]=useState<SettingsView>(entry?.view??"home");
 const entrySeq=useRef(entry?.seq);
 useEffect(()=>{if(entry&&entry.seq!==entrySeq.current){entrySeq.current=entry.seq;setMobileView(entry.view)}},[entry]);
 const hints=useMobileHints(narrow&&mobileView==="home");
 useEffect(()=>{if(tab==="admin"&&!isAdmin)onTab("general")},[tab,isAdmin,onTab]);
 const [visited,setVisited]=useState<SettingsTab[]>([tab]);
 const [drafts,setDrafts]=useState<Record<string,SettingsDraft>>({});
 const [pending,setPending]=useState<Target|null>(null);
 const [saving,setSaving]=useState(false);
 const savingRef=useRef(false);
 const dialog=useRef<HTMLDialogElement>(null);
 const register=useCallback((id:string,draft:SettingsDraft|null)=>setDrafts(current=>{
  if(!draft){if(!(id in current))return current;const next={...current};delete next[id];return next}
  return {...current,[id]:draft};
 }),[]);
 const activeDrafts=Object.values(drafts).filter(draft=>draft.page===tab&&draft.dirty);
 const busy=Object.values(drafts).some(draft=>draft.busy)||saving;
 const dirty=Object.values(drafts).some(draft=>draft.dirty);
 const go=(target:Target)=>{
  if(target==="close")onClose();
  else if(target==="home")setMobileView("home");
  else{onTab(target);setMobileView("page")}
 };
 const navigate=(target:Target)=>{
  if(pending)return;
  if(target===tab){setMobileView("page");return}
  if(busy||document.querySelector('.settings-content [aria-busy="true"]'))return;
  if(activeDrafts.length){setPending(target);return}
  if(target==="close"&&dirty){setPending(target);return}
  go(target);
 };
 const saveActive=async(all=false)=>{
  if(savingRef.current)return false;
  savingRef.current=true;setSaving(true);
  try{for(const draft of all?Object.values(drafts).filter(draft=>draft.dirty):activeDrafts)if(!await draft.save())return false;return true}
  finally{savingRef.current=false;setSaving(false)}
 };
 const finish=(target:Target)=>{setPending(null);go(target)};
 const saveAndContinue=async()=>{if(!pending)return;const target=pending;if(await saveActive(target==="close"))finish(target);else setPending(null)};
 const discardAndContinue=()=>{if(!pending||busy)return;for(const draft of Object.values(drafts).filter(item=>pending==="close"?item.dirty:item.page===tab&&item.dirty))draft.discard();finish(pending)};
 useEffect(()=>{const element=dialog.current;if(pending&&!element?.open)element?.showModal();else if(!pending&&element?.open)element.close()},[pending]);
 useEffect(()=>{if(!dirty)return;const warn=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue=""};window.addEventListener("beforeunload",warn);return()=>window.removeEventListener("beforeunload",warn)},[dirty]);
 useEffect(()=>{const close=(event:Event)=>{event.preventDefault();navigate("close")};window.addEventListener("tofi-settings-close",close);return()=>window.removeEventListener("tofi-settings-close",close)});
 useEffect(()=>{const key=(event:KeyboardEvent)=>{if(event.key==="Escape"&&!event.defaultPrevented&&!pending){event.preventDefault();navigate("close")}};document.addEventListener("keydown",key);return()=>document.removeEventListener("keydown",key)});
 const providers=useMemo(()=>Object.fromEntries(pagesForProviders(visited,tab).map(page=>[page,{page,register}])),[visited,tab,register]);
 useEffect(()=>setVisited(current=>current.includes(tab)?current:[...current,tab]),[tab]);
 const pages=visited.includes(tab)?visited:[...visited,tab];
 const selected=allItems.find(item=>item.id===tab) ?? allItems[0];

 // One indicator slides between rail items; it draws the selected background, border and shadow.
 const list=useRef<HTMLDivElement>(null);
 const [indicator,setIndicator]=useState<{y:number;h:number}|null>(null);
 const [slide,setSlide]=useState(false);
 const measure=useCallback(()=>{
  const button=list.current?.querySelector<HTMLElement>('[aria-current="page"]');
  if(!button||!button.offsetHeight){return}
  setIndicator(current=>current&&current.y===button.offsetTop&&current.h===button.offsetHeight?current:{y:button.offsetTop,h:button.offsetHeight});
 },[]);
 useLayoutEffect(measure,[measure,tab,language,isAdmin,attention]);
 useEffect(()=>{if(!indicator)return;const id=requestAnimationFrame(()=>setSlide(true));return()=>cancelAnimationFrame(id)},[indicator===null]);
 useEffect(()=>{const element=list.current;if(!element||typeof ResizeObserver==="undefined")return;const observer=new ResizeObserver(measure);observer.observe(element);return()=>observer.disconnect()},[measure]);

 const [slot,setSlot]=useState<HTMLElement|null>(null);
 const hint=(id:SettingsTab):string=>{
  if(id==="models")return hints.model??"";
  if(id==="computer")return hints.computer?t(`shell.hint.computer.${hints.computer==="ready"||hints.computer==="starting"||hints.computer==="stopped"?hints.computer:"waiting"}`):"";
  if(id==="keys")return hints.keys===undefined?"":String(hints.keys);
  return "";
 };
 const badge=(id:SettingsTab)=>{const count=counts[id];return count&&count.value>0?<span className={`settings-count is-${count.tone}`} aria-label={t("shell.attention",{count:count.value})}>{count.value}</span>:null};
 const accountBody=<><span className="settings-account-avatar" aria-hidden="true">{identity.initial}</span><span className="settings-account-copy"><strong>{identity.name}</strong>{identity.detail&&<small>{identity.detail}</small>}</span></>;
 const mobileOpen=mobileView==="page";
 return <div className="settings-shell" data-view={mobileView}>
  <nav className="settings-tabs" aria-label={t("shell.category")}>
   <button type="button" className="settings-account-card" aria-current={tab==="general"?"true":undefined} onClick={()=>navigate("general")}>{accountBody}</button>
   <div className="settings-rail-list" ref={list}>
    <span className={`settings-rail-indicator${slide?" is-ready":""}`} aria-hidden="true" style={indicator?{transform:`translateY(${indicator.y}px)`,height:indicator.h}:{display:"none"}}/>
    {visibleGroups.map(section=><div className="settings-nav-group" key={section.id}><p>{t(section.label)}</p>{section.items.map(item=><button key={item.id} data-tab={item.id} className={tab===item.id?"selected":""} aria-current={tab===item.id?"page":undefined} onClick={()=>navigate(item.id)}><TofiIcon name={item.icon} size={20} variant={tab===item.id?"filled":"outline"}/><span className="settings-nav-label">{t(item.name)}</span>{badge(item.id)}</button>)}</div>)}
   </div>
  </nav>
  <div className="settings-mhome" inert={narrow&&mobileOpen}>
   <header className="settings-mbar"><span aria-hidden="true"/><h2>{t("shell.title")}</h2><button className="settings-dismiss" aria-label={t("shell.close")} onClick={()=>navigate("close")}><TofiIcon name="close" size={20}/></button></header>
   <div className="settings-mscroll">
    <button type="button" className="settings-mlist settings-mrow settings-maccount" data-tab="general" onClick={()=>navigate("general")}>{accountBody}<TofiIcon name="chevron-right" size={16} aria-hidden="true"/></button>
    {visibleGroups.filter(section=>section.id!=="you").map(section=><section key={section.id} className="settings-mgroup"><p>{t(section.label)}</p><div className="settings-mlist">{section.items.map(item=><button type="button" className="settings-mrow" key={item.id} data-tab={item.id} onClick={()=>navigate(item.id)}><span className="settings-mtile" aria-hidden="true"><TofiIcon name={item.icon} size={18}/></span><span className="settings-mname">{t(item.name)}</span>{badge(item.id)}{hint(item.id)&&<span className="settings-mhint">{hint(item.id)}</span>}<TofiIcon name="chevron-right" size={16} aria-hidden="true"/></button>)}</div></section>)}
   </div>
  </div>
  <div className="settings-content" inert={narrow&&!mobileOpen}>
   <header className="settings-page-heading"><button type="button" className="settings-back" aria-label={t("shell.back")} onClick={()=>navigate("home")}><TofiIcon name="chevron-left" size={20}/></button><div className="settings-page-title"><h2>{t(selected.name)}</h2><p>{t(selected.description)}</p></div><div className="settings-header-actions" ref={setSlot}/><button className="settings-dismiss" aria-label={t("shell.close")} onClick={()=>navigate("close")}><TofiIcon name="close" size={20}/></button></header>
   <SettingsHeaderSlotContext.Provider value={slot}>{pages.map(page=><div key={page} hidden={page!==tab} data-page={page} className="settings-page-body"><SettingsDraftContext.Provider value={providers[page]}><SettingsActivityContext.Provider value={page===tab}>{renderPage(page)}</SettingsActivityContext.Provider></SettingsDraftContext.Provider></div>)}</SettingsHeaderSlotContext.Provider>
   {activeDrafts.length>0&&<div className="settings-save-bar" role="status"><span>{t("shell.unsaved")}</span><button type="button" disabled={busy} onClick={()=>activeDrafts.forEach(draft=>draft.discard())}>{t("shell.discard")}</button><button type="button" className="primary-button" disabled={busy} onClick={()=>void saveActive()}>{saving?t("shell.saving"):t("shell.save")}</button></div>}
  </div>
  <dialog ref={dialog} className="settings-leave-dialog" aria-labelledby="settings-leave-title" onCancel={event=>{event.preventDefault();if(!saving)setPending(null)}}><h3 id="settings-leave-title">{t("shell.leave_title")}</h3><p>{pending==="close"?t("shell.leave_close"):t("shell.leave_switch")}</p><div className="settings-leave-actions"><button type="button" onClick={()=>setPending(null)} disabled={saving} autoFocus>{t("shell.keep_editing")}</button><button type="button" onClick={discardAndContinue} disabled={saving}>{t("shell.discard")}</button><button type="button" className="primary-button" onClick={()=>void saveAndContinue()} disabled={saving}>{saving?t("shell.saving"):t("shell.save_continue")}</button></div></dialog>
 </div>;
}
function pagesForProviders(visited:SettingsTab[],tab:SettingsTab){return visited.includes(tab)?visited:[...visited,tab]}
