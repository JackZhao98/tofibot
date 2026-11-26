import { Button } from "./ds/Button/Button";
import {useCallback,useEffect,useRef,useState} from "react";
import {request} from "./api";
import {ConfirmAction} from "./ConfirmAction";
import {useTranslation} from "./i18n";
import {formatDateTime} from "./i18n/format";
import {KeyRow} from "./ds/KeyRow/KeyRow";
import {SettingsSection} from "./ds/SettingsSection/SettingsSection";
import {TextInput} from "./ds/TextInput/TextInput";
import "./bot-secrets.css";

type BotSecret={name:string;created_at:string;last_used_at?:string};

/** A Bot's saved secrets: names and times only. A value is never shown; Replace sends a new one. */
export function BotSecrets({botId}:{botId:string}){
 const {t}=useTranslation("bots");
 const [items,setItems]=useState<BotSecret[]>([]);const [loading,setLoading]=useState(true);
 const [replacing,setReplacing]=useState<string|null>(null);const [value,setValue]=useState("");
 const [busy,setBusy]=useState(false);const [error,setError]=useState("");const [status,setStatus]=useState("");
 const alive=useRef(false);const path=`/api/bots/${encodeURIComponent(botId)}/secrets`;
 const load=useCallback(async()=>{
  try{const result=await request<{secrets:BotSecret[]}>(path);if(alive.current){setItems(result.secrets??[]);setError("")}}
  catch{if(alive.current)setError(t("secrets.error.load"))}
  finally{if(alive.current)setLoading(false)}
 },[path,t]);
 useEffect(()=>{alive.current=true;setLoading(true);setItems([]);setReplacing(null);setValue("");void load();return()=>{alive.current=false}},[load]);
 function stopReplacing(){setReplacing(null);setValue("")}
 async function replace(name:string){
  if(busy||!value)return;
  setBusy(true);setError("");setStatus("");
  try{await request(`${path}/${encodeURIComponent(name)}`,{method:"PUT",body:JSON.stringify({value})});stopReplacing();setStatus(t("secrets.status.replaced",{name}));await load()}
  catch{setError(t("secrets.error.replace"))}
  finally{if(alive.current)setBusy(false)}
 }
 async function remove(name:string){
  await request(`${path}/${encodeURIComponent(name)}`,{method:"DELETE"});
  if(alive.current){setStatus(t("secrets.status.deleted",{name}));await load()}
 }
 const when=(iso:string)=>formatDateTime(iso);
 return <section className="bot-secrets" aria-labelledby={`bot-secrets-${botId}`}>
  <SettingsSection title={<span id={`bot-secrets-${botId}`}>{t("secrets.title")}</span>} description={t("secrets.hint")}>
   {loading?<p className="field-note" role="status">{t("secrets.loading")}</p>:!items.length&&!error?<p className="field-note">{t("secrets.empty")}</p>:items.map(item=><div className="bot-secret-item" key={item.name}>
    <KeyRow name={item.name} masked={`•••••••• · ${t("secrets.saved_at",{time:when(item.created_at)})} · ${item.last_used_at?t("secrets.last_used",{time:when(item.last_used_at)}):t("secrets.never_used")}`}

     actions={replacing===item.name?undefined:<><Button variant="ghost" size="sm" disabled={busy} onClick={()=>{setReplacing(item.name);setValue("");setStatus("")}}>{t("secrets.replace")}</Button><ConfirmAction label={t("secrets.delete")} question={t("secrets.delete_question",{name:item.name})} disabled={busy} onConfirm={()=>remove(item.name)}/></>}/>
    {replacing===item.name&&<form className="bot-secret-replace" onSubmit={event=>{event.preventDefault();void replace(item.name)}}>
     <TextInput type="password" aria-label={t("secrets.new_value",{name:item.name})} value={value} autoComplete="new-password" disabled={busy} placeholder={t("secrets.new_value",{name:item.name})} onChange={event=>setValue(event.target.value)}/>
     <Button type="submit" variant="primary" size="sm" disabled={busy||!value}>{busy?t("secrets.replacing"):t("secrets.replace_save")}</Button>
     <Button variant="ghost" size="sm" disabled={busy} onClick={stopReplacing}>{t("secrets.cancel")}</Button>
    </form>}
   </div>)}
  </SettingsSection>
  {status&&<p className="settings-feedback" role="status">{status}</p>}
  {error&&<p className="error-text" role="alert">{error}</p>}
 </section>
}
