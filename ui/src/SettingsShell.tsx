import {useCallback, useEffect, useMemo, useRef, useState, type ReactNode} from "react";
import {useOwnerSession} from "./OwnerSession";
import {TofiIcon, type TofiIconName} from "./icons";
import {SettingsActivityContext} from "./settingsActivity";
import {SettingsDraftContext, type SettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
export type SettingsTab="account"|"models"|"dictate"|"usage"|"connection"|"computers"|"credentials"|"mcp"|"skills"|"debug"|"admin";
// Names and descriptions are catalog keys (settings:shell.*), translated at render.
type Page={id:SettingsTab;icon:TofiIconName;name:`shell.page.${SettingsTab}.name`;description?:`shell.page.${Exclude<SettingsTab,"debug">}.description`};
type Section={label:`shell.group.${"preferences"|"workspace"|"advanced"|"admin"}`;items:Page[]};
const page=(id:SettingsTab,icon:TofiIconName):Page=>({id,icon,name:`shell.page.${id}.name`,description:id==="debug"?undefined:`shell.page.${id}.description`});
const sections:Section[]=[
 {label:"shell.group.preferences",items:[page("account","settings"),page("models","sparkles"),page("dictate","file-audio")]},
 {label:"shell.group.workspace",items:[page("usage","activity"),page("computers","monitor"),page("credentials","key"),page("mcp","plug"),page("skills","skill")]},
 {label:"shell.group.advanced",items:[page("connection","server"),page("debug","activity")]}
];
export function SettingsShell({tab,onTab,onClose,renderPage}:{tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;renderPage:(tab:SettingsTab)=>ReactNode}){
 const {t}=useTranslation("settings");
 const session=useOwnerSession();
 const visibleSections:Section[]=session?.multi_account && session.owner?.role==="admin" ? [...sections,{label:"shell.group.admin",items:[page("admin","settings")]}] : sections;
 useEffect(()=>{if(tab==="admin"&&!(session?.multi_account&&session.owner?.role==="admin"))onTab("account")},[tab,session?.multi_account,session?.owner?.role,onTab]);
 const [visited,setVisited]=useState<SettingsTab[]>([tab]);
 const [drafts,setDrafts]=useState<Record<string,SettingsDraft>>({});
 const [pending,setPending]=useState<SettingsTab|"close"|null>(null);
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
 const navigate=(target:SettingsTab|"close")=>{
  if(pending||target===tab)return;
  if(busy||document.querySelector('.settings-content [aria-busy="true"]'))return;
  if(activeDrafts.length){setPending(target);return}
  if(target==="close"&&dirty){setPending(target);return}
  if(target==="close")onClose();else onTab(target);
 };
 const saveActive=async(all=false)=>{
  if(savingRef.current)return false;
  savingRef.current=true;setSaving(true);
  try{for(const draft of all?Object.values(drafts).filter(draft=>draft.dirty):activeDrafts)if(!await draft.save())return false;return true}
  finally{savingRef.current=false;setSaving(false)}
 };
 const finish=(target:SettingsTab|"close")=>{setPending(null);if(target==="close")onClose();else onTab(target)};
 const saveAndContinue=async()=>{if(!pending)return;const target=pending;if(await saveActive(target==="close"))finish(target);else setPending(null)};
 const discardAndContinue=()=>{if(!pending||busy)return;for(const draft of Object.values(drafts).filter(item=>pending==="close"?item.dirty:item.page===tab&&item.dirty))draft.discard();finish(pending)};
 useEffect(()=>{const element=dialog.current;if(pending&&!element?.open)element?.showModal();else if(!pending&&element?.open)element.close()},[pending]);
 useEffect(()=>{if(!dirty)return;const warn=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue=""};window.addEventListener("beforeunload",warn);return()=>window.removeEventListener("beforeunload",warn)},[dirty]);
 useEffect(()=>{const close=(event:Event)=>{event.preventDefault();navigate("close")};window.addEventListener("tofi-settings-close",close);return()=>window.removeEventListener("tofi-settings-close",close)});
 useEffect(()=>{const key=(event:KeyboardEvent)=>{if(event.key==="Escape"&&!event.defaultPrevented&&!pending){event.preventDefault();navigate("close")}};document.addEventListener("keydown",key);return()=>document.removeEventListener("keydown",key)});
 const providers=useMemo(()=>Object.fromEntries(pagesForProviders(visited,tab).map(page=>[page,{page,register}])),[visited,tab,register]);
 useEffect(()=>setVisited(current=>current.includes(tab)?current:[...current,tab]),[tab]);
 const pages=visited.includes(tab)?visited:[...visited,tab];
 const selected=visibleSections.flatMap(s=>s.items).find(item=>item.id===tab) ?? visibleSections[0].items[0];
 return <><div className="settings-mobile-nav"><label htmlFor="settings-category">{t("shell.category")}</label><select id="settings-category" value={tab} onChange={event=>navigate(event.target.value as SettingsTab)}>{visibleSections.map(section=><optgroup label={t(section.label)} key={section.label}>{section.items.map(item=><option value={item.id} key={item.id}>{t(item.name)}</option>)}</optgroup>)}</select></div><nav className="settings-tabs" aria-label={t("shell.category")}><div className="settings-brand"><span>{t("shell.title")}</span></div>{visibleSections.map(section=><div className="settings-nav-group" key={section.label}><p>{t(section.label)}</p>{section.items.map(item=><button key={item.id} className={tab===item.id?"selected":""} aria-current={tab===item.id?"page":undefined} onClick={()=>navigate(item.id)}><TofiIcon name={item.icon} size={20} variant={tab===item.id?"filled":"outline"}/><span>{t(item.name)}</span></button>)}</div>)}</nav><div className="settings-content"><header className="settings-page-heading"><div><h2>{t(selected.name)}</h2>{selected.description&&<p>{t(selected.description)}</p>}</div><button className="settings-dismiss" aria-label={t("shell.close")} onClick={()=>navigate("close")}><TofiIcon name="close" size={20}/></button></header>{pages.map(page=><div key={page} hidden={page!==tab} className="settings-page-body"><SettingsDraftContext.Provider value={providers[page]}><SettingsActivityContext.Provider value={page===tab}>{renderPage(page)}</SettingsActivityContext.Provider></SettingsDraftContext.Provider></div>)}{activeDrafts.length>0&&<div className="settings-save-bar" role="status"><span>{t("shell.unsaved")}</span><button type="button" disabled={busy} onClick={()=>activeDrafts.forEach(draft=>draft.discard())}>{t("shell.discard")}</button><button type="button" className="primary-button" disabled={busy} onClick={()=>void saveActive()}>{saving?t("shell.saving"):t("shell.save")}</button></div>}</div><dialog ref={dialog} className="settings-leave-dialog" aria-labelledby="settings-leave-title" onCancel={event=>{event.preventDefault();if(!saving)setPending(null)}}><h3 id="settings-leave-title">{t("shell.leave_title")}</h3><p>{pending==="close"?t("shell.leave_close"):t("shell.leave_switch")}</p><div className="settings-leave-actions"><button type="button" onClick={()=>setPending(null)} disabled={saving} autoFocus>{t("shell.keep_editing")}</button><button type="button" onClick={discardAndContinue} disabled={saving}>{t("shell.discard")}</button><button type="button" className="primary-button" onClick={()=>void saveAndContinue()} disabled={saving}>{saving?t("shell.saving"):t("shell.save_continue")}</button></div></dialog></>;
}
function pagesForProviders(visited:SettingsTab[],tab:SettingsTab){return visited.includes(tab)?visited:[...visited,tab]}
