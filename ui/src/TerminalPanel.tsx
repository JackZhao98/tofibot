import { TofiIcon } from "./icons";
import { DelayedFeedback } from "./DelayedFeedback";
import {useCallback,useEffect,useRef,useState} from "react";
import {api,ApiError,request} from "./api";
import {TerminalScreen,type TerminalChunk} from "./TerminalScreen";
import "./terminal.css";

type Session={id:string;terminal_id?:string;command:string;closed:boolean;exited:boolean;exit_code:number};
type Listing={sessions?:Session[];terminals?:Session[];active_run_ids:string[]};
export function TerminalPanel({botId,botName,onClose}:{botId:string;botName:string;onClose:()=>void}){
 const [sessions,setSessions]=useState<Session[]>([]);const [selected,setSelected]=useState("");const [owned,setOwned]=useState(false);const [pending,setPending]=useState(false);const [error,setError]=useState("");const [confirm,setConfirm]=useState(false);const [runs,setRuns]=useState<string[]>([]);const [loading,setLoading]=useState(true);const [closeTarget,setCloseTarget]=useState("");
 const inputBuffer=useRef<{id:string;data:string;controlId:string}|null>(null);const inputTimer=useRef<ReturnType<typeof setTimeout>|undefined>(undefined);
 const createAfterAcquire=useRef(false);
 const control=useRef("");const seq=useRef(0);const epoch=useRef(0);const queue=useRef(Promise.resolve());const alive=useRef(true);
 const call=useCallback(async<T,>(action:string,args:Record<string,unknown>={},signal?:AbortSignal,keepalive=false)=>{const out=await request<{ok:boolean;result:T}>("/api/computers/firecracker/actions",{method:"POST",body:JSON.stringify({bot_id:botId,action:`terminal.${action}`,args}),signal,keepalive});return out.result},[botId]);
 const refresh=useCallback(async()=>{const data=await call<Listing>("list");if(!alive.current)return;const items=data.sessions??data.terminals??[];setSessions(items);setRuns(data.active_run_ids??[]);setSelected(id=>items.some(s=>s.id===id)?id:items.at(-1)?.id??"");setLoading(false);return data},[call]);
 const release=useCallback(async()=>{const id=control.current;clearTimeout(inputTimer.current);inputTimer.current=undefined;inputBuffer.current=null;control.current="";epoch.current++;if(alive.current)setOwned(false);if(id)await call("control.release",{control_id:id},undefined,true).catch(()=>{})},[call]);
 useEffect(()=>{alive.current=true;let active=true;const update=()=>void refresh().catch(e=>{if(active){setLoading(false);setError(e.message)}});update();const poll=setInterval(update,1500);const heartbeat=setInterval(()=>{const id=control.current;if(id)void call("control.renew",{control_id:id}).catch(()=>{if(control.current===id){setError("控制已断开，请重新接管");void release()}})},5000);
 const hidden=()=>{if(document.hidden)void release()};const blur=()=>void release();document.addEventListener("visibilitychange",hidden);window.addEventListener("blur",blur);window.addEventListener("pagehide",blur);
 return()=>{active=false;alive.current=false;clearInterval(poll);clearInterval(heartbeat);document.removeEventListener("visibilitychange",hidden);window.removeEventListener("blur",blur);window.removeEventListener("pagehide",blur);void release()}
 },[refresh,call,release]);
 function mutate(action:string,args:Record<string,unknown>){const id=control.current;queue.current=queue.current.then(async()=>{if(!id||id!==control.current)return;await call(action,{...args,control_id:id,seq:++seq.current})}).catch(e=>{if(id===control.current){if(alive.current)setError(e.message);void release()}});return queue.current}
 function flushInput(){clearTimeout(inputTimer.current);inputTimer.current=undefined;const chunk=inputBuffer.current;inputBuffer.current=null;if(chunk&&chunk.controlId===control.current)void mutate("write",{terminal_id:chunk.id,data:chunk.data})}
 function enqueueInput(data:string){if(!control.current)return;const chunk=inputBuffer.current;if(chunk&&(chunk.id!==selected||chunk.data.length+data.length>32768))flushInput();if(inputBuffer.current)inputBuffer.current.data+=data;else inputBuffer.current={id:selected,data,controlId:control.current};if(inputTimer.current===undefined)inputTimer.current=setTimeout(flushInput,16)}
 async function acquire(interrupt=false,openAfterAcquire=false){createAfterAcquire.current=openAfterAcquire;setPending(true);setError("");setConfirm(false);const version=++epoch.current;
 try{const state=await refresh();if(!alive.current||version!==epoch.current)return;const activeRuns=state?.active_run_ids??runs;
 if(!interrupt&&(activeRuns.length>0||sessions.some(s=>s.id===selected&&!s.closed&&!s.exited))){setConfirm(true);return}
 if(interrupt)for(const id of activeRuns){if(!alive.current||version!==epoch.current)return;await api.cancelRun(id)}
 let result:{control_id:string}|undefined;
 for(let attempt=0;attempt<10;attempt++){if(!alive.current||version!==epoch.current)return;try{result=await call<{control_id:string}>("control.acquire");break}catch(e){if(!interrupt||!(e instanceof ApiError)||e.code!=="computer_busy"||attempt===9)throw e;await new Promise(resolve=>setTimeout(resolve,200))}}
 if(!result?.control_id)throw new Error("无法取得控制权");
 if(!alive.current||version!==epoch.current){await call("control.release",{control_id:result.control_id});return}
 control.current=result.control_id;seq.current=0;queue.current=Promise.resolve();setOwned(true);
 if(interrupt&&selected){const fresh=await refresh();const stillOpen=(fresh?.sessions??fresh?.terminals??[]).some(s=>s.id===selected&&!s.closed&&!s.exited);if(stillOpen)await mutate("write",{terminal_id:selected,data:"\u0003"})}
 if(openAfterAcquire && alive.current && version===epoch.current)await create();
 }catch(e){if(alive.current)setError(e instanceof Error?e.message:"无法接管")}
 finally{if(alive.current)setPending(false)}
 }
 async function create(){if(!control.current)return;setPending(true);setError("");const id=control.current;queue.current=queue.current.then(async()=>{if(!id||id!==control.current)return;const result=await call<Session>("open",{control_id:id,seq:++seq.current,cols:100,rows:28});if(id!==control.current||!alive.current)return;await refresh();if(id===control.current&&alive.current)setSelected(result.id??result.terminal_id??"")}).catch(e=>{if(id===control.current){setError(e.message);void release()}});await queue.current;if(alive.current)setPending(false)}
 const current=sessions.find(s=>s.id===selected);
 return <section className="terminal-panel" onKeyDown={event=>{if(event.key==="Escape"&&(confirm||closeTarget)){event.preventDefault();event.stopPropagation();setConfirm(false);setCloseTarget("")}}} aria-label={`${botName} 的终端`}>
  <header className="terminal-header"><div><strong>{botName}</strong><span>终端</span></div><div className="terminal-actions">{owned?<><button type="button" onClick={()=>void create()} disabled={pending}>新终端</button><button type="button" onClick={()=>void release()}>释放控制</button></>:sessions.length>0?<button type="button" onClick={()=>void acquire()} disabled={pending}>接管</button>:null}<button type="button" className="terminal-close" onClick={onClose} aria-label="关闭终端面板"><TofiIcon name="close" size={20} style={{verticalAlign:"middle"}}/></button></div></header>
  {sessions.length>0&&<nav className="terminal-tabs" aria-label="终端会话">{sessions.map((session,index)=><button type="button" key={session.id} className={selected===session.id?"is-active":""} onClick={()=>setSelected(session.id)} title={session.command||"Shell"}><span className={`terminal-dot ${session.closed||session.exited?"is-ended":""}`}/><span>{session.command&&session.command!=="exec bash -i"?session.command.slice(0,30):`Shell ${index+1}`}</span></button>)}</nav>}
  <div className="terminal-stage">{selected?<TerminalScreen key={selected} sessionId={selected} controlled={owned&&!current?.closed} read={(cursor,signal)=>call<TerminalChunk>("read",{terminal_id:selected,cursor,max_bytes:65536},signal)} onInput={enqueueInput} onResize={(cols,rows)=>void mutate("resize",{terminal_id:selected,cols,rows})} onError={setError}/>:<div className="terminal-empty"><span aria-hidden="true"><TofiIcon name="terminal" size={24}/></span>{loading?<DelayedFeedback><p>正在连接…</p></DelayedFeedback>:<><p>还没有终端</p><button type="button" onClick={()=>void (owned?create():acquire(false,true))} disabled={pending}>新建终端</button></>}</div>}</div>
  {(current||owned||error)&&<footer className="terminal-status"><span>{owned?"你正在控制":current?"观看中":""}{current?.closed?" · 已结束":current?.exited?current.exit_code?` · 已退出 (${current.exit_code})`:" · 已完成":""}</span>{error&&<span role="alert">{error}</span>}{owned&&current&&!current.closed&&<button type="button" onClick={()=>setCloseTarget(selected)}>结束终端</button>}</footer>}
  {closeTarget&&<div className="terminal-confirm"><div role="alertdialog" aria-modal="true" aria-labelledby="terminal-close-title"><h3 id="terminal-close-title">结束这个终端？</h3><p>其中运行的进程也会停止。</p><div><button type="button" onClick={()=>setCloseTarget("")}>保留</button><button type="button" onClick={()=>{const id=closeTarget;setCloseTarget("");void mutate("close",{terminal_id:id}).then(()=>refresh())}}>结束终端</button></div></div></div>}
  {confirm&&<div className="terminal-confirm"><div role="alertdialog" aria-modal="true" aria-labelledby="terminal-interrupt-title"><h3 id="terminal-interrupt-title">接管当前终端？</h3><p>终端可能仍有命令运行。接管会停止 {botName} 的当前任务，并向选中的终端发送中断。</p><div><button type="button" onClick={()=>setConfirm(false)}>继续观看</button><button type="button" onClick={()=>void acquire(true,createAfterAcquire.current)}>停止并接管</button></div></div></div>}
 </section>
}
