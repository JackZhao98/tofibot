import {useEffect,useState} from "react";
import {request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {TofiIcon} from "./icons";
import {Banner,SettingsCard,SettingsRow,SettingsSection,StatusBadge} from "./settings/components";
import {isDesktop} from "./desktop";
import {i18n,useTranslation} from "./i18n";
import "./computer-pairing.css";
type Computer={id:string;name:string;kind:string;online:boolean};
type VMInfo={state:string;workspace_root?:string;browser?:string;error?:string;desktop_idle_seconds?:number};
/** Server computer states (internal/computer) to the words the page shows; hibernated computers wake on their own. */
const ENV_STATES:Record<string,"ready"|"starting"|"resuming"|"asleep"|"stopped"|"failed">={ready:"ready",starting:"starting",resuming:"resuming",hibernating:"asleep",hibernated:"asleep",stopped:"stopped",error:"failed"};
type Pairing = {pairing_id:string;code:string;expires_at:string;status?:"pending"|"paired"|"expired";device_name?:string};
function PairingCard({pairing}:{pairing:Pairing}){
 const {t}=useTranslation("computer");
 const paired=pairing.status==="paired", expired=pairing.status==="expired";
 return <div className={`verification-card web-pairing-card${paired?" is-paired":""}`} role="status"><div className="web-pairing-route" aria-hidden="true"><span><TofiIcon name="laptop" size={24}/><small>{t("pairing.this_device")}</small></span><i/><span><TofiIcon name="laptop" size={24}/><small>{t("pairing.mac_client")}</small></span>{paired&&<TofiIcon name="check" size={18}/>}</div><p>{paired?t("pairing.connected",{name:pairing.device_name||t("pairing.mac_client")}):expired?t("pairing.expired"):t("pairing.enter_code")}</p>{!paired&&!expired&&<code aria-label={t("pairing.code_aria",{code:pairing.code})}>{Array.from(pairing.code).map((digit,index)=><span className="web-pairing-digit" style={{animationDelay:`${index*80}ms`}} key={`${index}-${digit}`}>{digit}</span>)}</code>}{!paired&&!expired&&<small className="web-pairing-wait">{t("pairing.waiting")}</small>}</div>
}
/** `environment` is the Computer page; `devices` is the Advanced > Other devices page (pairing a Mac), still in development. */
export function ComputerPanel({part="environment"}:{part?:"environment"|"devices"}={}){
 const {t}=useTranslation("computer");
 const active=useSettingsActive();
 const [info,setInfo]=useState<VMInfo|null>(null);const [devices,setDevices]=useState<Computer[]>([]);const [busy,setBusy]=useState(false);const [error,setError]=useState("");const [pairing,setPairing]=useState<Pairing|null>(null);const [copied,setCopied]=useState(false);
 useEffect(()=>{
  if(!active)return;
  let closed=false;
  let refreshId=0;
  const ac=new AbortController();
  async function refresh(){
   const currentId=++refreshId;
   try{
    const [vm,all]=await Promise.all([request<VMInfo>("/api/computers/firecracker/info",{signal:ac.signal}),request<{computers:Computer[]}>("/api/computers",{signal:ac.signal})]);
    if(!closed&&currentId===refreshId){
     setInfo(vm);
     setDevices((all.computers??[]).filter(v=>v.kind==="personal"));
     setError(current=>current===i18n.t("computer:env.unavailable")?"":current);
    }
   }catch{if(!closed&&currentId===refreshId)setError(i18n.t("computer:env.unavailable"))}
  }
  void refresh();
  const timer=setInterval(refresh,10000);
  return()=>{closed=true;ac.abort();clearInterval(timer)};
 },[active]);
 useEffect(()=>{
  if(!active||!pairing?.pairing_id||pairing.status==="paired"||pairing.status==="expired")return;
  let closed=false;
  const check=async()=>{
   try{
    const result=await request<{status:"pending"|"paired"|"expired";device_name?:string}>(`/api/computers/pairings/${encodeURIComponent(pairing.pairing_id)}`);
    if(closed)return;
    if(result.status!=="pending"){
     setPairing(current=>current?.pairing_id===pairing.pairing_id?{...current,...result}:current);
     if(result.status==="paired"){
      const list=await request<{computers:Computer[]}>("/api/computers");
      if(!closed)setDevices(list.computers.filter(device=>device.kind==="personal"));
     }
    }
   }catch{/* A transient poll failure must not turn an unconfirmed pairing into success. */}
  };
  void check();
  const timer=window.setInterval(()=>void check(),2000);
  return()=>{closed=true;window.clearInterval(timer)};
 },[active,pairing?.pairing_id,pairing?.status]);
 async function retry(){setBusy(true);setError("");try{await request("/api/computers/firecracker/retry",{method:"POST"});setInfo(await request("/api/computers/firecracker/info"))}catch{setError(t("env.start_failed"))}finally{setBusy(false)}}
 if(part==="devices"){
  const openMac=isDesktop&&window.tofiDesktop?.openDesktopSettings?()=>{void window.tofiDesktop?.openDesktopSettings?.().catch(()=>{})}:null;
  const pair=()=>{setBusy(true);setCopied(false);void request<Pairing>("/api/computers/pairings",{method:"POST"}).then(setPairing).catch(()=>setError(t("devices.pair_failed"))).finally(()=>setBusy(false))};
  const disconnect=(id:string)=>{setBusy(true);void request(`/api/computers/${encodeURIComponent(id)}`,{method:"DELETE"}).then(()=>setDevices(list=>list.filter(v=>v.id!==id))).catch(()=>setError(t("devices.disconnect_failed"))).finally(()=>setBusy(false))};
  return <div className="settings-stack">
   {openMac&&<SettingsSection title={t("devices.this_mac")}><SettingsCard><SettingsRow label={t("devices.mac_settings")} description={t("devices.this_mac_note")} control={<button type="button" className="secondary-button" onClick={openMac}>{t("devices.open_mac")}</button>}/></SettingsCard></SettingsSection>}
   <SettingsSection title={t("devices.connected")} description={t("devices.note")}>
    <SettingsCard>
     {devices.map(device=><SettingsRow key={device.id} label={device.name} control={<><StatusBadge state={device.online?"ok":"asleep"}>{device.online?t("devices.online"):t("devices.offline")}</StatusBadge><button type="button" className="secondary-button" disabled={busy} onClick={()=>disconnect(device.id)}>{t("devices.disconnect")}</button></>}/>)}
     {!devices.length&&<p className="settings-card-note">{t("devices.empty")}</p>}
     {pairing&&openMac?<div className="device-pairing-code" role="status"><code aria-label={t("pairing.code_aria",{code:pairing.code})}>{pairing.code}</code><p>{t("pairing.enter_code")}</p><div className="device-pairing-actions"><button type="button" className="secondary-button" onClick={()=>{void navigator.clipboard?.writeText(pairing.code).then(()=>setCopied(true),()=>{})}}>{copied?t("devices.copied"):t("devices.copy")}</button><button type="button" className="secondary-button" onClick={openMac}>{t("devices.open_mac")}</button></div></div>:pairing&&<div className="device-pairing-web"><PairingCard pairing={pairing}/></div>}
     <div className="settings-row-v2 device-pairing-row"><button type="button" className="primary-button" disabled={busy} onClick={pair}>{t("devices.connect")}</button></div>
    </SettingsCard>
   </SettingsSection>
   {error&&<Banner tone="error" title={error}/>}
  </div>;
 }
 const envState=info?ENV_STATES[info.state]??"unknown":error?"unknown":"checking";
 return <section className="settings-section"><h3>{t("env.title")}</h3><p className="settings-description">{t("env.description")}</p><div className="environment-summary"><span className={`environment-dot ${envState}`}/><strong>{t(`env.state.${envState}`)}</strong><span>{t("kind.vm")} · {info?.browser??"Google Chrome"}</span></div>{info&&<p className="field-note">{info.desktop_idle_seconds?t("env.workspace_idle",{path:info.workspace_root??"/workspace",count:Math.round(info.desktop_idle_seconds/60)}):t("env.workspace",{path:info.workspace_root??"/workspace"})}</p>}{(envState==="failed"||envState==="stopped")&&<button className="secondary-button" disabled={busy} onClick={()=>void retry()}>{t("env.retry")}</button>}{error&&!(envState==="unknown"&&!info)&&<p className="error-text">{error}</p>}{isDesktop&&window.tofiDesktop?.openDesktopSettings&&<div className="permission-launch-card"><div><strong>{t("devices.this_mac")}</strong><p>{t("devices.this_mac_note")}</p></div><button type="button" className="secondary-button" onClick={()=>{void window.tofiDesktop?.openDesktopSettings?.().catch(()=>{})}}>{t("devices.open_mac")}</button></div>}</section>
}
