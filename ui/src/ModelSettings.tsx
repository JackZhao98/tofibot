import {useEffect, useState} from "react";
import {api, request} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {effortForModel, groupModels, modelEfforts} from "./modelCatalog";
import type {ModelCatalog} from "./types";
export type {ModelOption} from "./types";
export const effortLabels:Record<string,string>={none:"关闭",minimal:"最少",low:"低 · 更快",medium:"中 · 均衡",high:"高 · 深入",xhigh:"更高",max:"最高",ultra:"极高"};
export function ModelFields({model,effort,onChange,disabled=false,allowDefault=true}:{model:string;effort:string;onChange:(model:string,effort:string)=>void;disabled?:boolean;allowDefault?:boolean}){
 const [catalog,setCatalog]=useState<ModelCatalog|null>(null);const [error,setError]=useState("");const [version,setVersion]=useState(0);
 useEffect(()=>{const controller=new AbortController();setError("");api.models(controller.signal).then(setCatalog).catch(e=>{if(!controller.signal.aborted)setError(e.message)});return()=>controller.abort()},[version]);
 const models=catalog?.models??[];const groups=groupModels(models);
 const selected=models.find(item=>item.id===model);const efforts=modelEfforts(selected);
 const empty=Boolean(catalog)&&!models.length;
 const openConnection=()=>window.dispatchEvent(new Event("tofi:open-codex-settings"));
 return <div className="model-fields"><label>模型<select value={model} onChange={e=>{const next=models.find(item=>item.id===e.target.value);onChange(e.target.value,effortForModel(next,effort))}} disabled={disabled||!catalog}>{allowDefault&&<option value="">使用默认模型</option>}{!allowDefault&&!model&&<option value="" disabled>选择模型</option>}{model&&!selected&&<option value={model}>{model} · 当前配置</option>}{groups.map(group=><optgroup key={group.provider} label={group.label}>{group.models.map(item=><option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}</select></label>{efforts.length>0&&<label>思考强度<select value={efforts.includes(effort)?effort:""} onChange={e=>onChange(model,e.target.value)} disabled={disabled}><option value="">模型默认</option>{efforts.map(value=><option key={value} value={value}>{effortLabels[value]??value}</option>)}</select></label>}{empty?<p className="field-note">还没有可用的模型。在设置的「模型与连接」页连接 Codex，或添加 OpenAI / Claude API key。 <button type="button" className="text-button" onClick={openConnection}>前往设置</button></p>:catalog?.warning&&<p className="field-note">{catalog.source==="cache"?"当前显示最近获取的模型列表。":"部分模型提供方的列表暂时无法获取。"}</p>}{error&&<p className="error-text">暂时无法读取模型列表。<button type="button" className="text-button" disabled={disabled} onClick={()=>setVersion(v=>v+1)}>重试</button></p>}<p className="field-note">切换 Bot 的模型会保留已保存的记忆和聊天记录。记忆保存在对应的 Bot 或群中；不同模型使用这些内容的方式可能不同。</p></div>
}
export function ModelDefaults(){
 const [version,setVersion]=useState(0);
 const [value,setValue]=useState<{model:string;reasoning_effort:string}|null>(null);const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 const [savedValue,setSavedValue]=useState<{model:string;reasoning_effort:string}|null>(null);
 useEffect(()=>{const ac=new AbortController();setError("");request<{model:string;reasoning_effort:string}>("/api/model-settings",{signal:ac.signal}).then(next=>{setValue(next);setSavedValue(next)}).catch(e=>{if(!ac.signal.aborted)setError(e.message)});return()=>ac.abort()},[version]);
 const dirty=Boolean(value&&savedValue&&(value.model!==savedValue.model||value.reasoning_effort!==savedValue.reasoning_effort));
 async function save(){if(busy)return false;if(!value?.model){setError("请先选择模型。");return false}setBusy(true);setStatus("");setError("");try{await request("/api/model-settings",{method:"PUT",body:JSON.stringify(value)});setSavedValue(value);setStatus("已保存，新创建的 Bot 会使用这组配置。");return true}catch(e){setError(e instanceof Error?e.message:"保存失败，请重试。");return false}finally{setBusy(false)}}
 useSettingsDraft({label:"模型默认配置",dirty,busy,save,discard:()=>{setValue(savedValue);setStatus("");setError("")}});
 return <section className="settings-section"><h3>新 Bot 的默认配置</h3><p className="settings-description">仅影响之后创建的 Bot。</p>{value?<form aria-busy={busy} onSubmit={e=>{e.preventDefault();void save()}}><ModelFields disabled={busy} allowDefault={false} model={value.model} effort={value.reasoning_effort} onChange={(model,reasoning_effort)=>{setValue({model,reasoning_effort});setStatus("")}}/><div className="settings-form-footer"><span role="status">{status}</span></div></form>:!error&&<p className="muted">读取配置…</p>}{error&&<p className="error-text" role="alert">{error}{!value&&<button type="button" className="text-button" onClick={()=>setVersion(current=>current+1)}>重试</button>}</p>}</section>
}
