// Run with PLAYWRIGHT_MODULE pointing at an existing Playwright install.
import assert from 'node:assert/strict';
import {mkdirSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const output=resolve(process.env.CHAT_UI_OUTPUT||'../docs/acceptance/chat-ui-mobile-20261001');mkdirSync(output,{recursive:true});
const browser=await chromium.launch({headless:true,...(process.env.CHROME_EXECUTABLE?{executablePath:process.env.CHROME_EXECUTABLE}:{})});
const origin=process.env.CHAT_UI_ORIGIN||'http://127.0.0.1:5193';
const results=[];
try{
 for(const [name,width,height,mobile] of [['narrow',390,844,true],['tablet',820,1180,true],['desktop',1280,900,false]]){
  const context=await browser.newContext({viewport:{width,height},deviceScaleFactor:2,isMobile:mobile,hasTouch:mobile});const page=await context.newPage();
  for(const scenario of ['short','long-stream','long-resize','keyboard','consecutive','reduced','safearea','failure']){
   await page.goto(origin+'/tests/send-flight-audit.html'+(scenario==='safearea'?'?theme=light&safearea=1':''));
   if(scenario==='failure'){await page.getByRole('button',{name:'Fail',exact:true}).click();assert.match(await page.locator('#flight-result').innerText(),/draft preserved/);assert.equal(await page.locator('.v2-send-flight-ghost').count(),0);results.push({name,scenario,pass:true});continue;}
   if(scenario==='reduced')await page.emulateMedia({reducedMotion:'reduce'});
   if(scenario.startsWith('long')||scenario==='keyboard')await page.getByRole('button',{name:'Long draft',exact:true}).click();
   await page.getByRole('button',{name:scenario==='long-stream'?'Send + stream':scenario==='long-resize'?'Send + resize':'Send',exact:true}).click();
   if(scenario==='keyboard'){await page.waitForTimeout(300);await page.setViewportSize({width,height:Math.round(height*.65)});}
   await page.waitForTimeout(850);
   // Wait for the fixture's completed trace under concurrent Chrome/build load.
   await page.waitForFunction(()=>document.querySelector('#flight-result')?.textContent?.startsWith('{'));
   const result=JSON.parse(await page.locator('#flight-result').innerText());
   assert.equal(result.ghosts,0);assert.equal(result.visible,'');assert.equal(result.draft,'');assert.equal(result.focus,true);
   const last=result.frames.at(-1);if(last){assert.ok(Math.abs(last.dx)<1&&Math.abs(last.dy)<1,`${name}/${scenario} landing: ${JSON.stringify(last)}`);assert.ok(Math.abs(last.scale-1)<.005);}
   if(scenario==='consecutive'){await page.getByRole('button',{name:'Send',exact:true}).click();await page.waitForTimeout(850);assert.equal(JSON.parse(await page.locator('#flight-result').innerText()).ghosts,0);}
   const geometry=await page.locator('textarea').evaluate(e=>({font:getComputedStyle(e).fontSize,viewport:innerWidth,documentWidth:document.documentElement.scrollWidth,padding:getComputedStyle(document.querySelector('.message-scroll')).paddingLeft,mask:getComputedStyle(document.querySelector('.composer'),'::before').backgroundImage}));
   assert.equal(geometry.font,'16px');assert.ok(geometry.documentWidth<=geometry.viewport+1);assert.ok(parseFloat(geometry.padding)>=12);assert.ok(!geometry.mask.includes('/ 0.35'));
   results.push({name,scenario,result,geometry});if(scenario==='long-resize')await page.screenshot({path:`${output}/${name}.png`});
   if(scenario==='short'||scenario==='safearea')await page.screenshot({path:`${output}/${name}-${scenario}.png`});
   if(scenario==='safearea')assert.equal(await page.locator('.composer').evaluate(e=>getComputedStyle(e).paddingBottom),'34px');
   if(scenario==='keyboard')await page.setViewportSize({width,height});
   if(scenario==='reduced')await page.emulateMedia({reducedMotion:'no-preference'});
  }
  await page.goto(origin+'/tests/chat-scroll-audit.html');await page.getByRole('button',{name:'Run scroll matrix'}).click();await page.waitForTimeout(700);const scroll=await page.locator('#result').innerText();assert.match(scroll,/^PASS/);results.push({name,scroll});await context.close();
 }
 writeFileSync(`${output}/browser-results.json`,JSON.stringify(results,null,2));console.log('PASS: 24 real-Chrome flight/layout cases + 3 production-hook scroll matrices');
}finally{await browser.close()}
