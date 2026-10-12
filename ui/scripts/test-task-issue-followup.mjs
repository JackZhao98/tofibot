import assert from "node:assert/strict";
import {dirname} from "node:path";
import {fileURLToPath} from "node:url";
import {createServer} from "vite";
import {createElement} from "react";
import {renderToStaticMarkup} from "react-dom/server";
import {run, request, tool, draft} from "./task-issue-fixtures.mjs";

// Retry families with an earlier uncertain call or draft: the chat never shows an
// uncertainty card. The step row and the draft record carry the status; the Bot explains.
const ui=dirname(dirname(fileURLToPath(import.meta.url)));
const server=await createServer({configFile:false,root:ui,server:{middlewareMode:true,hmr:false,ws:false},logLevel:"error"});
const originalDocument=globalThis.document;
try {
  const i18n=await server.ssrLoadModule("/src/i18n/index.ts");
  await i18n.i18nReady;await i18n.loadLanguage("en");await i18n.loadLanguage("zh-CN");
  const p=await server.ssrLoadModule("/src/taskIssuePresentation.ts");
  const {TaskRunBlock}=await server.ssrLoadModule("/src/TaskRunBlock.tsx");
  const {MailDraftCard}=await server.ssrLoadModule("/src/MailDraftCard.tsx");
  const previous={...run,status:"failed",error:"server_is_overloaded"};
  const latest={...run,id:"synthetic-retry-done",parent_run_id:run.id,status:"done",created_at:"2026-10-05T10:05:00Z",updated_at:"2026-10-05T10:06:00Z"};
  const final={...request,id:"synthetic-later-answer",role:"assistant",run_id:latest.id,seq:2,content:"Synthetic follow-up answer."};
  const owner=p.buildTaskOwners([previous,latest],[request,final],[],[])[0];
  const uncertainTool={...tool,outcome:{...tool.outcome,code:"mcp_result_unknown",status:"uncertain_effect",execution_certainty:"unknown"}};
  const uncertainDraft={...draft,subject:"PRIVATE_SUBJECT_A",to:"private-a@example.invalid",body:"PRIVATE_BODY_A"};
  const render=(tools,drafts,locale)=>{
    globalThis.document={documentElement:{lang:locale}};
    return renderToStaticMarkup(createElement(TaskRunBlock,{owner,tools,drafts,questions:[],messages:[request,final],summaries:[],details:{[latest.id]:{hasMore:false}},locale,onOpenTools:()=>{},onRefresh:async()=>{},renderQuestion:()=>null,renderDraft:item=>createElement(MailDraftCard,{key:item.draft_id,draft:item,group:false,onChanged:async()=>{}})}));
  };
  for(const locale of ["zh-CN","en"]) {
    await i18n.setLanguage(locale);
    assert.equal(p.presentTaskIssue({run:latest,family:owner.family,tools:[uncertainTool],drafts:[uncertainDraft],locale,recordsComplete:true}),undefined,`${locale}: no issue view for an earlier uncertain call or draft`);
    const html=render([uncertainTool],[uncertainDraft],locale);
    assert(!html.includes("task-issue-card"),locale);
    for(const gone of ["Not sure this went through","不确定是否已完成","How to check","如何核对","uncertain_effect"]) assert(!html.includes(gone),`${locale}: ${gone}`);
    assert(html.includes(locale==="en"?"Result needs checking":"结果待核实"),`${locale}: step row shows the status`);
    assert(html.includes(locale==="en"?"Sending result needs checking":"发送结果待核实"),`${locale}: draft record shows the status`);
    for(const secret of [uncertainDraft.subject,uncertainDraft.to,uncertainDraft.body]) assert(!p.taskDiagnostics({kind:"unknown_failure",evidence:[],recordsComplete:true}).includes(secret));
  }
  console.log("PASS: earlier uncertain call/draft leaves status on the row and record only, no card");
} finally {globalThis.document=originalDocument;await server.close();}
