import {useEffect, useState} from "react";
import {api} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";
import {SettingsCard,SettingsRow,SettingsSection} from "./settings/components";
import { Select } from "./Select";

type DictationModel={id:string;name:string};
export type DictationSettingsValue={model:string;configured:boolean;auth_source:"api_key"|"codex"|"";models:DictationModel[]};

/**
 * Dictation needs OpenAI (the Anthropic API has no speech-to-text model): a ChatGPT sign-in, an OpenAI API key or a server
 * transcription key. The page reads this once per `version` and shows the section only when one of them is connected.
 */
export function useDictationStatus(version:number){
 const {t}=useTranslation("settings");
 const [value,setValue]=useState<DictationSettingsValue|null>(null);const [error,setError]=useState("");
 useEffect(()=>{let active=true;setError("");api.dictationSettings().then(next=>{if(active)setValue(next)}).catch(e=>{if(active)setError(e instanceof Error?e.message:t("dictation.load_failed"))});return()=>{active=false}},[version]);
 return {value,error};
}

/** The Dictation section: absent until an OpenAI method is connected, so there is nothing to configure and no mic in the composer. */
export function DictationSettings({value:loaded,error:loadError}:{value:DictationSettingsValue|null;error:string}){
 const {t}=useTranslation("settings");
 const [value,setValue]=useState<DictationSettingsValue|null>(loaded);const [choice,setChoice]=useState(loaded?.model??"");const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 useEffect(()=>{setValue(loaded);if(loaded)setChoice(current=>current&&loaded.models.some(model=>model.id===current)?current:loaded.model)},[loaded]);
 async function save(){if(!choice||busy)return false;setBusy(true);setStatus("");setError("");try{const next=await api.saveDictationSettings(choice);setChoice(next.model);setValue(current=>current?{...current,model:next.model}:current);setStatus(t("dictation.saved"));return true}catch(e){setError(e instanceof Error?e.message:t("action.save_failed"));return false}finally{setBusy(false)}}
 useSettingsDraft({label:t("dictation.draft_label"),dirty:Boolean(value&&choice!==value.model),busy,save,discard:()=>{setChoice(value?.model??"");setStatus("");setError("")}});
 const selected=value?.models.find(model=>model.id===choice);
 // Copy for each known model lives in the catalogs; an id we have no copy for shows nothing.
 const copies:Record<string,{hint:string;price:string}>={
  "gpt-4o-mini-transcribe":{hint:t("dictation.model.gpt-4o-mini-transcribe.hint"),price:t("dictation.model.gpt-4o-mini-transcribe.price")},
  "gpt-4o-transcribe":{hint:t("dictation.model.gpt-4o-transcribe.hint"),price:t("dictation.model.gpt-4o-transcribe.price")}
 };
 const known=selected&&Object.hasOwn(copies,selected.id)?copies[selected.id]:undefined;
 const hint=known?.hint??"";const price=known&&value?.auth_source==="api_key"?known.price:"";
 if(loadError&&!loaded)return <SettingsSection id="dictation" title={t("dictation.title")}><p className="error-text" role="alert">{loadError}</p></SettingsSection>;
 if(!value||!value.configured)return null;
 return <SettingsSection id="dictation" title={t("dictation.title")} className="dictation-settings">{value?<form aria-busy={busy} onSubmit={event=>{event.preventDefault();void save()}}><SettingsCard><SettingsRow labelFor="dictation-model" label={t("dictation.model_label")} description={t("dictation.description")} control={<Select id="dictation-model" value={choice} onValueChange={value =>{setChoice(value);setStatus("")}} disabled={busy}>{value.models.map(model=><option key={model.id} value={model.id}>{model.name}</option>)}</Select>}/>{(hint||price)&&<p className="settings-card-note">{hint}{hint&&price&&" · "}{price&&t("dictation.api_price",{cost:price})}</p>}{value.auth_source==="codex"&&<p className="settings-card-note">{t("dictation.codex_account")}</p>}</SettingsCard><div className="settings-form-footer"><span className="settings-feedback" role="status">{status}</span></div></form>:null}{error&&<p className="error-text" role="alert">{error}</p>}</SettingsSection>
}
