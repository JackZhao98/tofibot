import {useEffect, useState} from "react";
import {api, request} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {FOLLOW_GLOBAL, effortForModel, effortForPinnedModel, followGlobalLabel, followsGlobal, groupModels, modelEfforts, shortEffortLabel} from "./modelCatalog";
import type {Bot, ModelCatalog} from "./types";
import {i18n, useTranslation} from "./i18n";
import {SettingsCard, SettingsRow, SettingsSection, Segmented} from "./settings/components";
export type {ModelOption} from "./types";
const effortKeys={none:"effort.none",minimal:"effort.minimal",low:"effort.low",medium:"effort.medium",high:"effort.high",xhigh:"effort.xhigh",max:"effort.max",ultra:"effort.ultra"} as const;
/** Effort names in the active language, built per call ("中 · 均衡"; the part before " · " is the short name). */
export function effortLabels():Record<string,string>{return Object.fromEntries(Object.entries(effortKeys).map(([effort,key])=>[effort,i18n.t(key,{ns:"settings"})]))}
type GlobalModel={model:string;reasoning_effort:string};
/** Bot model picker. With allowDefault the first option follows the workspace's global model ("default"/"default"); the global panel itself passes allowDefault={false}. */
export function ModelFields({model,effort,onChange,disabled=false,allowDefault=true,layout="stack"}:{model:string;effort:string;onChange:(model:string,effort:string)=>void;disabled?:boolean;allowDefault?:boolean;layout?:"stack"|"rows"}){
 const {t}=useTranslation("settings");const labels=effortLabels();
 const [catalog,setCatalog]=useState<ModelCatalog|null>(null);const [error,setError]=useState("");const [version,setVersion]=useState(0);
 const [global,setGlobal]=useState<GlobalModel|null>(null);
 useEffect(()=>{const controller=new AbortController();setError("");api.models(controller.signal).then(setCatalog).catch(e=>{if(!controller.signal.aborted)setError(e.message)});return()=>controller.abort()},[version]);
 // The follow option names what it follows; without it the option still reads "跟随全局".
 useEffect(()=>{if(!allowDefault)return;const controller=new AbortController();request<GlobalModel>("/api/model-settings",{signal:controller.signal}).then(setGlobal).catch(()=>{});return()=>controller.abort()},[allowDefault,version]);
 const models=catalog?.models??[];const groups=groupModels(models);
 const following=allowDefault&&followsGlobal(model);
 const selected=following?undefined:models.find(item=>item.id===model);const efforts=modelEfforts(selected);
 const empty=Boolean(catalog)&&!models.length;
 const openConnection=()=>window.dispatchEvent(new Event("tofi:open-codex-settings"));
 const pick=(value:string)=>{
  if(allowDefault&&value===FOLLOW_GLOBAL){onChange(FOLLOW_GLOBAL,FOLLOW_GLOBAL);return}
  const next=models.find(item=>item.id===value);
  onChange(value,allowDefault?effortForPinnedModel(next,effort):effortForModel(next,effort));
 };
 const modelDefault=selected?.default_reasoning&&efforts.includes(selected.default_reasoning)?shortEffortLabel(selected.default_reasoning,labels):"";
 // Legacy Bots stored no effort and run on medium; say so rather than pretend it is the model default.
 const effortValue=efforts.includes(effort)?effort:allowDefault?(effort===""?"":FOLLOW_GLOBAL):"";
 if(layout==="rows"){
  // Defaults card: one row per control. Reasoning is a segmented control over the selected model's own list; more than four values fall back to a select.
  const modelSelect=<select id="model-default-select" value={following?FOLLOW_GLOBAL:model} onChange={e=>pick(e.target.value)} disabled={disabled||!catalog}>{allowDefault&&<option value={FOLLOW_GLOBAL}>{followGlobalLabel(models,global,labels)}</option>}{!allowDefault&&!model&&<option value="" disabled>{t("model.choose")}</option>}{model&&!following&&!selected&&<option value={model}>{t("model.current_config",{model})}</option>}{groups.map(group=><optgroup key={group.provider} label={group.label}>{group.models.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}</select>;
  const segmented=efforts.length>0&&efforts.length<=4;
  const effortControl=segmented?<Segmented label={t("model.effort_label")} value={efforts.includes(effort)?effort:""} disabled={disabled} options={efforts.map(value=>({value,label:shortEffortLabel(value,labels)}))} onChange={value=>onChange(model,value)}/>:<select id="model-effort-select" value={effortValue} onChange={e=>onChange(model,e.target.value)} disabled={disabled}><option value="">{t("model.model_default")}</option>{efforts.map(value=><option key={value} value={value}>{labels[value]??value}</option>)}</select>;
  return <div className="model-fields model-fields-rows"><SettingsCard><SettingsRow labelFor="model-default-select" label={t("model.label")} description={t("model.note")} control={modelSelect}/>{efforts.length>0&&<SettingsRow inline={segmented} labelFor={segmented?undefined:"model-effort-select"} label={t("model.effort_label")} description={modelDefault?t("model.effort_hint",{effort:modelDefault}):undefined} control={effortControl}/>}</SettingsCard>{empty?<p className="field-note">{t("model.no_models")} <button type="button" className="text-button" onClick={openConnection}>{t("model.open_settings")}</button></p>:catalog?.warning&&<p className="field-note">{catalog.source==="cache"?t("model.cached_list"):t("model.partial_list")}</p>}{error&&<p className="error-text">{t("model.load_failed")}<button type="button" className="text-button" disabled={disabled} onClick={()=>setVersion(v=>v+1)}>{t("action.retry")}</button></p>}</div>
 }
 return <div className="model-fields"><label>{t("model.label")}<select value={following?FOLLOW_GLOBAL:model} onChange={e=>pick(e.target.value)} disabled={disabled||!catalog}>{allowDefault&&<option value={FOLLOW_GLOBAL}>{followGlobalLabel(models,global,labels)}</option>}{!allowDefault&&!model&&<option value="" disabled>{t("model.choose")}</option>}{model&&!following&&!selected&&<option value={model}>{t("model.current_config",{model})}</option>}{groups.map(group=><optgroup key={group.provider} label={group.label}>{group.models.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}</select></label>{!following&&efforts.length>0&&<label>{t("model.effort_label")}<select value={effortValue} onChange={e=>onChange(model,e.target.value)} disabled={disabled}>{allowDefault?<>{effort===""&&<option value="">{t("model.legacy_effort",{effort:shortEffortLabel("medium",labels)})}</option>}<option value={FOLLOW_GLOBAL}>{modelDefault?t("model.model_default_effort",{effort:modelDefault}):t("model.model_default")}</option></>:<option value="">{t("model.model_default")}</option>}{efforts.map(value=><option key={value} value={value}>{labels[value]??value}</option>)}</select></label>}{empty?<p className="field-note">{t("model.no_models")} <button type="button" className="text-button" onClick={openConnection}>{t("model.open_settings")}</button></p>:catalog?.warning&&<p className="field-note">{catalog.source==="cache"?t("model.cached_list"):t("model.partial_list")}</p>}{error&&<p className="error-text">{t("model.load_failed")}<button type="button" className="text-button" disabled={disabled} onClick={()=>setVersion(v=>v+1)}>{t("action.retry")}</button></p>}<p className="field-note">{following?t("model.note_following"):t("model.note")}</p></div>
}
export function ModelDefaults({bots}:{bots?:Bot[]}={}){
 const {t}=useTranslation("settings");
 const [version,setVersion]=useState(0);
 const [value,setValue]=useState<GlobalModel|null>(null);const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 const [savedValue,setSavedValue]=useState<GlobalModel|null>(null);
 useEffect(()=>{const ac=new AbortController();setError("");request<GlobalModel>("/api/model-settings",{signal:ac.signal}).then(next=>{setValue(next);setSavedValue(next)}).catch(e=>{if(!ac.signal.aborted)setError(e.message)});return()=>ac.abort()},[version]);
 const dirty=Boolean(value&&savedValue&&(value.model!==savedValue.model||value.reasoning_effort!==savedValue.reasoning_effort));
 const followers=bots?.filter(bot=>!bot.archived&&followsGlobal(bot.model)).length;
 async function save(){if(busy)return false;if(!value?.model){setError(t("model.choose_first"));return false}setBusy(true);setStatus("");setError("");try{await request("/api/model-settings",{method:"PUT",body:JSON.stringify(value)});setSavedValue(value);setStatus(t("model.saved"));return true}catch(e){setError(e instanceof Error?e.message:t("action.save_failed"));return false}finally{setBusy(false)}}
 useSettingsDraft({label:t("model.global_title"),dirty,busy,save,discard:()=>{setValue(savedValue);setStatus("");setError("")}});
 return <SettingsSection title={t("model.global_title")} description={followers===undefined?t("model.global_description"):followers?t("model.global_description_followers",{count:followers}):t("model.global_description_no_followers")}>{value?<form aria-busy={busy} onSubmit={e=>{e.preventDefault();void save()}}><ModelFields layout="rows" disabled={busy} allowDefault={false} model={value.model} effort={value.reasoning_effort} onChange={(model,reasoning_effort)=>{setValue({model,reasoning_effort});setStatus("")}}/><div className="settings-form-footer"><span role="status">{status}</span></div></form>:!error&&<p className="muted">{t("action.loading_settings")}</p>}{error&&<p className="error-text" role="alert">{error}{!value&&<button type="button" className="text-button" onClick={()=>setVersion(current=>current+1)}>{t("action.retry")}</button>}</p>}</SettingsSection>
}
