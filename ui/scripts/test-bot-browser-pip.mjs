// Real UI + synthetic NDJSON only. External host mocks prove the protocol, never native focus.
import assert from 'node:assert/strict';
import {readFile, mkdtemp, writeFile, rm} from 'node:fs/promises';
import {join, basename} from 'node:path';
import {pathToFileURL} from 'node:url';
import {openUiModules} from './ui-modules.mjs';
import {installStubs, startUi, launch, ui} from './computer-window-fixture.mjs';

export const source = (id = 'mac-synthetic') => ({computer_id: id, kind: 'mac_browser', label_key: 'browser.source', supported: true, enabled: true, share_frames: true, state: 'idle', code: null, connection_epoch: '11111111-1111-4111-8111-111111111111', session_epoch: '22222222-2222-4222-8222-222222222222', target_id: '33333333-3333-4333-8333-333333333333', viewport_epoch: 1, activity: null});

// Dev StrictMode replays mount effects. Snapshot only once one live stream remains.
async function mountedStreamCounts(page, requests = '__requests') {
  await page.waitForFunction(key => window[key].length - window.__aborts === 1, requests);
  return page.evaluate(key => ({requests: window[key].length, aborts: window.__aborts}), requests);
}

export async function exerciseBrowserEntry(browser, origin, checkControls) {
  for (const vm of [false, true]) {
    const page = await browser.newPage({viewport: {width: 1024, height: 800}});
    const state = {info: {state: 'ready'}, capture: 'image', requests: []};
    let rows = [source(), source('mac-second')];
    await installStubs(page, state);
    await page.route('**/api/**', route => {
      const p = new URL(route.request().url()).pathname;
      const json = body => route.fulfill({contentType: 'application/json', body: JSON.stringify(body)});
      if (p === '/api/server-info') return json({service: 'tofi', protocol_version: 1, instance_id: '00000000-0000-4000-8000-000000000001', auth: {mode: 'none'}, bot_browser: {version: 1, upload: 'websocket-json', view: 'ndjson-jpeg'}});
      if (p === '/api/config') return json({computer: {backend: vm ? 'kvm' : 'none', available: vm, capabilities: []}});
      if (p === '/api/browser-sources') return json({v: 1, sources: rows});
      return route.fallback();
    });
    await page.addInitScript(() => {
      const original = window.fetch.bind(window);
      window.__streamRequests = []; window.__aborts = 0; window.__streamAborts = [];
      window.fetch = async (input, init) => {
        if (!String(input).endsWith('/browser/stream')) return original(input, init);
        window.__streamRequests.push(String(input));
        const stream = new ReadableStream({start(controller) { const timer=setInterval(()=>controller.enqueue(new TextEncoder().encode(JSON.stringify({type:'ping',v:1,server_time_ms:Date.now()})+'\n')),500); init.signal.addEventListener('abort', () => {clearInterval(timer); window.__aborts++; window.__streamAborts.push(String(input)); controller.close(); }); }});
        return new Response(stream, {headers: {'content-type': 'application/x-ndjson'}});
      };
    });
    await page.goto(origin);
    await page.locator('[data-browser-toggle="mac-synthetic"]').click();
    await page.locator('[data-browser-source="mac-synthetic"]').waitFor();
    assert.match(await page.locator('.bot-browser-panel .desktop-chin').innerText(), /Your Mac · Bot browser/);
    const mounted = await mountedStreamCounts(page, '__streamRequests');
    assert.deepEqual(await page.evaluate(() => [...new Set(window.__streamRequests)]), ['/api/computers/mac-synthetic/browser/stream']);
    await page.locator('.bot-browser-panel .desktop-enlarge').click();
    await page.locator('.desktop-floating-shell.is-expanded').waitFor();
    await page.waitForFunction(() => document.activeElement?.closest('.desktop-window-controls'));
    if (checkControls) { await page.evaluate(() => {document.documentElement.dataset.desktop="darwin";}); await checkControls(page); }
    assert.deepEqual(await page.evaluate(() => ({requests: window.__streamRequests.length, aborts: window.__aborts})), mounted, 'enlarge keeps the same mounted stream without a request or abort');
    if (!vm) assert.equal(state.requests.some(p => /^\/api\/computer\//.test(p) || p.includes('/firecracker/')), false, 'Mac entry never starts or polls a VM');
    // The enlarged shell covers the header; shrink it before choosing another source.
    await page.locator('.bot-browser-panel .desktop-window-controls button').nth(1).click();
    await page.waitForFunction(() => !document.querySelector('.desktop-floating-shell.is-expanded'));
    if (vm) {
      await page.locator('[data-computer-toggle]').click();
      await page.waitForFunction(() => !document.querySelector('.bot-browser-panel'));
      await page.waitForFunction(baseline => window.__aborts === baseline.aborts + 1 && window.__streamRequests.length === baseline.requests, mounted);
      await page.locator('.desktop-floating-shell').waitFor();
    }
    await page.locator('[data-browser-toggle="mac-second"]').click();
    await page.locator('[data-browser-source="mac-second"]').waitFor();
    await page.waitForFunction(baseline => {
      const first = '/api/computers/mac-synthetic/browser/stream';
      const second = '/api/computers/mac-second/browser/stream';
      return window.__streamAborts.filter(url => url === first).length === baseline.aborts + 1
        && window.__streamRequests.filter(url => url === second).length - window.__streamAborts.filter(url => url === second).length === 1;
    }, mounted);
    const secondMounted = await mountedStreamCounts(page, '__streamRequests');
    assert.deepEqual(await page.evaluate(() => ({requests: window.__streamRequests.filter(url => url === '/api/computers/mac-synthetic/browser/stream').length, aborts: window.__streamAborts.filter(url => url === '/api/computers/mac-synthetic/browser/stream').length})), {requests: mounted.requests, aborts: mounted.aborts + 1}, 'leaving the first source aborts its live stream exactly once without reopening it');
    assert.equal(await page.locator('.bot-browser-panel').count(), 1, 'one source at a time');
    await page.locator('.bot-browser-panel .desktop-window-controls button').first().click();
    await page.waitForFunction(baseline => window.__aborts === baseline.aborts + 1 && window.__streamRequests.length === baseline.requests, secondMounted);
    rows = [];
    await page.waitForTimeout(1100);
    assert.equal(await page.locator('[data-browser-toggle]').count(), 0);
    if (!vm) {
      assert.equal(await page.locator('[data-computer-toggle]').count(), 0, 'no VM or Mac has no computer entry');
      assert.equal(state.requests.some(p => /^\/api\/computer\//.test(p) || p.includes('/firecracker/')), false);
    }
    await page.close();
  }
}

async function compatibilityCases(browser, origin) {
  for(const legacy of [true,false]) {
    const page=await browser.newPage({viewport:{width:1024,height:800}});
    const state={info:{state:'ready'},capture:'image',requests:[]};let newRoutes=0,streams=0;
    await installStubs(page,state);
    await page.route('**/api/**',route=>{
      const path=new URL(route.request().url()).pathname;
      const json=body=>route.fulfill({contentType:'application/json',body:JSON.stringify(body)});
      if(path==='/api/server-info') return json({service:'tofi',protocol_version:1,instance_id:'00000000-0000-4000-8000-000000000001',auth:{mode:'none'},...(legacy?{}:{bot_browser:{version:1,upload:'websocket-json',view:'ndjson-jpeg'}})});
      if(path==='/api/config') return json({computer:{backend:'none',available:false,capabilities:[]}});
      if(path==='/api/computers') return json({computers:[{id:'mac-synthetic',kind:'personal'}]});
      if(path==='/api/browser-sources') {newRoutes++;return json({v:1,sources:[{...source(),supported:false,state:'unsupported',code:'browser_client_unsupported'}]});}
      if(path.endsWith('/browser/stream')) {streams++;return route.abort();}
      return route.fallback();
    });
    let computerReads=0;page.on('request',r=>{if(new URL(r.url()).pathname.startsWith('/api/computer'))computerReads++;});
    await page.goto(origin);
    if(legacy) {
      // An older server: no Mac source, no new route and no computer request at all.
      await page.waitForLoadState('networkidle');
      assert.equal(await page.locator('[data-browser-toggle]').count(),0,'old server shows no Mac source');
      assert.equal(newRoutes,0,'old server sees no new route or 404 retry loop');
      assert.equal(computerReads,0,'old server makes no computer request');
    } else {
      await page.locator('[data-browser-toggle]').click();
      await page.locator('.bot-browser-state').getByText('Update the Mac App to use the Bot browser',{exact:true}).waitFor();
    }
    assert.equal(streams,0,'unsupported sources never subscribe');
    await page.close();
  }
}

async function automaticCases(browser, origin) {
  const page = await browser.newPage({viewport:{width:1024,height:800}});
  const state = {info:{state:'ready'},capture:'image',requests:[]};
  let row = source();
  await installStubs(page,state);
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    const json = body => route.fulfill({contentType:'application/json',body:JSON.stringify(body)});
    if(path==='/api/config') return json({computer:{backend:'none',available:false,capabilities:[]}});
    if(path==='/api/server-info') return json({service:'tofi',protocol_version:1,instance_id:'00000000-0000-4000-8000-000000000001',auth:{mode:'none'},bot_browser:{version:1,upload:'websocket-json',view:'ndjson-jpeg'}});
    if(path==='/api/browser-sources') return json({v:1,sources:[row]});
    return route.fallback();
  });
  await page.addInitScript(() => {
    Object.defineProperty(document,'hasFocus',{configurable:true,value:()=>true});
    const original=window.fetch.bind(window);
    window.fetch=async (url,init)=>String(url).endsWith('/browser/stream')?new Response(new ReadableStream({start(controller){const timer=setInterval(()=>controller.enqueue(new TextEncoder().encode(JSON.stringify({type:'ping',v:1,server_time_ms:Date.now()})+'\n')),500);init.signal.addEventListener('abort',()=>{clearInterval(timer);controller.close();});}}),{headers:{'content-type':'application/x-ndjson'}}):original(url,init);
  });
  await page.goto(origin);
  await page.locator('[data-browser-toggle]').waitFor();
  await page.clock.install();
  row={...row,state:'working',activity:{bot_id:'bot-synthetic',run_id:'run-auto',conversation_id:'conv-synthetic'}};
  await page.clock.runFor(1500);
  await page.locator('.bot-browser-panel').waitFor();
  await page.locator('.bot-browser-panel .desktop-window-controls button').first().click();
  await page.clock.runFor(2000);
  assert.equal(await page.locator('.bot-browser-panel').count(),0,'manual close suppresses this run');
  row={...row,activity:{...row.activity,run_id:'run-next'}};
  await page.clock.runFor(1500);
  await page.locator('.bot-browser-panel').waitFor();
  row={...row,state:'idle',activity:null};
  await page.clock.runFor(1500);
  await page.clock.fastForward(30_100);
  await page.waitForFunction(()=>!document.querySelector('.bot-browser-panel'));
  await page.locator('[data-browser-toggle]').click();
  await page.locator('.bot-browser-panel').waitFor();
  await page.clock.fastForward(31_000);
  assert.equal(await page.locator('.bot-browser-panel').count(),1,'manual window stays until manually closed');
  await page.close();
}

export async function browserStaticChecks() {
  const video = await readFile(join(ui, 'src/BotBrowserVideo.tsx'), 'utf8');
  assert.doesNotMatch(video, /localStorage|indexedDB|createObjectURL|DesktopVideo|computer\/.*capture/);
  assert.match(video, /credentials: 'same-origin', cache: 'no-store', signal:/);
  assert.match(video, /bitmap\?\.close\(\)/);
  assert.match(video, /352 \* 1024/);
  const frameBranch = video.slice(video.indexOf("value.type === 'frame'"), video.indexOf('} else throw'));
  assert.doesNotMatch(frameBranch, /set[A-Z]\w*\(/, 'frames never commit React state');
  const modules = await openUiModules({language:'en'});
  try {
    const model = await modules.load('/src/botBrowserSource.ts');
    const {validBrowserFrame} = await modules.load('/src/BotBrowserVideo.tsx');
    const s=source();
    assert.equal(model.isBrowserSource(s),true);
    assert.equal(model.isBrowserSource({...s,url:'https://synthetic.test/'}),false,'source metadata cannot carry page content');
    assert.equal(model.isBrowserSource({...s,connection_epoch:'invalid'}),false);
    assert.equal(model.validSourceKey('mac:https://synthetic.test'),false);
    assert.equal(model.validSourceKey('mac:'+ 'x'.repeat(160)),false);
    assert.equal(model.supportsBotBrowser({}),false);
    assert.equal(model.supportsBotBrowser({bot_browser:{version:2,upload:'websocket-json',view:'ndjson-jpeg'}}),false);
    assert.equal(model.supportsBotBrowser({bot_browser:{version:1,upload:'websocket-json',view:'ndjson-jpeg'}}),true);
    assert.equal(model.canShowBrowserFrame({...s,share_frames:false}),false);
    for(const state of ['human','stopped','closed','disabled','offline','unsupported','minimized','locked','unavailable']) assert.equal(model.canShowBrowserFrame({...s,state}),false,state);
    const frame={type:'frame',v:1,connection_epoch:s.connection_epoch,observation_id:'44444444-4444-4444-8444-444444444444',session_epoch:s.session_epoch,target_id:s.target_id,viewport_epoch:1,seq:1,captured_at_ms:1,received_at_ms:2,width:32,height:20,jpeg:'/9j/2Q=='};
    assert.equal(validBrowserFrame(frame,s,null,0),true);
    for(const patch of [{seq:0},{seq:Number.MAX_SAFE_INTEGER+1},{viewport_epoch:2},{target_id:'55555555-5555-4555-8555-555555555555'},{width:1281},{height:801},{jpeg:'x'.repeat(350000)},{extra:true},{observation_id:'bad'}]) assert.equal(validBrowserFrame({...frame,...patch},s,null,0),false,JSON.stringify(Object.keys(patch)));
    assert.equal(validBrowserFrame(frame,s,frame.observation_id,1),false,'duplicate sequence');
    assert.equal(validBrowserFrame(frame,s,'55555555-5555-4555-8555-555555555555',0),false,'retired observation');
    assert.equal(validBrowserFrame(frame,{...s,state:'human'},null,0),false,'takeover revokes frames');
  } finally {await modules.close();}
  console.log('PASS protocol validators + static boundaries (browser behavior not run by --static)');
}

async function streamCases(browser, origin) {
  const fixture = await mkdtemp(join(ui, '.bot-browser-pip-'));
  try {
    await writeFile(join(fixture, 'index.html'), '<div id="root"></div><script type="module" src="./main.tsx"></script>');
    await writeFile(join(fixture, 'main.tsx'), `
import React,{Profiler,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady} from '../src/i18n';import {BotBrowserVideo} from '../src/BotBrowserVideo';
import {BotBrowserPanel} from '../src/BotBrowserPanel';import {FloatingDesktop} from '../src/FloatingDesktop';
import '../src/desktop-system.css';import '../src/v2-foundations.css';
function Probe(){const [s,S]=useState((window as any).__source);const [v,V]=useState(true);const [big,B]=useState(false);const [open,O]=useState(true);
(window as any).__setSource=S;(window as any).__visible=V;(window as any).__close=()=>O(false);
return <div className="workspace">{open&&(location.search.includes('shell')?<FloatingDesktop small={!big} expanded={big} onClose={()=>O(false)}><BotBrowserPanel source={s} expanded={big} onClose={()=>O(false)} onExpand={()=>B(!big)}/></FloatingDesktop>:<Profiler id="video" onRender={()=>{(window as any).__commits++}}><BotBrowserVideo source={s} visible={v} onStatus={S}/></Profiler>)}</div>}
void i18nReady.then(()=>createRoot(document.getElementById('root')!).render(<Probe/>));`);
    async function open({shell = false} = {}) {
      const page = await browser.newPage({viewport: {width: 1024, height: 800}, reducedMotion: 'reduce'});
      await page.addInitScript(({s}) => {
        window.__source = s; window.__commits = 0; window.__aborts = 0; window.__requests = [];
        window.__streams = []; window.__draws = 0;
        const draw = CanvasRenderingContext2D.prototype.drawImage;
        CanvasRenderingContext2D.prototype.drawImage = function(...args) {window.__draws++; return draw.apply(this, args);};
        window.__message = value => window.__streams.at(-1).enqueue(new TextEncoder().encode(typeof value === 'string' ? value : JSON.stringify(value) + '\n'));
        window.fetch = async (url, init) => {
          window.__requests.push(String(url));
          if (!String(url).endsWith('/browser/stream')) throw new Error('unexpected request');
          const stream = new ReadableStream({start(controller) {window.__streams.push(controller); init.signal.addEventListener('abort', () => {window.__aborts++; controller.close();});}});
          return new Response(stream, {headers: {'content-type': 'application/x-ndjson'}});
        };
      }, {s: source()});
      await page.goto(`${origin}/${basename(fixture)}/index.html${shell ? '?shell' : ''}`);
      return page;
    }
    // Vite optimizes this fixture's dependencies on the first visit and then reloads the page; warm it once.
    { const warm = await open(); await warm.waitForLoadState('networkidle'); await warm.close(); }
    const page = await open();
    const mounted = await mountedStreamCounts(page);
    await page.evaluate(async () => {
      const canvas = document.createElement('canvas');canvas.width=32;canvas.height=20;
      canvas.getContext('2d').fillRect(0,0,32,20);
      window.__jpeg = canvas.toDataURL('image/jpeg').split(',')[1];
      window.__frame = seq => ({type:'frame',v:1,connection_epoch:window.__source.connection_epoch,observation_id:'44444444-4444-4444-8444-444444444444',session_epoch:window.__source.session_epoch,target_id:window.__source.target_id,viewport_epoch:1,seq,captured_at_ms:1,received_at_ms:2,width:32,height:20,jpeg:window.__jpeg});
      window.__message({type:'status',v:1,source:window.__source});
      window.__message(window.__frame(1));
    });
    await page.waitForFunction(() => window.__draws === 1);
    const commits = await page.evaluate(() => window.__commits);
    await page.evaluate(() => {for(let seq=2;seq<30;seq++) window.__message(window.__frame(seq));});
    await page.waitForFunction(() => window.__draws > 1);
    assert.equal(await page.evaluate(() => window.__commits), commits, 'zero per-frame React commits');
    await page.waitForTimeout(150);
    const drawn = await page.evaluate(() => window.__draws);
    await page.evaluate(() => {window.__message(window.__frame(1)); window.__message({...window.__frame(40),viewport_epoch:2});});
    await page.waitForTimeout(100);
    assert.equal(await page.evaluate(() => window.__draws), drawn, 'old/out-of-order/viewport frames discarded');
    await page.evaluate(() => window.__message({type:'status',v:1,source:{...window.__source,state:'human',code:'user_takeover'}}));
    await page.waitForFunction(() => document.querySelector('canvas').getContext('2d').getImageData(0,0,1,1).data[3] === 0);
    await page.evaluate(() => window.__message('x'.repeat(352*1024+1)));
    await page.getByText('Preview interrupted').waitFor();
    await page.waitForFunction(baseline => window.__aborts === baseline.aborts + 1 && window.__requests.length === baseline.requests, mounted);
    assert.equal(await page.locator('img,video').count(), 0, 'no screenshot or video fallback');
    await page.close();

    const hidden = await open();
    const beforeHide = await mountedStreamCounts(hidden);
    await hidden.evaluate(() => window.__visible(false));
    await hidden.waitForFunction(baseline => window.__aborts === baseline.aborts + 1 && window.__requests.length === baseline.requests, beforeHide);
    await hidden.evaluate(() => window.__visible(true));
    await hidden.waitForFunction(baseline => window.__requests.length === baseline.requests + 1 && window.__aborts === baseline.aborts + 1, beforeHide);
    const beforeClose = await mountedStreamCounts(hidden);
    await hidden.evaluate(() => window.__close());
    await hidden.waitForFunction(baseline => window.__aborts === baseline.aborts + 1 && window.__requests.length === baseline.requests, beforeClose);
    await hidden.close();

    console.log('PASS stream validation, bounded lines, clear/abort, zero frame commits');
  } finally {await rm(fixture, {recursive:true,force:true});}
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await browserStaticChecks();
  if (!process.argv.includes('--static')) {
    const {server,origin} = await startUi();let browser;
    try {browser=await launch();await exerciseBrowserEntry(browser,origin);await compatibilityCases(browser,origin);await automaticCases(browser,origin);await streamCases(browser,origin);}
    finally {await browser?.close();await server.close();}
  }
}
