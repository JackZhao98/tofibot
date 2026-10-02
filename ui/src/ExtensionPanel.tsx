import { useCallback, useEffect, useRef, useState } from "react";
import type { Bot } from "./types";
import { request } from "./api";
import { ConfirmAction, Disclosure } from "./InteractionSystem";
import { useDebugMode } from "./debugMode";
import { TofiIcon } from "./icons";
import { skillCatalog } from "./skillCatalog";
import { MCPSettings } from "./MCPSettings";
import "./extensions-system.css";

type Skill = {name:string; description:string};
const message = (error: unknown) => {
 const value=error instanceof Error?error.message:String(error);
 if(value==="skill already exists")return "这个名称已存在，请换一个名称。";
 return value;
};
const post = (body:unknown) => ({method:"POST", body:JSON.stringify(body)});

export function ExtensionPanel(props:{bots:Bot[];kind:"mcp"|"skills";refreshToken?:number}) {
 return props.kind === "mcp" ? <MCPSettings bots={props.bots} refreshToken={props.refreshToken}/> : <SkillsSettings refreshToken={props.refreshToken}/>;
}
function SkillsSettings({refreshToken=0}:{refreshToken?:number}) {
 const debug=useDebugMode();
 const [skills,setSkills]=useState<Skill[]>([]);
 const [loaded,setLoaded]=useState(false); const [loading,setLoading]=useState(true);
 const [error,setError]=useState(""); const [notice,setNotice]=useState(""); const [busy,setBusy]=useState(false);
 const [skillForm,setSkillForm]=useState(false); const [skillName,setSkillName]=useState(""); const [skillText,setSkillText]=useState("");
 const [skillFiles,setSkillFiles]=useState<Record<string,string>>({});
 const requestVersion=useRef(0);
 const folderInput=useRef<HTMLInputElement>(null);
 const refresh=useCallback(async()=>{
  const version=++requestVersion.current;
  try {const v=await request<{skills:Skill[];diagnostics?:{message:string}[]}>("/api/extensions/skills");if(version!==requestVersion.current)return;setSkills(v.skills??[]);setLoaded(true);setError(v.diagnostics?.map(d=>d.message).join("；")??"");}catch(e){if(version===requestVersion.current)setError(message(e));}finally{if(version===requestVersion.current)setLoading(false);}
 },[]);
 useEffect(()=>{void refresh();return()=>{requestVersion.current++;};},[refresh,refreshToken]);
 useEffect(()=>{const focus=()=>{if(!document.hidden)void refresh();};window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);return()=>{window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus);};},[refresh]);
 async function act(fn:()=>Promise<unknown>,success="已保存") {
  if(busy)return;setBusy(true);setError("");setNotice("");try{await fn();setNotice(success);await refresh();}catch(e){setError(message(e));}finally{setBusy(false);}
 }
 return <div className="extension-panel">
 {error&&!skillForm&&<p className="error-banner" role="alert">{error}</p>}{notice&&<p className="extension-notice" role="status">{notice}</p>}
   <div className="extension-toolbar"><span className="extension-global">工作区共享 · 按需读取</span><button className="secondary-button" disabled={busy} onClick={()=>setSkillForm(!skillForm)}><TofiIcon name="plus" size={16}/>安装 Skill</button></div>
   {skillForm&&<form className="extension-form" aria-busy={busy} onSubmit={e=>{e.preventDefault();if(busy)return;if(!/^[a-zA-Z0-9_\-]+$/.test(skillName)||!skillText.trim()){setError("请选择含 SKILL.md 的文件夹，或展开手动编辑填写名称和内容。");return;}void act(async()=>{await request("/api/extensions/skills",post({name:skillName,files:{...skillFiles,"SKILL.md":skillText}}));setSkillForm(false);setSkillFiles({});setSkillText("");setSkillName("");});}}>
    <fieldset className="form-fields" disabled={busy}>
    <div className="skill-folder-picker"><input aria-label="选择 Skill 文件夹" hidden type="file" multiple ref={el=>{folderInput.current=el;el?.setAttribute("webkitdirectory","")}} onChange={async e=>{try{const files=Array.from(e.target.files??[]);if(files.length>256)throw new Error("最多 256 个文件");let total=0;const result:Record<string,string>={};for(const f of files){total+=f.size;if(total>1024*1024)throw new Error("Skill 内容最多 1 MB");const path=f.webkitRelativePath.split("/").slice(1).join("/")||f.name;result[path]=await f.text();}if(!result["SKILL.md"])throw new Error("文件夹根目录需要 SKILL.md");setSkillFiles(result);setSkillText(result["SKILL.md"]);setSkillName(files[0]?.webkitRelativePath.split("/")[0]??"");setError("");}catch(cause){setError(message(cause));}}}/><button type="button" className="secondary-button" onClick={()=>folderInput.current?.click()}><TofiIcon name="folder-open" size={20}/>选择文件夹</button><span>{Object.keys(skillFiles).length?`${skillName} · ${Object.keys(skillFiles).length} 个文件`:"包含 SKILL.md"}</span></div>
    <Disclosure title="手动编辑">
    <label>名称<input value={skillName} onChange={e=>setSkillName(e.target.value)} placeholder="research-assistant"/></label>
    <label>SKILL.md<textarea rows={9} value={skillText} onChange={e=>setSkillText(e.target.value)} placeholder={'---\nname: research-assistant\ndescription: Research and verify sources\n---\nWorkflow instructions…'}/></label>
    </Disclosure>
    <p className="field-note">支持说明与参考文件；不自动运行附带脚本。</p>
    {error&&<p className="error-text" role="alert">{error}</p>}
    <div className="extension-actions"><button disabled={busy} type="submit" className="primary-button">{busy?"安装中…":"安装"}</button><button type="button" disabled={busy} onClick={()=>setSkillForm(false)}>取消</button></div>
    </fieldset>
   </form>}

   {loading&&!loaded&&<p className="field-note" role="status">正在读取…</p>}
   {!skillForm&&loaded&&skillCatalog.some(item=>!skills.some(s=>s.name===item.name))&&<section className="integration-section skill-starters"><h3>Tofi 工作技能</h3><div className="integration-grid">{skillCatalog.filter(item=>!skills.some(s=>s.name===item.name)).map(skill=><button className="integration-tile" disabled={busy||skills.some(s=>s.name===skill.name)} key={skill.name} onClick={()=>void act(()=>request("/api/extensions/skills",post({name:skill.name,files:{"SKILL.md":skill.body}})),"已安装，所有 Bot 均可按需读取")}><span className="service-mark"><TofiIcon name="skill" size={20}/></span><span><strong>{skill.title}</strong><small>{skill.description}</small></span>{skills.some(s=>s.name===skill.name)?<span className="integration-added">已安装</span>:<TofiIcon name="plus" size={16}/>}</button>)}</div></section>}
   <div className="extension-list">{skills.map(skill=><article key={skill.name} className="extension-card"><strong>{skillCatalog.find(item=>item.name===skill.name)?.title??skill.name}</strong><p>{skillCatalog.find(item=>item.name===skill.name)?.description??skill.description}</p><div className="extension-actions"><ConfirmAction label="移除" question="移除这个 Skill？" disabled={busy} onConfirm={()=>act(()=>request(`/api/extensions/skills/${encodeURIComponent(skill.name)}`,{method:"DELETE"}),"Skill 已移除")}/></div></article>)}</div>
 {(error||debug)&&<button className="extension-refresh text-button" disabled={busy||loading} onClick={()=>{setLoading(true);void refresh()}}>重新载入</button>}
 </div>;
}
