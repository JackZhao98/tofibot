// Full canonical App; HTTP/SSE/owner/desktop bridge data is synthetic.
import assert from 'node:assert/strict';
import {mkdirSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.EXPIRY_UI_ORIGIN||'http://127.0.0.1:5287';
const output=resolve(process.env.EXPIRY_UI_OUTPUT||'docs/acceptance/approval-expiry');mkdirSync(output,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.CHROME_EXECUTABLE?{executablePath:process.env.CHROME_EXECUTABLE}:{})});
const results=[];let activePage;
try{
 for(const [name,width,native] of [['web-mobile',390,false],['web-desktop',1280,false],['native-ui',1280,true]]){
  const context=await browser.newContext({viewport:{width,height:950}});
  if(native)await context.addInitScript(()=>{window.tofiDesktop={platform:'darwin',themePreference:'system',onCommand:()=>()=>{},onFlushState:()=>()=>{},onWindowState:()=>()=>{},remoteInputFocus:()=>{},readWorkspaceState:async()=>({}),writeWorkspaceState:async()=>{},appearance:async()=>{}}});
  const page=await context.newPage(),errors=[];activePage=page;page.on('pageerror',e=>errors.push(e.message));
  page.setDefaultNavigationTimeout(90000);
  const ready=async(question=true)=>{await page.locator('.conversation-row').first().waitFor();if(width<600)await page.locator('.conversation-row').first().click();if(question)await page.locator('[data-question-id="approval-old"]').waitFor();await page.waitForFunction(()=>window.runtimeAudit.streamReady())};
  const check=async(label)=>{assert.deepEqual(errors,[]);assert.equal(await page.getByRole('button',{name:'重新请求审批',exact:true}).count(),0);const calls=await page.evaluate(()=>window.expiryAudit.requests.filter(r=>r.method!=='GET'&&!r.path.endsWith('/read')&&!r.path.endsWith('/answer')&&r.path!=='/api/auth/login'));assert.deepEqual(calls,[]);assert.ok(await page.locator('body').evaluate(e=>e.scrollWidth<=innerWidth+1));results.push({viewport:name,case:label,pass:true})};
  await page.goto(origin+'/test-fixtures/runtime/expiry-audit.html');await ready();
  await page.locator('[data-question-id="approval-old"] .approval-card.is-pending').waitFor();
  await page.evaluate(()=>window.expiryAudit.expire());
  await page.locator('[data-question-id="approval-old"]').getByText('已过期',{exact:true}).waitFor();
  await page.getByText('正在收尾',{exact:true}).first().waitFor();
  const disclosure=page.locator(native?'.v2-tool-run':'.web-tool-run').first();
  if(native)await disclosure.evaluate(e=>{e.open=true});else await disclosure.locator('.web-tool-toggle').click();
  const step=page.locator(native?'.tool-status-expired':'.web-tool-step.is-expired').first();await step.waitFor();assert.match(await step.innerText(),/已过期/);
  const before=await step.innerText();await page.evaluate(()=>window.expiryAudit.otherBot());await page.waitForTimeout(1200);assert.equal(await step.innerText(),before,'expired execution timer must freeze during other Bot activity');
  await check('expiry-label-bounded-finishing-frozen-timer');await page.screenshot({path:output+'/'+name+'-finishing.png',fullPage:true});
  await page.reload();await ready();await page.getByText('正在收尾',{exact:true}).first().waitFor();await check('reload-preserves-expired-finishing');
  await page.evaluate(()=>window.failureAudit.reconnect());await page.waitForFunction(()=>window.runtimeAudit.streamReady());await page.locator('[data-question-id="approval-old"]').getByText('已过期',{exact:true}).waitFor();await check('sse-reconnect-preserves-expiry');
  await page.evaluate(()=>{window.expiryAudit.conclude();window.expiryAudit.conclude()});await page.locator('[data-message-id="expiry-summary"] .message-content').getByText('审批已过期，本次工作已停止。已完成结果保留；需要审批的步骤和批次尾部未执行。',{exact:true}).waitFor();assert.equal(await page.getByText('正在收尾',{exact:true}).count(),0);await check('one-visible-system-conclusion-and-terminal-expiry');
  await page.reload();await ready();await page.locator('[data-message-id="expiry-summary"] .message-content').getByText('审批已过期，本次工作已停止。已完成结果保留；需要审批的步骤和批次尾部未执行。',{exact:true}).waitFor();await check('terminal-reload-no-executing-or-renew');await page.screenshot({path:output+'/'+name+'-finished.png',fullPage:true});
  await page.evaluate(()=>{window.expiryAudit.reset()});await page.reload();await ready();await page.locator('[data-question-id="approval-old"]').getByRole('button',{name:'批准',exact:true}).click();await page.locator('[data-question-id="approval-old"]').getByText('已批准',{exact:true}).waitFor();await page.evaluate(()=>window.expiryAudit.expire());await page.locator('[data-question-id="approval-old"]').getByText('已过期',{exact:true}).waitFor();assert.equal(await page.locator('[data-question-id="approval-old"]').getByText('已批准',{exact:true}).count(),0);await check('answer-then-expiry-projects-authoritative-state');
  await page.evaluate(()=>window.runtimeAudit.loseSession());await page.getByRole('heading',{name:'重新登录',exact:true}).waitFor();await page.locator('.owner-card input[name="identifier"]').fill('synthetic-owner');await page.locator('.owner-card input[name="password"]').fill('synthetic-test-password');await page.getByRole('button',{name:'登录',exact:true}).click();await ready();await page.locator('[data-question-id="approval-old"]').getByText('已过期',{exact:true}).waitFor();assert.equal(await page.locator('[role="alert"]').count(),0);await check('relogin-restores-expiry-without-stale-error-flood');
  await page.evaluate(()=>window.expiryAudit.reset());await page.goto(origin+'/test-fixtures/runtime/expiry-audit.html?scenario=running');await ready(false);await page.evaluate(()=>window.failureAudit.status('retry-1','failed'));await page.locator('[data-latest-run-id="retry-1"] .run-terminal-notice').waitFor();assert.match(await page.locator('[data-latest-run-id="retry-1"] .run-terminal-notice').innerText(),/连接中断.*任务未完成/);await check('true-stream-failure-remains-visible');
  await context.close();
 }
 writeFileSync(output+'/results.json',JSON.stringify(results,null,2));console.log('PASS '+results.length+' full-App expiry checks across web mobile, web desktop and native UI branch; synthetic only.');
}catch(error){if(activePage&&!activePage.isClosed()){writeFileSync(output+"/failure-debug.json",JSON.stringify(await activePage.evaluate(()=>({text:document.body.innerText,runs:window.failureAudit.runs,questions:window.runtimeAudit.questions(),requests:window.expiryAudit.requests,ready:window.runtimeAudit.streamReady()})),null,2));await activePage.screenshot({path:output+"/failure-debug.png",fullPage:true});}throw error;}finally{await browser.close()}
