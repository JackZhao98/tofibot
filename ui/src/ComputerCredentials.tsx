import {useCallback,useEffect,useRef,useState} from "react";
import {ApiError,request} from "./api";
import {ConfirmAction,Disclosure} from "./InteractionSystem";
import type {Bot} from "./types";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import "./computer-credentials.css";

type Credential={id:string;name:string;kind:"env"|"ssh";target:string;created_at:string};
type SSHKey={name:string;path:string;public_key:string;fingerprint:string;has_private:boolean;encrypted:boolean;source:"computer";public_key_verified?:boolean};
type Tab="env"|"ssh";
function message(cause:unknown,fallback:string){
 if(!(cause instanceof Error))return fallback;
 const text=cause.message;
 if(/already exists/i.test(text))return "此文件名已存在，请换一个名称。";
 if(/busy|current task/i.test(text))return "电脑正在处理任务，请稍后重试。";
 if(/private key.*parsed|valid private key/i.test(text))return "无法识别私钥，请检查文件格式。";
 if(/private key filename/i.test(text))return "请使用私钥文件名，例如 id_ed25519_work；不要使用 .pub、config 或 known_hosts。";
 if(/SSH directory/i.test(text))return "电脑的 .ssh 目录不可用，请检查目录后重试。";
 if(/OpenSSH tools/i.test(text))return "电脑尚未安装 OpenSSH 工具。";
 if(/SSH key response|read SSH keys|SSH key operation/i.test(text))return "无法读取或配置密钥，请稍后重试。";
 return text;
}

