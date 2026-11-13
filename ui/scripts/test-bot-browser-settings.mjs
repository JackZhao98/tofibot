// Local settings acceptance. No live server, account, credentials or native permissions.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {startUi, launch, ui} from './computer-window-fixture.mjs';
const languages = ['en','zh-CN','zh-TW','ja','ko','de','fr'];
const booleans = ['botBrowserEnabled','botBrowserRetainLogin','botBrowserShareFrames'];
const catalogs = {};
for (const lang of languages) {
  const settings = JSON.parse(await readFile(join(ui,'src/locales',lang,'settings.json'),'utf8')).bot_browser;
  const computer = JSON.parse(await readFile(join(ui,'src/locales',lang,'computer.json'),'utf8')).browser;
  catalogs[lang] = settings;
  assert.deepEqual(Object.keys(settings).sort(), Object.keys(catalogs.en).sort(), `${lang}: all settings keys`);
  assert.ok(computer.source && computer.status.browser_transport_unavailable);
  for (const key of [...booleans,'login_hint','frames_hint']) {
    assert.ok(settings[key]);
    assert.doesNotMatch(settings[key], /GPT|Claude|Gemini|Codex|OpenAI|ChatGPT/i, `${lang}: no model names`);
  }
}
const pageSource = await readFile(join(ui,'src/desktop-settings/MacSettingsPage.tsx'),'utf8');
const local = pageSource.slice(pageSource.indexOf('export function BotBrowserSettings'));
assert.doesNotMatch(local, /fetch\(|request\(|localStorage|sessionStorage|\.remoteControl\./);
assert.match(local, /showSaved=\{false\}/);
console.log('PASS settings static boundaries, three local booleans, seven complete catalogs');
if (!process.argv.includes('--static')) {
  const {server,origin} = await startUi();let browser;
  try {
    browser = await launch();
    async function open({bridge = true, desktop = true, supported = true, code = null, language = 'en'} = {}) {
      const page = await browser.newPage();
      const requests = [];
      page.on('request', req => {if(new URL(req.url()).pathname.startsWith('/api/')) requests.push(req.url());});
      await page.route('**/api/**', route => route.abort());
      await page.addInitScript(({bridge,desktop,supported,code,language}) => {
        localStorage.setItem('tofi.language', language);
        const s = {supported,code,deviceID:'mac-synthetic',phase:'idle',settings:{botBrowserEnabled:true,botBrowserRetainLogin:true,botBrowserShareFrames:true}};
        window.__browserStatus=s;window.__calls=[];window.__fail=null;
        const copy = () => JSON.parse(JSON.stringify(s));
        const result = () => ({ok:true,code:null,status:copy()});
        const api = {
          status:async()=>copy(),onStatus:()=>()=>{},
          update:async patch=>{window.__calls.push(patch);if(window.__fail==='update') throw new Error('synthetic');Object.assign(s.settings,patch);return result();},
          clear:async()=>window.__fail==='clear'?{ok:false,code:'browser_clear_failed',status:copy()}:result(),
          open:async()=>result(),
        };
        if (desktop) window.tofiDesktop={...(bridge?{botBrowser:api}:{})};
      },{bridge,desktop,supported,code,language});
      await page.goto(origin+'/desktop-settings.html');
      await page.locator('.bot-browser-settings summary').click();
      return {page,requests};
    }
    for(const language of languages){
      const {page,requests}=await open({language});const words=catalogs[language];
      for(const key of booleans) await page.getByRole('switch',{name:words[key],exact:true}).waitFor();
      assert.equal(await page.locator('.bot-browser-settings [role=switch]').count(),3);
      for(const key of booleans) assert.equal(await page.getByRole('switch',{name:words[key],exact:true}).getAttribute('aria-checked'),'true');
      await page.getByRole('switch',{name:words.botBrowserShareFrames,exact:true}).click();
      assert.equal(await page.getByRole('switch',{name:words.botBrowserShareFrames,exact:true}).getAttribute('aria-checked'),'false');
      await page.getByRole('switch',{name:words.botBrowserEnabled,exact:true}).click();
      await page.getByRole('switch',{name:words.botBrowserEnabled,exact:true}).click();
      assert.equal(await page.getByRole('switch',{name:words.botBrowserShareFrames,exact:true}).getAttribute('aria-checked'),'false','frame permission remains independent');
      await page.evaluate(()=>{window.__fail='update';});
      await page.getByRole('switch',{name:words.botBrowserRetainLogin,exact:true}).click();
      await page.locator('.bot-browser-settings [role=alert]').getByText(words.browser_control_lost).waitFor();
      assert.equal(await page.getByRole('switch',{name:words.botBrowserRetainLogin,exact:true}).getAttribute('aria-checked'),'true','failed save never publishes success');
      await page.evaluate(()=>{window.__fail='clear';});
      await page.getByRole('button',{name:words.clear,exact:true}).click();
      await page.getByText(words.browser_clear_failed,{exact:true}).waitFor();
      assert.equal(await page.getByText(words.cleared,{exact:true}).count(),0);
      assert.deepEqual(requests,[],'local settings make zero server requests');
      await page.close();
    }
    for(const config of [{bridge:false},{desktop:false},{supported:false,code:'browser_server_unsupported'},{supported:false,code:'browser_client_unsupported'},{code:'no_connection'}]){
      const {page}=await open(config);
      const expected = !config.desktop && config.desktop===false?'in_app':config.bridge===false?'browser_client_unsupported':config.code;
      await page.getByText(catalogs.en[expected],{exact:true}).waitFor();
      if(config.code) assert.equal(await page.locator('.bot-browser-settings [role=switch]:not([disabled])').count(),0);
      await page.close();
    }
    console.log('PASS local settings: independent defaults, failure wording, no false success, compatibility, seven languages');
  } finally {await browser?.close();await server.close();}
}
