import {useEffect, useState} from "react";
import {api} from "./api";
import {useSettingsDraft} from "./settingsDraft";

type DictationModel={id:string;name:string;description:string;cost:string};
type DictationSettingsValue={model:string;configured:boolean;auth_source:"api_key"|"codex"|"";models:DictationModel[]};

export function DictationSettings(){
 const [value,setValue]=useState<DictationSettingsValue|null>(null);const [choice,setChoice]=useState("");const [busy,setBusy]=useState(false);const [status,setStatus]=useState("");const [error,setError]=useState("");
 useEffect(()=>{let active=true;setError("");api.dictationSettings().then(next=>{if(active){setValue(next);setChoice(next.model)}}).catch(e=>{if(active)setError(e instanceof Error?e.message:"读取 Dictate 设置失败")});return()=>{active=false}},[]);
 async function save(){if(!choice||busy)return false;setBusy(true);setStatus("");setError("");try{const next=await api.saveDictationSettings(choice);setChoice(next.model);setValue(current=>current?{...current,model:next.model}:current);setStatus("已保存");return true}catch(e){setError(e instanceof Error?e.message:"保存失败，请重试。");return false}finally{setBusy(false)}}
 useSettingsDraft({label:"听写模型",dirty:Boolean(value&&choice!==value.model),busy,save,discard:()=>{setChoice(value?.model??"");setStatus("");setError("")}});
 const selected=value?.models.find(model=>model.id===choice);
 return <section className="settings-section"><p className="settings-description">将录音发送到 OpenAI 转写为文字，再插入当前草稿。不会自动发送消息。</p>{value?<form aria-busy={busy} onSubmit={event=>{event.preventDefault();void save()}}><label>转写模型<select value={choice} onChange={event=>{setChoice(event.target.value);setStatus("")}} disabled={busy}>{value.models.map(model=><option key={model.id} value={model.id}>{model.name}</option>)}</select></label>{selected&&<p className="field-note">{selected.description}{value.auth_source==="api_key"&&<> · API 参考价 {selected.cost}</>}</p>}{value.auth_source==="codex"&&<p className="field-note">使用已连接的 Codex 账户。</p>}{!value.configured&&<p className="settings-notice">请在连接设置中连接 Codex，或配置服务端转写 API key。</p>}<div className="settings-form-footer"><span className="settings-feedback" role="status">{status}</span></div></form>:!error&&<p className="muted">读取 Dictate 设置…</p>}{error&&<p className="error-text" role="alert">{error}</p>}</section>
}
