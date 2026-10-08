import {useEffect,useRef,useState} from "react";
import {request} from "./api";
import {BotAvatar} from "./BotAvatar";
import {isDesktop} from "./desktop";
import {useTranslation} from "./i18n";
import type {Bot} from "./types";
import "./secret-input-card.css";
type SecretRequest={id:string;bot_id:string;label:string;purpose:string;status:string};
function Lock({closed}:{closed:boolean}){return <svg viewBox="0 0 24 24" className={`secret-lab-lock${closed?" is-closed":""}`} aria-hidden="true"><path className="secret-lab-shackle" d="M8 11V8a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/><rect x="5" y="11" width="14" height="10" rx="3" fill="currentColor"/><circle cx="12" cy="16" r="1.6" fill="var(--surface)"/></svg>}
function SecretCard({item,bot,onDone}:{item:SecretRequest;bot?:Bot;onDone:()=>void}){
 const {t}=useTranslation("tasks");
 const [value,setValue]=useState("");const [busy,setBusy]=useState(false);const [error,setError]=useState("");const [done,setDone]=useState(false);const finishTimer=useRef<number|undefined>(undefined);
 useEffect(()=>()=>window.clearTimeout(finishTimer.current),[]);
 async function finish(cancel=false){if(busy||done)return;setBusy(true);setError("");try{await request(`/api/secret-inputs/${encodeURIComponent(item.id)}`,cancel?{method:"DELETE"}:{method:"POST",body:JSON.stringify({value})});setValue("");if(!cancel&&!isDesktop){setDone(true);finishTimer.current=window.setTimeout(onDone,window.matchMedia("(prefers-reduced-motion: reduce)").matches?0:500)}else onDone()}catch{setError(t("secret.submit_failed"))}finally{setBusy(false)}}
 const name=bot?.name??"Bot";
 return <article className="secret-message"><BotAvatar id={item.bot_id} mini/><div><span className="secret-sender">{name}</span><form className={`secret-input-card${done?" is-done":""}`} onSubmit={e=>{e.preventDefault();void finish()}}>{!isDesktop&&<div className="secret-lab-head"><span className="secret-lab-lock-wrap"><Lock closed={done}/></span><span>{done?t("secret.done",{name}):t("secret.needed",{name})}</span></div>}<div className="secret-lab-fold"><div className="secret-lab-inner"><span className="settings-tag">{t("secret.tag")}</span><h3>{item.label}</h3>{item.purpose&&<p>{item.purpose}</p>}<label><span className="sr-only">{item.label}</span><input type="password" value={value} onChange={e=>setValue(e.target.value)} autoComplete="new-password" placeholder={t("secret.placeholder")} disabled={busy||done} required/></label><p className="field-note">{t("secret.note")}</p><div className="settings-form-footer"><button type="button" className="text-button" disabled={busy||done} onClick={()=>void finish(true)}>{t("secret.cancel")}</button><button className="primary-button" disabled={busy||done||!value}>{busy?t("secret.submitting"):t("secret.submit")}</button></div>{error&&<p className="error-text" role="alert">{error}</p>}</div></div></form></div></article>
}
export function SecretInputs({conversationId,bots}:{conversationId:string;bots:Bot[]}){
 const [items,setItems]=useState<SecretRequest[]>([]);const [version,setVersion]=useState(0);
 useEffect(()=>{setItems([]);let closed=false;let timer:number;let controller:AbortController;async function refresh(){controller=new AbortController();try{const value=await request<{requests:SecretRequest[]}>(`/api/secret-inputs?conversation_id=${encodeURIComponent(conversationId)}`,{signal:controller.signal});if(!closed)setItems(value.requests??[])}catch{/* Keep pending card during a transient read failure. */}finally{if(!closed)timer=window.setTimeout(refresh,2500)}}void refresh();return()=>{closed=true;clearTimeout(timer);controller?.abort()}},[conversationId,version]);
 return <>{items.map(item=><SecretCard key={item.id} item={item} bot={bots.find(b=>b.id===item.bot_id)} onDone={()=>{setItems(current=>current.filter(v=>v.id!==item.id));setVersion(v=>v+1)}}/>)}</>
}
