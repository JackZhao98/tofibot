import {useCallback,useEffect,useRef,useState} from "react";
import {useDebugMode} from "./debugMode";
import {ApiError,request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
import "./computer-resources.css";
import {ResourceSheet,validAllocation,type Allocation,type Resources} from "./ResourceSheet";
import {useOwnerSession} from "./OwnerSession";
import {ConfirmDialog} from "./overlays/ModalDialog";

const resourceError=(cause:unknown,fallback:string)=>cause instanceof Error?cause.message:fallback;

function RestartConfirmation({onCancel,onConfirm}:{onCancel:()=>void;onConfirm:()=>void}) {
 const {t}=useTranslation("computer");
 const cancel=useRef<HTMLButtonElement>(null);
 // Bots that wait for a command to end: a restart stops those commands, so say so before the person confirms.
 const [waiting,setWaiting]=useState<string[]>([]);
 useEffect(()=>{let active=true;request<{count:number;bots:{name:string}[]}>("/api/command-waits").then(out=>{if(active&&out.count>0)setWaiting(out.bots.map(bot=>bot.name||"Bot"))}).catch(()=>{});return()=>{active=false}},[]);
 return <ConfirmDialog onClose={onCancel} initialFocus={cancel} overlayClassName="resource-restart-overlay"><div className="resource-restart-dialog" aria-labelledby="resource-restart-title" aria-describedby="resource-restart-impact">
  <h3 id="resource-restart-title">{t("resources.restart.title")}</h3>
  <p id="resource-restart-impact">{t("resources.restart.impact")}</p>
  {waiting.length>0&&<p role="status">{t("resources.restart.command_waits",{names:waiting.join(", ")})}</p>}
  <div className="resource-restart-actions"><button ref={cancel} type="button" className="secondary-button" onClick={onCancel}>{t("resources.cancel")}</button><button type="button" className="primary-button" onClick={onConfirm}>{t("resources.restart.confirm")}</button></div>
 </div></ConfirmDialog>;
}


/** Single-account: the owner is the admin and sets the size here. Multi-account: an admin sets it in the Admin console; a member only sees it. */
export function ComputerResources({onOpenAdmin}:{onOpenAdmin?:()=>void}){
 const {t}=useTranslation("computer");
 const session=useOwnerSession();
 const canEdit=!session?.multi_account;
 const isAdmin=Boolean(session?.multi_account&&session.owner?.role==="admin");
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
  if(!validAllocation(desired,data)){setActionError(t("resources.error.out_of_range"));return false}
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
  <div className="settings-row"><h3>{t("resources.title")}</h3>{canEdit&&data&&!editing&&<button type="button" className="secondary-button" disabled={busy||restarting} onClick={()=>{const next=!editing;editingRef.current=next;setDesired(data.desired);setEditing(next);setActionError("")}}>{editing?t("resources.cancel"):t("resources.adjust")}</button>}</div>
  {data&&<>
   <div className="resource-grid"><div><span>CPU</span><strong>{data.current.vcpus}<small> {t("resources.unit.cores")}</small></strong></div><div><span>{t("resources.memory")}</span><strong>{Number((data.current.memory_mib/1024).toFixed(1))}<small> GB</small></strong></div><div><span>{t("resources.disk_allocated")}</span><strong>{data.current.disk_gib}<small> GB</small></strong></div></div>
   {!canEdit&&<p className="field-note resource-admin-note">{isAdmin?t("resources.admin_sets_admin"):t("resources.admin_sets")}{isAdmin&&onOpenAdmin&&<> <button type="button" className="text-button" onClick={onOpenAdmin}>{t("resources.open_admin")}</button></>}</p>}
   {debug&&(canEdit||isAdmin)&&<p className="field-note">{t("resources.host",{cpus:data.host.cpus,memory:Math.round(data.host.memory_total_mib/1024),disk:Math.floor(data.host.disk_available_gib)})}</p>}
   {data.pending&&<div className="settings-notice resource-pending"><span>{t("resources.pending",{cpus:data.desired.vcpus,memory:data.desired.memory_mib/1024,disk:data.desired.disk_gib})}</span>{!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.restart_apply")}</button>}</div>}
  </>}
  {canEdit&&editing&&data&&desired&&<ResourceSheet data={data} desired={desired} dirty={dirty} busy={busy||restarting} onChange={setDesired} onSave={()=>void save()} onCancel={()=>{setDesired(data.desired);editingRef.current=false;setEditing(false);setActionError("")}}/>}
  {restarting&&<p className="settings-feedback" role="status">{t("resources.restarting")}</p>}
  {!restarting&&status&&<p className="settings-feedback" role="status">{status}</p>}
  {failure&&<div className="resource-read-error" role="alert"><span>{failure}</span>{data?.state==="error"&&!data.pending&&!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.retry_restart")}</button>}</div>}
  {readError&&<div className="resource-read-error" role="alert"><span>{readError}</span><button type="button" className="text-button" disabled={reading||busy} onClick={()=>void refresh()}>{reading?t("resources.reading"):t("resources.retry")}</button></div>}
  {!data&&!readError&&<p className="field-note" role="status">{t("resources.loading")}</p>}
  {confirming&&<RestartConfirmation onCancel={()=>setConfirming(false)} onConfirm={()=>void apply()}/>}
 </section>;
}
