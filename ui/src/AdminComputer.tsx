import {useEffect, useRef, useState} from "react";

export type ComputerStatus = {
 account_id:string; computer_id:string; generation:string; state:string;
 operation_id?:string; phase:string; error:string; supported:boolean;
 resources_released:boolean; slot:number; quota_bytes:number;
};
export type ComputerAccount = {id:string; username:string; disabled:boolean};
export type ComputerConfirmation = {
 operation_id:string; expected_generation:string; confirm_computer_id:string;
 confirm_account_id:string; confirm_username:string; acknowledge_data_loss:boolean;
 quota_gib?:number;
};
export function AdminComputer({account, status, allocatedBytes, busy, run}:{
 account:ComputerAccount; status:ComputerStatus; allocatedBytes?:number; busy:boolean;
 run:(path:string, method:string, body:ComputerConfirmation)=>Promise<void>;
}) {
 const [confirm,setConfirm]=useState<"delete"|"recreate"|null>(null);
 const [typed,setTyped]=useState("");
 const [accepted,setAccepted]=useState(false);
 const [quota,setQuota]=useState(8);
 const [submitting,setSubmitting]=useState(false);
 const dialog=useRef<HTMLDialogElement>(null);
 const trigger=useRef<HTMLButtonElement>(null);
 useEffect(()=>{
  if(confirm){dialog.current?.showModal()}
  else if(dialog.current?.open){dialog.current.close();trigger.current?.focus()}
 },[confirm]);
 function closeConfirmation(){dialog.current?.close();setConfirm(null);trigger.current?.focus()}
 function openConfirmation(next:"delete"|"recreate"){
  setTyped("");setAccepted(false);
  if(next==="recreate")setQuota(status.quota_bytes?status.quota_bytes/2**30:8);
  setConfirm(next);
 }
 const pending=status.state==="deleting"||status.state==="cleanup_failed";
 const recreating=status.state==="recreating";
 const deleted=status.state==="deleted"&&status.resources_released;
 const available=status.supported&&Boolean(status.generation);
 const label=deleted?"已删除，资源已释放":pending?"删除尚未完成，预留容量仍保留":recreating?"重建尚未确认完成":status.state==="active"?"云电脑可用":"电脑状态未验证";
 return <div className="admin-computer">
  <p role="status">{label}{status.phase?` · ${status.phase}`:""}</p>
  {status.error&&<p className="admin-intro">{status.error}</p>}
  {available&&<small>电脑 {status.computer_id} · 版本 {status.generation}{status.slot?` · 槽位 ${status.slot}`:""}</small>}
  {available&&<button ref={trigger} type="button" className={deleted||recreating?"":"admin-danger"} disabled={busy||submitting||(deleted&&account.disabled)} onClick={()=>openConfirmation(deleted||recreating?"recreate":"delete")}>
   {deleted?"重新创建空白电脑":recreating?"重试原重建操作":pending?"重试原删除操作":"删除云电脑并释放资源"}
  </button>}
  {deleted&&account.disabled&&<p className="admin-intro">先恢复账号，再明确重新创建电脑；恢复账号不会自动创建电脑。</p>}
  <dialog ref={dialog} className="admin-computer-dialog" aria-labelledby={`computer-confirm-${account.id}`} onCancel={event=>{event.preventDefault();if(!busy&&!submitting)closeConfirmation()}}>
   <form onSubmit={event=>{
    event.preventDefault();
    if(!confirm||busy||submitting||!accepted||typed!==status.computer_id)return;
    setSubmitting(true);
    const retry=pending||recreating;
    const body:ComputerConfirmation={operation_id:retry&&status.operation_id?status.operation_id:crypto.randomUUID(),expected_generation:status.generation,confirm_computer_id:status.computer_id,confirm_account_id:account.id,confirm_username:account.username,acknowledge_data_loss:true};
    if(confirm==="recreate")body.quota_gib=quota;
    const method=confirm==="recreate"?"POST":"DELETE";
    const path=`/api/admin/accounts/${account.id}/computer${confirm==="recreate"?"/recreate":""}`;
    void run(path,method,body).finally(()=>{closeConfirmation();setSubmitting(false)});
   }}>
    <h3 id={`computer-confirm-${account.id}`}>{confirm==="recreate"?"重新创建空白云电脑":"永久删除云电脑"}</h3>
    <p>账号：<strong>{account.username}</strong>（{account.id}）</p>
    <p>电脑：<strong>{status.computer_id}</strong></p>
    {confirm==="delete"?<>
     <p>将停止这台电脑，永久删除其中的工作区文件、应用、浏览器登录状态、工具和插件数据（包括电脑内的授权信息）、保存在云电脑中的聊天附件，以及该电脑目录内的恢复备份。此操作无法撤销。</p>
     <p>保留账号和登录权限、聊天记录及附件条目、服务端凭据；删除的附件内容将无法打开。电脑目录之外的备份和其他账号的数据会保留。</p>
     <p>已预留 {(status.quota_bytes/2**30).toFixed(1)} GiB；{allocatedBytes===undefined?"实际占用尚未测得":`工作区磁盘实际占用 ${(allocatedBytes/2**30).toFixed(2)} GiB（恢复备份另计）`}。只有验证清理成功后才释放磁盘预留和槽位。以后必须明确重新创建。</p>
    </>:<><p>将重新预留容量并创建空白电脑，原电脑的文件和附件不会恢复。</p><label>新电脑容量（GiB）<input type="number" min={8} max={1024} step={1} value={quota} disabled={busy} onChange={event=>setQuota(Number(event.target.value))}/></label></>}
    <label className="admin-confirm-check"><input type="checkbox" checked={accepted} disabled={busy} onChange={event=>setAccepted(event.target.checked)}/>我已确认所选账号、电脑和上述数据影响</label>
    <label>输入完整电脑 ID 以确认<input value={typed} autoComplete="off" disabled={busy} onChange={event=>setTyped(event.target.value)}/></label>
    <div className="admin-account-actions"><button type="button" disabled={busy||submitting} onClick={closeConfirmation}>取消</button><button type="submit" className="admin-danger" disabled={busy||submitting||!accepted||typed!==status.computer_id||(confirm==="recreate"&&(!Number.isInteger(quota)||quota<8||quota>1024))}>{busy||submitting?"正在验证清理…":confirm==="recreate"?"确认创建空白电脑":"确认永久删除并释放资源"}</button></div>
   </form>
  </dialog>
 </div>;
}
