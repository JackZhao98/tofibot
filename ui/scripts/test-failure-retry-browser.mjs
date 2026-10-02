// Actual production App, in-memory HTTP and EventSource, no executable endpoints.
import assert from 'node:assert/strict';
import {mkdirSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const output=resolve(process.env.FAILURE_UI_OUTPUT||'../docs/acceptance/failure-retry-20261001');mkdirSync(output,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.CHROME_EXECUTABLE?{executablePath:process.env.CHROME_EXECUTABLE}:{})});
const origin=process.env.FAILURE_UI_ORIGIN||'http://127.0.0.1:5194',results=[];
try{
 for(const [name,width,height] of [['narrow',390,844],['desktop',1280,900]]){
  const context=await browser.newContext({viewport:{width,height},deviceScaleFactor:2});const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  const open=async(scenario='')=>{await page.goto(`${origin}/tests/retry-family-audit.html${scenario?'?scenario='+scenario:''}`);if(width<600)await page.locator('.conversation-row').first().click();await page.locator('[data-message-id="fixture-trigger"]').waitFor();await page.waitForTimeout(200)};
  const family=()=>page.locator('.run-attempt-family');
  const check=async(label)=>{assert.equal(errors.length,0,errors.join('\n'));assert.equal(await page.getByText(/Unexpected synthetic request/).count(),0);assert.ok(await page.locator('body').evaluate(e=>e.scrollWidth<=innerWidth+1));const writes=await page.evaluate(()=>window.failureAudit.requests.filter(r=>r.method!=='GET'&&!r.path.endsWith('/read')));assert.deepEqual(writes,[],label+' cannot execute/replay');results.push({name,label,pass:true})};
  await open();await page.evaluate(()=>window.failureAudit.status('original','failed'));
  await page.getByText('系统提示 · Synthetic Bot',{exact:true}).waitFor();assert.equal(await page.locator('.web-tool-run.is-live').count(),0);assert.equal(await page.getByRole('button',{name:/停止 .* 的工作/}).count(),0);await check('successful-tool-then-break');
  await page.evaluate(()=>window.failureAudit.late());await page.waitForTimeout(100);assert.equal(await page.getByText('LATE_TEXT_MUST_NOT_RENDER').count(),0);assert.equal(await page.locator('.web-tool-run.is-live').count(),0);await check('late-delta-run-tool-terminal-guard');
  await page.evaluate(()=>window.failureAudit.retry('retry-1','original'));await family().filter({has:page.locator('[data-latest-run-id]')}).count();
  await page.locator('[data-latest-run-id="retry-1"]').waitFor();assert.equal(await page.locator('.run-attempt-history').getAttribute('open'),null);assert.equal(await page.locator('.web-tool-run.is-live:visible').count(),1);await check('retry-latest-only');
  await page.locator('.run-attempt-history>summary').click();assert.equal(await page.locator('.run-attempt-history').getAttribute('open'),'');
  const history=page.locator('.run-attempt-history');await history.getByRole('button',{name:/工作过程/}).click();await history.locator('.web-tool-step-detail>summary').click();await history.getByText('PRESERVED_SUCCESS_RESULT',{exact:true}).waitFor();await check('expanded-history-success-audit');await page.locator('.run-attempt-history>summary').click();
  await page.evaluate(()=>window.failureAudit.status('retry-1','failed'));await page.locator('[data-latest-run-id="retry-1"] .run-terminal-notice').waitFor();await check('retry-fails-terminal');
  await page.evaluate(()=>window.failureAudit.retry('retry-2','retry-1'));await page.locator('[data-latest-run-id="retry-2"]').waitFor();assert.match(await page.locator('.run-attempt-history>summary').innerText(),/2 次尝试/);await page.evaluate(()=>window.failureAudit.finish('retry-2'));await page.getByText('LATEST_SUCCESS_ANSWER',{exact:true}).waitFor();assert.equal(await family().locator('.run-terminal-notice:visible').count(),0);assert.equal(await page.locator('.web-tool-run.is-live').count(),0);await check('multi-retry-success');
  await page.evaluate(()=>window.failureAudit.reconnect());await page.waitForTimeout(1250);assert.equal(await page.getByText('LATEST_SUCCESS_ANSWER',{exact:true}).count(),1);await check('reconnect-retains-terminal-family');await page.screenshot({path:`${output}/retry-${name}-success.png`});
  for(const scenario of ['failed','cancelled','done']){await open(scenario);await page.locator('[data-latest-run-id="retry-1"]').waitFor();assert.equal(await page.locator('.run-attempt-history').getAttribute('open'),null);assert.equal(await page.locator('.web-tool-run.is-live').count(),0);if(scenario!=='done')assert.equal(await family().locator('.run-terminal-notice').count(),1);await check('reload-'+scenario);if(scenario==='failed')await page.screenshot({path:`${output}/retry-${name}-failed.png`});}
  await page.goto(`${origin}/tests/retry-family-audit.html?scenario=done&paged=1`);if(width<600)await page.locator('.conversation-row').first().click();await page.locator('[data-latest-run-id="retry-1"]').waitFor();await page.locator('.run-attempt-history>summary').click();await page.locator('.run-attempt-history').getByRole('button',{name:/工作过程/}).click();await page.locator('.run-attempt-history .web-tool-step-detail>summary').click();await page.locator('.run-attempt-history').getByText('PRESERVED_SUCCESS_RESULT',{exact:true}).waitFor();await check('offpage-trigger-history-summary-detail');
  await page.goto(`${origin}/tests/retry-family-audit.html?scenario=running&oldOnly=1`);if(width<600)await page.locator('.conversation-row').first().click();await page.getByText('OLD_PROGRESS_REMAINS_REACHABLE',{exact:true}).waitFor();assert.equal(await family().count(),0);await check('no-anchor-retains-old-loaded-progress');
  await context.close();
 }
 writeFileSync(`${output}/retry-results.json`,JSON.stringify(results,null,2));console.log('PASS: full-App Chrome failure/retry/SSE/reload matrix at390/1280; zero execution mutations');
}finally{await browser.close()}
