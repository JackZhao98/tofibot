import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { VMOAuthDialog, type VMOAuthSession } from "./VMOAuthDialog";
import { Disclosure } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import { IntegrationBrowser, ServiceMark } from "./IntegrationBrowser";
import { googleAPIEnableURL, googleAudienceURL, integrationCatalog, type IntegrationPreset } from "./integrationCatalog";
import { isDesktop } from "./desktop";
import { mcpOAuthRoute, type OAuthOptions, type OAuthRoute } from "./mcpOAuthRoute";
import { CatStage, type CatHandle } from "./CatStage";
import { LocalMCPPanel } from "./LocalMCPPanel";
import { Banner } from "./settings/components";
import { mcpTokenHeaders } from "./mcpTokenHeaders";
import { i18n, useTranslation } from "./i18n";
import "./mcp-test-motion.css";
import "./oauth-link-motion.css";

function OAuthLinkMotion({linked,title}:{linked:boolean;title:string}) {
 const { t } = useTranslation("extensions");
 const cat=useRef<CatHandle>(null);
 const mounted=useRef(linked);
 useEffect(()=>{
  if(linked&&!mounted.current){
   const timer=window.setTimeout(()=>{const handle=cat.current;if(handle)void handle.play("wake").then(()=>handle.play("happy"));},620);
   mounted.current=linked;
   return()=>window.clearTimeout(timer);
  }
  if(!linked&&mounted.current)void cat.current?.play("sleep");
  mounted.current=linked;
 },[linked]);
 return <div className={`web-oauth-link${linked?" is-linked":""}`} aria-label={linked?t("oauth.linked",{title}):t("oauth.waiting",{title})}><span className="web-oauth-node"><CatStage config={{shape:"curl",pattern:"calico",palette:"calico"}} initialState={linked?"awake":"asleep"} ref={cat}/></span><svg viewBox="0 0 200 60" aria-hidden="true"><path className="web-oauth-wait" d="M6 44 Q100 -6 194 44"/><path className="web-oauth-done" d="M6 44 Q100 -6 194 44" pathLength={1}/></svg><span className="web-oauth-provider"><TofiIcon name="key" size={22}/><small>{title}</small><i><TofiIcon name="check" size={12} variant="filled"/></i></span></div>;
}

function MCPTestMotion({phase}:{phase:"checking"|"ready"|"error"}) {
 const plug=useRef<SVGGElement>(null);
 const approach=useRef<Animation|null>(null);
 const sequence=useRef(0);
 const [visualPhase,setVisualPhase]=useState<"checking"|"ready"|"error">("checking");
 useEffect(()=>{
  const element=plug.current;
  if(!element)return;
  const id=++sequence.current;
  if(window.matchMedia("(prefers-reduced-motion: reduce)").matches){setVisualPhase(phase);return;}
  const startApproach=()=>element.animate({transform:["translateX(0)","translateX(10px)","translateX(8px)","translateX(15px)"],offset:[0,.5,.62,1]},{duration:1000,easing:"ease-in-out",fill:"forwards"});
  if(phase==="checking"){
   element.getAnimations().forEach(animation=>animation.cancel());
   setVisualPhase("checking");
   approach.current=startApproach();
   return;
  }
  if(!approach.current)approach.current=startApproach();
  void (async()=>{
   try{await approach.current?.finished;}catch{return;}
   if(sequence.current!==id)return;
   const result=phase==="ready"
    ?element.animate({transform:["translateX(15px)","translateX(22px)"]},{duration:120,easing:"cubic-bezier(.6,0,1,1)",fill:"forwards"})
    :element.animate({transform:["translateX(15px)","translateX(19px)","translateX(-5px)","translateX(2px)","translateX(0)"],offset:[0,.15,.55,.8,1]},{duration:620,easing:"ease-out",fill:"forwards"});
   try{await result.finished;}catch{return;}
   if(sequence.current===id)setVisualPhase(phase);
  })();
 },[phase]);
 useEffect(()=>()=>{sequence.current++;plug.current?.getAnimations().forEach(animation=>animation.cancel());},[]);
 return <svg className={`web-mcp-plug is-${visualPhase}`} viewBox="0 0 170 56" aria-hidden="true"><g ref={plug}><path className="web-mcp-cable" d="M-20 28 C 10 28, 22 28, 46 28"/><rect className="web-mcp-body" x="46" y="13" width="36" height="30" rx="8"/><rect className="web-mcp-prong" x="82" y="19" width="16" height="5" rx="1.5"/><rect className="web-mcp-prong" x="82" y="32" width="16" height="5" rx="1.5"/></g><rect className="web-mcp-socket" x="112" y="8" width="44" height="40" rx="10"/><rect className="web-mcp-hole" x="112" y="19" width="9" height="5"/><rect className="web-mcp-hole" x="112" y="32" width="9" height="5"/><g className="web-mcp-sparks"><path d="M108 8 l-6 -6"/><path d="M104 28 h-9"/><path d="M108 48 l-6 6"/></g></svg>;
}

