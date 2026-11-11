import {useMemo,useState} from "react";
import {browserTimezone,timezoneChoices,validTimezone} from "./timezone";
import "./timezone-settings.css";
import {SavedNote,SettingsRow,SettingsSwitch,useSavedFlash} from "./settings/components";
import {useTranslation} from "./i18n";
import {Select} from "./Select";
import {useUserTimezone} from "./UserTimezone";
/** Settings controls for the account preferences; kept out of the entry chunk. */
export function ShowActivitySetting(){
 const {t}=useTranslation("settings");
 const preference=useUserTimezone();const [error,setError]=useState(false);const [busy,setBusy]=useState(false);
 async function change(show:boolean){setBusy(true);setError(false);const ok=await preference.saveShowActivity(show);if(!ok)setError(true);setBusy(false);return ok}
 return <>
  <SettingsRow inline label={t("general.activity_title")} control={<SettingsSwitch label={t("general.activity_title")} checked={preference.showActivity} disabled={busy||!preference.ready} busy={busy} onChange={change}/>}/>
  {error&&<p className="timezone-error" role="alert">{t("general.activity_save_failed")}</p>}
 </>;
}
export function TimezoneSelect({value,onChange,disabled,label,hideLabel=false}:{value:string;onChange:(value:string)=>void;disabled?:boolean;label?:string;/** The label stays the accessible name; the row already shows the text. */hideLabel?:boolean}){
 const {t}=useTranslation("settings");
 const zones=useMemo(()=>timezoneChoices(value),[value]);
 return <label className="timezone-select"><span className={hideLabel?"sr-only":undefined}>{label??t("timezone.label")}</span><Select value={value} disabled={disabled} onValueChange={value =>onChange(value)}>{zones.map(zone=><option key={zone} value={zone}>{zone.replaceAll("_"," ")}</option>)}</Select></label>;
}
/** The zone applies as soon as it is chosen, like the language beside it. */
export function TimezoneSetting(){
 const {t}=useTranslation(["settings","common"]);
 const preference=useUserTimezone();const [saved,flash]=useSavedFlash();
 const choose=async(zone:string)=>{if(await preference.save(zone))flash()};
 return <section className="timezone-setting"><div><h3>{t("timezone.label")}</h3><p className="field-note">{t("timezone.note")}</p></div>
  <SavedNote show={saved}/>
  <TimezoneSelect hideLabel label={t("timezone.current")} value={preference.timezone} disabled={preference.busy||!preference.ready} onChange={zone=>void choose(zone)}/>
  {!preference.ready&&<p className="field-note">{t("timezone.loading")}</p>}
  {preference.error&&<div className="timezone-error" role="alert"><span>{preference.error}</span><button className="text-button" type="button" onClick={()=>void preference.refresh()} disabled={preference.busy}>{t("common:action.retry")}</button></div>}
 </section>;
}
