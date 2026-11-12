import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { join, basename } from 'node:path';
import { pathToFileURL } from 'node:url';
import { openUiModules, uiRoot } from './ui-modules.mjs';

const at = n => `2026-11-12T10:00:00.${String(n).padStart(9, '0')}Z`;
const tool = (id, n) => ({ conversation_id:'c', bot_id:'b', run_id:'r', call_id:id, name:'computer_shell', arguments:'{"command":"pwd"}', result:'ok', status:'completed', started_at:at(n), updated_at:at(n+1) });
const tools = [tool('a', 1), tool('b', 2), tool('c', 5)];
const notes = [{ id:'n', conversation_id:'c', run_id:'r', role:'assistant', kind:'progress', seq:2, content:'The first check passed.', created_at:at(4) }];
const captures = Array.from({ length:5 }, (_, i) => ({ seq:i+1, call_id:'a', attachment_id:`cap${i}`, taken_at:at(2), label:`Synthetic ${i}`, redacted:false }));
const modules = await openUiModules({ language:'en' });
try {
  const model = await modules.load('/src/workTimelineModel.ts');
  const entries = model.workEntries([...tools].reverse(), notes);
  assert.deepEqual(entries.map(entry => entry.kind), ['group','narration','group']);
  assert.deepEqual(entries[0].tools.map(item => item.call_id), ['a','b']);
  assert.equal(model.workEntries(tools, [{...notes[0], kind:'internal'}]).length, 1, 'private text is never narration');
  assert.equal(model.workGroupKind(tools), 'commands');
  assert.equal(model.workGroupKind([{...tools[0], name:'web_fetch'}]), 'pages');
  assert.equal(model.groupPictures(tools, [...captures, {...captures[0], attachment_id:'secret', redacted:true}]).length, 5);
  assert.equal(model.groupPictures([tools[2]], captures).length, 0, 'captures attach by call identity');
  const shared = {...tools[0], result:JSON.stringify({shared:{attachment_id:'shared',name:'Shared capture'}})};
  assert.equal(model.groupPictures([shared], captures).at(-1).id, 'shared');
  const images = await modules.load('/src/attachmentImages.ts');
  assert.ok(images.singleBox('wide').width <= 240);
  assert.ok(images.singleBox('wide').height <= 240);
  const app = await readFile(join(uiRoot,'src/App.tsx'),'utf8');
  assert.ok(app.includes('</div>}{!isUser && hasAttachments && <div className="message-attachments bot-attachments">'), 'Bot attachments follow the closed bubble');
  console.log('PASS timeline ordering, grouping, privacy, shared capture identity, thumbnail bounds, attachment placement');
} finally { await modules.close(); }
if (!process.argv.includes('--browser')) process.exit(0);
assert.ok(process.env.PLAYWRIGHT_MODULE, 'Set PLAYWRIGHT_MODULE to run browser assertions');
const { chromium, webkit } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const { createServer } = await import('vite');
const { default: react } = await import('@vitejs/plugin-react');
const fixture = await mkdtemp(join(uiRoot,'.work-timeline-'));
let server, browser;
try {
  const main = await readFile(join(uiRoot,'src/main.tsx'),'utf8');
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match=>`import '../src/${match[1]}';`).join('\n');
  await writeFile(join(fixture,'index.html'), '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture,'main.tsx'), `${styles}
import React from 'react'; import {createRoot} from 'react-dom/client';
import WorkTimeline from '../src/WorkTimeline'; import {MessageBubble} from '../src/App';
import {i18nReady,setLanguage} from '../src/i18n'; import {TimezoneProvider} from '../src/UserTimezone';
const query=new URLSearchParams(location.search);
const runs=[{id:'r',conversation_id:'c',bot_id:'b',status:'done',trace:{attachment_id:'strip',tiles:5,steps:5,created_at:'2026-11-12T10:00:01Z'},created_at:'2026-11-12T10:00:00Z',updated_at:'2026-11-12T10:00:01Z'}];
const message={id:'final',conversation_id:'c',role:'assistant',run_id:'r',seq:3,content:'Both checks passed.',created_at:'2026-11-12T10:00:01Z',attachments:[{id:'output',conversation_id:'c',mime:'image/png',name:'Synthetic output',size:100,created_at:'2026-11-12T10:00:01Z'},{id:'document',conversation_id:'c',mime:'text/plain',name:'Synthetic notes.txt',size:80,created_at:'2026-11-12T10:00:01Z'}]};
document.documentElement.dataset.theme=query.get('theme')||'light';
void i18nReady.then(()=>setLanguage(query.get('lang')||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><div className="workspace" style={{padding:16}}><WorkTimeline tools={${JSON.stringify(tools)}} notes={${JSON.stringify(notes)}} runs={runs as any}/><MessageBubble message={message as any} showAvatar={false}/></div></TimezoneProvider>));`);
  server = await createServer({configFile:false,root:uiRoot,plugins:[react()],server:{host:'127.0.0.1',port:0,hmr:false},logLevel:'error'});
  await server.listen();
  const origin=`http://127.0.0.1:${server.httpServer.address().port}`;
  for (const engine of [chromium, webkit]) {
    browser=await engine.launch();
    for (const {theme,width} of [{theme:'light',width:1000},{theme:'dark',width:1000},{theme:'light',width:390},{theme:'dark',width:390}]) {
      const page=await browser.newPage({viewport:{width,height:800},reducedMotion:'reduce'});
      const errors=[]; page.on('pageerror', error=>errors.push(error.message));
      await page.route('**/*', async route=>{
        const url=new URL(route.request().url());
        if(url.origin!==origin) return route.abort();
        if(url.pathname==='/api/runs/r/captures') return route.fulfill({json:{captures}});
        // TimezoneProvider reads the person's preferences once.
        if(url.pathname==='/api/preferences') return route.fulfill({json:{timezone:'UTC'}});
        if(url.pathname.startsWith('/api/attachments/')) return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800"><rect width="1280" height="800" fill="#6688bb"/></svg>'});
        if(url.pathname.startsWith('/api/')) throw new Error(`Unexpected API ${url.pathname}`);
        return route.continue();
      });
      await page.goto(`${origin}/${basename(fixture)}/index.html?theme=${theme}`);
      const groups=page.locator('.work-command-group'); await groups.first().waitFor();
      assert.equal(await groups.count(),2);
      assert.equal(await groups.locator('[open]').count(),0);
      assert.equal(await groups.first().getAttribute('open'),null);
      await groups.first().locator('summary').click();
      assert.equal(await groups.first().locator('li:visible').count(),2);
      assert.equal(await page.locator('.work-narration').textContent(),'The first check passed.');
      const stack=page.locator('.work-capture-stack'); await stack.waitFor();
      assert.equal(await stack.locator('img').count(),3);
      assert.equal(await stack.locator('span').textContent(),'+2');
      await stack.click(); const dialog=page.locator('dialog[open]'); await dialog.waitFor();
      await page.keyboard.press('ArrowRight'); assert.ok((await dialog.locator('img').getAttribute('src')).endsWith('/cap1'));
      await page.keyboard.press('ArrowLeft'); assert.ok((await dialog.locator('img').getAttribute('src')).endsWith('/cap0'));
      assert.equal(await dialog.evaluate(el=>getComputedStyle(el,'::backdrop').backgroundColor),'rgba(8, 8, 10, 0.96)');
      await dialog.locator('img').evaluate(img=>img.decode());
      assert.ok((await dialog.locator('img').boundingBox()).width>Math.min(500,width-64));
      await dialog.evaluate(el=>{ for(const [type,key,x] of [['touchstart','touches',200],['touchend','changedTouches',80]]) { const event=new Event(type,{bubbles:true}); Object.defineProperty(event,key,{value:[{clientX:x,clientY:100}]}); el.dispatchEvent(event); } });
      assert.ok((await dialog.locator('img').getAttribute('src')).endsWith('/cap1'));
      await page.keyboard.press('Escape'); await dialog.waitFor({state:'detached'});
      assert.equal(await page.locator('.message-content .attachment-gallery').count(),0);
      assert.equal(await page.locator('.message-content .delivered-attachment').count(),0);
      assert.equal(await page.locator('.bot-attachments .delivered-attachment.is-file').count(),1);
      assert.equal(await stack.evaluate(el=>el===document.activeElement),true,'focus returns to the stack');
      const output=page.locator('.bot-attachments .attachment-thumb'); await output.waitFor();
      const box=await output.boundingBox(); assert.ok(box.width<=240 && box.height<=240);
      await output.click(); await page.locator('dialog[open]').waitFor();
      await page.keyboard.press('Escape'); await page.locator('dialog[open]').waitFor({state:'detached'});
      assert.deepEqual(errors,[]);
      await page.close();
    }
    await browser.close(); browser=null;
  }
  console.log('PASS Chromium/WebKit: light/dark groups, narration, stacks, large preview, keyboard, external outputs');
} finally { await browser?.close(); await server?.close(); await rm(fixture,{recursive:true,force:true}); }
