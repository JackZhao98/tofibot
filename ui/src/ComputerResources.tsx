import {useCallback,useEffect,useRef,useState} from "react";
import {useDebugMode} from "./debugMode";
import {ApiError,request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
import "./computer-resources.css";

type Allocation={vcpus:number;memory_mib:number;disk_gib:number};
type Resources={current:Allocation;desired:Allocation;pending:boolean;state:string;error?:string;host:{cpus:number;memory_total_mib:number;memory_available_mib:number;disk_available_gib:number};limits:{max_vcpus:number;max_memory_mib:number;max_disk_gib:number}};
const resourceError=(cause:unknown,fallback:string)=>cause instanceof Error?cause.message:fallback;

function RestartConfirmation({onCancel,onConfirm}:{onCancel:()=>void;onConfirm:()=>void}) {
 const {t}=useTranslation("computer");
 const dialog=useRef<HTMLDialogElement>(null);
 useEffect(()=>{const element=dialog.current;element?.showModal();return()=>element?.close()},[]);
 return <dialog ref={dialog} className="resource-restart-dialog" aria-labelledby="resource-restart-title" aria-describedby="resource-restart-impact" onCancel={event=>{event.preventDefault();onCancel()}} onKeyDown={event=>{if(event.key==="Escape"){event.preventDefault();event.stopPropagation();onCancel()}}}>
  <h3 id="resource-restart-title">{t("resources.restart.title")}</h3>
  <p id="resource-restart-impact">{t("resources.restart.impact")}</p>
  <div className="resource-restart-actions"><button type="button" className="secondary-button" autoFocus onClick={onCancel}>{t("resources.cancel")}</button><button type="button" className="primary-button" onClick={onConfirm}>{t("resources.restart.confirm")}</button></div>
 </dialog>;
}

export function ComputerResources(){
 const {t}=useTranslation("computer");
 const active=useSettingsActive();
 const activeRef=useRef(active);activeRef.current=active;
 const debug=useDebugMode();
 const [data,setData]=useState<Resources|null>(null);
 const [desired,setDesired]=useState<Allocation|null>(null);
 const [editing,setEditing]=useState(false);
 const [busy,setBusy]=useState(false);
 const [applying,setApplying]=useState(false);
 const [confirming,setConfirming]=useState(false);
 const [reading,setReading]=useState(false);
 const [readError,setReadError]=useState("");
 const [actionError,setActionError]=useState("");
 const [status,setStatus]=useState("");
 const alive=useRef(false);
 const editingRef=useRef(false);
 const applyingRef=useRef(false);
 const mutating=useRef(false);
 const readVersion=useRef(0);
 const readController=useRef<AbortController|null>(null);
 const restartButton=useRef<HTMLButtonElement>(null);

 const refresh=useCallback(async()=>{
  if(mutating.current||!activeRef.current)return;
  const version=++readVersion.current;
  readController.current?.abort();
  const controller=new AbortController();readController.current=controller;
  setReading(true);
  try {
   const next=await request<Resources>("/api/computer/resources",{signal:controller.signal});
   if(!alive.current||controller.signal.aborted||version!==readVersion.current)return;
   setData(next);setReadError("");
   if(!editingRef.current)setDesired(next.desired);
   if(next.pending)setStatus("");
   if(applyingRef.current&&(next.error||next.state==="error")){
    applyingRef.current=false;setApplying(false);setStatus("");setActionError(next.error||t("resources.error.restart_incomplete"));
   }else if(applyingRef.current&&next.state==="ready"&&!next.pending){
    applyingRef.current=false;setApplying(false);setActionError("");setStatus(t("resources.status.applied"));
   }
  }catch(cause){
   if(alive.current&&!controller.signal.aborted&&version===readVersion.current)setReadError(resourceError(cause,t("resources.error.read")));
  }finally{if(alive.current&&version===readVersion.current)setReading(false)}
 // eslint-disable-next-line react-hooks/exhaustive-deps -- t only words messages; the poll keeps one identity.
 },[]);

 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 useEffect(()=>{
  if(!active)return;
  let stopped=false;let timer:ReturnType<typeof setTimeout>;
  async function poll(){await refresh();if(!stopped)timer=setTimeout(poll,applyingRef.current?1500:3000)}
  void poll();
  const focused=()=>{if(!document.hidden)void refresh()};
  window.addEventListener("focus",focused);document.addEventListener("visibilitychange",focused);
  return()=>{stopped=true;readVersion.current++;readController.current?.abort();clearTimeout(timer);window.removeEventListener("focus",focused);document.removeEventListener("visibilitychange",focused)};
 },[refresh,active]);

 function beginMutation(){mutating.current=true;readVersion.current++;readController.current?.abort();setReading(false);setBusy(true);setActionError("");setStatus("")}
 async function save(){
  if(mutating.current||applyingRef.current||!desired||!data)return false;
  if(![desired.vcpus,desired.memory_mib,desired.disk_gib].every(Number.isFinite)||!Number.isInteger(desired.vcpus)||desired.vcpus<1||desired.vcpus>data.limits.max_vcpus||desired.memory_mib<512||desired.memory_mib>data.limits.max_memory_mib||desired.disk_gib<data.current.disk_gib||desired.disk_gib>data.limits.max_disk_gib){setActionError(t("resources.error.out_of_range"));return false}
  beginMutation();
  try{
   const next=await request<Resources>("/api/computer/resources",{method:"PUT",body:JSON.stringify(desired)});
   if(!alive.current)return false;
   setData(next);setDesired(next.desired);editingRef.current=false;setEditing(false);setReadError("");setStatus(next.pending?t("resources.status.saved_pending"):t("resources.status.saved"));
   return true;
  }catch(cause){if(alive.current)setActionError(resourceError(cause,t("resources.error.save")));return false}
  finally{mutating.current=false;if(alive.current)setBusy(false)}
 }
 async function apply(){
  setConfirming(false);
  if(mutating.current||applyingRef.current||!data||(!data.pending&&data.state!=="error"))return;
  beginMutation();
  try{
   await request("/api/computer/resources/apply",{method:"POST",body:JSON.stringify({confirm:true})});
   if(!alive.current)return;
   applyingRef.current=true;setApplying(true);setReadError("");
  }catch(cause){
   if(alive.current)setActionError(cause instanceof ApiError&&cause.status===409?t("resources.error.busy"):resourceError(cause,t("resources.error.restart_request")));
  }finally{
   mutating.current=false;
   if(alive.current){setBusy(false);void refresh();restartButton.current?.focus()}
  }
 }
 const restarting=applying||data?.state==="restarting"||(Boolean(data?.pending)&&data?.state==="starting");
 const failure=actionError||data?.error||(!restarting&&data?.state==="error"?t("resources.error.start_failed"):"");
 const dirty=Boolean(editing&&data&&desired&&(desired.vcpus!==data.desired.vcpus||desired.memory_mib!==data.desired.memory_mib||desired.disk_gib!==data.desired.disk_gib));
 useSettingsDraft({label:t("resources.draft_label"),dirty,busy,save,discard:()=>{if(data)setDesired(data.desired);editingRef.current=false;setEditing(false);setActionError("")}});
 return <section className="settings-section">
  <div className="settings-row"><h3>{t("resources.title")}</h3>{data&&<button type="button" className="secondary-button" disabled={busy||restarting} onClick={()=>{const next=!editing;editingRef.current=next;setDesired(data.desired);setEditing(next);setActionError("")}}>{editing?t("resources.cancel"):t("resources.adjust")}</button>}</div>
  {data&&<>
   <div className="resource-grid"><div><span>CPU</span><strong>{data.current.vcpus}<small> {t("resources.unit.cores")}</small></strong></div><div><span>{t("resources.memory")}</span><strong>{Number((data.current.memory_mib/1024).toFixed(1))}<small> GB</small></strong></div><div><span>{t("resources.disk_allocated")}</span><strong>{data.current.disk_gib}<small> GB</small></strong></div></div>
   {debug&&<p className="field-note">{t("resources.host",{cpus:data.host.cpus,memory:Math.round(data.host.memory_total_mib/1024),disk:Math.floor(data.host.disk_available_gib)})}</p>}
   {data.pending&&<div className="settings-notice resource-pending"><span>{t("resources.pending",{cpus:data.desired.vcpus,memory:data.desired.memory_mib/1024,disk:data.desired.disk_gib})}</span>{!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.restart_apply")}</button>}</div>}
  </>}
  {editing&&data&&desired&&<form aria-busy={busy} className="resource-form" onSubmit={event=>{event.preventDefault();void save()}}>
   <fieldset className="resource-fields" disabled={busy||restarting}>
    <label>{t("resources.field.cpus")}<input required type="number" min="1" max={data.limits.max_vcpus} value={desired.vcpus} onChange={event=>setDesired({...desired,vcpus:Number(event.target.value)})}/></label>
    <label>{t("resources.field.memory")}<input required type="number" min="0.5" step="0.5" max={data.limits.max_memory_mib/1024} value={desired.memory_mib/1024} onChange={event=>setDesired({...desired,memory_mib:Number(event.target.value)*1024})}/></label>
    <label>{t("resources.field.disk")}<input required type="number" min={data.current.disk_gib} max={data.limits.max_disk_gib} value={desired.disk_gib} onChange={event=>setDesired({...desired,disk_gib:Number(event.target.value)})}/></label>
    <p className="field-note">{t("resources.field_note")}</p>
   </fieldset>
  </form>}
  {restarting&&<p className="settings-feedback" role="status">{t("resources.restarting")}</p>}
  {!restarting&&status&&<p className="settings-feedback" role="status">{status}</p>}
  {failure&&<div className="resource-read-error" role="alert"><span>{failure}</span>{data?.state==="error"&&!data.pending&&!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.retry_restart")}</button>}</div>}
  {readError&&<div className="resource-read-error" role="alert"><span>{readError}</span><button type="button" className="text-button" disabled={reading||busy} onClick={()=>void refresh()}>{reading?t("resources.reading"):t("resources.retry")}</button></div>}
  {!data&&!readError&&<p className="field-note" role="status">{t("resources.loading")}</p>}
  {confirming&&<RestartConfirmation onCancel={()=>setConfirming(false)} onConfirm={()=>void apply()}/>}
 </section>;
}
