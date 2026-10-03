import {useCallback, useEffect, useMemo, useRef, useState, type ReactNode} from "react";
import {useOwnerSession} from "./OwnerSession";
import {TofiIcon, type TofiIconName} from "./icons";
import {SettingsActivityContext} from "./settingsActivity";
import {SettingsDraftContext, type SettingsDraft} from "./settingsDraft";
export type SettingsTab="account"|"models"|"dictate"|"usage"|"connection"|"computers"|"credentials"|"mcp"|"skills"|"debug"|"admin";
const sections:{label:string;items:{id:SettingsTab;name:string;description:string;icon:TofiIconName}[]}[]=[
 {label:"偏好",items:[{id:"account",name:"通用",description:"让 Tofi 更适合你的日常",icon:"settings"},{id:"models",name:"模型与思考",description:"选择新 Bot 的默认模型与思考强度",icon:"sparkles"},{id:"dictate",name:"语音听写",description:"选择语音听写模型",icon:"file-audio"}]},
 {label:"工作空间",items:[{id:"usage",name:"用量",description:"查看 Agent 的模型消耗与上下文压缩进度",icon:"activity"},{id:"computers",name:"电脑与资源",description:"共享 Linux 电脑、存储与运行资源",icon:"monitor"},{id:"credentials",name:"密钥与环境",description:"直接配置电脑需要的环境变量和 SSH 密钥",icon:"key"},{id:"mcp",name:"工具",description:"连接外部服务，扩展 Bot 的能力",icon:"plug"},{id:"skills",name:"Skills",description:"管理可复用的工作方法",icon:"skill"}]},
 {label:"高级",items:[{id:"connection",name:"服务器与 Codex",description:"查看连接并管理模型服务",icon:"server"},{id:"debug",name:"调试",description:"",icon:"activity"}]}
];
export function SettingsShell({tab,onTab,onClose,renderPage}:{tab:SettingsTab;onTab:(tab:SettingsTab)=>void;onClose:()=>void;renderPage:(tab:SettingsTab)=>ReactNode}){
 const session=useOwnerSession();
 const visibleSections=session?.multi_account && session.owner?.role==="admin" ? [...sections,{label:"管理",items:[{id:"admin" as SettingsTab,name:"Admin 控制台",description:"邀请、管理和停用账号",icon:"settings" as TofiIconName}]}] : sections;
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
 return <><div className="settings-mobile-nav"><label htmlFor="settings-category">设置分类</label><select id="settings-category" value={tab} onChange={event=>navigate(event.target.value as SettingsTab)}>{visibleSections.map(section=><optgroup label={section.label} key={section.label}>{section.items.map(item=><option value={item.id} key={item.id}>{item.name}</option>)}</optgroup>)}</select></div><nav className="settings-tabs" aria-label="设置分类"><div className="settings-brand"><span>设置</span></div>{visibleSections.map(section=><div className="settings-nav-group" key={section.label}><p>{section.label}</p>{section.items.map(item=><button key={item.id} className={tab===item.id?"selected":""} aria-current={tab===item.id?"page":undefined} onClick={()=>navigate(item.id)}><TofiIcon name={item.icon} size={20} variant={tab===item.id?"filled":"outline"}/><span>{item.name}</span></button>)}</div>)}</nav><div className="settings-content"><header className="settings-page-heading"><div><h2>{selected.name}</h2>{selected.description&&<p>{selected.description}</p>}</div><button className="settings-dismiss" aria-label="关闭设置" onClick={()=>navigate("close")}><TofiIcon name="close" size={20}/></button></header>{pages.map(page=><div key={page} hidden={page!==tab} className="settings-page-body"><SettingsDraftContext.Provider value={providers[page]}><SettingsActivityContext.Provider value={page===tab}>{renderPage(page)}</SettingsActivityContext.Provider></SettingsDraftContext.Provider></div>)}{activeDrafts.length>0&&<div className="settings-save-bar" role="status"><span>有未保存的更改</span><button type="button" disabled={busy} onClick={()=>activeDrafts.forEach(draft=>draft.discard())}>放弃更改</button><button type="button" className="primary-button" disabled={busy} onClick={()=>void saveActive()}>{saving?"保存中…":"保存更改"}</button></div>}</div><dialog ref={dialog} className="settings-leave-dialog" aria-labelledby="settings-leave-title" onCancel={event=>{event.preventDefault();if(!saving)setPending(null)}}><h3 id="settings-leave-title">还有未保存的更改</h3><p>{pending==="close"?"关闭设置会丢失未保存的输入。":"切换分类前，保存或放弃当前更改。"}</p><div className="settings-leave-actions"><button type="button" onClick={()=>setPending(null)} disabled={saving} autoFocus>继续编辑</button><button type="button" onClick={discardAndContinue} disabled={saving}>放弃更改</button><button type="button" className="primary-button" onClick={()=>void saveAndContinue()} disabled={saving}>{saving?"保存中…":"保存并继续"}</button></div></dialog></>;
}
function pagesForProviders(visited:SettingsTab[],tab:SettingsTab){return visited.includes(tab)?visited:[...visited,tab]}