type OAuth = {client_id:string;client_secret?:string;scopes?:string[];auth_server_metadata_url?:string;connected?:boolean};
type MCPTransport = "streamable_http"|"sse";
type MCP = {name:string;url:string;transport?:MCPTransport;headers?:Record<string,string>;tool_allowlist?:string[];tool_denylist?:string[];oauth?:OAuth};
const message=(e:unknown)=>{
 const text=e instanceof Error?e.message:String(e);
 if(text==="Failed to fetch")return i18n.t("extensions:error.offline");
 if(text==="MCP server connection or discovery failed")return i18n.t("extensions:error.discovery_failed");
 if(text.includes("HTTP redirects must use localhost"))return i18n.t("extensions:error.redirect_needs_https");
 if(text.includes("OAuth discovery or client registration failed"))return i18n.t("extensions:error.oauth_discovery_failed");
 if(text.startsWith("OAuth connection could not be started"))return i18n.t("extensions:error.oauth_start_failed");
 if(text==="MCP endpoint changed; re-enter or remove saved headers before saving")return i18n.t("extensions:error.endpoint_changed_headers");
 if(text==="MCP endpoint changed; explicitly configure OAuth for the new endpoint before saving")return i18n.t("extensions:error.endpoint_changed_oauth");
 if(text==="OAuth metadata destination changed; re-enter or remove the saved client secret before saving")return i18n.t("extensions:error.metadata_changed_secret");
 return text;
};
/** Protocol names, not translated. */
const transportLabels:Record<MCPTransport,string>={streamable_http:"Streamable HTTP",sse:"SSE"};
const post=(body:unknown)=>({method:"POST",body:JSON.stringify(body)});
const words=(value:string)=>value.split(/[,\n]/).map(x=>x.trim()).filter(Boolean);
const endpoint=(name:string)=>`/api/extensions/mcp/${encodeURIComponent(name)}`;
const presetFor=(server:MCP)=>integrationCatalog.find(item=>item.url===server.url);

