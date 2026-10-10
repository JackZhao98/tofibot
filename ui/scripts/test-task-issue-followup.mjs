import assert from "node:assert/strict";
import {mkdir, readFile, writeFile} from "node:fs/promises";
import {dirname, join} from "node:path";
import {fileURLToPath} from "node:url";
import {createServer} from "vite";
import {createElement} from "react";
import {renderToStaticMarkup} from "react-dom/server";
import {at, run, request, tool, draft} from "./task-issue-fixtures.mjs";

// The two designer HOLD findings only: no listener, browser or provider calls.
const ui=dirname(dirname(fileURLToPath(import.meta.url)));
const evidence=join(ui,"../review-evidence");
const server=await createServer({configFile:false,root:ui,server:{middlewareMode:true,hmr:false,ws:false},logLevel:"error"});
const originalDocument=globalThis.document;
try {
  // MailDraftCard follows the active UI language; the loop below switches it.
  const i18n=await server.ssrLoadModule("/src/i18n/index.ts");
  await i18n.i18nReady;
  await i18n.loadLanguage("en");
  await i18n.loadLanguage("zh-CN");
  const p=await server.ssrLoadModule("/src/taskIssuePresentation.ts");
  const {TaskRunBlock}=await server.ssrLoadModule("/src/TaskRunBlock.tsx");
  const {MailDraftCard}=await server.ssrLoadModule("/src/MailDraftCard.tsx");
  const {reconcileToolActivity}=await server.ssrLoadModule("/src/toolTimeline.ts");
  const previous={...run,status:"failed",error:"server_is_overloaded"};
  const latest={...run,id:"synthetic-retry-done",parent_run_id:run.id,status:"done",created_at:"2026-10-05T10:05:00Z",updated_at:"2026-10-05T10:06:00Z"};
  const final={...request,id:"synthetic-later-answer",role:"assistant",run_id:latest.id,seq:2,content:"Synthetic follow-up answer; it does not confirm the earlier send."};
  const owner=p.buildTaskOwners([previous,latest],[request,final],[],[])[0];
  const uncertainTool={...tool,outcome:{...tool.outcome,code:"mcp_result_unknown",status:"uncertain_effect",execution_certainty:"unknown"}};
  const secondTool={...uncertainTool,call_id:"synthetic-call-2",started_at:"2026-10-05T10:00:01Z"};
  const uncertainDraft={...draft,subject:"PRIVATE_SUBJECT_A",to:"private-a@example.invalid",body:"PRIVATE_BODY_A"};
  const sentDraft={...draft,draft_id:"synthetic-draft-2",run_id:latest.id,created_at:"2026-10-05T10:05:00Z",status:"sent",subject:"PRIVATE_SUBJECT_B",to:"private-b@example.invalid",body:"PRIVATE_BODY_B"};
  const familyView=(tools,drafts,locale="en",extra={})=>p.presentTaskIssue({run:latest,family:owner.family,tools,drafts,locale,recordsComplete:true,...extra});
  const render=(tools,drafts,locale="en")=>{
    globalThis.document={documentElement:{lang:locale}};
    return renderToStaticMarkup(createElement(TaskRunBlock,{owner,tools,drafts,questions:[],messages:[request,final],summaries:[],details:{[latest.id]:{hasMore:false}},locale,onOpenTools:()=>{},onRefresh:async()=>{},renderQuestion:()=>null,renderDraft:item=>createElement(MailDraftCard,{key:item.draft_id,draft:item,group:false,issueOwned:true,onChanged:async()=>{}})}));
  };
  // Remove all closed details bodies, retaining their summaries, including
  // nested disclosures. This asserts collapsed-view content rather than merely
  // finding an uncertainty word somewhere in the full hidden markup.
  function closedText(html) {
    const stack=[];let text="";
    for(const token of html.match(/<[^>]+>|[^<]+/g)??[]) {
      if(/^<details\b/.test(token)){assert(!/\bopen(?:=|\s|>)/.test(token));stack.push({summary:false});}
      else if(/^<\/details>/.test(token))stack.pop();
      else if(/^<summary\b/.test(token)){if(stack.length)stack.at(-1).summary=true;}
      else if(/^<\/summary>/.test(token)){if(stack.length)stack.at(-1).summary=false;}
      else if(token[0]!=="<"&&stack.every(level=>level.summary))text+=token+" ";
    }
    return text.replace(/&#x27;/g,"'").replace(/&amp;/g,"&").replace(/\s+/g," ").trim();
  }
  const draftLabel=(item,locale="en")=>p.taskDraftLabel(item,locale);
  const toolLabel=(item,locale="en")=>p.taskToolLabel(item,locale);

  // P1: later completion has no authority over either earlier unknown object.
  const initial=familyView([uncertainTool],[uncertainDraft,sentDraft]);
  assert.equal(initial.kind,"uncertain_effect");assert.equal(initial.action,"verify_steps");
  assert(initial.facts.some(fact=>fact.startsWith(toolLabel(uncertainTool))));
  assert(initial.facts.some(fact=>fact.startsWith(draftLabel(uncertainDraft))));
  assert(initial.secondary.some(fact=>fact.includes("later attempt ended")));
  const initialHTML=render([uncertainTool],[uncertainDraft,sentDraft]);
  const visible=closedText(initialHTML);
  assert.equal((initialHTML.match(/data-task-owner=/g)??[]).length,1);
  assert.equal((initialHTML.match(/class="task-issue-card"/g)??[]).length,1);
  // Compact card: title, what, one sentence, one button. No per-call clock lines.
  assert(visible.includes("Not sure this went through"));assert(visible.includes("Check your sent mail before trying again."));assert(visible.includes("How to check"));
  assert(visible.includes(p.taskToolLabel(uncertainTool).replace(/^\S+\s/,"").split(" · ")[0]),"names the action");
  assert(!visible.includes(toolLabel(uncertainTool)),"no per-call time line in the closed card");
  assert(!visible.includes("Completed tool steps are retained")&&!visible.includes("not fully loaded")&&!visible.includes("may have taken effect"));
  assert(initial.compact&&initial.compact.sentence==="Check your sent mail before trying again.");
  assert.equal((initialHTML.match(/class="task-issue-fact/g)??[]).length,1,"one subject line");
  assert(initialHTML.replace(/<[^>]+>/g," ").includes(draftLabel(uncertainDraft)),"per-object records stay in the collapsed details");
  assert(!initialHTML.includes(final.content),"normal latest final is not moved into the task owner");
  assert(!initialHTML.includes("approval-actions"));assert(!initialHTML.includes("Retry"));

  const resolvedTool=reconcileToolActivity(uncertainTool,{...uncertainTool,status:"completed",outcome:undefined,result:"Authoritative synthetic completion",updated_at:"2026-10-05T10:07:00Z"});
  const oneCallResolved=familyView([resolvedTool,secondTool],[uncertainDraft,sentDraft]);
  assert(!oneCallResolved.facts.some(fact=>fact.startsWith(toolLabel(uncertainTool))));
  assert(oneCallResolved.facts.some(fact=>fact.startsWith(toolLabel(secondTool))));
  assert(oneCallResolved.facts.some(fact=>fact.startsWith(draftLabel(uncertainDraft))));
  const resolvedDraft={...uncertainDraft,status:"sent",revision:2,updated_at:"2026-10-05T10:07:00Z"};
  const oneDraftResolved=familyView([uncertainTool,secondTool],[resolvedDraft,sentDraft]);
  assert(!oneDraftResolved.facts.some(fact=>fact.startsWith(draftLabel(uncertainDraft))));
  assert(oneDraftResolved.facts.some(fact=>fact.startsWith(toolLabel(uncertainTool))));
  assert(oneDraftResolved.facts.some(fact=>fact.startsWith(toolLabel(secondTool))));
  assert.equal(familyView([resolvedTool,tool],[resolvedDraft,sentDraft]),undefined,"resolved historical context/overload must stay in history");
  assert(!render([resolvedTool,tool],[resolvedDraft,sentDraft]).includes("task-issue-card"));
  const differentAttemptCall={...resolvedTool,run_id:latest.id};
  assert(familyView([uncertainTool,differentAttemptCall],[]).facts.some(fact=>fact.startsWith(toolLabel(uncertainTool))),"same call text in another attempt does not resolve the earlier call");
  assert.equal(familyView([{...uncertainTool,bot_id:"other-bot"},{...uncertainTool,conversation_id:"other-chat"}],[]),undefined);
  const app=await readFile(join(ui,"src/App.tsx"),"utf8");
  const announcementSource=app.slice(app.indexOf("const taskAnnouncements ="),app.indexOf("function conversationRow",app.indexOf("const taskAnnouncements =")));
  assert(announcementSource.includes("family:owner.family, tools:loadedToolActivities, questions:questions.items, drafts:mailDrafts.items"));
  assert(announcementSource.includes("...issue.facts"));assert(announcementSource.includes("...issue.secondary"));
  console.log("PASS P1: prior call/draft unknown visible after linked done/final; only exact-object resolution clears its fact; family issue feeds announcements; resolved history stays folded");

  // P2: actual compact MailDraftCard and the verification card expose different
  // stable labels without leaking subject, recipient or body while closed.
  const languages=[];
  for(const locale of ["zh-CN","en"]) {
    await i18n.setLanguage(locale);
    const html=render([uncertainTool,secondTool],[uncertainDraft,sentDraft],locale);
    const text=closedText(html),view=familyView([uncertainTool,secondTool],[uncertainDraft,sentDraft],locale);
    const a=draftLabel(uncertainDraft,locale),b=draftLabel(sentDraft,locale);
    assert.notEqual(a,b);assert(html.replace(/<[^>]+>/g," ").replace(/&#x27;/g,"'").replace(/\s+/g," ").includes(`${a}: ${locale==="en"?"Sending result needs checking.":"发送结果待核实。"}`),"identity kept in details");
    assert(!text.includes(a),"closed card does not repeat per-call labels");
    assert(text.includes(`${b}: ${locale==="en"?"Email was sent":"邮件已发送"}`));
    assert(!view.facts.some(fact=>fact.startsWith(b)),"confirmed draft B is not described as unknown");
    const diag=p.taskDiagnostics(view);
    for(const secret of [uncertainDraft.subject,uncertainDraft.to,uncertainDraft.body,sentDraft.subject,sentDraft.to,sentDraft.body]){assert(!text.includes(secret));assert(!diag.includes(secret));}
    const reversed=render([secondTool,uncertainTool],[sentDraft,{...uncertainDraft,subject:"Changed private subject"}],locale).replace(/<[^>]+>/g," ").replace(/&#x27;/g,"'").replace(/\s+/g," ");
    assert(reversed.includes(a));assert(reversed.includes(b));
    assert(view.facts.some(fact=>fact.startsWith(toolLabel(uncertainTool,locale))));
    assert(view.facts.some(fact=>fact.startsWith(toolLabel(secondTool,locale))));
    languages.push({locale,unknownDraftLabel:a,sentDraftLabel:b,collapsedText:text,html});
  }
  const demoText=closedText(render([],[{...sentDraft,demo:true}],"en"));assert(demoText.includes("Demo completed; no email was sent"));
  const css=await readFile(join(ui,"src/task-issue-card.css"),"utf8");assert(css.includes("overflow-wrap:anywhere"));
  console.log("PASS P2: bilingual closed-view identities for unknown A and sent B; scoped distinct calls; neutral labels stable through reordering/subject edit; diagnostics contain no private business text");

  const report={parent:"b5f008295b808a48d68819c6b6556e5b71713da2",mode:"CPU-only targeted SSR/components; no browser/listener",findings:["P1","P2"],ownerCount:1,issueCardCount:1,latestStatus:latest.status,latestFinalRemainsOutsideOwner:true,activityInitiallyOpen:false,exactObjectResolution:true,announcementFamilyInput:true,languages:languages.map(({html,...row})=>row),staticReproWidth:390,pixelLayoutRevalidated:false};
  await mkdir(evidence,{recursive:true});
  await writeFile(join(evidence,"followup-repro.json"),JSON.stringify(report,null,2)+"\n");
  const styles=(await Promise.all(["styles.css","v2-foundations.css","task-issue-card.css","mail-draft-card.css"].map(file=>readFile(join(ui,"src",file),"utf8")))).join("\n").replace(/@import[^;]+;/g,"");
  const demo=languages.map(({locale,html})=>`<section class="repro" lang="${locale}"><h2>${locale} · 390px static component reproduction</h2>${html}<article class="normal-final">${final.content}</article></section>`).join("");
  await writeFile(join(evidence,"followup-repro.html"),`<!doctype html><html lang="en"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>Two targeted designer corrections</title><style>${styles}\nbody{display:block;height:auto;overflow:auto;padding:24px}.repro{width:390px;max-width:100%;margin:0 0 32px}.repro h2{font:var(--type-meta)}.normal-final{margin-top:16px;font:var(--type-body)}</style><body>${demo}</body></html>`);
} finally {globalThis.document=originalDocument;await server.close();}
