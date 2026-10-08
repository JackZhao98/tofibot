import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {dirname, join, basename} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
import {openUiModules} from "./ui-modules.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
// Load through Vite so the i18n catalogs resolve; assertions pin the shipped zh-CN copy.
const modules = await openUiModules({language: "zh-CN"});
try {
  const {reconcileQuestion, buildQuestionTimeline, autoReviewPresentation} = await modules.load("/src/questionTimeline.ts");
  const approved = {question_id: "synthetic", conversation_id: "c", run_id: "r", bot_id: "b", type: "question", question_type: "approval", question: "Allow?", status: "answered", answer: true, answered_by: "auto-review", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:01.000001Z", approval: {action: "read", target: "public fact", impact: "read", review: {source: "auto-review", status: "approved", reason: "synthetic <script>untrusted</script>", model: "codex-auto-review"}}};
  const invalidated = {...approved, status: "pending", answer: undefined, answered_by: undefined, updated_at: "2026-01-01T00:00:01.000002Z", approval: {...approved.approval, review: {...approved.approval.review, status: "invalidated", reason: "Mode changed; human review required."}}};
  assert.deepEqual(reconcileQuestion(invalidated, approved), invalidated, "off-switch SSE must replace an old locally cached auto approval");
  assert.deepEqual(reconcileQuestion(approved, invalidated), invalidated, "older reconnect data must not restore automatic authorization");
  const human = {...approved, answered_by: "human", updated_at: "2026-01-01T00:00:02Z", approval: {...approved.approval, review: {...approved.approval.review, status: "human_required"}}};
  assert.equal(reconcileQuestion(human, approved).answered_by, "human", "human race retains the real decision source");
  const expired = {...approved, status: "expired", updated_at: approved.updated_at};
  assert.equal(reconcileQuestion(expired, approved).status, "expired", "expiry wins even for legacy equal-version snapshots");
  const restored = JSON.parse(JSON.stringify(approved));
  const timeline = buildQuestionTimeline([], [restored], false);
  assert.equal(timeline[0].question.answered_by, "auto-review");
  assert.equal(timeline[0].question.approval.review.reason, approved.approval.review.reason, "metadata stays plain data through reconnect");
  for (const [status, label] of [["setup_required", "配置缺口"], ["context_required", "上下文缺口"], ["unavailable", "审查不可用"], ["policy_denied", "策略判决拒绝"]]) {
    const blocked = {...approved, status:"cancelled", answer:undefined, answered_by:undefined, updated_at:"2026-01-01T00:00:03Z", approval:{...approved.approval, review:{...approved.approval.review,status}}};
    assert.equal(autoReviewPresentation(blocked).label, label, `${status} is not a generic approval demand`);
    assert.equal(reconcileQuestion(approved, blocked).status,"cancelled", "reconnect cannot reopen a blocked proposal");
  }
  assert.equal(autoReviewPresentation(expired).label,"提案已失效");
  assert.equal(autoReviewPresentation({...approved,status:"cancelled"}).label,"不可执行");
  assert.equal(autoReviewPresentation(human).label,"人工决定有效");
  const awaitingHuman = {...human,status:"pending",answered_by:undefined,answer:undefined};
  assert.equal(autoReviewPresentation(awaitingHuman).label,"策略需要人工决定");
  assert.equal(autoReviewPresentation({...awaitingHuman,approval:{...human.approval,review:{...human.approval.review,status:"not_eligible"}}}).label,"未获自动执行资格");
  for (const status of ["shadow_allow","shadow_deny","shadow_needs_human","shadow_context_required","shadow_setup_required","shadow_unavailable","shadow_invalidated","shadow_reviewing"]) {
    const advice = {...awaitingHuman,status:"run_done",approval:{...human.approval,review_only:true,review:{...human.approval.review,status}}};
    assert.match(autoReviewPresentation(advice).label,/观察/);
    assert.match(autoReviewPresentation(advice).badge,/原有执行策略/);
    assert.equal(JSON.parse(JSON.stringify(advice)).answered_by,undefined);
  }
  const humanWithShadowDeny={...human,approval:{...human.approval,review:{...human.approval.review,status:"shadow_deny"}}};
  assert.equal(autoReviewPresentation(humanWithShadowDeny).badge,"人工已决定 · 观察建议");
  assert.equal(autoReviewPresentation(humanWithShadowDeny).label,"观察建议拒绝");
  const v5Denied={...human,approval:{...human.approval,review:{...human.approval.review,status:"policy_denied",risk_level:"low",confirmation_required:false,policy_version:"mcp-all-external-v5"}}};
  assert.equal(autoReviewPresentation(v5Denied).label,"策略判决拒绝","a human race must not label a hard denial as execution permission");
  for (const [status,label] of [["setup_required","配置缺口"],["context_required","上下文缺口"],["unavailable","审查不可用"],["terminal","不可执行"]]) {
    assert.equal(autoReviewPresentation({...v5Denied,approval:{...v5Denied.approval,review:{...v5Denied.approval.review,status}}}).label,label);
  }
  const v5Reviewing={...awaitingHuman,approval:{...human.approval,review:{...human.approval.review,status:"reviewing",policy_version:"mcp-all-external-v5"}}};
  assert.equal(autoReviewPresentation(v5Reviewing).badge,"AutoReview 审查中");
  await modules.setLanguage("en");
  assert.equal(autoReviewPresentation(v5Reviewing).badge,"AutoReview checking","English copy comes from the en catalog");
  assert.equal(autoReviewPresentation(v5Denied).label,"Denied by policy");
  await modules.setLanguage("zh-CN");
  console.log("PASS AutoReview UI reconnect, off switch, expiry and human decision provenance");
} finally {await modules.close();}

// Optional finite rendering acceptance. The supplied Playwright/Chrome are local
// tools; every API response and displayed identity below is newly synthetic.
if (process.env.TOFI_AUTOREVIEW_RENDERED === "1") {
  assert.ok(process.env.PLAYWRIGHT_MODULE, "Set PLAYWRIGHT_MODULE to a trusted installed module");
  const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
  const {createServer} = await import("vite");
  const {default:react} = await import("@vitejs/plugin-react");
  const fixture = await mkdtemp(join(ui, ".autoreview-composition-"));
  let server, browser;
  try {
    // Keep actual component styles, with the existing remote font import removed
    // only from this temporary fixture. Rendering acceptance needs no font host.
    const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
    const offlineFoundations = foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, "");
    assert.notEqual(offlineFoundations, foundations, "Expected the known remote font import");
    await writeFile(join(fixture, "foundations.css"), offlineFoundations);
    await writeFile(join(fixture,"index.html"),'<div id="root"></div><script type="module" src="./main.tsx"></script>');
    await writeFile(join(fixture,"main.tsx"),`import React from 'react';import {createRoot} from 'react-dom/client';import {i18nReady,setLanguage} from '../src/i18n';import {TimezoneProvider} from '../src/UserTimezone';import {AutoReviewSettings} from '../src/AutoReviewSettings';import {QuestionCard} from '../src/QuestionCard';import '../src/styles.css';import '../src/settings-system.css';import './foundations.css';const status=new URLSearchParams(location.search).get('status')||'context_required';const state=new URLSearchParams(location.search).get('state')||'pending';const item={question_id:'synthetic-question',conversation_id:'synthetic-conversation',bot_id:'synthetic-bot',run_id:'synthetic-run',type:'question',question_type:'approval',question:'Synthetic bounded operation',status:state,created_at:'2026-01-01T00:00:00Z',approval:{review_only:status.startsWith('shadow_'),action:'Synthetic read',target:'Synthetic fact',impact:'Synthetic effect',review:{source:'auto-review',status,reason:'Synthetic <script>untrusted</script>',model:'codex-auto-review'}}};// Assertions pin the shipped zh-CN copy; render once its catalog is in.
void i18nReady.then(()=>setLanguage('zh-CN')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><AutoReviewSettings/><QuestionCard item={item as any} bot={undefined} group={false} archived={false} onChanged={async()=>{}}/></TimezoneProvider>));`);
    server = await createServer({configFile:false,root:ui,plugins:[react()],server:{host:"127.0.0.1",port:0},logLevel:"error"});
    await server.listen();
    const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
    browser = await chromium.launch({executablePath:process.env.TOFI_TEST_CHROME});
    const page = await browser.newPage();
    let writes = 0, unexpected = 0, cases = 0;
    const unexpectedPaths = [];
    await page.route("**/*",async route=>{
      const request=route.request(), url=new URL(request.url());
      if(url.origin!==origin){unexpected++;unexpectedPaths.push(url.origin+url.pathname);await route.abort();return;}
      if(!url.pathname.startsWith("/api/")){await route.continue();return;}
      if(request.method()!=="GET"){writes++;await route.fulfill({status:500,body:"Synthetic writes forbidden"});return;}
      const body=url.pathname==="/api/auto-review-settings"?{mode:"off",revision:0,review_scope:"all_external_tools"}:url.pathname==="/api/preferences"?{timezone:"UTC",timezone_configured:true}:undefined;
      if(!body){unexpected++;unexpectedPaths.push(url.pathname);await route.fulfill({status:500,body:"Unexpected synthetic request"});return;}
      await route.fulfill({contentType:"application/json",body:JSON.stringify(body)});
    });
    for(const viewport of [{width:1280,height:900},{width:390,height:844}]){
      await page.setViewportSize(viewport);
      for(const [status,state] of [["shadow_allow","run_done"],["shadow_deny","run_done"],["shadow_unavailable","run_done"],["setup_required","pending"],["context_required","pending"],["unavailable","pending"],["policy_denied","pending"],["terminal","pending"],["approved","expired"],["approved","cancelled"],["approved","run_done"]]){
        await page.goto(`${origin}/${basename(fixture)}/index.html?status=${status}&state=${state}`);
        const settings=page.locator("section").filter({has:page.getByRole("heading",{name:"AutoReview",exact:true})});
        await settings.getByText("审查范围：所有外部工具。",{exact:false}).waitFor({timeout:10000});
        assert.equal(await settings.getByRole("combobox").inputValue(),"off");
        for (const [mode, text] of [["shadow","不新增等待或执行权限"],["auto","高风险、需要确认的提案仍须由你批准"],["off","不请求审查"]]) {
          await settings.getByRole("combobox").selectOption(mode);
          await settings.getByText(text,{exact:false}).waitFor();
        }
        const card=page.locator('[data-question-id="synthetic-question"]');
        await card.getByText("Synthetic <script>untrusted</script>",{exact:false}).waitFor({timeout:10000});
        assert.equal(await card.getByRole("button").count(),0,`${status}/${state} must have no approval controls`);
        assert.equal(await card.locator("script").count(),0,"review reason remains plain text");
        cases++;
      }
    }
    assert.equal(writes,0);assert.equal(unexpected,0,JSON.stringify(unexpectedPaths));
    console.log(`PASS ${cases} rendered AutoReview composition cases: desktop/narrow default OFF/all-external scope, technical gaps/terminal cards, plain-text reasons, zero writes/external requests`);
  } finally {
    await browser?.close();await server?.close();await rm(fixture,{recursive:true,force:true});
  }
}
