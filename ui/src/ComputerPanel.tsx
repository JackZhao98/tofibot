import {useEffect,useState} from "react";
import {request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {PermissionCoach} from "./PermissionCoach";
import {TofiIcon} from "./icons";
import "./computer-pairing.css";
type Computer={id:string;name:string;kind:string;online:boolean};
type VMInfo={state:string;workspace_root?:string;browser?:string;error?:string;desktop_idle_seconds?:number};
type Pairing = {pairing_id:string;code:string;expires_at:string;status?:"pending"|"paired"|"expired";device_name?:string};
function PairingCard({pairing}:{pairing:Pairing}){
 const paired=pairing.status==="paired", expired=pairing.status==="expired";
 return <div className={`verification-card web-pairing-card${paired?" is-paired":""}`} role="status"><div className="web-pairing-route" aria-hidden="true"><span><TofiIcon name="laptop" size={24}/><small>这台设备</small></span><i/><span><TofiIcon name="laptop" size={24}/><small>Mac 客户端</small></span>{paired&&<TofiIcon name="check" size={18}/>}</div><p>{paired?`${pairing.device_name||"Mac 客户端"} 已连接。`:expired?"配对码已过期，请重新生成。":"在 Mac 客户端输入此配对码，5 分钟内有效。"}</p>{!paired&&!expired&&<code aria-label={`配对码 ${pairing.code}`}>{Array.from(pairing.code).map((digit,index)=><span className="web-pairing-digit" style={{animationDelay:`${index*80}ms`}} key={`${index}-${digit}`}>{digit}</span>)}</code>}{!paired&&!expired&&<small className="web-pairing-wait">等待设备确认…</small>}</div>
}
export function ComputerPanel(){
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
     setError(current=>current==="运行环境暂不可用。"?"":current);
    }
   }catch{if(!closed&&currentId===refreshId)setError("运行环境暂不可用。")}
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
 async function retry(){setBusy(true);setError("");try{await request("/api/computers/firecracker/retry",{method:"POST"});setInfo(await request("/api/computers/firecracker/info"))}catch{setError("电脑未能启动，请稍后重试。")}finally{setBusy(false)}}
 return <section className="settings-section"><h3>运行环境</h3><p className="settings-description">Linux、Google Chrome、文件浏览器和终端使用同一套环境。每个 Bot 有自己的工作文件夹，也可互相访问。</p><div className="environment-summary"><span className={`environment-dot ${info?.state==="ready"?"ready":""}`}/><strong>{info?.state==="ready"?"共享电脑已就绪":info?.state==="starting"?"正在准备电脑":info?.state==="stopped"?"共享电脑已停止":"等待电脑连接"}</strong><span>{info?.browser??"Google Chrome"}</span></div>{info&&<p className="field-note">工作目录 {info.workspace_root??"/workspace"}{info.desktop_idle_seconds?` · 桌面闲置 ${Math.round(info.desktop_idle_seconds/60)} 分钟后自动关闭`:""}</p>}{(info?.state==="error"||info?.state==="stopped")&&<button className="secondary-button" disabled={busy} onClick={()=>void retry()}>重新准备电脑</button>}{error&&<p className="error-text">{error}</p>}{hasNativePermissions&&<div className="permission-launch-card"><div><strong>Mac 权限</strong><p>管理屏幕录制和辅助功能权限，允许 Tofi 在你授权后查看屏幕并操作桌面。</p></div><button type="button" className="secondary-button" onClick={()=>setPermissionCardOpen(true)}>查看授权卡片</button></div>}<details className="other-computers"><summary>其他设备</summary><p className="field-note">也可以连接已安装 Tofi 客户端的 Mac。</p><button className="secondary-button" disabled={busy} onClick={()=>{setBusy(true);void request<Pairing>("/api/computers/pairings",{method:"POST"}).then(setPairing).catch(()=>setError("暂时无法创建配对码。")).finally(()=>setBusy(false))}}>连接 Mac</button>{pairing&&(window.tofiDesktop?<div className="verification-card"><p>在 Mac 客户端输入此配对码，5 分钟内有效。</p><code>{pairing.code}</code></div>:<PairingCard pairing={pairing}/>)}{devices.map(device=><div className="credential-row" key={device.id}><div><strong>{device.name}</strong><p>{device.online?"在线":"离线"}</p></div><button className="text-button" disabled={busy} onClick={()=>{setBusy(true);void request(`/api/computers/${encodeURIComponent(device.id)}`,{method:"DELETE"}).then(()=>setDevices(list=>list.filter(v=>v.id!==device.id))).catch(()=>setError("未能断开设备。")).finally(()=>setBusy(false))}}>断开</button></div>)}</details>{hasNativePermissions&&<PermissionCoach permissions={["screen","accessibility"]} open={permissionCardOpen} onDismiss={()=>setPermissionCardOpen(false)}/>}</section>
}
