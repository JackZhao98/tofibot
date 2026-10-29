import {createContext,useCallback,useContext,useEffect,useMemo,useRef,useState,type ReactNode} from "react";
import {request} from "./api";
import {browserTimezone,timezoneChoices,validTimezone} from "./timezone";
import "./timezone-settings.css";
import {useSettingsDraft} from "./settingsDraft";
import {i18n,syncServerLanguagePreference,useTranslation} from "./i18n";
import { Select } from "./Select";
type Preferences={timezone:string;timezone_configured:boolean;language?:string;show_activity?:boolean};
type TimezoneState={timezone:string;ready:boolean;busy:boolean;error:string;refresh:()=>Promise<void>;save:(zone:string)=>Promise<boolean>;/** Chat shows finished runs' Activity rows. Off by default; stored on the account. */showActivity:boolean;saveShowActivity:(show:boolean)=>Promise<boolean>};
const TimezoneContext=createContext<TimezoneState|null>(null);
/** Reads /api/preferences: the timezone lives here; the UI language is applied from the same read. */
export function TimezoneProvider({children}:{children:ReactNode}){
 const [timezone,setTimezone]=useState(browserTimezone);
 const [ready,setReady]=useState(false);const [busy,setBusy]=useState(false);const [error,setError]=useState("");
 const [showActivity,setShowActivity]=useState(false);
 const version=useRef(0);const mounted=useRef(false);const writing=useRef(false);const controller=useRef<AbortController|null>(null);
 const refresh=useCallback(async()=>{
  if(writing.current)return;
  const current=++version.current;controller.current?.abort();const abort=new AbortController();controller.current=abort;
  try{
   let result=await request<Preferences>("/api/preferences",{signal:abort.signal});
   if(!result.timezone_configured)result=await request<Preferences>("/api/preferences",{method:"PUT",body:JSON.stringify({timezone:browserTimezone(),initialize_only:true}),signal:abort.signal});
   if(!mounted.current||current!==version.current||abort.signal.aborted)return;
   void syncServerLanguagePreference(result.language);
   if(!validTimezone(result.timezone))throw new Error(i18n.t("settings:timezone.invalid_reselect"));
   setTimezone(result.timezone);setShowActivity(result.show_activity===true);setReady(true);setError("");
  }catch(cause){if(mounted.current&&current===version.current&&!abort.signal.aborted)setError(cause instanceof Error?cause.message:i18n.t("settings:timezone.read_failed"))}
 },[]);
 const save=useCallback(async(zone:string)=>{
  if(writing.current)return false;
  writing.current=true;const current=++version.current;controller.current?.abort();setBusy(true);setError("");
  try{
   const result=await request<Preferences>("/api/preferences",{method:"PUT",body:JSON.stringify({timezone:zone})});
   if(!mounted.current||current!==version.current)return false;
   if(!validTimezone(result.timezone))throw new Error(i18n.t("settings:timezone.invalid_retry"));
   setTimezone(result.timezone);setReady(true);return true;
  }catch(cause){if(mounted.current&&current===version.current)setError(cause instanceof Error?cause.message:i18n.t("settings:timezone.save_failed"));return false}
  finally{writing.current=false;if(mounted.current)setBusy(false)}
 },[]);
 const saveShowActivity=useCallback(async(show:boolean)=>{
  const before=showActivity;setShowActivity(show);
  try{
   const result=await request<Preferences>("/api/preferences",{method:"PUT",body:JSON.stringify({show_activity:show})});
   if(mounted.current)setShowActivity(result.show_activity===true);
   return true;
  }catch{if(mounted.current)setShowActivity(before);return false}
 },[showActivity]);
 useEffect(()=>{
  mounted.current=true;void refresh();
  const focus=()=>{if(!document.hidden)void refresh()};window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);
  return()=>{mounted.current=false;version.current++;controller.current?.abort();window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus)};
 },[refresh]);
 return <TimezoneContext.Provider value={{timezone,ready,busy,error,refresh,save,showActivity,saveShowActivity}}>{children}</TimezoneContext.Provider>;
}
export function useUserTimezone(){const state=useContext(TimezoneContext);if(!state)throw new Error("TimezoneProvider is required");return state}
/** Whether chat shows Activity rows (tool steps) for finished runs. Outside the provider (fixtures) it is off. */
export function useShowActivity(){const state=useContext(TimezoneContext);return state?.showActivity??false}
export function ShowActivitySetting(){
 const {t}=useTranslation("settings");
 const preference=useUserTimezone();const [error,setError]=useState(false);const [busy,setBusy]=useState(false);
 async function change(show:boolean){setBusy(true);setError(false);const ok=await preference.saveShowActivity(show);if(!ok)setError(true);setBusy(false)}
 return <>
  <label className="debug-toggle"><span>{t("general.activity_title")}<small>{t("general.activity_note")}</small></span><input type="checkbox" role="switch" checked={preference.showActivity} disabled={busy||!preference.ready} onChange={event=>void change(event.target.checked)}/></label>
  {error&&<p className="timezone-error" role="alert">{t("general.activity_save_failed")}</p>}
 </>;
}
export function TimezoneSelect({value,onChange,disabled,label}:{value:string;onChange:(value:string)=>void;disabled?:boolean;label?:string}){
 const {t}=useTranslation("settings");
 const zones=useMemo(()=>timezoneChoices(value),[value]);
 return <label className="timezone-select">{label??t("timezone.label")}<Select value={value} disabled={disabled} onValueChange={value =>onChange(value)}>{zones.map(zone=><option key={zone} value={zone}>{zone.replaceAll("_"," ")}</option>)}</Select></label>;
}
export function TimezoneSetting(){
 const {t}=useTranslation(["settings","common"]);
 const preference=useUserTimezone();const [draft,setDraft]=useState(preference.timezone);const [saved,setSaved]=useState(false);const dirty=useRef(false);
 useEffect(()=>{if(!dirty.current)setDraft(preference.timezone)},[preference.timezone]);
 async function save(){const ok=await preference.save(draft);if(ok){dirty.current=false;setSaved(true)}return ok}
 useSettingsDraft({label:t("timezone.label"),dirty:preference.ready&&draft!==preference.timezone,busy:preference.busy,save,discard:()=>{dirty.current=false;setDraft(preference.timezone);setSaved(false)}});
 return <section className="timezone-setting"><div><h3>{t("timezone.label")}</h3><p className="field-note">{t("timezone.note")}</p></div>
  <form onSubmit={event=>{event.preventDefault();void save()}}>
   <TimezoneSelect label={t("timezone.current")} value={draft} disabled={preference.busy} onChange={zone=>{dirty.current=true;setDraft(zone);setSaved(false)}}/>
   {!preference.ready&&<p className="field-note">{t("timezone.loading")}</p>}
  </form>
  {saved&&<p className="settings-feedback" role="status">{t("timezone.saved")}</p>}
  {preference.error&&<div className="timezone-error" role="alert"><span>{preference.error}</span><button className="text-button" type="button" onClick={()=>void preference.refresh()} disabled={preference.busy}>{t("common:action.retry")}</button></div>}
 </section>;
}
