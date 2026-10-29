import {createContext,useCallback,useContext,useEffect,useMemo,useRef,useState,type ReactNode} from "react";
import {request} from "./api";
import {browserTimezone,timezoneChoices,validTimezone} from "./timezone";
import "./timezone-settings.css";
import {useSettingsDraft} from "./settingsDraft";
import {i18n,syncServerLanguagePreference,useTranslation} from "./i18n";
type Preferences={timezone:string;timezone_configured:boolean;language?:string;show_activity?:boolean;notify_chats?:boolean;notify_tasks?:boolean};
type TimezoneState={timezone:string;ready:boolean;busy:boolean;error:string;refresh:()=>Promise<void>;save:(zone:string)=>Promise<boolean>;/** Chat shows finished runs' Activity rows. Off by default; stored on the account. */showActivity:boolean;saveShowActivity:(show:boolean)=>Promise<boolean>;/** Browser notifications for chat replies and for finished task results. On by default; stored on the account. */notifyChats:boolean;notifyTasks:boolean;saveNotify:(key:"chats"|"tasks",on:boolean)=>Promise<boolean>};
const TimezoneContext=createContext<TimezoneState|null>(null);
/** Reads /api/preferences: the timezone lives here; the UI language is applied from the same read. */
export function TimezoneProvider({children}:{children:ReactNode}){
 const [timezone,setTimezone]=useState(browserTimezone);
 const [ready,setReady]=useState(false);const [busy,setBusy]=useState(false);const [error,setError]=useState("");
 const [showActivity,setShowActivity]=useState(false);
 const [notify,setNotify]=useState({chats:true,tasks:true});
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
   setTimezone(result.timezone);setShowActivity(result.show_activity===true);setNotify({chats:result.notify_chats!==false,tasks:result.notify_tasks!==false});setReady(true);setError("");
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
 const saveNotify=useCallback(async(key:"chats"|"tasks",on:boolean)=>{
  const before=notify;setNotify(current=>({...current,[key]:on}));
  try{
   const result=await request<Preferences>("/api/preferences",{method:"PUT",body:JSON.stringify(key==="chats"?{notify_chats:on}:{notify_tasks:on})});
   if(mounted.current)setNotify({chats:result.notify_chats!==false,tasks:result.notify_tasks!==false});
   return true;
  }catch{if(mounted.current)setNotify(before);return false}
 },[notify]);
 useEffect(()=>{
  mounted.current=true;void refresh();
  const focus=()=>{if(!document.hidden)void refresh()};window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);
  return()=>{mounted.current=false;version.current++;controller.current?.abort();window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus)};
 },[refresh]);
 return <TimezoneContext.Provider value={{timezone,ready,busy,error,refresh,save,showActivity,saveShowActivity,notifyChats:notify.chats,notifyTasks:notify.tasks,saveNotify}}>{children}</TimezoneContext.Provider>;
}
export function useUserTimezone(){const state=useContext(TimezoneContext);if(!state)throw new Error("TimezoneProvider is required");return state}
/** Whether chat shows Activity rows (tool steps) for finished runs. Outside the provider (fixtures) it is off. */
export function useShowActivity(){const state=useContext(TimezoneContext);return state?.showActivity??false}
