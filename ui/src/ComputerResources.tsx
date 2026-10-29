import {useCallback,useEffect,useRef,useState,type CSSProperties} from "react";
import {useDebugMode} from "./debugMode";
import {ApiError,request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
import "./computer-resources.css";
import {ConfirmDialog} from "./overlays/ModalDialog";

type Allocation={vcpus:number;memory_mib:number;disk_gib:number};
type Resources={current:Allocation;desired:Allocation;pending:boolean;state:string;error?:string;host:{cpus:number;memory_total_mib:number;memory_available_mib:number;disk_available_gib:number};limits:{max_vcpus:number;max_memory_mib:number;max_disk_gib:number}};
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


const GB=1024;
type FieldKey=keyof Allocation;
function validAllocation(a:Allocation,data:Resources){return [a.vcpus,a.memory_mib,a.disk_gib].every(Number.isFinite)&&Number.isInteger(a.vcpus)&&a.vcpus>=1&&a.vcpus<=data.limits.max_vcpus&&a.memory_mib>=512&&a.memory_mib<=data.limits.max_memory_mib&&a.disk_gib>=data.current.disk_gib&&a.disk_gib<=data.limits.max_disk_gib}
const gbText=(mib:number)=>String(Number((mib/GB).toFixed(1)));

/** One resource: current value, the new value, a slider with -/+ steppers, and what the host can give. */
function ResourceField({id,label,unit,current,value,min,max,step,hint,disabled,format,onChange}:{id:string;label:string;unit:string;current:number;value:number;min:number;max:number;step:number;hint:string;disabled:boolean;format:(n:number)=>string;onChange:(n:number)=>void}) {
 const {t}=useTranslation("computer");
 const clamp=(n:number)=>Math.min(max,Math.max(min,n));
 const changed=value!==current;
 const pct=max>min?((value-min)/(max-min))*100:100;
 return <div className={`resource-field${changed?" is-changed":""}`} role="group" aria-labelledby={id+"-label"}>
  <div className="resource-field-head">
   <span id={id+"-label"} className="resource-field-label">{label}</span>
   <span className="resource-field-values"><span className="resource-field-was">{format(current)}</span>{changed&&<><span aria-hidden="true" className="resource-field-arrow">→</span><strong className="resource-field-now">{format(value)}</strong></>}<small>{unit}</small></span>
  </div>
  <div className="resource-field-control">
   <button type="button" className="resource-step" aria-label={t("resources.decrease",{field:label})} disabled={disabled||value<=min} onClick={()=>onChange(clamp(value-step))}>−</button>
   <input id={id} type="range" aria-labelledby={id+"-label"} min={min} max={max} step={step} value={value} disabled={disabled||max<=min} style={{"--fill":pct+"%"} as CSSProperties} onChange={event=>onChange(clamp(Number(event.target.value)))}/>
   <button type="button" className="resource-step" aria-label={t("resources.increase",{field:label})} disabled={disabled||value>=max} onClick={()=>onChange(clamp(value+step))}>+</button>
  </div>
  <div className="resource-field-foot"><span>{format(min)}</span><span className="resource-field-hint">{hint}</span><span>{format(max)}</span></div>
 </div>;
}

function ResourceSheet({data,desired,dirty,busy,onChange,onSave,onCancel}:{data:Resources;desired:Allocation;dirty:boolean;busy:boolean;onChange:(a:Allocation)=>void;onSave:()=>void;onCancel:()=>void}) {
 const {t}=useTranslation("computer");
 const valid=validAllocation(desired,data);
 const set=(key:FieldKey)=>(n:number)=>onChange({...desired,[key]:n});
 const lines:string[]=[];
 if(desired.vcpus!==data.desired.vcpus)lines.push(`CPU ${data.desired.vcpus} → ${desired.vcpus} ${t("resources.unit.cores")}`);
 if(desired.memory_mib!==data.desired.memory_mib)lines.push(`${t("resources.memory")} ${gbText(data.desired.memory_mib)} → ${gbText(desired.memory_mib)} GB`);
 if(desired.disk_gib!==data.desired.disk_gib)lines.push(`${t("resources.disk_allocated")} ${data.desired.disk_gib} → ${desired.disk_gib} GB`);
 return <form aria-busy={busy} className="resource-sheet" onSubmit={event=>{event.preventDefault();if(valid&&dirty&&!busy)onSave()}}>
  <fieldset className="resource-fields" disabled={busy}>
   <ResourceField id="resource-cpus" label={t("resources.field.cpus")} unit={t("resources.unit.cores")} current={data.desired.vcpus} value={desired.vcpus} min={1} max={data.limits.max_vcpus} step={1} format={String} disabled={busy} hint={t("resources.host_cpus",{cpus:data.host.cpus})} onChange={set("vcpus")}/>
   <ResourceField id="resource-memory" label={t("resources.field.memory")} unit="GB" current={data.desired.memory_mib} value={desired.memory_mib} min={512} max={Math.max(512,data.limits.max_memory_mib)} step={512} format={gbText} disabled={busy} hint={t("resources.host_memory",{value:gbText(data.host.memory_available_mib)})} onChange={set("memory_mib")}/>
   <ResourceField id="resource-disk" label={t("resources.field.disk")} unit="GB" current={data.desired.disk_gib} value={desired.disk_gib} min={data.current.disk_gib} max={Math.max(data.current.disk_gib,data.limits.max_disk_gib)} step={1} format={String} disabled={busy} hint={t("resources.host_disk",{value:Math.floor(data.host.disk_available_gib)})} onChange={set("disk_gib")}/>
  </fieldset>
  <div className="resource-summary" aria-live="polite">
   {!valid?<p className="resource-summary-error" role="alert">{t("resources.error.out_of_range")}</p>:dirty?<><strong>{t("resources.summary.title")}</strong><ul>{lines.map(line=><li key={line}>{line}</li>)}</ul><p>{t("resources.summary.restart")}</p></>:<p>{t("resources.summary.none")}</p>}
   <p className="resource-summary-note">{t("resources.field_note")}</p>
  </div>
  <div className="resource-sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("resources.cancel")}</button><button type="submit" className="primary-button" disabled={busy||!valid||!dirty}>{busy?t("resources.saving"):t("resources.save")}</button></div>
 </form>;
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
  <div className="settings-row"><h3>{t("resources.title")}</h3>{data&&<button type="button" className="secondary-button" disabled={busy||restarting} onClick={()=>{const next=!editing;editingRef.current=next;setDesired(data.desired);setEditing(next);setActionError("")}}>{editing?t("resources.cancel"):t("resources.adjust")}</button>}</div>
  {data&&<>
   <div className="resource-grid"><div><span>CPU</span><strong>{data.current.vcpus}<small> {t("resources.unit.cores")}</small></strong></div><div><span>{t("resources.memory")}</span><strong>{Number((data.current.memory_mib/1024).toFixed(1))}<small> GB</small></strong></div><div><span>{t("resources.disk_allocated")}</span><strong>{data.current.disk_gib}<small> GB</small></strong></div></div>
   {debug&&<p className="field-note">{t("resources.host",{cpus:data.host.cpus,memory:Math.round(data.host.memory_total_mib/1024),disk:Math.floor(data.host.disk_available_gib)})}</p>}
   {data.pending&&<div className="settings-notice resource-pending"><span>{t("resources.pending",{cpus:data.desired.vcpus,memory:data.desired.memory_mib/1024,disk:data.desired.disk_gib})}</span>{!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.restart_apply")}</button>}</div>}
  </>}
  {editing&&data&&desired&&<ResourceSheet data={data} desired={desired} dirty={dirty} busy={busy||restarting} onChange={setDesired} onSave={()=>void save()} onCancel={()=>{setDesired(data.desired);editingRef.current=false;setEditing(false);setActionError("")}}/>}
  {restarting&&<p className="settings-feedback" role="status">{t("resources.restarting")}</p>}
  {!restarting&&status&&<p className="settings-feedback" role="status">{status}</p>}
  {failure&&<div className="resource-read-error" role="alert"><span>{failure}</span>{data?.state==="error"&&!data.pending&&!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>{t("resources.retry_restart")}</button>}</div>}
  {readError&&<div className="resource-read-error" role="alert"><span>{readError}</span><button type="button" className="text-button" disabled={reading||busy} onClick={()=>void refresh()}>{reading?t("resources.reading"):t("resources.retry")}</button></div>}
  {!data&&!readError&&<p className="field-note" role="status">{t("resources.loading")}</p>}
  {confirming&&<RestartConfirmation onCancel={()=>setConfirming(false)} onConfirm={()=>void apply()}/>}
 </section>;
}
