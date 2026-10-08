import {useEffect,useState} from "react";
import {request} from "./api";
import {useDebugMode,setDebugMode} from "./debugMode";
import type {Bot,Conversation} from "./types";
import {useTranslation} from "./i18n";
import {formatDateTime} from "./i18n/format";
type ToolDefinition={name:string;description:string;parameters:Record<string,unknown>};
type Preview={system_prompt:string;tools:ToolDefinition[];last_run_tools?:ToolDefinition[];last_run_at?:string};
export function BotInspector({botId,conversationId,tools=false}:{botId:string;conversationId?:string;tools?:boolean}){
 const {t}=useTranslation("bots");
 const [open,setOpen]=useState(tools);const [data,setData]=useState<Preview|null>(null);const [error,setError]=useState("");const [version,setVersion]=useState(0);const [copied,setCopied]=useState(false);const [query,setQuery]=useState("");
 useEffect(()=>{setData(null);setError("");setCopied(false);if(!open)return;const ac=new AbortController();const search=conversationId?`?conversation_id=${encodeURIComponent(conversationId)}`:"";request<Preview>(`/api/bots/${encodeURIComponent(botId)}/debug-preview${search}`,{signal:ac.signal}).then(setData).catch(()=>{if(!ac.signal.aborted)setError(t("inspector.error.read"))});return()=>ac.abort()},[botId,conversationId,open,version,t]);
 async function copy(){try{await navigator.clipboard.writeText(data?.system_prompt??"");setCopied(true)}catch{setError(t("inspector.error.copy"))}}
 const definitions=data?.last_run_tools?.length?data.last_run_tools:data?.tools??[];
 return <details className="bot-inspector" open={open} onToggle={e=>setOpen(e.currentTarget.open)}><summary>{tools?t("inspector.tools"):t("inspector.system_prompt")}</summary>{open&&<div className="inspector-body">{error&&<p role="alert" className="error-text">{error} <button type="button" className="text-button" onClick={()=>setVersion(v=>v+1)}>{t("inspector.retry")}</button></p>}{!data&&!error&&<p className="muted">{t("inspector.loading")}</p>}{data&&(tools?<><p className="field-note">{data.last_run_tools?.length?t("inspector.last_run",{time:data.last_run_at?formatDateTime(data.last_run_at):""}):t("inspector.builtin_note")}</p><input aria-label={t("inspector.search")} placeholder={t("inspector.search")} value={query} onChange={e=>setQuery(e.target.value)}/><div className="debug-tools">{definitions.filter(tool=>(tool.name+" "+tool.description).toLowerCase().includes(query.toLowerCase())).map(tool=><details key={tool.name}><summary>{tool.name}</summary><p>{tool.description}</p><pre>{JSON.stringify(tool.parameters,null,2)}</pre></details>)}</div></>:<><div className="inspector-caption"><span>{t("inspector.preview_caption")}</span><button type="button" className="text-button" onClick={()=>void copy()}>{copied?t("inspector.copied"):t("inspector.copy")}</button></div><pre className="prompt-preview" tabIndex={0}>{data.system_prompt}</pre><p className="field-note">{t("inspector.runtime_note")}</p></>)}</div>}</details>
}
export function DebugSettings({bots,conversation}:{bots:Bot[];conversation?:Conversation}){
 const {t}=useTranslation("bots");
 const debug=useDebugMode();const [chosen,setChosen]=useState("");const botId=bots.some(b=>b.id===chosen)?chosen:bots[0]?.id??"";
 return <section className="settings-section"><label className="debug-toggle"><span>{t("debug.mode")}<small>{t("debug.mode_hint")}</small></span><input type="checkbox" role="switch" checked={debug} onChange={e=>setDebugMode(e.target.checked)}/></label>{debug&&<><label>Bot<select value={botId} onChange={e=>setChosen(e.target.value)}>{bots.map(bot=><option key={bot.id} value={bot.id}>{bot.name}</option>)}</select></label>{botId?<BotInspector key={botId} botId={botId} conversationId={conversation&&(conversation.bot_id===botId||conversation.bot_ids.includes(botId))?conversation.id:undefined} tools/>:<p className="muted">{t("debug.no_bots")}</p>}</>}</section>
}
