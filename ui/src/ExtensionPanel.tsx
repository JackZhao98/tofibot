import { useCallback, useEffect, useRef, useState } from "react";
import type { Bot } from "./types";
import { request } from "./api";
import { ConfirmAction, Disclosure } from "./InteractionSystem";
import { useDebugMode } from "./debugMode";
import { TofiIcon } from "./icons";
import { skillCatalog } from "./skillCatalog";
import { MCPSettings } from "./MCPSettings";
import { Banner } from "./settings/components";
import { SkillAccessRow, SkillAccessSheet, type SkillAccess } from "./settings/SkillAccess";
import { i18n, useTranslation } from "./i18n";
import { CATS, EmptyState } from "./EmptyCat";
import "./extensions-system.css";

type Skill = {name:string; description:string; access?:SkillAccess};
const message = (error: unknown) => {
 const value=error instanceof Error?error.message:String(error);
 if(value==="skill already exists")return i18n.t("extensions:skills.error.name_taken");
 return value;
};
const post = (body:unknown) => ({method:"POST", body:JSON.stringify(body)});

export function ExtensionPanel(props:{bots:Bot[];kind:"mcp"|"skills";refreshToken?:number}) {
 return props.kind === "mcp" ? <MCPSettings bots={props.bots} refreshToken={props.refreshToken}/> : <SkillsSettings bots={props.bots} refreshToken={props.refreshToken}/>;
}
function SkillsSettings({bots,refreshToken=0}:{bots:Bot[];refreshToken?:number}) {
 const { t } = useTranslation("extensions");
 const debug=useDebugMode();
 const [skills,setSkills]=useState<Skill[]>([]);
 const [loaded,setLoaded]=useState(false); const [loading,setLoading]=useState(true);
 const [error,setError]=useState(""); const [notice,setNotice]=useState(""); const [busy,setBusy]=useState(false);
 const [skillForm,setSkillForm]=useState(false); const [skillName,setSkillName]=useState(""); const [skillText,setSkillText]=useState("");
 const [skillFiles,setSkillFiles]=useState<Record<string,string>>({});
 const [accessSkill,setAccessSkill]=useState<Skill|null>(null);
 const requestVersion=useRef(0);
 const folderInput=useRef<HTMLInputElement>(null);
 const refresh=useCallback(async()=>{
  const version=++requestVersion.current;
  try {const v=await request<{skills:Skill[];diagnostics?:{message:string}[]}>("/api/extensions/skills");if(version!==requestVersion.current)return;setSkills(v.skills??[]);setLoaded(true);setError(v.diagnostics?.map(d=>d.message).join(t("diagnostics_separator"))??"");}catch(e){if(version===requestVersion.current)setError(message(e));}finally{if(version===requestVersion.current)setLoading(false);}
 },[]);
 useEffect(()=>{void refresh();return()=>{requestVersion.current++;};},[refresh,refreshToken]);
 useEffect(()=>{const focus=()=>{if(!document.hidden)void refresh();};window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);return()=>{window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus);};},[refresh]);
 async function act(fn:()=>Promise<unknown>,success?:string) {
  if(busy)return;setBusy(true);setError("");setNotice("");try{await fn();setNotice(success??t("skills.saved"));await refresh();}catch(e){setError(message(e));}finally{setBusy(false);}
 }
 return <div className="extension-panel">
 {error&&!skillForm&&<Banner tone="error" title={error}/>}{notice&&<p className="extension-notice" role="status">{notice}</p>}
   <div className="extension-toolbar"><span className="extension-global">{t("skills.scope")}</span><button className="secondary-button" disabled={busy} onClick={()=>setSkillForm(!skillForm)}><TofiIcon name="plus" size={16}/>{t("skills.install")}</button></div>
   {skillForm&&<form className="extension-form" aria-busy={busy} onSubmit={e=>{e.preventDefault();if(busy)return;if(!/^[a-zA-Z0-9_\-]+$/.test(skillName)||!skillText.trim()){setError(t("skills.error.missing_content"));return;}void act(async()=>{await request("/api/extensions/skills",post({name:skillName,files:{...skillFiles,"SKILL.md":skillText}}));setSkillForm(false);setSkillFiles({});setSkillText("");setSkillName("");});}}>
    <fieldset className="form-fields" disabled={busy}>
    <div className="skill-folder-picker"><input aria-label={t("skills.folder_label")} hidden type="file" multiple ref={el=>{folderInput.current=el;el?.setAttribute("webkitdirectory","")}} onChange={async e=>{try{const files=Array.from(e.target.files??[]);if(files.length>256)throw new Error(t("skills.error.too_many_files", { max: 256 }));let total=0;const result:Record<string,string>={};for(const f of files){total+=f.size;if(total>1024*1024)throw new Error(t("skills.error.too_large"));const path=f.webkitRelativePath.split("/").slice(1).join("/")||f.name;result[path]=await f.text();}if(!result["SKILL.md"])throw new Error(t("skills.error.no_skill_md"));setSkillFiles(result);setSkillText(result["SKILL.md"]);setSkillName(files[0]?.webkitRelativePath.split("/")[0]??"");setError("");}catch(cause){setError(message(cause));}}}/><button type="button" className="secondary-button" onClick={()=>folderInput.current?.click()}><TofiIcon name="folder-open" size={20}/>{t("skills.choose_folder")}</button><span>{Object.keys(skillFiles).length?`${skillName} · ${t("skills.file_count", { count: Object.keys(skillFiles).length })}`:t("skills.needs_skill_md")}</span></div>
    <Disclosure title={t("skills.manual")}>
    <label>{t("skills.name")}<input value={skillName} onChange={e=>setSkillName(e.target.value)} placeholder="research-assistant"/></label>
    <label>SKILL.md<textarea rows={9} value={skillText} onChange={e=>setSkillText(e.target.value)} placeholder={'---\nname: research-assistant\ndescription: Research and verify sources\n---\nWorkflow instructions…'}/></label>
    </Disclosure>
    <p className="field-note">{t("skills.support_note")}</p>
    {error&&<p className="error-text" role="alert">{error}</p>}
    <div className="extension-actions"><button disabled={busy} type="submit" className="primary-button">{busy?t("skills.installing"):t("skills.install_submit")}</button><button type="button" className="secondary-button" disabled={busy} onClick={()=>setSkillForm(false)}>{t("action.cancel")}</button></div>
    </fieldset>
   </form>}

   {loading&&!loaded&&<p className="field-note" role="status">{t("skills.loading")}</p>}
   {!skillForm&&loaded&&skillCatalog.some(item=>!skills.some(s=>s.name===item.name))&&<section className="integration-section skill-starters"><h3>{t("skills.starters")}</h3><div className="integration-grid">{skillCatalog.filter(item=>!skills.some(s=>s.name===item.name)).map(skill=><button className="integration-tile" disabled={busy||skills.some(s=>s.name===skill.name)} key={skill.name} onClick={()=>void act(()=>request("/api/extensions/skills",post({name:skill.name,files:{"SKILL.md":skill.body}})),t("skills.installed_notice"))}><span className="service-mark"><TofiIcon name="skill" size={20}/></span><span><strong>{skill.title}</strong><small>{skill.description}</small></span>{skills.some(s=>s.name===skill.name)?<span className="integration-added">{t("skills.installed")}</span>:<TofiIcon name="plus" size={16}/>}</button>)}</div></section>}
   {!skillForm&&loaded&&!error&&!skills.length&&<EmptyState name="skills-empty" className="extension-empty" look={CATS.taro} pose="curious" size={72} title={t("skills.empty")}><p>{t("skills.empty_hint")}</p></EmptyState>}
   <div className="extension-list">{skills.map(skill=><article key={skill.name} className="extension-card"><strong>{skillCatalog.find(item=>item.name===skill.name)?.title??skill.name}</strong><p>{skillCatalog.find(item=>item.name===skill.name)?.description??skill.description}</p><div className="skill-access"><SkillAccessRow skill={skill.name} access={skill.access??{mode:"all"}} bots={bots} disabled={busy} onChange={()=>setAccessSkill(skill)}/></div><div className="extension-actions"><ConfirmAction label={t("skills.remove")} question={t("skills.remove_confirm")} disabled={busy} onConfirm={()=>act(()=>request(`/api/extensions/skills/${encodeURIComponent(skill.name)}`,{method:"DELETE"}),t("skills.removed"))}/></div></article>)}</div>
 {accessSkill&&<SkillAccessSheet skill={accessSkill.name} title={skillCatalog.find(item=>item.name===accessSkill.name)?.title??accessSkill.name} access={accessSkill.access??{mode:"all"}} bots={bots} onClose={()=>setAccessSkill(null)} onSaved={()=>{setAccessSkill(null);setNotice(t("skills.saved"));void refresh();}}/>}
 {(error||debug)&&<button className="extension-refresh text-button" disabled={busy||loading} onClick={()=>{setLoading(true);void refresh()}}>{t("skills.reload")}</button>}
 </div>;
}
