import {useEffect, useState} from "react";
import {api} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";

type DictationModel={id:string;name:string;description:string;cost:string};
type DictationSettingsValue={model:string;configured:boolean;auth_source:"api_key"|"codex"|"";models:DictationModel[]};

export function DictationSettings(){
 const {t}=useTranslation("settings");
 const [value,setValue]=useState<DictationSettingsValue|null>(null);const [choice,setChoice]=useState("");const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 useEffect(()=>{let active=true;setError("");api.dictationSettings().then(next=>{if(active){setValue(next);setChoice(next.model)}}).catch(e=>{if(active)setError(e instanceof Error?e.message:t("dictation.load_failed"))});return()=>{active=false}},[]);
 async function save(){if(!choice||busy)return false;setBusy(true);setStatus("");setError("");try{const next=await api.saveDictationSettings(choice);setChoice(next.model);setValue(current=>current?{...current,model:next.model}:current);setStatus(t("dictation.saved"));return true}catch(e){setError(e instanceof Error?e.message:t("action.save_failed"));return false}finally{setBusy(false)}}
 useSettingsDraft({label:t("dictation.draft_label"),dirty:Boolean(value&&choice!==value.model),busy,save,discard:()=>{setChoice(value?.model??"");setStatus("");setError("")}});
 const selected=value?.models.find(model=>model.id===choice);
 return <section className="settings-section"><p className="settings-description">{t("dictation.description")}</p>{value?<form aria-busy={busy} onSubmit={event=>{event.preventDefault();void save()}}><label>{t("dictation.model_label")}<select value={choice} onChange={event=>{setChoice(event.target.value);setStatus("")}} disabled={busy}>{value.models.map(model=><option key={model.id} value={model.id}>{model.name}</option>)}</select></label>{selected&&<p className="field-note">{selected.description}{value.auth_source==="api_key"&&<> · {t("dictation.api_price",{cost:selected.cost})}</>}</p>}{value.auth_source==="codex"&&<p className="field-note">{t("dictation.codex_account")}</p>}{!value.configured&&<p className="settings-notice">{t("dictation.not_configured")}</p>}<div className="settings-form-footer"><span className="settings-feedback" role="status">{status}</span></div></form>:!error&&<p className="muted">{t("dictation.loading")}</p>}{error&&<p className="error-text" role="alert">{error}</p>}</section>
}
