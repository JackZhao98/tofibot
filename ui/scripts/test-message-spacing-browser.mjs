// Real Chrome, production components/CSS, synthetic messages; no production API.
import assert from 'node:assert/strict';
import {mkdirSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const output=resolve(process.env.FAILURE_UI_OUTPUT||'../docs/acceptance/failure-retry-20261001');mkdirSync(output,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.CHROME_EXECUTABLE?{executablePath:process.env.CHROME_EXECUTABLE}:{})});
const origin=process.env.FAILURE_UI_ORIGIN||'http://127.0.0.1:5194';
const results=[];
try{
 for(const [name,width,height] of [['narrow',390,844],['desktop',1280,1000]]){
  const context=await browser.newContext({viewport:{width,height},deviceScaleFactor:2,timezoneId:'America/Los_Angeles'});const page=await context.newPage();
  await page.route('**/api/preferences',r=>r.fulfill({json:{timezone:'America/Los_Angeles',timezone_configured:true}}));
  for(const theme of ['dark','light'])for(const desktop of [false,true]){
   const errors=[];page.on('pageerror',e=>errors.push(e.message));
   await page.goto(`${origin}/tests/message-spacing-audit.html?theme=${theme}${desktop?'&desktop=1&identity=1':''}`);
   await page.locator('[data-message-id="b5"]').waitFor();
   const geometry=await page.evaluate(()=>Array.from(document.querySelectorAll('.message')).map((e,i,a)=>{
    const c=e.querySelector('.message-content').getBoundingClientRect(),r=e.getBoundingClientRect(),next=a[i+1]?.getBoundingClientRect();
    return {margin:parseFloat(getComputedStyle(e).marginBottom),gap:next?next.top-r.bottom:null,contentHeight:c.height,title:e.title};
   }));
   assert.equal(errors.length,0,errors.join('\n'));
   for(let i=0;i<geometry.length;i++){assert.equal(geometry[i].margin,i===8?6:10);assert.ok(geometry[i].title.includes('America/Los_Angeles'));if(i<5)assert.ok(Math.abs(geometry[i].gap-10)<.1,JSON.stringify(geometry));}
   assert.ok(await page.locator('body').evaluate(e=>e.scrollWidth<=innerWidth+1),'no horizontal overflow');
   assert.equal(await page.locator('.reaction-chip').count(),1);
   const reaction=await page.locator('.reaction-chip').boundingBox(),prev=await page.locator('[data-message-id="b3"] .message').boundingBox(),next=await page.locator('[data-message-id="u4"] .message').boundingBox();
   assert.ok(reaction.y>=prev.y+prev.height-1);assert.ok(next.y>=reaction.y+reaction.height-1,'reaction does not overlap next message');
   const work=page.getByRole('button',{name:/工作过程/});await work.click();assert.equal(await work.getAttribute('aria-expanded'),'true');assert.ok(await page.getByText('检查对话记录',{exact:false}).count()>0);await work.click();
   await page.screenshot({path:`${output}/spacing-${name}-${theme}${desktop?'-desktop-shell':''}.png`,fullPage:true});
   results.push({name,theme,desktop,geometry});
  }
  await context.close();
 }
 writeFileSync(`${output}/spacing-results.json`,JSON.stringify(results,null,2));console.log('PASS: 8 real-Chrome spacing cases (390/1280, dark/light, Web/desktop shell, multiline, timestamp, reaction, tool disclosure, compact)');
}finally{await browser.close()}