export function ComputerCredentials({bots}:{bots:Bot[]}){
 const active=useSettingsActive();
 const activeRef=useRef(active);activeRef.current=active;
 const [tab,setTab]=useState<Tab>("env");
 const [items,setItems]=useState<Credential[]>([]);const [keys,setKeys]=useState<SSHKey[]>([]);
 const [adding,setAdding]=useState(false);const [mode,setMode]=useState<"import"|"generate">("import");
 const [name,setName]=useState("");const [target,setTarget]=useState("");const [value,setValue]=useState("");
 const [busy,setBusy]=useState(false);const [loading,setLoading]=useState(true);const [error,setError]=useState("");const [loadError,setLoadError]=useState("");const [status,setStatus]=useState("");
 const [syncPending,setSyncPending]=useState(false);const [truncated,setTruncated]=useState(false);
 const [openKey,setOpenKey]=useState<string|null>(null);const [copied,setCopied]=useState<string|null>(null);const [dragging,setDragging]=useState(false);
 const reading=useRef(false);const alive=useRef(false);const generation=useRef(0);const mutation=useRef(false);const fileInput=useRef<HTMLInputElement>(null);const fileRead=useRef(0);const selectPublic=useRef<string|null>(null);
 const botID=bots[0]?.id;
 const refresh=useCallback(async()=>{
  if(mutation.current||reading.current||!activeRef.current)return;
  reading.current=true;
  const current=++generation.current;
  try{
   const credentials=await request<{credentials:Credential[]}>("/api/computer/credentials");
   if(!alive.current||!activeRef.current||current!==generation.current)return;
   const ssh=tab==="ssh"&&botID?await request<{keys:SSHKey[];truncated?:boolean}>(`/api/computer/ssh-keys?bot_id=${encodeURIComponent(botID)}`):null;
   if(!alive.current||current!==generation.current)return;
   setItems(credentials.credentials??[]);if(ssh){setKeys(ssh.keys??[]);setTruncated(Boolean(ssh.truncated))}setLoadError("");setSyncPending(false);
  }catch(cause){if(alive.current&&current===generation.current){if(cause instanceof ApiError&&cause.status===409&&/busy|current task/i.test(cause.message)){setSyncPending(true);setLoadError("")}else{setSyncPending(false);setLoadError(message(cause,"无法读取配置，请重试。"))}}}
  finally{reading.current=false;if(alive.current&&current===generation.current)setLoading(false)}
 },[tab,botID]);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 useEffect(()=>{
  if(!active)return;
  setLoading(true);setLoadError("");void refresh();
  const focus=()=>{if(!document.hidden)void refresh()};
  const timer=window.setInterval(focus,5000);window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);
  return()=>{generation.current++;window.clearInterval(timer);window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus)};
 },[refresh,active]);
 useEffect(()=>()=>{fileRead.current++},[]);
 function clearForm(){fileRead.current++;setAdding(false);setName("");setTarget("");setValue("");setDragging(false)}
 function changeTab(next:Tab){if(busy||next===tab)return false;if(adding&&(name||target||value)){setError("请先保存或放弃当前输入，再切换类型。");return false}clearForm();setTab(next);setError("");setStatus("");setOpenKey(null);setCopied(null);setSyncPending(false);return true}
 async function apply(id:string){if(!botID)throw new Error("请先创建一个 Bot，再配置共享电脑。");await request(`/api/computer/credentials/${encodeURIComponent(id)}/apply`,{method:"POST",body:JSON.stringify({bot_id:botID})})}
 async function mutate(action:()=>Promise<void>){if(mutation.current)return false;mutation.current=true;generation.current++;setBusy(true);setError("");setStatus("");try{await action();return true}catch(cause){setError(message(cause,"操作失败，请重试。"));return false}finally{mutation.current=false;if(alive.current){setBusy(false);void refresh()}}}
 async function save(){if(!target.trim()||(tab==="env"||mode==="import")&&!value||(tab==="ssh"&&!botID)){setError("请填写必填项后重试。");return false}if(tab==="env"?!/^[A-Z_][A-Z0-9_]*$/.test(target):!/^[A-Za-z0-9_.-]+$/.test(target)){setError("名称格式不正确，请检查后重试。");return false}return mutate(async()=>{
  if(tab==="env"){
   const credential=await request<Credential>("/api/computer/credentials",{method:"POST",body:JSON.stringify({name:name.trim()||target.trim(),kind:"env",target:target.trim(),value})});
   clearForm();try{await apply(credential.id);setStatus("已保存并应用。新启动的命令和终端会读取配置。")}catch{setStatus("已保存，尚未应用到电脑。可在列表中重试应用。")}
  }else{
   if(!botID)throw new Error("请先创建一个 Bot，再配置共享电脑。");
   if(mode==="import"&&!/-----BEGIN (?:OPENSSH|RSA|EC|DSA|ENCRYPTED)? ?PRIVATE KEY-----/.test(value))throw new Error("请输入私钥内容；.pub 文件是公钥，不能作为私钥导入。");
   if(new Blob([value]).size>64*1024)throw new Error("私钥不能超过 64 KB。");
   const result=await request<{key:SSHKey}>("/api/computer/ssh-keys",{method:"POST",body:JSON.stringify({bot_id:botID,action:mode,name:target.trim(),...(mode==="import"?{private_key:value}:{})})});
   clearForm();setKeys(current=>[result.key,...current.filter(key=>key.name!==result.key.name)]);setOpenKey(result.key.name);setCopied(null);setStatus(mode==="generate"?"密钥已生成。可复制公钥到目标服务。":"密钥已导入。")
  }
 })}
 useSettingsDraft({label:tab==="env"?"环境变量":"SSH 密钥",dirty:adding&&Boolean(name||target||value),busy,save,discard:()=>{clearForm();setError("")}});
 async function readFile(file:File){const current=++fileRead.current;setError("");if(file.name.endsWith(".pub")){setError("这是公钥文件，请选择对应的私钥文件（通常不带 .pub）。");return}if(file.size>64*1024){setError("请选择小于 64 KB 的私钥文件。");return}try{const text=await file.text();if(!alive.current||fileRead.current!==current)return;if(!/-----BEGIN (?:OPENSSH|RSA|EC|DSA|ENCRYPTED)? ?PRIVATE KEY-----/.test(text)){setError("文件中没有找到私钥，请选择私钥文件或直接粘贴内容。");return}setValue(text);if(!target.trim())setTarget(file.name);setStatus("")}catch{if(fileRead.current===current)setError("无法读取文件，请重试或直接粘贴私钥。")}}
 async function copyKey(key:SSHKey){try{await navigator.clipboard.writeText(key.public_key);setCopied(key.name);setError("")}catch{selectPublic.current=key.name;setOpenKey(key.name);setError("请在下方公钥框内复制（⌘C / Ctrl+C）。")}}
 function savedRows(rows:Credential[]){return rows.map(item=><article className="credential-row" key={item.id}><div className="credential-copy"><strong>{item.name}</strong>{item.name!==item.target&&<p>{item.target}</p>}</div><div className="credential-actions"><button disabled={busy||!botID} onClick={()=>void mutate(async()=>{await apply(item.id);setStatus("已应用到共享电脑。")})}>应用到电脑</button><ConfirmAction label="删除" question="删除保存项？电脑中的副本会保留。" disabled={busy} onConfirm={async()=>{await mutate(async()=>{await request(`/api/computer/credentials/${encodeURIComponent(item.id)}`,{method:"DELETE"});setStatus("已删除保存项，电脑中的副本保留。")})}}/></div></article>)}
 const legacy=items.filter(item=>item.kind==="ssh");
 return <section className="computer-credentials">
   <div className="credential-tabs" role="tablist" aria-label="凭据类型">{([['env','环境变量'],['ssh','SSH Key']] as const).map(([id,label])=><button key={id} type="button" role="tab" id={`credential-tab-${id}`} aria-selected={tab===id} aria-controls="credential-panel" tabIndex={tab===id?0:-1} disabled={busy} onClick={()=>changeTab(id)} onKeyDown={event=>{if(['ArrowLeft','ArrowRight','Home','End'].includes(event.key)){event.preventDefault();const next=event.key==='Home'?'env':event.key==='End'?'ssh':tab==='env'?'ssh':'env';if(changeTab(next))document.getElementById(`credential-tab-${next}`)?.focus()}}}>{label}</button>)}</div>
  <div id="credential-panel" role="tabpanel" aria-labelledby={`credential-tab-${tab}`}>
   <div className="credential-toolbar"><span className="field-note">{tab==="env"?"所有 Bot 共用，新启动的命令生效。":"共享电脑中的密钥，自动同步列表。"}</span><button className="secondary-button" disabled={busy} onClick={()=>{if(adding)clearForm();else{setAdding(true);setError("");setStatus("")}}}>{adding?"取消":tab==="env"?"添加变量":"添加密钥"}</button></div>
   {adding&&<form aria-busy={busy} className="credential-form" onSubmit={event=>{event.preventDefault();void save()}}>
    {tab==="ssh"&&<div className="settings-segment" role="group" aria-label="添加密钥方式"><button type="button" aria-pressed={mode==="import"} disabled={busy} onClick={()=>{setMode("import");setValue("");fileRead.current++}}>导入密钥</button><button type="button" aria-pressed={mode==="generate"} disabled={busy} onClick={()=>{setMode("generate");setValue("");fileRead.current++}}>生成新密钥</button></div>}
    {tab==="env"&&<label>显示名称（可选）<input value={name} disabled={busy} onChange={event=>setName(event.target.value)} placeholder="例如：工作账号" autoComplete="off"/></label>}
    <label>{tab==="env"?"变量名":"密钥文件名"}<input required value={target} disabled={busy} onChange={event=>setTarget(event.target.value)} placeholder={tab==="env"?"MY_API_KEY":"id_ed25519_work"} pattern={tab==="env"?"[A-Z_][A-Z0-9_]*":"[A-Za-z0-9_.-]+"} autoComplete="off" spellCheck={false}/></label>
    {tab==="env"?<label>变量值<input required type="password" value={value} disabled={busy} onChange={event=>setValue(event.target.value)} autoComplete="new-password"/></label>:mode==="import"?<div className={`credential-dropzone ${dragging?'is-dragging':''}`} onDragOver={event=>{event.preventDefault();if(!busy)setDragging(true)}} onDragLeave={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))setDragging(false)}} onDrop={event=>{event.preventDefault();setDragging(false);if(!busy){if(event.dataTransfer.files.length!==1)setError("请一次只拖入一个私钥文件。");else void readFile(event.dataTransfer.files[0])}}}><label>私钥内容<textarea required value={value} disabled={busy} onChange={event=>{fileRead.current++;setValue(event.target.value)}} rows={5} autoComplete="off" spellCheck={false} placeholder="粘贴私钥，或将文件拖到这里"/></label><input ref={fileInput} type="file" hidden onChange={event=>{const file=event.target.files?.[0];event.target.value='';if(file)void readFile(file)}}/><button type="button" className="text-button" disabled={busy} onClick={()=>fileInput.current?.click()}>选择文件</button><span className="field-note">私钥不会发到聊天。加密私钥需在电脑中解锁。</span></div>:<p className="field-note">生成 Ed25519 密钥，私钥保留在共享电脑。不会覆盖已有文件。</p>}
    <button className="primary-button" disabled={busy||!target.trim()||(tab==="env"||mode==="import")&&!value||(tab==="ssh"&&!botID)}>{busy?"处理中…":tab==="env"?"保存并应用":mode==="import"?"导入密钥":"生成密钥"}</button>
   </form>}
   {loading&&<p className="field-note" role="status">正在读取…</p>}
   {tab==="env"?<div className="credential-list">{savedRows(items.filter(item=>item.kind==="env"))}{!loading&&!loadError&&!items.some(item=>item.kind==="env")&&!adding&&<div className="settings-empty">暂无环境变量</div>}</div>:<><div className="credential-list">{keys.map(key=><article className="ssh-key-row" key={key.name}><div className="credential-row"><div className="credential-copy"><strong>{key.name}</strong><p>{!key.has_private?`仅公钥${key.fingerprint?` · ${key.fingerprint}`:""}`:key.encrypted&&!key.public_key?"加密私钥，未找到公钥":(key.public_key&&key.public_key_verified===false?"公钥与私钥的匹配未验证 · ":"")+(key.fingerprint||"未找到公钥")}</p></div>{key.public_key&&<div className="credential-actions"><button aria-expanded={openKey===key.name} onClick={()=>setOpenKey(openKey===key.name?null:key.name)}>{openKey===key.name?"收起公钥":"查看公钥"}</button><button onClick={()=>void copyKey(key)}>{copied===key.name?"已复制":"复制公钥"}</button></div>}</div>{openKey===key.name&&key.public_key&&<textarea ref={element=>{if(element&&selectPublic.current===key.name){element.focus();element.select();selectPublic.current=null}}} className="ssh-public-key" aria-label={`${key.name} 公钥`} readOnly value={key.public_key} rows={3} onFocus={event=>event.target.select()}/>}</article>)}</div>{!botID?<p className="field-note">创建一个 Bot 后，可读取共享电脑中的密钥。</p>:!loading&&!loadError&&!syncPending&&!keys.length&&!adding&&<div className="settings-empty">电脑中暂无 SSH Key</div>}{legacy.length>0&&<Disclosure title="已保存的密钥"><p className="field-note">以前保存的配置，可应用到电脑。</p>{savedRows(legacy)}</Disclosure>}</>}
   {tab==="ssh"&&syncPending&&<p className="field-note" role="status">电脑暂忙，稍后自动同步。</p>}{tab==="ssh"&&truncated&&<p className="field-note">密钥较多，当前仅显示部分结果。</p>}
   {status&&<p className="settings-feedback" role="status">{status}</p>}
   {error&&<p className="error-text" role="alert">{error}</p>}
   {loadError&&<div className="credential-load-error" role="alert"><span>{loadError}</span><button className="text-button" disabled={busy} onClick={()=>void refresh()}>重试</button></div>}
  </div>
 </section>
}
