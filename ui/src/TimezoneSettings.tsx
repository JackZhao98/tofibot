import {useMemo,useState} from "react";
import {timezoneChoices} from "./timezone";
import {SavedNote,SettingsSwitch,useSavedFlash} from "./settings/components";
import {SettingsRow} from "./ds/SettingsRow/SettingsRow";
import {Button} from "./ds/Button/Button";
import {useTranslation} from "./i18n";
import {Select} from "./Select";
import {useUserTimezone} from "./UserTimezone";
import "./settings/general.css";
/** Settings controls for the account preferences; kept out of the entry chunk. */
export function ShowActivitySetting(){
 const {t}=useTranslation("settings");
 const preference=useUserTimezone();const [error,setError]=useState(false);const [busy,setBusy]=useState(false);
 async function change(show:boolean){setBusy(true);setError(false);const ok=await preference.saveShowActivity(show);if(!ok)setError(true);setBusy(false);return ok}
 return <>
  <SettingsRow label={t("general.activity_title")} data-setting="show-activity"><SettingsSwitch label={t("general.activity_title")} checked={preference.showActivity} disabled={busy||!preference.ready} busy={busy} onChange={change}/></SettingsRow>
  {error&&<p className="gen-note gen-note--danger" role="alert">{t("general.activity_save_failed")}</p>}
 </>;
}
export function TimezoneSelect({value,onChange,disabled,label,hideLabel=false}:{value:string;onChange:(value:string)=>void;disabled?:boolean;label?:string;/** The label stays the accessible name; the row already shows the text. */hideLabel?:boolean}){
 const {t}=useTranslation("settings");
 const zones=useMemo(()=>timezoneChoices(value),[value]);
 return <label><span className={hideLabel?"sr-only":undefined}>{label??t("timezone.label")}</span><Select value={value} disabled={disabled} onValueChange={value =>onChange(value)}>{zones.map(zone=><option key={zone} value={zone}>{zone.replaceAll("_"," ")}</option>)}</Select></label>;
}
/** The zone applies as soon as it is chosen, like the language above it. */
export function TimezoneSetting(){
 const {t}=useTranslation(["settings","common"]);
 const preference=useUserTimezone();const [saved,flash]=useSavedFlash();
 const choose=async(zone:string)=>{if(await preference.save(zone))flash()};
 const zones=useMemo(()=>timezoneChoices(preference.timezone),[preference.timezone]);
 return <>
  <SettingsRow fill label={t("timezone.label")} hint={t("timezone.note")} data-setting="timezone">
   <span className="gen-saved-ctl"><SavedNote show={saved}/>
    <span className="gen-ctl"><Select aria-label={t("timezone.current")} value={preference.timezone} disabled={preference.busy||!preference.ready} onValueChange={zone=>void choose(zone)}>{zones.map(zone=><option key={zone} value={zone}>{zone.replaceAll("_"," ")}</option>)}</Select></span>
   </span>
  </SettingsRow>
  {!preference.ready&&<p className="gen-note">{t("timezone.loading")}</p>}
  {preference.error&&<div className="gen-note gen-note--danger" role="alert"><span>{preference.error}</span> <Button variant="ghost" size="sm" onClick={()=>void preference.refresh()} disabled={preference.busy}>{t("common:action.retry")}</Button></div>}
 </>;
}
