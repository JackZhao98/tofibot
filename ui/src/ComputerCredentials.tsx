import {useCallback,useEffect,useRef,useState} from "react";
import { CATS, EmptyState } from "./EmptyCat";
import {ApiError,request} from "./api";
import {ConfirmAction,Disclosure} from "./InteractionSystem";
import type {Bot} from "./types";
import {useSettingsActive} from "./settingsActivity";
import {useSettingsDraft} from "./settingsDraft";
import {i18n,useTranslation} from "./i18n";
import "./computer-credentials.css";

type Credential={id:string;name:string;kind:"env"|"ssh";target:string;created_at:string};
type SSHKey={name:string;path:string;public_key:string;fingerprint:string;has_private:boolean;encrypted:boolean;source:"computer";public_key_verified?:boolean};
type Tab="env"|"ssh";
function message(cause:unknown,fallback:string){
 if(!(cause instanceof Error))return fallback;
 const text=cause.message;
 if(/already exists/i.test(text))return i18n.t("computer:credentials.error.exists");
 if(/busy|current task/i.test(text))return i18n.t("computer:credentials.error.busy");
 if(/private key.*parsed|valid private key/i.test(text))return i18n.t("computer:credentials.error.private_key_invalid");
 if(/private key filename/i.test(text))return i18n.t("computer:credentials.error.private_key_filename");
 if(/SSH directory/i.test(text))return i18n.t("computer:credentials.error.ssh_dir");
 if(/OpenSSH tools/i.test(text))return i18n.t("computer:credentials.error.openssh_missing");
 if(/SSH key response|read SSH keys|SSH key operation/i.test(text))return i18n.t("computer:credentials.error.key_io");
 return text;
}

