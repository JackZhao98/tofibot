import {useEffect, useState} from "react";
import {api, request} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {FOLLOW_GLOBAL, effortForModel, effortForPinnedModel, followGlobalLabel, followsGlobal, groupModels, modelEfforts, shortEffortLabel} from "./modelCatalog";
import type {Bot, ModelCatalog} from "./types";
export type {ModelOption} from "./types";
export const effortLabels:Record<string,string>={none:"关闭",minimal:"最少",low:"低 · 更快",medium:"中 · 均衡",high:"高 · 深入",xhigh:"更高",max:"最高",ultra:"极高"};
type GlobalModel={model:string;reasoning_effort:string};
/** Bot model picker. With allowDefault the first option follows the workspace's global model ("default"/"default"); the global panel itself passes allowDefault={false}. */
export function ModelFields({model,effort,onChange,disabled=false,allowDefault=true}:{model:string;effort:string;onChange:(model:string,effort:string)=>void;disabled?:boolean;allowDefault?:boolean}){
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
 const modelDefault=selected?.default_reasoning&&efforts.includes(selected.default_reasoning)?`（${shortEffortLabel(selected.default_reasoning,effortLabels)}）`:"";
 // Legacy Bots stored no effort and run on medium; say so rather than pretend it is the model default.
 const effortValue=efforts.includes(effort)?effort:allowDefault?(effort===""?"":FOLLOW_GLOBAL):"";
 return <div className="model-fields"><label>模型<select value={following?FOLLOW_GLOBAL:model} onChange={e=>pick(e.target.value)} disabled={disabled||!catalog}>{allowDefault&&<option value={FOLLOW_GLOBAL}>{followGlobalLabel(models,global,effortLabels)}</option>}{!allowDefault&&!model&&<option value="" disabled>选择模型</option>}{model&&!following&&!selected&&<option value={model}>{model} · 当前配置</option>}{groups.map(group=><optgroup key={group.provider} label={group.label}>{group.models.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}</select></label>{!following&&efforts.length>0&&<label>思考强度<select value={effortValue} onChange={e=>onChange(model,e.target.value)} disabled={disabled}>{allowDefault?<>{effort===""&&<option value="">沿用旧设置（{shortEffortLabel("medium",effortLabels)}）</option>}<option value={FOLLOW_GLOBAL}>模型默认{modelDefault}</option></>:<option value="">模型默认</option>}{efforts.map(value=><option key={value} value={value}>{effortLabels[value]??value}</option>)}</select></label>}{empty?<p className="field-note">还没有可用的模型。在设置的「模型与连接」页连接 Codex，或添加 OpenAI / Claude API key。 <button type="button" className="text-button" onClick={openConnection}>前往设置</button></p>:catalog?.warning&&<p className="field-note">{catalog.source==="cache"?"当前显示最近获取的模型列表。":"部分模型提供方的列表暂时无法获取。"}</p>}{error&&<p className="error-text">暂时无法读取模型列表。<button type="button" className="text-button" disabled={disabled} onClick={()=>setVersion(v=>v+1)}>重试</button></p>}<p className="field-note">{following&&"使用设置中「模型」页的全局模型和思考强度，全局更改后从下一条消息起生效。"}切换 Bot 的模型会保留已保存的记忆和聊天记录。记忆保存在对应的 Bot 或群中；不同模型使用这些内容的方式可能不同。</p></div>
}
export function ModelDefaults({bots}:{bots?:Bot[]}={}){
 const [version,setVersion]=useState(0);
 const [value,setValue]=useState<GlobalModel|null>(null);const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 const [savedValue,setSavedValue]=useState<GlobalModel|null>(null);
 useEffect(()=>{const ac=new AbortController();setError("");request<GlobalModel>("/api/model-settings",{signal:ac.signal}).then(next=>{setValue(next);setSavedValue(next)}).catch(e=>{if(!ac.signal.aborted)setError(e.message)});return()=>ac.abort()},[version]);
 const dirty=Boolean(value&&savedValue&&(value.model!==savedValue.model||value.reasoning_effort!==savedValue.reasoning_effort));
 const followers=bots?.filter(bot=>!bot.archived&&followsGlobal(bot.model)).length;
 async function save(){if(busy)return false;if(!value?.model){setError("请先选择模型。");return false}setBusy(true);setStatus("");setError("");try{await request("/api/model-settings",{method:"PUT",body:JSON.stringify(value)});setSavedValue(value);setStatus("已保存，跟随全局的 Bot 从下一条消息起使用这组配置。");return true}catch(e){setError(e instanceof Error?e.message:"保存失败，请重试。");return false}finally{setBusy(false)}}
 useSettingsDraft({label:"全局模型",dirty,busy,save,discard:()=>{setValue(savedValue);setStatus("");setError("")}});
 return <section className="settings-section"><h3>全局模型</h3><p className="settings-description">设为「跟随全局」的 Bot 使用这里的模型和思考强度{followers===undefined?"":followers?`，当前有 ${followers} 个 Bot 跟随`:"，当前没有 Bot 跟随"}。新建的 Bot 默认跟随全局。</p>{value?<form aria-busy={busy} onSubmit={e=>{e.preventDefault();void save()}}><ModelFields disabled={busy} allowDefault={false} model={value.model} effort={value.reasoning_effort} onChange={(model,reasoning_effort)=>{setValue({model,reasoning_effort});setStatus("")}}/><div className="settings-form-footer"><span role="status">{status}</span></div></form>:!error&&<p className="muted">读取配置…</p>}{error&&<p className="error-text" role="alert">{error}{!value&&<button type="button" className="text-button" onClick={()=>setVersion(current=>current+1)}>重试</button>}</p>}</section>
}
