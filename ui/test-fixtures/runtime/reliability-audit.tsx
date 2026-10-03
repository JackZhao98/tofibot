// Actual App/API/SSE callback acceptance with in-memory synthetic responses only.
import {createRoot} from "react-dom/client";
import App from "../../src/App";
import {OwnerSessionGate} from "../../src/OwnerSession";
import type {Question} from "../../src/questionTimeline";
import type {Run,Message,ToolActivity} from "../../src/types";
import "../../src/styles.css";
import "../../src/interaction-system.css";
import "../../src/settings-system.css";
import "../../src/desktop-system.css";
import "../../src/conversation-workspace.css";
import "../../src/v2-foundations.css";
import "../../src/v2-app.css";
import "../../src/web-tool-steps.css";
import "../../src/chat-header.css";
import "../../src/context-card.css";
const at="2026-10-02T17:00:00Z",conv="fixture-conversation",bot="fixture-bot",trigger="fixture-trigger";
const error="LLM call failed: stream read error: stream error: stream ID 13; INTERNAL_ERROR; received from peer";
let cursor=100;
const runs:Run[]=[{id:"original",conversation_id:conv,bot_id:bot,trigger_message_id:trigger,status:"running",created_at:at,updated_at:at}];
const messages:Message[]=[{id:trigger,conversation_id:conv,seq:1,run_id:"original",role:"user",content:"Synthetic request — no external action",created_at:at}];
const tools:ToolActivity[]=[{conversation_id:conv,bot_id:bot,run_id:"original",call_id:"already-successful",name:"search_history",arguments:"{}",result:"PRESERVED_SUCCESS_RESULT",status:"completed",truncated:false,started_at:at,updated_at:at}];
let authenticated=true;
const notifications:string[]=[];
class SyntheticNotification {static permission="granted";constructor(title:string){notifications.push(title)}close(){} }
window.Notification=SyntheticNotification as unknown as typeof Notification;
const approval=(id:string,status:Question["status"]="pending"):Question=>({question_id:id,conversation_id:conv,bot_id:bot,run_id:"original",type:"question",question_type:"approval",question:"Review the synthetic action",approval:{action:"Write synthetic fixture",target:"synthetic record",impact:"One fixture effect",payload:'{"target":"synthetic"}'},status,created_at:at,updated_at:new Date(Date.parse(at)+cursor*1000).toISOString(),outcome:{status:status==="expired"?"approval_expired":"need_approval",execution_certainty:"not_executed",message:"Fixture wait",next_action:status==="expired"?"renew_approval":"answer_approval"}});
let questions:Question[]=[];
const requests:Array<{method:string;path:string}>=[];
class SyntheticEvents extends EventTarget {
 static sources:SyntheticEvents[]=[];
 onopen:((e:Event)=>void)|null=null;onerror:((e:Event)=>void)|null=null;closed=false;opened=false;
 constructor(public url:string){super();SyntheticEvents.sources.push(this);setTimeout(()=>{if(!this.closed){this.opened=true;this.onopen?.(new Event("open"));}},30)}
 close(){this.closed=true}
 emit(type:string,data:unknown){if(!this.closed)this.dispatchEvent(new MessageEvent(type,{data:JSON.stringify(data),lastEventId:String(++cursor)}))}
}
window.EventSource=SyntheticEvents as unknown as typeof EventSource;
const emit=(type:string,data:Record<string,unknown>)=>SyntheticEvents.sources.filter(s=>s.url.includes(`/conversations/${conv}/`)&&!s.closed).forEach(s=>s.emit(type,{conversation_id:conv,...data}));
const status=(id:string,state:Run["status"])=>{
 cursor+=2;
 const r=runs.find(r=>r.id===id)!;Object.assign(r,{status:state,updated_at:new Date(Date.parse(at)+cursor*1000).toISOString(),error:state==="failed"?error:undefined,failure:state==="failed"?{source:"runtime",code:"connection_interrupted",message:"Connection interrupted. Task did not finish."}:undefined});emit("run",{...r});
};
function retry(id:string,parent:string){const r:Run={...runs[0],id,parent_run_id:parent,status:"running",error:undefined,failure:undefined,created_at:new Date(Date.parse(at)+(cursor+1)*1000).toISOString(),updated_at:new Date(Date.parse(at)+(cursor+1)*1000).toISOString()};runs.push(r);emit("run",{...r});const t={...tools[0],run_id:id,call_id:`tool-${id}`};tools.push(t);emit("tool",t)}
const scenario=new URLSearchParams(location.search).get("scenario");
if(!scenario){ runs[0].status="waiting"; questions=[approval("approval-old")]; }
for(let i=0;i<6;i++)runs.push({...runs[0],id:`historical-${i}`,trigger_message_id:`offpage-${i}`,status:"failed",error,created_at:"2026-09-01T00:00:00Z",updated_at:"2026-09-01T00:00:00Z"});
if(scenario){status("original","failed");retry("retry-1","original");if(scenario!=="running")status("retry-1",scenario as Run["status"]);if(scenario==="done")messages.push({id:"retry-answer",conversation_id:conv,seq:2,role:"assistant",sender_bot_id:bot,run_id:"retry-1",content:"LATEST_SUCCESS_ANSWER",created_at:at})}
const paged=new URLSearchParams(location.search).has('paged');
const oldOnly=new URLSearchParams(location.search).has('oldOnly');
if(paged)messages.splice(0,1);
if(oldOnly)messages.splice(0,messages.length,{id:'old-progress',conversation_id:conv,seq:2,role:'assistant',sender_bot_id:bot,run_id:'original',kind:'progress',content:'OLD_PROGRESS_REMAINS_REACHABLE',created_at:at});
window.fetch=async(input,init)=>{
 const path=String(input),url=new URL(path,location.origin),method=init?.method||"GET";requests.push({method,path});
 if(url.pathname==="/api/auth/session")return Response.json({enabled:true,setup_required:false,authenticated,password_transport_allowed:true,owner:authenticated?{id:"synthetic-owner",username:"Synthetic Owner",email:"owner@example.test"}:undefined});
 if(url.pathname==="/api/auth/login"&&method==="POST"){authenticated=true;return Response.json({enabled:true,setup_required:false,authenticated:true,password_transport_allowed:true})}
 if(method==="POST"&&url.pathname==="/api/questions/approval-old/renew"){
  const old=questions.find(q=>q.question_id==="approval-old")!;
  old.outcome={...old.outcome!,next_action:"explain_blocker"};
  if(!questions.some(q=>q.question_id==="approval-fresh"))questions.push(approval("approval-fresh"));
  return Response.json({question:questions.find(q=>q.question_id==="approval-fresh")});
 }
 if(method==="POST"&&(url.pathname==="/api/questions/approval-fresh/answer"||url.pathname==="/api/questions/approval-old/answer")){
  const q=questions.find(q=>q.question_id===url.pathname.split("/")[3])!;
  if(q.status!=="pending")return Response.json({question:q});
  q.status="answered";q.updated_at=new Date(Date.parse(at)+(++cursor)*1000).toISOString();q.answer=JSON.parse(String(init?.body)).value;q.outcome=undefined;
  if(q.question_id==="approval-fresh")setTimeout(()=>{status("original",q.answer?"done":"failed")},80);
  return Response.json({question:q});
 }
 // UI read receipts are synthetic too; all execution mutations fail closed.
 if(method==="POST"&&url.pathname.endsWith("/read"))return Response.json({read_seq:99});
 if(method!=="GET")throw new Error(`Execution mutations forbidden in synthetic acceptance: ${method} ${path}`);
 if(url.pathname==="/api/preferences")return Response.json({timezone:"America/Los_Angeles",timezone_configured:true});
 if(url.pathname==="/api/server-info")return Response.json({service:"tofi",protocol_version:1,instance_id:"00000000-0000-4000-8000-000000000001"});
 if(url.pathname==="/api/bots")return Response.json({bots:[{id:bot,name:"Synthetic Bot",instructions:"",model:"synthetic",dm_conversation_id:conv,created_at:at}]});
 if(url.pathname==="/api/conversations")return Response.json({conversations:[{id:conv,name:"Synthetic Bot",kind:"dm",bot_id:bot,bot_ids:[bot],working_bot_ids:[bot],updated_at:at}]});
 if(url.pathname==="/api/config")return Response.json({model_configured:true,default_model:"synthetic",provider:"synthetic"});
 if(url.pathname==="/api/auth/codex")return Response.json({connected:false});
 if(url.pathname==="/api/computers/firecracker/info")return Response.json({kind:"firecracker",state:"ready"});
 if(url.pathname.endsWith("/messages"))return Response.json({messages,drafts:[],has_more:false,event_cursor:100});
 if(url.pathname.endsWith("/runs"))return Response.json({runs});
 if(url.pathname.endsWith("/memories"))return Response.json({memories:[]});
 if(url.pathname.endsWith("/tools")){
  const runID=url.searchParams.get("run_id"),runIDs=url.searchParams.get("run_ids");
  if(runID)return Response.json({run_id:runID,activities:tools.filter(t=>t.run_id===runID),tool_count:tools.filter(t=>t.run_id===runID).length,has_more:false,next_offset:1});
  if(runIDs)return Response.json({summaries:runIDs.split(",").map(id=>({run_id:id,bot_id:bot,tool_count:tools.filter(t=>t.run_id===id).length,completed_count:tools.filter(t=>t.run_id===id).length,failed_count:0,interrupted_count:0,pending_count:0,started_at:at,updated_at:at}))});
  return Response.json({activities:paged?[]:tools});
 }
 if(url.pathname.endsWith("/questions"))return Response.json({questions});
 if(url.pathname.endsWith("/schedules"))return Response.json({schedules:[]});
 if(url.pathname.endsWith("/schedule-occurrences"))return Response.json({occurrences:[]});
 if(url.pathname.endsWith("/work-items"))return Response.json({work_items:[]});
 if(url.pathname.endsWith("/drafts"))return Response.json({drafts:[]});
 if(url.pathname==="/api/mail-drafts")return Response.json({drafts:[]});
 if(url.pathname==="/api/secret-inputs")return Response.json({secret_inputs:[],requests:[]});
 if(url.pathname==="/api/computer/desktop-ownership")return Response.json({owner:null});
 throw new Error(`Unexpected synthetic request: ${method} ${path}`);
};
Object.assign(window,{failureAudit:{status,retry,requests,runs,emit,reconnect:()=>{for(const s of SyntheticEvents.sources)if(!s.closed&&s.url.includes('/conversations/'))s.onerror?.(new Event('error'))},late:()=>{emit('delta',{run_id:'original',bot_id:bot,message_id:'late-draft',conversation_id:conv,seq:2,text:'LATE_TEXT_MUST_NOT_RENDER',content:'LATE_TEXT_MUST_NOT_RENDER',revision:9,created_at:at});emit('run',{...runs[0],status:'running',updated_at:'2099-01-01T00:00:00Z'});emit('tool',{...tools[0],call_id:'late-tool',status:'running',updated_at:'2099-01-01T00:00:00Z'})},finish:(id:string)=>{status(id,'done');const message:Message={id:`answer-${id}`,conversation_id:conv,seq:messages.length+1,role:'assistant',sender_bot_id:bot,run_id:id,content:'LATEST_SUCCESS_ANSWER',created_at:at};messages.push(message);emit('message',{message})}}});
Object.assign(window,{runtimeAudit:{requests,notifications,streamReady:()=>SyntheticEvents.sources.some(s=>s.opened&&!s.closed&&s.url.includes('/conversations/')),questions:()=>questions,budgetFailure:()=>{for(const q of questions){q.status="run_done";q.outcome=undefined;q.updated_at=new Date(Date.parse(at)+(++cursor)*1000).toISOString()}emit("question",questions[0] as unknown as Record<string,unknown>);status("original","failed");Object.assign(runs[0],{error:"budget exhausted: maximum active duration",failure:{source:"runtime",code:"budget_exhausted",message:"Run budget exhausted. Partial output only."}});emit("run",{...runs[0],updated_at:new Date(Date.parse(at)+(++cursor)*1000).toISOString()});const m:Message={id:"budget-partial",conversation_id:conv,seq:2,role:"assistant",sender_bot_id:bot,run_id:"original",content:"SYNTHETIC_PARTIAL_OUTPUT",created_at:at};messages.push(m);emit("message",{message:m})},information:()=>{questions=[{...approval("approval-fresh"),question_type:"text",question:"Provide a synthetic value to continue",approval:undefined,outcome:{status:"need_information",execution_certainty:"not_executed",message:"Waiting for information",next_action:"answer_question"}}];emit("question",questions[0] as unknown as Record<string,unknown>)},expire:()=>{cursor+=2;questions[0]=approval("approval-old","expired");emit("question",questions[0] as unknown as Record<string,unknown>)},loseSession:()=>{authenticated=false;window.dispatchEvent(new Event("tofi:unauthorized"))},replacePending:()=>{questions=[{...approval("reconnected-question"),question:"Current pending review after reconnect"}]},replayFailures:()=>{for(const r of runs.filter(r=>r.status==="failed"))emit("run",{...r})}}});
createRoot(document.getElementById('root')!).render(<OwnerSessionGate><App/></OwnerSessionGate>);
