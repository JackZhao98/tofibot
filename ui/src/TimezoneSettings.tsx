import {useEffect,useMemo,useRef,useState} from "react";
import {browserTimezone,timezoneChoices,validTimezone} from "./timezone";
import "./timezone-settings.css";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
import {Select} from "./Select";
import {useUserTimezone} from "./UserTimezone";
/** Settings controls for the account preferences; kept out of the entry chunk. */
export function ShowActivitySetting(){
 const {t}=useTranslation("settings");
 const preference=useUserTimezone();const [error,setError]=useState(false);const [busy,setBusy]=useState(false);
 async function change(show:boolean){setBusy(true);setError(false);const ok=await preference.saveShowActivity(show);if(!ok)setError(true);setBusy(false)}
 return <>
  <label className="debug-toggle"><span>{t("general.activity_title")}</span><input type="checkbox" role="switch" checked={preference.showActivity} disabled={busy||!preference.ready} onChange={event=>void change(event.target.checked)}/></label>
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
