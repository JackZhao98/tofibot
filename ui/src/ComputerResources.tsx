import {useCallback,useEffect,useRef,useState} from "react";
import {useDebugMode} from "./debugMode";
import {ApiError,request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import "./computer-resources.css";

type Allocation={vcpus:number;memory_mib:number;disk_gib:number};
type Resources={current:Allocation;desired:Allocation;pending:boolean;state:string;error?:string;host:{cpus:number;memory_total_mib:number;memory_available_mib:number;disk_available_gib:number};limits:{max_vcpus:number;max_memory_mib:number;max_disk_gib:number}};
const resourceError=(cause:unknown,fallback:string)=>cause instanceof Error?cause.message:fallback;

function RestartConfirmation({onCancel,onConfirm}:{onCancel:()=>void;onConfirm:()=>void}) {
 const dialog=useRef<HTMLDialogElement>(null);
 useEffect(()=>{const element=dialog.current;element?.showModal();return()=>element?.close()},[]);
 return <dialog ref={dialog} className="resource-restart-dialog" aria-labelledby="resource-restart-title" aria-describedby="resource-restart-impact" onCancel={event=>{event.preventDefault();onCancel()}} onKeyDown={event=>{if(event.key==="Escape"){event.preventDefault();event.stopPropagation();onCancel()}}}>
  <h3 id="resource-restart-title">重启并应用配置？</h3>
  <p id="resource-restart-impact">请先停止正在执行的 Bot 任务。重启会中断所有 Bot 的桌面和终端，其中运行的进程也会停止；工作文件会保留。</p>
  <div className="resource-restart-actions"><button type="button" className="secondary-button" autoFocus onClick={onCancel}>取消</button><button type="button" className="primary-button" onClick={onConfirm}>确认重启</button></div>
 </dialog>;
}

export function ComputerResources(){
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
    applyingRef.current=false;setApplying(false);setStatus("");setActionError(next.error||"重启未完成，请重试。");
   }else if(applyingRef.current&&next.state==="ready"&&!next.pending){
    applyingRef.current=false;setApplying(false);setActionError("");setStatus("配置已应用。");
   }
  }catch(cause){
   if(alive.current&&!controller.signal.aborted&&version===readVersion.current)setReadError(resourceError(cause,"暂时无法读取电脑资源，请重试。"));
  }finally{if(alive.current&&version===readVersion.current)setReading(false)}
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
  if(![desired.vcpus,desired.memory_mib,desired.disk_gib].every(Number.isFinite)||!Number.isInteger(desired.vcpus)||desired.vcpus<1||desired.vcpus>data.limits.max_vcpus||desired.memory_mib<512||desired.memory_mib>data.limits.max_memory_mib||desired.disk_gib<data.current.disk_gib||desired.disk_gib>data.limits.max_disk_gib){setActionError("资源配置超出允许范围，请检查后重试。");return false}
  beginMutation();
  try{
   const next=await request<Resources>("/api/computer/resources",{method:"PUT",body:JSON.stringify(desired)});
   if(!alive.current)return false;
   setData(next);setDesired(next.desired);editingRef.current=false;setEditing(false);setReadError("");setStatus(next.pending?"配置已保存，重启后应用。":"配置已保存。");
   return true;
  }catch(cause){if(alive.current)setActionError(resourceError(cause,"保存失败，请重试。"));return false}
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
   if(alive.current)setActionError(cause instanceof ApiError&&cause.status===409?"电脑正在执行任务或应用配置。请先停止任务，稍后重试。":resourceError(cause,"重启请求未完成，请重试。"));
  }finally{
   mutating.current=false;
   if(alive.current){setBusy(false);void refresh();restartButton.current?.focus()}
  }
 }
 const restarting=applying||data?.state==="restarting"||(Boolean(data?.pending)&&data?.state==="starting");
 const failure=actionError||data?.error||(!restarting&&data?.state==="error"?"电脑启动失败，请重试。":"");
 const dirty=Boolean(editing&&data&&desired&&(desired.vcpus!==data.desired.vcpus||desired.memory_mib!==data.desired.memory_mib||desired.disk_gib!==data.desired.disk_gib));
 useSettingsDraft({label:"电脑资源",dirty,busy,save,discard:()=>{if(data)setDesired(data.desired);editingRef.current=false;setEditing(false);setActionError("")}});
 return <section className="settings-section">
  <div className="settings-row"><h3>资源</h3>{data&&<button type="button" className="secondary-button" disabled={busy||restarting} onClick={()=>{const next=!editing;editingRef.current=next;setDesired(data.desired);setEditing(next);setActionError("")}}>{editing?"取消":"调整配置"}</button>}</div>
  {data&&<>
   <div className="resource-grid"><div><span>CPU</span><strong>{data.current.vcpus}<small> 核</small></strong></div><div><span>内存</span><strong>{Number((data.current.memory_mib/1024).toFixed(1))}<small> GB</small></strong></div><div><span>已分配磁盘</span><strong>{data.current.disk_gib}<small> GB</small></strong></div></div>
   {debug&&<p className="field-note">主机：{data.host.cpus} 核 · {Math.round(data.host.memory_total_mib/1024)} GB 内存，磁盘剩余 {Math.floor(data.host.disk_available_gib)} GB。</p>}
   {data.pending&&<div className="settings-notice resource-pending"><span>待应用：{data.desired.vcpus} 核 · {data.desired.memory_mib/1024} GB 内存 · {data.desired.disk_gib} GB 磁盘。</span>{!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>重启并应用</button>}</div>}
  </>}
  {editing&&data&&desired&&<form aria-busy={busy} className="resource-form" onSubmit={event=>{event.preventDefault();void save()}}>
   <fieldset className="resource-fields" disabled={busy||restarting}>
    <label>CPU 核数<input required type="number" min="1" max={data.limits.max_vcpus} value={desired.vcpus} onChange={event=>setDesired({...desired,vcpus:Number(event.target.value)})}/></label>
    <label>内存（GB）<input required type="number" min="0.5" step="0.5" max={data.limits.max_memory_mib/1024} value={desired.memory_mib/1024} onChange={event=>setDesired({...desired,memory_mib:Number(event.target.value)*1024})}/></label>
    <label>磁盘（GB）<input required type="number" min={data.current.disk_gib} max={data.limits.max_disk_gib} value={desired.disk_gib} onChange={event=>setDesired({...desired,disk_gib:Number(event.target.value)})}/></label>
    <p className="field-note">磁盘只扩容、不缩小。保存后需重启电脑才能应用，关闭桌面不会应用配置。</p>
   </fieldset>
  </form>}
  {restarting&&<p className="settings-feedback" role="status">正在重启并应用配置…</p>}
  {!restarting&&status&&<p className="settings-feedback" role="status">{status}</p>}
  {failure&&<div className="resource-read-error" role="alert"><span>{failure}</span>{data?.state==="error"&&!data.pending&&!restarting&&<button ref={restartButton} type="button" className="secondary-button" disabled={busy||editing} onClick={()=>setConfirming(true)}>重试重启</button>}</div>}
  {readError&&<div className="resource-read-error" role="alert"><span>{readError}</span><button type="button" className="text-button" disabled={reading||busy} onClick={()=>void refresh()}>{reading?"读取中…":"重试"}</button></div>}
  {!data&&!readError&&<p className="field-note" role="status">正在读取资源…</p>}
  {confirming&&<RestartConfirmation onCancel={()=>setConfirming(false)} onConfirm={()=>void apply()}/>}
 </section>;
}