export function MCPSettings({refreshToken=0,bots=[]}:{refreshToken?:number;bots?:Bot[]}) {
 const { t } = useTranslation(["extensions","common"]);
 const [options,setOptions]=useState<OAuthOptions|null>(null);
 useEffect(()=>{let alive=true;void request<OAuthOptions>("/api/extensions/oauth-options").then(result=>{if(alive)setOptions(result);}).catch(()=>{if(alive)setOptions({vm_available:false,web_callback_origin:"",desktop_redirect_uri:""});});return()=>{alive=false;};},[]);
 const route=mcpOAuthRoute(window.location.protocol,isDesktop,Boolean(window.tofiDesktop?.authorizeMCP),options);
 const [servers,setServers]=useState<MCP[]>([]);
 const [loaded,setLoaded]=useState(false);
 const [listError,setListError]=useState("");
 const [browse,setBrowse]=useState(false);
 const [query,setQuery]=useState("");
 const [editor,setEditor]=useState<{server:MCP|null;preset?:IntegrationPreset}|null>(null);
 const [saving,setSaving]=useState(false);
 const [checks,setChecks]=useState<Record<string,number>>({});
 const version=useRef(0);
 const refresh=useCallback(async()=>{
  const current=++version.current;
  try {const value=await request<{servers:MCP[]}>("/api/extensions/mcp");if(current!==version.current)return;setServers(value.servers??[]);setListError("");setLoaded(true);}
  catch(e){if(current===version.current){setListError(message(e));setLoaded(true);}}
 },[]);
 useEffect(()=>{void refresh();return()=>{version.current++;};},[refresh,refreshToken]);
 useEffect(()=>{const focus=()=>{if(!document.hidden)void refresh();};window.addEventListener("focus",focus);document.addEventListener("visibilitychange",focus);return()=>{window.removeEventListener("focus",focus);document.removeEventListener("visibilitychange",focus);};},[refresh]);
 async function save(value:MCP){
  setSaving(true);
  try {
   await request("/api/extensions/mcp",{method:editor?.server?"PUT":"POST",body:JSON.stringify(value)});
   // Keep the saved row visible immediately, even if refreshing the full list fails.
   setServers(items=>[...items.filter(item=>item.name!==value.name),value]);
   setChecks(values=>({...values,[value.name]:(values[value.name]??0)+1}));
   setEditor(null);setBrowse(false);await refresh();
  } finally {setSaving(false);}
 }
 return <div className="extension-panel mcp-settings">
  {editor?<><button className="extension-back" disabled={saving} onClick={()=>setEditor(null)}><TofiIcon name="arrow-left" size={16}/>{t("mcp.back")}</button><MCPForm key={editor.server?.name??editor.preset?.id??"new"} initial={editor.server} preset={editor.preset} options={options} route={route} busy={saving} onSave={save} onCancel={()=>setEditor(null)}/></>:browse?<IntegrationBrowser query={query} servers={servers} onQueryChange={setQuery} onBack={()=>setBrowse(false)} onSelect={preset=>setEditor({server:null,preset})} onCustom={()=>setEditor({server:null})}/>:<>
   {!isDesktop&&<LocalMCPPanel onChanged={refresh} callbackOrigin={options?.web_callback_origin} attachedIDs={servers.filter(server=>server.name.startsWith("local_")).map(server=>server.name.slice(6))}/>}
   <div className="mcp-toolbar"><h3>{t("mcp.added")} <span>{servers.length||""}</span></h3><button className="mcp-button" onClick={()=>setBrowse(true)}><TofiIcon name="plus" size={16}/>{t("mcp.add")}</button></div>
   {listError&&<div className="mcp-list-error" role="alert"><span>{listError}</span><button className="mcp-button" onClick={()=>void refresh()}>{t("common:action.retry")}</button></div>}
   {!loaded&&<p className="mcp-placeholder">{t("mcp.loading")}</p>}
   {loaded&&!listError&&servers.length===0&&<div className="extension-empty"><TofiIcon name="plug" size={24}/><h3>{t("mcp.empty")}</h3></div>}
   <div className="mcp-rows">{servers.map(server=><MCPRow key={server.name} route={route} botId={bots.find(bot=>!bot.archived)?.id} server={server} checkRequest={checks[server.name]??0} refresh={refresh} onEdit={()=>setEditor({server,preset:presetFor(server)})} onRemoved={()=>{setServers(items=>items.filter(item=>item.name!==server.name));void refresh();}}/>)}</div>
  </>}
 </div>;
}