export function ComputerCredentials({bots}:{bots:Bot[]}){
 const {t}=useTranslation("computer");
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
  }catch(cause){if(alive.current&&current===generation.current){if(cause instanceof ApiError&&cause.status===409&&/busy|current task/i.test(cause.message)){setSyncPending(true);setLoadError("")}else{setSyncPending(false);setLoadError(message(cause,t("credentials.error.load")))}}}
  finally{reading.current=false;if(alive.current&&current===generation.current)setLoading(false)}
 },[tab,botID,t]);
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
 function changeTab(next:Tab){if(busy||next===tab)return false;if(adding&&(name||target||value)){setError(t("credentials.error.switch_unsaved"));return false}clearForm();setTab(next);setError("");setStatus("");setOpenKey(null);setCopied(null);setSyncPending(false);return true}
 async function apply(id:string){if(!botID)throw new Error(t("credentials.error.no_bot"));await request(`/api/computer/credentials/${encodeURIComponent(id)}/apply`,{method:"POST",body:JSON.stringify({bot_id:botID})})}
 async function mutate(action:()=>Promise<void>){if(mutation.current)return false;mutation.current=true;generation.current++;setBusy(true);setError("");setStatus("");try{await action();return true}catch(cause){setError(message(cause,t("credentials.error.operation")));return false}finally{mutation.current=false;if(alive.current){setBusy(false);void refresh()}}}
 async function save(){if(!target.trim()||(tab==="env"||mode==="import")&&!value||(tab==="ssh"&&!botID)){setError(t("credentials.error.required"));return false}if(tab==="env"?!/^[A-Z_][A-Z0-9_]*$/.test(target):!/^[A-Za-z0-9_.-]+$/.test(target)){setError(t("credentials.error.name_format"));return false}return mutate(async()=>{
  if(tab==="env"){
   const credential=await request<Credential>("/api/computer/credentials",{method:"POST",body:JSON.stringify({name:name.trim()||target.trim(),kind:"env",target:target.trim(),value})});
   clearForm();try{await apply(credential.id);setStatus(t("credentials.status.saved_applied"))}catch{setStatus(t("credentials.status.saved_not_applied"))}
  }else{
   if(!botID)throw new Error(t("credentials.error.no_bot"));
   if(mode==="import"&&!/-----BEGIN (?:OPENSSH|RSA|EC|DSA|ENCRYPTED)? ?PRIVATE KEY-----/.test(value))throw new Error(t("credentials.error.not_private_key"));
   if(new Blob([value]).size>64*1024)throw new Error(t("credentials.error.too_large"));
   const result=await request<{key:SSHKey}>("/api/computer/ssh-keys",{method:"POST",body:JSON.stringify({bot_id:botID,action:mode,name:target.trim(),...(mode==="import"?{private_key:value}:{})})});
   clearForm();setKeys(current=>[result.key,...current.filter(key=>key.name!==result.key.name)]);setOpenKey(result.key.name);setCopied(null);setStatus(mode==="generate"?t("credentials.status.generated"):t("credentials.status.imported"))
  }
 })}
 useSettingsDraft({label:tab==="env"?t("credentials.tab.env"):t("credentials.draft.ssh"),dirty:adding&&Boolean(name||target||value),busy,save,discard:()=>{clearForm();setError("")}});
 async function readFile(file:File){const current=++fileRead.current;setError("");if(file.name.endsWith(".pub")){setError(t("credentials.error.public_key_file"));return}if(file.size>64*1024){setError(t("credentials.error.file_too_large"));return}try{const text=await file.text();if(!alive.current||fileRead.current!==current)return;if(!/-----BEGIN (?:OPENSSH|RSA|EC|DSA|ENCRYPTED)? ?PRIVATE KEY-----/.test(text)){setError(t("credentials.error.no_private_key_in_file"));return}setValue(text);if(!target.trim())setTarget(file.name);setStatus("")}catch{if(fileRead.current===current)setError(t("credentials.error.read_file"))}}
 async function copyKey(key:SSHKey){try{await navigator.clipboard.writeText(key.public_key);setCopied(key.name);setError("")}catch{selectPublic.current=key.name;setOpenKey(key.name);setError(t("credentials.error.copy_manual"))}}
 function savedRows(rows:Credential[]){return rows.map(item=><article className="credential-row" key={item.id}><div className="credential-copy"><strong>{item.name}</strong>{item.name!==item.target&&<p>{item.target}</p>}</div><div className="credential-actions"><button className="text-button" disabled={busy||!botID} onClick={()=>void mutate(async()=>{await apply(item.id);setStatus(t("credentials.status.applied"))})}>{t("credentials.apply")}</button><ConfirmAction label={t("credentials.delete")} question={t("credentials.delete_question")} disabled={busy} onConfirm={async()=>{await mutate(async()=>{await request(`/api/computer/credentials/${encodeURIComponent(item.id)}`,{method:"DELETE"});setStatus(t("credentials.status.deleted"))})}}/></div></article>)}
 const legacy=items.filter(item=>item.kind==="ssh");
 return <section className="computer-credentials">
   <div className="credential-tabs" role="tablist" aria-label={t("credentials.tabs_aria")}>{([['env',t("credentials.tab.env")],['ssh',t("credentials.tab.ssh")]] as const).map(([id,label])=><button key={id} type="button" role="tab" id={`credential-tab-${id}`} aria-selected={tab===id} aria-controls="credential-panel" tabIndex={tab===id?0:-1} disabled={busy} onClick={()=>changeTab(id)} onKeyDown={event=>{if(['ArrowLeft','ArrowRight','Home','End'].includes(event.key)){event.preventDefault();const next=event.key==='Home'?'env':event.key==='End'?'ssh':tab==='env'?'ssh':'env';if(changeTab(next))document.getElementById(`credential-tab-${next}`)?.focus()}}}>{label}</button>)}</div>
  <div id="credential-panel" role="tabpanel" aria-labelledby={`credential-tab-${tab}`}>
   <div className="credential-toolbar"><span className="field-note">{tab==="env"?t("credentials.note.env"):t("credentials.note.ssh")}</span><button className="secondary-button" disabled={busy} onClick={()=>{if(adding)clearForm();else{setAdding(true);setError("");setStatus("")}}}>{adding?t("credentials.cancel"):tab==="env"?t("credentials.add_env"):t("credentials.add_key")}</button></div>
   {adding&&<form aria-busy={busy} className="credential-form" onSubmit={event=>{event.preventDefault();void save()}}>
    {tab==="ssh"&&<div className="settings-segment" role="group" aria-label={t("credentials.mode_aria")}><button type="button" aria-pressed={mode==="import"} disabled={busy} onClick={()=>{setMode("import");setValue("");fileRead.current++}}>{t("credentials.mode.import")}</button><button type="button" aria-pressed={mode==="generate"} disabled={busy} onClick={()=>{setMode("generate");setValue("");fileRead.current++}}>{t("credentials.mode.generate")}</button></div>}
    {tab==="env"&&<label>{t("credentials.field.display_name")}<input value={name} disabled={busy} onChange={event=>setName(event.target.value)} placeholder={t("credentials.field.display_name_placeholder")} autoComplete="off"/></label>}
    <label>{tab==="env"?t("credentials.field.env_name"):t("credentials.field.key_file")}<input required value={target} disabled={busy} onChange={event=>setTarget(event.target.value)} placeholder={tab==="env"?"MY_API_KEY":"id_ed25519_work"} pattern={tab==="env"?"[A-Z_][A-Z0-9_]*":"[A-Za-z0-9_.-]+"} autoComplete="off" spellCheck={false}/></label>
    {tab==="env"?<label>{t("credentials.field.env_value")}<input required type="password" value={value} disabled={busy} onChange={event=>setValue(event.target.value)} autoComplete="new-password"/></label>:mode==="import"?<div className={`credential-dropzone ${dragging?'is-dragging':''}`} onDragOver={event=>{event.preventDefault();if(!busy)setDragging(true)}} onDragLeave={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node))setDragging(false)}} onDrop={event=>{event.preventDefault();setDragging(false);if(!busy){if(event.dataTransfer.files.length!==1)setError(t("credentials.error.drop_single"));else void readFile(event.dataTransfer.files[0])}}}><label>{t("credentials.field.private_key")}<textarea required value={value} disabled={busy} onChange={event=>{fileRead.current++;setValue(event.target.value)}} rows={5} autoComplete="off" spellCheck={false} placeholder={t("credentials.field.private_key_placeholder")}/></label><input ref={fileInput} type="file" hidden onChange={event=>{const file=event.target.files?.[0];event.target.value='';if(file)void readFile(file)}}/><button type="button" className="secondary-button" disabled={busy} onClick={()=>fileInput.current?.click()}>{t("credentials.choose_file")}</button><span className="field-note">{t("credentials.private_key_note")}</span></div>:<p className="field-note">{t("credentials.generate_note")}</p>}
    <button className="primary-button" disabled={busy||!target.trim()||(tab==="env"||mode==="import")&&!value||(tab==="ssh"&&!botID)}>{busy?t("credentials.submit.working"):tab==="env"?t("credentials.submit.save_apply"):mode==="import"?t("credentials.submit.import"):t("credentials.submit.generate")}</button>
   </form>}
   {loading&&<p className="field-note" role="status">{t("credentials.loading")}</p>}
   {tab==="env"?<div className="credential-list">{savedRows(items.filter(item=>item.kind==="env"))}{!loading&&!loadError&&!items.some(item=>item.kind==="env")&&!adding&&<EmptyState name="credentials-env-empty" className="settings-empty is-compact" look={CATS.nori} pose="asleep"><p>{t("credentials.empty_env")}</p></EmptyState>}</div>:<><div className="credential-list">{keys.map(key=><article className="ssh-key-row" key={key.name}><div className="credential-row"><div className="credential-copy"><strong>{key.name}</strong><p>{!key.has_private?(key.fingerprint?t("credentials.key.public_only_fingerprint",{fingerprint:key.fingerprint}):t("credentials.key.public_only")):key.encrypted&&!key.public_key?t("credentials.key.encrypted_no_public"):key.public_key&&key.public_key_verified===false?t("credentials.key.unverified",{fingerprint:key.fingerprint||t("credentials.key.no_public")}):(key.fingerprint||t("credentials.key.no_public"))}</p></div>{key.public_key&&<div className="credential-actions"><button className="text-button" aria-expanded={openKey===key.name} onClick={()=>setOpenKey(openKey===key.name?null:key.name)}>{openKey===key.name?t("credentials.key.hide_public"):t("credentials.key.show_public")}</button><button className="text-button" onClick={()=>void copyKey(key)}>{copied===key.name?t("credentials.key.copied"):t("credentials.key.copy_public")}</button></div>}</div>{openKey===key.name&&key.public_key&&<textarea ref={element=>{if(element&&selectPublic.current===key.name){element.focus();element.select();selectPublic.current=null}}} className="ssh-public-key" aria-label={t("credentials.key.public_aria",{name:key.name})} readOnly value={key.public_key} rows={3} onFocus={event=>event.target.select()}/>}</article>)}</div>{!botID?<p className="field-note">{t("credentials.no_bot_note")}</p>:!loading&&!loadError&&!syncPending&&!keys.length&&!adding&&<EmptyState name="credentials-ssh-empty" className="settings-empty is-compact" look={CATS.nori} pose="asleep"><p>{t("credentials.empty_ssh")}</p></EmptyState>}{legacy.length>0&&<Disclosure title={t("credentials.legacy_title")}><p className="field-note">{t("credentials.legacy_note")}</p>{savedRows(legacy)}</Disclosure>}</>}
   {tab==="ssh"&&syncPending&&<p className="field-note" role="status">{t("credentials.sync_pending")}</p>}{tab==="ssh"&&truncated&&<p className="field-note">{t("credentials.truncated")}</p>}
   {status&&<p className="settings-feedback" role="status">{status}</p>}
   {error&&<p className="error-text" role="alert">{error}</p>}
   {loadError&&<div className="credential-load-error" role="alert"><span>{loadError}</span><button className="text-button" disabled={busy} onClick={()=>void refresh()}>{t("credentials.retry")}</button></div>}
  </div>
 </section>
}
