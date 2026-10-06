import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {execFileSync} from "node:child_process";
import {fileURLToPath} from "node:url";
import {dirname, join} from "node:path";
import {createServer} from "vite";
import {createElement} from "react";
import {renderToStaticMarkup} from "react-dom/server";
import {at, run, request, tool, question, draft, summary, scenario} from "./task-issue-fixtures.mjs";
const ui=dirname(dirname(fileURLToPath(import.meta.url)));
const server=await createServer({configFile:false,root:ui,server:{middlewareMode:true,hmr:false,ws:false},logLevel:"error"});
try {
 const p=await server.ssrLoadModule("/src/taskIssuePresentation.ts");
 const {TaskRunBlock}=await server.ssrLoadModule("/src/TaskRunBlock.tsx");
 const {toolDisplayLabel,reconcileToolActivity}=await server.ssrLoadModule("/src/toolTimeline.ts");
 const {reconcileQuestion}=await server.ssrLoadModule("/src/questionTimeline.ts");
 const {mergeRunUpdates}=await server.ssrLoadModule("/src/runMerge.ts");
 const view=(fixture,locale="zh-CN")=>p.presentTaskIssue({...fixture,locale,summary:fixture.summaries?.[0]});
 const markup=(fixture,locale="zh-CN")=>renderToStaticMarkup(createElement(TaskRunBlock,{owner:p.buildTaskOwners([fixture.run],fixture.messages,fixture.questions,fixture.drafts)[0],...fixture,messages:fixture.messages,locale,onOpenTools:()=>{},onRefresh:async()=>{},renderQuestion:q=>createElement("p",{key:q.question_id,"data-valid-proposal":q.question_id},q.question),renderDraft:d=>createElement("p",{key:d.draft_id,"data-draft-status":d.status},d.demo?"Demo; not sent":d.status)}));
 // 1. Independent context and later model failure, one issue, safe diagnostic copy.
 const incident=scenario("incident"), issue=view(incident), html=markup(incident);
 // The blocked step is red on the step itself; only the run-level overload raises a card.
 assert.equal(issue.kind,"provider_busy");assert(!issue.facts.join(" ").includes("本次工具调用未执行"));assert(!issue.secondary.join(" ").includes("执行前检查"));
 assert(html.includes('task-step is-not_executed is-problem'));assert(html.includes("task-step-problem"));assert(html.includes("执行前检查缺少必要信息"));assert(html.includes("复制诊断信息"));
 assert.equal((html.match(/class="task-issue-card"/g)||[]).length,1);assert(!html.includes("data-valid-proposal"));assert(!html.includes('role="status"'));
 const diagnostic=p.taskDiagnostics(issue);for(const secret of ["PRIVATE_SECRET","PRIVATE_BODY","PRIVATE_REASONING","synthetic@example.invalid"]) assert(!diagnostic.includes(secret));assert(diagnostic.includes("intent_provenance_unverified"));assert(diagnostic.includes("server_is_overloaded"));assert(diagnostic.includes("not_executed"));
 console.log("PASS 1: blocked step marked on the step, run-level overload card, diagnostic allowlist");
 // 2. Overload preserves completed facts and incomplete paging, never grants replay.
 const busy=view(scenario("busy"));assert.equal(busy.kind,"provider_busy");assert.equal(busy.action,"view_activity");assert.equal(busy.recordsComplete,false);assert(!busy.facts.join(" ").includes("未执行"));assert(busy.facts.join(" ").includes("已完成"));
 for(const error of ["busy", "503", "xserver_is_overloaded", "server_is_overloaded_extra"]) assert.equal(view({...scenario("busy"),run:{...run,status:"failed",error}}).kind,"unknown_failure");
 assert.equal(view({...scenario("busy"),run:{...run,status:"failed",error:"server_is_overloaded"},tools:[]}).kind,"provider_busy");
 console.log("PASS 2: exact run token, completed results, partial page, no replay action");
 // 2b. Model account failures name the account state, route to model settings and state recorded tool facts.
 const zero={...summary,tool_count:0,failed_count:0};
 for(const [code,kind,title] of [["model_auth_invalid","model_auth","模型账户登录已失效"],["model_unconfigured","model_unconfigured","没有可用的 AI 提供方"],["model_quota_exhausted","model_quota","模型账户额度已用尽"]]){
  const fixture={...scenario("busy"),run:{...run,status:"failed",error:"PRIVATE_SECRET",failure:{code,source:"runtime",message:"PRIVATE_BODY"}},tools:[],summaries:[zero]};
  const issue=view(fixture),html=markup(fixture);
  assert.equal(issue.kind,kind);assert.equal(issue.title,title);assert.equal(issue.recordsComplete,true);
  assert.equal(issue.action,"open_codex");assert.equal(issue.actionLabel,"打开 Codex 设置");
  assert.match(issue.facts.join(" "),/没有执行任何工具步骤/);assert(!issue.facts.join(" ").includes("无法确认原因"));assert(!issue.secondary.join(" ").includes("尚未完整加载"));
  assert(!JSON.stringify(issue).includes("PRIVATE_SECRET"));assert(!html.includes("读取执行记录"));assert(html.includes("本次没有执行任何工具步骤"));
 }
 const unloaded=view({...scenario("busy"),run:{...run,status:"failed",failure:{code:"model_auth_invalid",source:"runtime",message:""}},tools:[],summaries:[]});
 assert.equal(unloaded.kind,"model_auth");assert(!unloaded.facts.join(" ").includes("没有执行任何工具"));
 const generic=view({...scenario("busy"),run:{...run,status:"failed",error:"",failure:{code:"execution_failed",source:"runtime",message:""}},tools:[],summaries:[zero]});
 assert.equal(generic.kind,"unknown_failure");assert.match(generic.facts.join(" "),/没有记录具体原因/);assert.equal(generic.recordsComplete,true);
 assert.equal(view({...scenario("busy"),run:{...run,status:"failed",failure:{code:"model_auth_invalid",source:"runtime",message:""}},tools:[],summaries:[zero]},"en").title,"Model account sign-in is no longer valid");
 console.log("PASS 2b: model account failures are named, routed to model settings, and zero-tool runs say so");
 // 2c. Activity records name steps in words: no raw tool names, call ids, hash labels or raw review codes.
 const steps=markup(scenario("busy"));
 assert(steps.includes('class="task-step is-completed"'));assert(steps.includes("检查文档"));
 const visibleText=html=>html.replace(/<[^>]*>/g," ");
 const records=visibleText(steps.slice(steps.indexOf('<div class="task-activity-records">')));
 for(const raw of ["mcp_email_read","synthetic-call-1","工具调用 · "]) assert(!records.includes(raw),`raw ${raw} leaked into activity records`);
 const review=markup(scenario("incident"));assert(review.includes("执行前检查缺少必要信息"));assert(review.includes("未执行"));
 // Raw codes belong only in a step's collapsed technical details.
 const reviewRecords=visibleText(review.slice(review.indexOf('<div class="task-activity-records">')).replace(/<details class="task-step-technical">[\s\S]*?<\/details>/g,""));
 assert(review.includes('<details class="task-step-technical">'));assert(!review.includes("决定与执行前检查记录"));
 for(const raw of ["context_required","not_executed","synthetic-question-1"]) assert(!reviewRecords.includes(raw),`raw ${raw} leaked into review record`);
 // Issue facts name the step in words and link to it; no hash fingerprints remain.
 const factCard=markup(scenario("unknown"));
 assert(factCard.includes('class="task-fact-link"'));assert(!/工具调用 · [0-9A-F]{8}/.test(visibleText(factCard)));
 assert(steps.includes("工作过程<span class=\"task-activity-detail\"> · 10 个工具"));
 // Production outcomes name the cause in a code; the step reads it in words.
 const prodBlocked={...scenario("busy"),run:{...run,status:"done"},tools:[{...tool,outcome:{version:1,status:"need_information",code:"mcp_review_context_missing",execution_certainty:"not_executed",message:"Necessary durable user authorization or context is missing"}}]};
 const prodHTML=markup(prodBlocked);assert(prodHTML.includes("执行前检查缺少必要信息"));assert(!visibleText(prodHTML.replace(/<details class="task-step-technical">[\s\S]*?<\/details>/g,"")).includes("状态待确认"));
 assert.equal(p.taskOutcomeText({code:"stale_schema",status:"validation_error"},"zh-CN"),"工具信息已过期，需要重新查找");assert.equal(p.taskOutcomeText({status:"need_information"},"zh-CN"),"缺少必要信息");
 console.log("PASS 2c: activity records use human step names, states and counts");
 // 3. Setup and context have different bounded actions; neither has controls.
 // Setup and context gaps live on the blocked step: setup offers tool settings there, context offers diagnostics.
 const setupHTML=markup(scenario("setup"));assert.equal(view(scenario("setup")),undefined);assert(setupHTML.includes("打开工具设置"));assert(!setupHTML.includes("data-valid-proposal"));assert(!setupHTML.includes("task-issue-card"));
 assert(html.includes("复制诊断信息"));assert(!html.includes("打开工具设置"));
 assert.equal(p.canAnswerQuestion(incident.questions[0]),false);assert.equal(p.canAnswerQuestion(scenario("setup").questions[0]),false);
 console.log("PASS 3: setup and context gaps act from the step, no technical-gap approval");
 // 4. Only a current manual proposal is actionable; observations never succeed.
 assert.equal(p.canAnswerQuestion(question),true);assert.equal(p.canAnswerQuestion(question,true),false);
 assert.equal(p.canAnswerQuestion({...question,outcome:{status:"approval_expired",execution_certainty:"not_executed"}}),false);
 assert.equal(p.canAnswerQuestion({...question,outcome:{status:"uncertain_effect",execution_certainty:"unknown"}}),false);
 for(const status of ["reviewing","approved","shadow_allow","setup_required","context_required","unavailable","policy_denied","terminal"]) assert.equal(p.canAnswerQuestion({...question,approval:{...question.approval,review:{...question.approval.review,status}}}),false,status);
 assert.equal(p.canAnswerQuestion({...question,approval:{...question.approval,review_only:true}}),false);assert.equal(view(scenario("shadow")),undefined);
 assert(markup({...incident,questions:[...incident.questions,{...question,question_id:"independent-human"}]}).includes('data-valid-proposal="independent-human"'));
 const questionSource=await readFile(join(ui,"src/QuestionCard.tsx"),"utf8");assert(questionSource.includes("if (sending.current || !answerable) return;"));assert(questionSource.indexOf("sending.current = true")<questionSource.indexOf("await request<{ question: Question }>",questionSource.indexOf("sending.current = true")));
 console.log("PASS 4: current manual guard, shadow read-only, independent proposal, single-flight POST guard");
 // 5. Expiry replaces even local answered state; human versus policy refusal distinct.
 const expired=scenario("expired");assert.equal(view(expired).kind,"expired");assert.equal(p.canAnswerQuestion(expired.questions[0]),false);assert.equal(reconcileQuestion(expired.questions[0],{...question,status:"answered",answer:true}).status,"expired");
 assert.equal(view({...expired,run:{...run,finishing_reason:"approval_expired"}}).phase,"finishing");
 // A person's own refusal and a policy refusal of one step are not run-level cards; the step and record carry them.
 assert.equal(view({run,questions:[{...question,status:"answered",answer:false,answered_by:"human"}]}),undefined);
 assert.equal(view({run,questions:[{...question,approval:{...question.approval,review:{...question.approval.review,status:"policy_denied"}}}]}),undefined);
 console.log("PASS 5: expiry reconciliation, finishing, refusals stay on steps, no old controls");
 // 6. Unknown outranks overload/expiry; verify before acting; another sent result retained.
 const unknown=scenario("unknown"), uv=view(unknown);assert.equal(uv.kind,"uncertain_effect");assert.equal(uv.action,"verify_steps");assert(uv.facts.join(" ").includes("已发送记录"));assert(markup(unknown).includes('data-draft-status="sent"'));
 assert.equal(p.toolExecutionState({...tool,status:"completed"}),"unknown");
 const completedTool={...tool,status:"completed",outcome:undefined,result:"Synthetic completed business result",updated_at:"2026-10-05T10:00:00.000000002Z"};
 assert.equal(reconcileToolActivity(completedTool,{...tool,updated_at:"2026-10-05T10:00:00.000000001Z"}).result,completedTool.result);
 const conflict=reconcileToolActivity(completedTool,{...tool,updated_at:completedTool.updated_at});assert.equal(p.toolExecutionState(conflict),"unknown");assert.equal(conflict.result,completedTool.result);
assert.equal(toolDisplayLabel({...tool,status:"completed"}),"结果待核实");
 assert.equal(view({...expired,tools:unknown.tools}).kind,"uncertain_effect");assert(markup({...unknown,drafts:[{...draft,status:"sent",demo:true}]}).includes("Demo; not sent"));
 console.log("PASS 6: uncertain effects first, visible verification, conflict conservative, sent/demo independent");
 // 7. Durable anchor/key through all phases and reconnect; no success card; isolation.
 const ownerKey=p.buildTaskOwners([run],[request],[],[])[0].key;
 for(const status of ["queued","running","waiting","failed","done"]) {const owner=p.buildTaskOwners([{...run,status}],[request],[],[])[0];assert.equal(owner.key,ownerKey);assert.equal(owner.anchor.id,request.id);}
 assert.equal(view(scenario("disconnected")).kind,"connection_status");assert.equal(view({...scenario("disconnected"),connected:true}),undefined);
 assert(!markup(scenario("done")).includes("task-issue-card"));assert(!markup(scenario("done")).includes("task-run-phase"));
 assert.equal(mergeRunUpdates([{...run,status:"failed"}],[{...run,status:"running",updated_at:"2026-10-05T11:00:00Z"}])[0].status,"failed");
 for(const change of [{bot_id:"other"},{conversation_id:"other"},{kind:"schedule"}]) assert.equal(p.buildTaskOwners([incident.run,{...run,...change,id:"other",parent_run_id:run.id,status:"running"}],[request],[],[]).length,2);
 assert.equal(p.buildTaskOwners([{...run,status:"failed"}],[],[],[]).length,0);
 const retry={...run,id:"synthetic-retry",parent_run_id:run.id};
 const priorOutput={...request,id:"synthetic-prior-output",run_id:run.id,role:"assistant",seq:2,content:"Synthetic prior result",attachments:[{id:"synthetic-file",name:"synthetic-result.txt"}]};
 const historyOwner=p.buildTaskOwners([{...run,status:"failed"},retry],[request,priorOutput],[],[])[0];
 const historyMarkup=renderToStaticMarkup(createElement(TaskRunBlock,{owner:historyOwner,tools:[],questions:[],drafts:[],messages:[request,priorOutput],summaries:[],onOpenTools:()=>{},onRefresh:async()=>{},renderQuestion:()=>null,renderDraft:()=>null,renderMessage:message=>createElement("p",null,message.attachments[0].name)}));
 assert(historyMarkup.includes("synthetic-result.txt"),"previous attachment-bearing outputs reach the existing message renderer");
 console.log("PASS 7: stable owner, no success card, connection versus run, terminal no revival, bot/schedule isolation");
 // 8. Both languages, neutral section, native details and untouched protected components.
 const english=markup(incident,"en");assert(english.includes("Pre-execution check is missing information"));assert(english.includes("Model service is temporarily busy"));assert(english.includes("Copy diagnostics"));assert(html.includes("技术详情"));assert(!html.includes('<details class="task-activity" open'));
 const css=await readFile(join(ui,"src/task-issue-card.css"),"utf8");for(const rule of ["min-height:44px","max-width:var(--chat-max)","prefers-reduced-motion","overflow-wrap:anywhere","max-width:560px"])assert(css.includes(rule));
 for(const file of ["ui/src/BotDesktopPanel.tsx","ui/src/MemoryPanel.tsx","ui/src/ModelSettings.tsx"]) {const current=await readFile(join(ui,"..",file));const base=execFileSync("git",["show",`fcb2157c58069db40ea13c0bdb12697cb6b2e2c5:${file}`],{cwd:join(ui,"..")});assert(current.equals(base),file);}
 console.log("PASS 8: bilingual copy, collapsed activity, semantic sections, 44px/reduced motion, protected components byte-identical");
} finally {await server.close();}