type Phase="idle"|"checking"|"ready"|"auth"|"authorizing"|"waiting"|"error"|"removing";
function MCPRow({server,checkRequest,refresh,onEdit,onRemoved,route,botId}:{route:OAuthRoute;botId?:string;server:MCP;checkRequest:number;refresh:()=>Promise<void>;onEdit:()=>void;onRemoved:()=>void}) {
 const { t } = useTranslation(["extensions","common"]);
 const [phase,setPhase]=useState<Phase>(server.oauth&&!server.oauth.connected?"auth":"idle");
 const [testAttempted,setTestAttempted]=useState(false);
 const [count,setCount]=useState<number>();
 const [error,setError]=useState("");
 const [authURL,setAuthURL]=useState("");
 const [vmSession,setVMSession]=useState<VMOAuthSession|null>(null);
 const [menu,setMenu]=useState(false);
 const [confirm,setConfirm]=useState<"remove"|"disconnect"|null>(null);
 const alive=useRef(true);const operation=useRef(0);const abort=useRef<AbortController|null>(null);
 const menuRef=useRef<HTMLDivElement>(null);const trigger=useRef<HTMLButtonElement>(null);
 const previouslyConnected=useRef(server.oauth?.connected);
 const authorizationPopup=useRef<Window|null>(null);
 const nativePending=useRef(false);
 const preset=presetFor(server);
 const display=preset?.name??server.name;
 const busy=vmSession!==null||(route.mode==="desktop"&&phase==="waiting")||["checking","authorizing","removing"].includes(phase);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;operation.current++;abort.current?.abort();if(nativePending.current)void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(()=>{});};},[server.name]);
 useEffect(()=>{
  if(!menu)return;
  // Dismiss this topmost surface before Settings' document-level Escape handler.
  const close=(event:MouseEvent|KeyboardEvent)=>{if(event instanceof KeyboardEvent){if(event.key!=="Escape"||event.defaultPrevented)return;event.preventDefault();event.stopPropagation();setMenu(false);trigger.current?.focus();}else if(!menuRef.current?.contains(event.target as Node))setMenu(false);};
  document.addEventListener("mousedown",close);document.addEventListener("keydown",close,true);
  return()=>{document.removeEventListener("mousedown",close);document.removeEventListener("keydown",close,true);};
 },[menu]);
 const check=useCallback(async()=>{
  const id=++operation.current;abort.current?.abort();const controller=new AbortController();abort.current=controller;
  setTestAttempted(true);
  setError("");setPhase("checking");
  try {
   const result=await request<{ok:boolean;tool_count?:number;auth_required?:boolean;diagnostics?:{message:string}[]}>(endpoint(server.name)+"/test",{...post({}),signal:controller.signal});
   if(!alive.current||operation.current!==id)return;
   if(result.ok){setCount(result.tool_count??0);setPhase("ready");setAuthURL("");}
   else if(result.auth_required){setPhase("auth");setCount(undefined);}
   else {setPhase("error");setError(result.diagnostics?.map(item=>message(item.message)).join(t("diagnostics_separator"))||t("error.tools_failed"));}
  } catch(e){if(alive.current&&operation.current===id){setPhase("error");setError(message(e));}}
 },[server.name,server.oauth?.connected]);
 useEffect(()=>{if(checkRequest>0)void check();},[checkRequest,check]);
 useEffect(()=>{if(!vmSession&&!previouslyConnected.current&&server.oauth?.connected&&checkRequest===0){setAuthURL("");void check();}else if(previouslyConnected.current&&!server.oauth?.connected){setCount(undefined);setPhase("auth");}previouslyConnected.current=server.oauth?.connected;},[server.oauth?.connected,checkRequest,check,vmSession]);
 useEffect(()=>{
  if(phase!=="waiting"||vmSession||isDesktop)return;
  const timer=window.setInterval(()=>{
   const popup=authorizationPopup.current;
   try {
    if(popup&&!popup.closed&&popup.location.origin===window.location.origin&&popup.location.pathname===endpoint(server.name)+"/oauth/callback") {
     if(popup.document.title==="Connection failed") {setPhase("auth");setError(i18n.t("extensions:error.auth_incomplete"));return;}
     if(popup.document.title==="Connection complete") {authorizationPopup.current=null;void refresh();void check();return;}
    }
   } catch { /* The provider page is cross-origin until it returns to Tofi. */ }
   if(!document.hidden)void refresh();
  },2000);
  const timeout=window.setTimeout(()=>{setPhase("auth");setError(i18n.t("extensions:error.auth_timeout"));},10*60*1000);
  return()=>{window.clearInterval(timer);window.clearTimeout(timeout);};
 },[phase,refresh,check,server.name,vmSession]);
 async function authorize(){
  const id=++operation.current;abort.current?.abort();const controller=new AbortController();abort.current=controller;
  setError("");setAuthURL("");setPhase("authorizing");setMenu(false);
  if(route.mode==="blocked"){setPhase("auth");setError(route.note);return;}
  if(route.mode==="desktop"){
   nativePending.current=true;setPhase("waiting");
   try {
    await window.tofiDesktop!.authorizeMCP!(server.name);
    if(alive.current&&id===operation.current){await refresh();void check();}
   } catch(e){if(alive.current&&id===operation.current){setPhase("auth");setError(message(e));}}
   finally {nativePending.current=false;}
   return;
  }
  if(route.mode==="vm"){
   try {
    if(!botId)throw new Error(t("error.needs_bot"));
    const result=await request<VMOAuthSession>(endpoint(server.name)+"/oauth/vm/start",{...post({bot_id:botId}),signal:controller.signal});
    if(!alive.current||id!==operation.current){void request(endpoint(server.name)+`/oauth/vm/${encodeURIComponent(result.session_id)}/cancel`,{...post({}),keepalive:true}).catch(()=>{});return;}
    setVMSession(result);setPhase("waiting");
   } catch(e){if(alive.current&&id===operation.current){setPhase("auth");setError(message(e));}}
   return;
  }
  // Open during the user gesture; asynchronous window.open is blocked by many browsers.
  let popup:Window|null=null;
  try {
   popup=window.open(endpoint(server.name)+"/oauth/pending","_blank");authorizationPopup.current=popup;if(popup)popup.opener=null;
   const result=await request<{authorization_url:string}>(endpoint(server.name)+"/oauth/start",{...post({}),signal:controller.signal});
   if(!alive.current||id!==operation.current){popup?.close();return;}
   const url=new URL(result.authorization_url);if(!["https:","http:"].includes(url.protocol))throw new Error(t("error.invalid_auth_url"));
   setAuthURL(url.href);setPhase("waiting");if(popup&&!popup.closed)popup.location.replace(url.href);
  } catch(e){popup?.close();if(alive.current&&id===operation.current){setPhase("auth");setError(message(e));}}
 }
 async function confirmAction(){
  const id=++operation.current;setPhase("removing");setError("");
  try {await request(endpoint(server.name)+(confirm==="disconnect"?"/oauth/disconnect":""),confirm==="disconnect"?post({}):{method:"DELETE"});if(!alive.current||id!==operation.current)return;if(confirm==="remove")onRemoved();else {setAuthURL("");setCount(undefined);setPhase("auth");await refresh();}setConfirm(null);}
  catch(e){if(alive.current&&id===operation.current){setPhase("error");setError(message(e));}}
 }
 const needsAuth=Boolean(server.oauth)&&(!server.oauth?.connected||phase==="auth");
 const status=phase==="checking"?t("status.checking"):phase==="ready"?t("status.tools",{count:count??0}):phase==="authorizing"?t("status.authorizing"):phase==="waiting"?t("status.waiting"):phase==="removing"?t("status.removing"):error?t("status.failed"):needsAuth?t("status.needs_auth"):t("status.unverified");
 return <article className="mcp-service" aria-label={display} data-state={error?"error":phase}>
  <div className="mcp-service-row"><ServiceMark name={display}/><div className="mcp-service-identity"><strong>{display}</strong><span className="mcp-service-status" role="status"><i aria-hidden="true"/>{status}</span></div>
   <div className="mcp-service-actions">{phase==="waiting"&&route.mode==="desktop"?<button className="mcp-button" onClick={()=>void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(e=>setError(message(e)))}>{t("mcp.cancel_auth")}</button>:phase==="waiting"&&authURL?<a className="mcp-button" href={authURL} target="_blank" rel="noopener noreferrer">{t("mcp.continue_auth")}</a>:needsAuth?<button className="mcp-button" disabled={busy||route.mode==="blocked"} onClick={()=>void authorize()}>{error?t("common:action.retry"):t("mcp.connect")}</button>:(phase==="idle"||phase==="error")?<button className="mcp-button" disabled={busy} onClick={()=>void check()}>{error?t("common:action.retry"):t("mcp.connect")}</button>:null}
   <div className="mcp-menu-anchor" ref={menuRef}><button ref={trigger} className="mcp-button mcp-menu-trigger" aria-label={t("mcp.more_actions",{name:display})} aria-expanded={menu} disabled={busy} onClick={()=>setMenu(value=>!value)}><TofiIcon name="more" size={20} variant={menu?"filled":"outline"}/></button>
    {menu&&<div className="mcp-menu"><button onClick={()=>{setMenu(false);onEdit();}}>{t("mcp.menu.edit")}</button><button onClick={()=>{setMenu(false);void check();}}>{t("mcp.menu.refresh")}</button>{server.oauth?.connected&&<button onClick={()=>{setMenu(false);setConfirm("disconnect");}}>{t("mcp.menu.disconnect")}</button>}<button className="mcp-menu-danger" onClick={()=>{setMenu(false);setConfirm("remove");}}>{t("mcp.menu.remove")}</button></div>}
   </div></div>
  </div>
  {!isDesktop&&server.oauth&&["authorizing","waiting","ready"].includes(phase)&&<OAuthLinkMotion linked={phase==="ready"&&Boolean(server.oauth.connected)} title={display}/>}
  {!isDesktop&&testAttempted&&["checking","ready","error"].includes(phase)&&<MCPTestMotion phase={phase as "checking"|"ready"|"error"}/>}
  {error&&<p className="mcp-service-error" role="alert">{error}</p>}
  {needsAuth&&<div className="mcp-login-note"><p role="status">{route.note}</p></div>}
  {preset?.googleAPIs&&<div className="mcp-service-setup"><Disclosure key={needsAuth||phase==="error"||error?"attention":"available"} title={t("google.help")} defaultOpen={Boolean(needsAuth||phase==="error"||error)}><GoogleSetupLinks preset={preset} docs/></Disclosure></div>}
  {vmSession&&<VMOAuthDialog session={vmSession} serverName={server.name} title={display} onFinish={(status,detail)=>{setVMSession(null);if(status==="complete"){void refresh();void check();}else{setPhase("auth");setError(detail||"");}}}/>}
  {confirm&&<div className="mcp-confirm"><span>{confirm==="remove"?t("mcp.confirm.remove",{name:display}):t("mcp.confirm.disconnect",{name:display})}</span><button className="mcp-button" disabled={busy} onClick={()=>setConfirm(null)}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={busy} onClick={()=>void confirmAction()}>{confirm==="remove"?t("mcp.confirm.remove_submit"):t("mcp.confirm.disconnect_submit")}</button></div>}
 </article>;
}
function OAuthLoginHelp({route}:{route:OAuthRoute}) {
 const { t } = useTranslation("extensions");
 return <Disclosure title={t("oauth.help_title")}><div className="mcp-login-help">
  <p>{route.note}</p>
  <p>{t("oauth.help_body")}</p>
 </div></Disclosure>;
}
function GoogleSetupLinks({preset,docs=false}:{preset:IntegrationPreset;docs?:boolean}) {
 const { t } = useTranslation("extensions");
 if(!preset.googleAPIs)return null;
 return <section className="mcp-google-setup" aria-label={t("google.links_label",{name:preset.name})}>
  <p>{t("google.project_hint")}</p>
  <nav aria-label={t("google.nav_label",{name:preset.name})}>
   <a href={googleAPIEnableURL(preset.googleAPIs.api)} target="_blank" rel="noopener noreferrer">{t("google.enable_api")}</a>
   <a href={googleAPIEnableURL(preset.googleAPIs.mcp)} target="_blank" rel="noopener noreferrer">{t("google.enable_mcp")}</a>
   <a href={googleAudienceURL} target="_blank" rel="noopener noreferrer">{t("google.test_users")}</a>
   {docs&&<a href={preset.docsURL} target="_blank" rel="noopener noreferrer">{t("google.docs")}</a>}
  </nav>
 </section>;
}
function CallbackAddress({label,value}:{label:string;value:string}) {
 const { t } = useTranslation("extensions");
 const [notice,setNotice]=useState("");
 async function copy(){try {await navigator.clipboard.writeText(value);setNotice(t("callback.copied"));}catch {setNotice(t("callback.copy_failed"));}}
 return <div className="integration-callback"><span>{label}</span><code>{value}</code><button className="mcp-button" type="button" onClick={()=>void copy()}>{t("callback.copy")}</button>{notice&&<small role="status">{notice}</small>}</div>;
}
function MCPForm({initial,preset,options,route,busy,onSave,onCancel}:{initial:MCP|null;preset?:IntegrationPreset;options:OAuthOptions|null;route:OAuthRoute;busy:boolean;onSave:(v:MCP)=>Promise<void>;onCancel:()=>void}) {
 const { t } = useTranslation("extensions");
 const [name,setName]=useState(initial?.name??preset?.id??"");
 const [url,setURL]=useState(initial?.url??preset?.url??"");
 const [transport,setTransport]=useState<MCPTransport>(initial?.transport??"streamable_http");
 const [headers,setHeaders]=useState(JSON.stringify(initial?.headers??{},null,2));
 const [token,setToken]=useState("");
 const [allow,setAllow]=useState(initial?.tool_allowlist?.join(", ")??"");
 const [deny,setDeny]=useState(initial?.tool_denylist?.join(", ")??"");
 const [oauth,setOAuth]=useState(initial?Boolean(initial.oauth):preset?.auth==="oauth");
 const [clientID,setClientID]=useState(initial?.oauth?.client_id??"");
 const [secret,setSecret]=useState(initial?.oauth?.client_secret??"");
 const [scopes,setScopes]=useState(initial?.oauth?.scopes?.join(", ")??preset?.scopes?.join(", ")??"");
 const [metadata,setMetadata]=useState(initial?.oauth?.auth_server_metadata_url??preset?.metadataURL??"");
 const [error,setError]=useState("");
 return <form className="extension-form integration-setup" aria-busy={busy} onSubmit={async e=>{e.preventDefault();if(busy)return;try{
  if(!/^[a-zA-Z0-9_.-]{1,64}$/.test(name)||name==="."||name==="..")throw new Error(t("form.name_invalid"));
  const parsed=mcpTokenHeaders(headers,token,preset);
  setError("");await onSave({name,url,transport,headers:parsed,tool_allowlist:words(allow),tool_denylist:words(deny),...(oauth?{oauth:{client_id:clientID,client_secret:secret,scopes:words(scopes),auth_server_metadata_url:metadata}}:{})});
 }catch(cause){setError(message(cause));}}}>
  <fieldset className="form-fields" disabled={busy}>
  <header className="integration-setup-heading"><ServiceMark name={preset?.name??initial?.name??"MCP"}/><div><h3>{preset?.name??(initial?t("form.edit_title"):t("form.custom_title"))}</h3><p>{preset?.description??t("form.custom_description")}</p></div></header>
  {preset&&<Disclosure title={t("form.guide")} defaultOpen={preset.category==="google"}><div className="integration-guide"><div><strong>{preset.category==="google"?t("form.google_prepare"):t("form.guide_heading")}</strong><a href={preset.docsURL} target="_blank" rel="noopener noreferrer">{preset.upstream==="community"?t("docs.community"):t("docs.vendor")} <TofiIcon name="external-link" size={16} style={{verticalAlign:"middle"}}/></a></div><ol>{preset.setup.map(step=><li key={step}>{step}</li>)}</ol>{preset.note&&<p>{preset.note}</p>}<GoogleSetupLinks preset={preset}/></div></Disclosure>}
  {oauth&&<OAuthLoginHelp route={route}/>}
  {!preset&&<label>{t("form.name")}<input required maxLength={64} disabled={Boolean(initial)} value={name} onChange={e=>setName(e.target.value)} placeholder="my-service"/></label>}
  {!preset&&<label>{t("form.url")}<input required type="url" value={url} onChange={e=>{setURL(e.target.value);setToken("");}} placeholder="https://example.com/mcp"/></label>}
  {preset?.auth==="token"&&<label>{t("form.token")}<input type="password" autoComplete="off" value={token} onChange={e=>setToken(e.target.value)} placeholder={initial?t("form.token_keep"):t("form.token_paste")} required={!initial}/></label>}
  {!preset&&<label className="extension-check"><input type="checkbox" checked={oauth} onChange={e=>setOAuth(e.target.checked)} disabled={Boolean(initial?.oauth)}/>{t("auth.oauth")}</label>}
  {oauth&&preset?.id!=="notion"&&<><label>{preset?.category!=="google"?t("form.client_id_optional"):t("form.client_id")}<input required={preset?.category==="google"} value={clientID} onChange={e=>setClientID(e.target.value)} autoComplete="off"/></label><label>{t("form.client_secret")}<input required={preset?.category==="google"} type="password" autoComplete="off" value={secret} onChange={e=>setSecret(e.target.value)} placeholder={t("form.client_secret_hint")}/></label>
   {options?.web_callback_origin&&<CallbackAddress label={t("callback.web")} value={`${options.web_callback_origin}${endpoint(name||t("form.name"))}/oauth/callback`}/>}
   {options?.desktop_redirect_uri&&<CallbackAddress label={t("callback.desktop")} value={options.desktop_redirect_uri}/>}
   {preset?.category==="google"&&<p className="field-note">{t("google.redirect_hint")}</p>}
  </>}
  <Disclosure title={t("form.advanced")}>
   {preset&&<label>{t("form.name")}<input required maxLength={64} disabled={Boolean(initial)} value={name} onChange={e=>setName(e.target.value)} placeholder="my-service"/></label>}
   {preset&&<label>{t("form.url")}<input required type="url" value={url} onChange={e=>{setURL(e.target.value);setToken("");}}/></label>}
   <label>{t("form.transport")}<select value={transport} disabled={busy} onChange={e=>setTransport(e.target.value as MCPTransport)}><option value="streamable_http">{transportLabels.streamable_http}</option><option value="sse">{transportLabels.sse}</option></select></label>
   <label>{t("form.headers")}<textarea rows={3} value={headers} onChange={e=>setHeaders(e.target.value)} spellCheck={false}/></label>{initial&&<p className="field-note">{t("form.headers_note")}</p>}
   {oauth&&<><label>{t("form.scopes")}<input value={scopes} onChange={e=>setScopes(e.target.value)} placeholder={t("form.comma_separated")}/></label><label>{t("form.metadata_url")}<input type="url" value={metadata} onChange={e=>setMetadata(e.target.value)}/></label></>}
   <label>{t("form.allowlist")}<input value={allow} onChange={e=>setAllow(e.target.value)} placeholder={t("form.allowlist_hint")}/></label><label>{t("form.denylist")}<input value={deny} onChange={e=>setDeny(e.target.value)} placeholder={t("form.comma_separated")}/></label>
  </Disclosure>
  {error&&<Banner tone="error" title={error}/>}
  <div className="extension-actions"><button className="primary-button" disabled={busy} type="submit">{busy?t("form.saving"):initial?t("form.save"):t("form.add")}</button><button disabled={busy} type="button" onClick={onCancel}>{t("action.cancel")}</button></div>
  </fieldset>
 </form>;
}
