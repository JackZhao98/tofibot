import {useEffect,useState} from "react";
import {request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {PermissionCoach} from "./PermissionCoach";
import {TofiIcon} from "./icons";
import {i18n,useTranslation} from "./i18n";
import "./computer-pairing.css";
type Computer={id:string;name:string;kind:string;online:boolean};
type VMInfo={state:string;workspace_root?:string;browser?:string;error?:string;desktop_idle_seconds?:number};
type Pairing = {pairing_id:string;code:string;expires_at:string;status?:"pending"|"paired"|"expired";device_name?:string};
function PairingCard({pairing}:{pairing:Pairing}){
 const {t}=useTranslation("computer");
 const paired=pairing.status==="paired", expired=pairing.status==="expired";
 return <div className={`verification-card web-pairing-card${paired?" is-paired":""}`} role="status"><div className="web-pairing-route" aria-hidden="true"><span><TofiIcon name="laptop" size={24}/><small>{t("pairing.this_device")}</small></span><i/><span><TofiIcon name="laptop" size={24}/><small>{t("pairing.mac_client")}</small></span>{paired&&<TofiIcon name="check" size={18}/>}</div><p>{paired?t("pairing.connected",{name:pairing.device_name||t("pairing.mac_client")}):expired?t("pairing.expired"):t("pairing.enter_code")}</p>{!paired&&!expired&&<code aria-label={t("pairing.code_aria",{code:pairing.code})}>{Array.from(pairing.code).map((digit,index)=><span className="web-pairing-digit" style={{animationDelay:`${index*80}ms`}} key={`${index}-${digit}`}>{digit}</span>)}</code>}{!paired&&!expired&&<small className="web-pairing-wait">{t("pairing.waiting")}</small>}</div>
}
export function ComputerPanel(){
 const {t}=useTranslation("computer");
 const active=useSettingsActive();
 const [info,setInfo]=useState<VMInfo|null>(null);const [devices,setDevices]=useState<Computer[]>([]);const [busy,setBusy]=useState(false);const [error,setError]=useState("");const [pairing,setPairing]=useState<Pairing|null>(null);
 const [permissionCardOpen,setPermissionCardOpen]=useState(false);
 const hasNativePermissions=window.tofiDesktop?.platform==="darwin";
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
     setDevices(all.computers.filter(v=>v.kind==="mac"));
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
      if(!closed)setDevices(list.computers.filter(device=>device.kind==="mac"));
     }
    }
   }catch{/* A transient poll failure must not turn an unconfirmed pairing into success. */}
  };
  void check();
  const timer=window.setInterval(()=>void check(),2000);
  return()=>{closed=true;window.clearInterval(timer)};
 },[active,pairing?.pairing_id,pairing?.status]);
 async function retry(){setBusy(true);setError("");try{await request("/api/computers/firecracker/retry",{method:"POST"});setInfo(await request("/api/computers/firecracker/info"))}catch{setError(t("env.start_failed"))}finally{setBusy(false)}}
 return <section className="settings-section"><h3>{t("env.title")}</h3><p className="settings-description">{t("env.description")}</p><div className="environment-summary"><span className={`environment-dot ${info?.state==="ready"?"ready":""}`}/><strong>{info?.state==="ready"?t("env.state.ready"):info?.state==="starting"?t("env.state.starting"):info?.state==="stopped"?t("env.state.stopped"):t("env.state.waiting")}</strong><span>{info?.browser??"Google Chrome"}</span></div>{info&&<p className="field-note">{info.desktop_idle_seconds?t("env.workspace_idle",{path:info.workspace_root??"/workspace",count:Math.round(info.desktop_idle_seconds/60)}):t("env.workspace",{path:info.workspace_root??"/workspace"})}</p>}{(info?.state==="error"||info?.state==="stopped")&&<button className="secondary-button" disabled={busy} onClick={()=>void retry()}>{t("env.retry")}</button>}{error&&<p className="error-text">{error}</p>}{hasNativePermissions&&<div className="permission-launch-card"><div><strong>{t("mac.title")}</strong><p>{t("mac.description")}</p></div><button type="button" className="secondary-button" onClick={()=>setPermissionCardOpen(true)}>{t("mac.open_card")}</button></div>}<details className="other-computers"><summary>{t("devices.title")}</summary><p className="field-note">{t("devices.note")}</p><button className="secondary-button" disabled={busy} onClick={()=>{setBusy(true);void request<Pairing>("/api/computers/pairings",{method:"POST"}).then(setPairing).catch(()=>setError(t("devices.pair_failed"))).finally(()=>setBusy(false))}}>{t("devices.connect")}</button>{pairing&&(window.tofiDesktop?<div className="verification-card"><p>{t("pairing.enter_code")}</p><code>{pairing.code}</code></div>:<PairingCard pairing={pairing}/>)}{devices.map(device=><div className="credential-row" key={device.id}><div><strong>{device.name}</strong><p>{device.online?t("devices.online"):t("devices.offline")}</p></div><button className="text-button" disabled={busy} onClick={()=>{setBusy(true);void request(`/api/computers/${encodeURIComponent(device.id)}`,{method:"DELETE"}).then(()=>setDevices(list=>list.filter(v=>v.id!==device.id))).catch(()=>setError(t("devices.disconnect_failed"))).finally(()=>setBusy(false))}}>{t("devices.disconnect")}</button></div>)}</details>{hasNativePermissions&&<PermissionCoach permissions={["screen","accessibility"]} open={permissionCardOpen} onDismiss={()=>setPermissionCardOpen(false)}/>}</section>
}
