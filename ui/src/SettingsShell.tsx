import {useCallback, useEffect, useMemo, useRef, useState, type ReactNode} from "react";
import {useOwnerSession} from "./OwnerSession";
import {type TofiIconName} from "./icons";
import {Brand} from "./ds/Brand/Brand";
import {Button} from "./ds/Button/Button";
import {SettingsShell as ShellFrame, type SettingsShellGroup} from "./ds/SettingsShell/SettingsShell";
import {SettingsActivityContext} from "./settingsActivity";
import {SettingsDraftContext, type SettingsDraft} from "./settingsDraft";
import {SettingsHeaderSlotContext} from "./settings/headerSlot";
import {useConnectionAttention} from "./settings/useConnectionAttention";
import {legacySettingsTabs} from "./settings/deepLinks";
import {takeSettingsSection} from "./settings/pendingSection";
import {jumpWhenReady} from "./settings/sectionJump";
import {revealSection} from "./settings/subpages";
import {SETTINGS_SECTIONS} from "./settings/sections";
import {useSavedFlash} from "./settings/components";
import {useTranslation} from "./i18n";
import {ConfirmDialog} from "./overlays/ModalDialog";
import "./layout/settings-page.css";

export type SettingsTab="general"|"connections"|"skills"|"permissions"|"models"|"computer"|"devices"|"usage"|"advanced"|"admin"|"admin-groups"|"admin-resources"|"admin-design";
// Compile-time check: every tab has its section list in settings/sections.ts (the ids a link can scroll to).
export const SETTINGS_TAB_SECTIONS:Record<SettingsTab,readonly string[]>=SETTINGS_SECTIONS;
/** Not used since V3 PR 6.1: a narrow window shows the page with a strip of links, not a list that pushes the page in. Callers may still pass it. */
export type SettingsView="home"|"page";
/** `seq` changes on every open request, so a deep link to the tab already selected still jumps to its section. */
export type SettingsEntry={seq:number;view?:SettingsView};
// Names and descriptions are catalog keys (settings:shell.*), translated at render.
/** `beta` and `dev` show a small tag next to the name: a working preview, and a feature still being built. */
export type PageTag="beta"|"dev";
// A page's description (`shell.page.<id>.description`) is no longer shown in the window; the manual and reference pages the server generates still read it (internal/app/settings_artifacts.go).
type Page={id:SettingsTab;icon:TofiIconName;tag?:PageTag;name:`shell.page.${SettingsTab}.name`};
// The rail groups (owner Q2, V3 PLAN section 11): You; Organization (the Bot pages, and the account pages for an admin); Server.
type GroupId="you"|"organization"|"server";
type Group={id:GroupId;items:Page[]};
const page=(id:SettingsTab,icon:TofiIconName,tag?:PageTag):Page=>({id,icon,tag,name:`shell.page.${id}.name`});
const group=(id:GroupId,items:Page[]):Group=>({id,items});
// Account management is multi-account only; the single-account owner also gets the design library.
const adminPages=[page("admin","group"),page("admin-groups","layers"),page("admin-resources","key")];
const designPage=page("admin-design","layout-grid");
// The "advanced" tab id is the System page: version, server, Behavior (a subpage), data, troubleshooting, Start over.
const baseGroups=(admin:boolean,multi:boolean):Group[]=>[
 group("you",[page("general","sliders")]),
 group("organization",[page("models","sparkles"),page("connections","plug"),page("skills","skill"),page("permissions","shield-check"),page("computer","layers"),page("usage","progress"),...(multi?adminPages:[])]),
 group("server",[page("advanced","settings"),...(admin?[designPage]:[])])
];
type Target=SettingsTab|"close";
export function SettingsShell({tab:requestedTab,onTab,onClose,renderPage,entry,refreshToken=0}:{tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;renderPage:(tab:SettingsTab)=>ReactNode;entry?:SettingsEntry;refreshToken?:number}){
 const {t}=useTranslation("settings");
 // An old tab id (a saved link, an event from before the rename) lands on the page that replaced it.
 const tab=(legacySettingsTabs[requestedTab as string]??requestedTab) as SettingsTab;
 const session=useOwnerSession();
 const isAdmin=Boolean(session&&(!session.multi_account||session.owner?.role==="admin"));
 const isMultiAdmin=isAdmin&&Boolean(session?.multi_account);
 const visibleGroups:Group[]=useMemo(()=>baseGroups(isAdmin,isMultiAdmin),[isAdmin,isMultiAdmin]);
 const allItems=visibleGroups.flatMap(item=>item.items);
 const attention=useConnectionAttention(refreshToken);
 const counts:Partial<Record<SettingsTab,number>>={...(attention>0?{connections:attention}:{})};
 useEffect(()=>{if(tab!==requestedTab)onTab(tab)},[tab,requestedTab,onTab]);
 useEffect(()=>{if((tab==="admin-design"&&!isAdmin)||((tab==="admin"||tab==="admin-groups"||tab==="admin-resources")&&!isMultiAdmin))onTab("general")},[tab,isAdmin,isMultiAdmin,onTab]);
 const [visited,setVisited]=useState<SettingsTab[]>([tab]);
 const [drafts,setDrafts]=useState<Record<string,SettingsDraft>>({});
 const [pending,setPending]=useState<Target|null>(null);
 const [saving,setSaving]=useState(false);
 const [saved,flashSaved]=useSavedFlash();
 const savingRef=useRef(false);
 const keepEditing=useRef<HTMLButtonElement>(null);
 const register=useCallback((id:string,draft:SettingsDraft|null)=>setDrafts(current=>{
  if(!draft){if(!(id in current))return current;const next={...current};delete next[id];return next}
  return {...current,[id]:draft};
 }),[]);
 const activeDrafts=Object.values(drafts).filter(draft=>draft.page===tab&&draft.dirty);
 const busy=Object.values(drafts).some(draft=>draft.busy)||saving;
 const dirty=Object.values(drafts).some(draft=>draft.dirty);
 const go=(target:Target)=>{if(target==="close")onClose();else onTab(target)};
 const navigate=(target:Target)=>{
  if(pending)return;
  if(target===tab)return;
  if(busy||document.querySelector('.settings-content .settings-page-body:not([hidden]) [aria-busy="true"]'))return;
  if(activeDrafts.length){setPending(target);return}
  if(target==="close"&&dirty){setPending(target);return}
  go(target);
 };
 const saveActive=async(all=false)=>{
  if(savingRef.current)return false;
  savingRef.current=true;setSaving(true);
  try{for(const draft of all?Object.values(drafts).filter(draft=>draft.dirty):activeDrafts)if(!await draft.save())return false;flashSaved();return true}
  finally{savingRef.current=false;setSaving(false)}
 };
 const finish=(target:Target)=>{setPending(null);go(target)};
 const saveAndContinue=async()=>{if(!pending)return;const target=pending;if(await saveActive(target==="close"))finish(target);else setPending(null)};
 const discardAndContinue=()=>{if(!pending||busy)return;for(const draft of Object.values(drafts).filter(item=>pending==="close"?item.dirty:item.page===tab&&item.dirty))draft.discard();finish(pending)};
 useEffect(()=>{if(!dirty)return;const warn=(event:BeforeUnloadEvent)=>{event.preventDefault();event.returnValue=""};window.addEventListener("beforeunload",warn);return()=>window.removeEventListener("beforeunload",warn)},[dirty]);
 useEffect(()=>{const close=(event:Event)=>{event.preventDefault();navigate("close")};window.addEventListener("tofi-settings-close",close);return()=>window.removeEventListener("tofi-settings-close",close)});
 const providers=useMemo(()=>Object.fromEntries(pagesForProviders(visited,tab).map(page=>[page,{page,register}])),[visited,tab,register]);
 useEffect(()=>setVisited(current=>current.includes(tab)?current:[...current,tab]),[tab]);
 const pages=visited.includes(tab)?visited:[...visited,tab];
 const selected=allItems.find(item=>item.id===tab) ?? allItems[0];
 const selectedGroup=visibleGroups.find(item=>item.items.some(page=>page.id===selected.id));

 // A link that names a section (`/settings/models#defaults`): scroll to it once its page is mounted and flash it.
 const jump=useRef<string|null>(null);
 useEffect(()=>{
  const section=takeSettingsSection("settings",tab);
  if(section)jump.current=section;
  const target=jump.current;
  if(!target)return;
  // A section inside a subpage or a folded group: open that first, then scroll to it.
  revealSection(tab,target);
  return jumpWhenReady(()=>document.querySelector(`.settings-content [data-page="${tab}"]`),target,()=>{if(jump.current===target)jump.current=null;revealSection(tab,null)});
 },[entry?.seq,tab]);
 const [slot,setSlot]=useState<HTMLElement|null>(null);
 const railGroups:SettingsShellGroup[]=visibleGroups.map(section=>({id:section.id,label:t(`shell.group.${section.id}`),items:section.items.map(item=>{const count=counts[item.id];return {value:item.id,label:t(item.name),icon:item.icon,badge:count,badgeLabel:count?t("shell.attention",{count}):undefined}})}));
 // The beta / in-development state is a quiet word beside the page title, not a pill in the rail.
 const pageTag=selected.tag?t(`shell.tag.${selected.tag}`):undefined;
 return <div className="settings-shell">
  <header className="settings-bar">
   <Brand size={30}/>
   <span className="settings-bar-sep" aria-hidden="true">/</span>
   <span className="settings-bar-title">{t("shell.title")}</span>
   <Button className="settings-dismiss" variant="secondary" size="sm" icon="arrow-left" onClick={()=>navigate("close")}>{t("shell.close")}</Button>
  </header>
  <ShellFrame
   hooks={{root:"settings-frame",nav:"settings-tabs",group:"settings-nav-group",linkLabel:"settings-nav-label",badge:"settings-count is-honey",head:"settings-page-heading",headText:"settings-page-title",actions:"settings-header-actions",main:"settings-content"}}
   groups={railGroups} active={selected.id} onNavigate={id=>navigate(id as SettingsTab)} navLabel={t("shell.category")}
   crumb={selectedGroup?t(`shell.group.${selectedGroup.id}`):undefined} title={t(selected.name)} tag={pageTag}
   status={saved?t("saved"):undefined} actionsRef={setSlot}>
   <SettingsHeaderSlotContext.Provider value={slot}>{pages.map(page=><div key={page} hidden={page!==tab} data-page={page} className="settings-page-body"><SettingsDraftContext.Provider value={providers[page]}><SettingsActivityContext.Provider value={page===tab}>{renderPage(page)}</SettingsActivityContext.Provider></SettingsDraftContext.Provider></div>)}</SettingsHeaderSlotContext.Provider>
   {activeDrafts.length>0&&<div className="settings-save-bar" role="status"><span>{t("shell.unsaved")}</span><Button size="sm" disabled={busy} onClick={()=>activeDrafts.forEach(draft=>draft.discard())}>{t("shell.discard")}</Button><Button variant="primary" size="sm" disabled={busy} onClick={()=>void saveActive()}>{saving?t("shell.saving"):t("shell.save")}</Button></div>}
  </ShellFrame>
  {pending&&<ConfirmDialog onClose={()=>setPending(null)} locked={saving} initialFocus={keepEditing} overlayClassName="settings-leave-overlay"><div className="settings-leave-dialog" aria-labelledby="settings-leave-title"><h3 id="settings-leave-title">{t("shell.leave_title")}</h3><p>{pending==="close"?t("shell.leave_close"):t("shell.leave_switch")}</p><div className="settings-leave-actions"><Button ref={keepEditing} onClick={()=>setPending(null)} disabled={saving}>{t("shell.keep_editing")}</Button><Button onClick={discardAndContinue} disabled={saving}>{t("shell.discard")}</Button><Button variant="primary" onClick={()=>void saveAndContinue()} disabled={saving}>{saving?t("shell.saving"):t("shell.save_continue")}</Button></div></div></ConfirmDialog>}
 </div>;
}
function pagesForProviders(visited:SettingsTab[],tab:SettingsTab){return visited.includes(tab)?visited:[...visited,tab]}
