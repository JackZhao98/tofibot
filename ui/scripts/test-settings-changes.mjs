// Browser acceptance for the audit of settings a Bot changed (docs/design/bot-settings-and-manual.md, slice S4):
//   the chat notice "Timezone: A → B · Undo" calls POST /api/settings-changes/{id}/revert and then says Undone;
//   a 409 settings_changed_since says the setting changed again; a reverted notice offers no Undo;
//   Settings > Maintenance > Changes by Bots lists the changes with Revert, reloads after one, and says what cannot be reverted.
// Mounts the real components in a Vite fixture with every /api/* call answered by a stub. Needs PLAYWRIGHT_MODULE.
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".settings-changes-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import SettingsChangeNotice from '../src/SettingsChangeNotice';
import {SettingsChanges} from '../src/settings/SettingsChanges';
const base={type:'settings_change',from_bot_id:'b',to_bot_id:'b',target_conversation_id:'c',target_run_id:'',setting_key:'account.timezone',label_key:'settings:timezone.label',old_value:'America/Los_Angeles',new_value:'Asia/Shanghai',revertable:true};
function Fixture(){
 return <div className="workspace" style={{padding:16,maxWidth:560}}>
  <div id="n-live"><SettingsChangeNotice fallback="fallback" notice={{...base,change_id:'chg_live'}}/></div>
  <div id="n-stale"><SettingsChangeNotice fallback="fallback" notice={{...base,change_id:'chg_stale'}}/></div>
  <div id="n-done"><SettingsChangeNotice fallback="fallback" notice={{...base,change_id:'chg_done',reverted:true}}/></div>
  <div id="n-unknown"><SettingsChangeNotice fallback="Plain fallback line" notice={{...base,change_id:'chg_u',label_key:'settings:no.such.key'}}/></div>
  <section id="card"><SettingsChanges/></section>
 </div>;
}
void i18nReady.then(()=>setLanguage('en')).then(()=>createRoot(document.getElementById('root')!).render(<Fixture/>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});
  const page = await (await browser.newContext({viewport: {width: 900, height: 900}})).newPage();
  page.on("pageerror", error => console.log("pageerror:", error.message));
  const reverts = [];
  const reverted = new Set();
  const json = (status, body) => ({status, contentType: "application/json", body: JSON.stringify(body)});
  const rows = () => [
    {id: "chg_a", at: new Date().toISOString(), actor_kind: "bot", bot_name: "Mochi", key: "account.timezone", label_key: "settings:timezone.label", old_value: "America/Los_Angeles", new_value: "Asia/Shanghai", risk: "direct", revertable: true, ...(reverted.has("chg_a") ? {reverted_by: "rev_1"} : {})},
    {id: "chg_b", at: new Date().toISOString(), actor_kind: "bot", bot_name: "Mochi", key: "bot.instructions", label_key: "chat:botPanel.instructions", target_name: "Mochi", old_value: "", new_value: "A very long instruction text ".repeat(20), risk: "direct", revertable: false},
    {id: "chg_c", at: new Date().toISOString(), actor_kind: "bot", bot_name: "Taro", key: "account.language", label_key: "settings:language.label", old_value: "", new_value: "ja", risk: "direct", revertable: true},
  ];
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    if (url.origin !== origin) { await route.abort(); return; }
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    if (url.pathname === "/api/settings-changes" && request.method() === "GET") { await route.fulfill(json(200, {changes: rows()})); return; }
    const match = /^\/api\/settings-changes\/([^/]+)\/revert$/.exec(url.pathname);
    if (match && request.method() === "POST") {
      reverts.push(match[1]);
      if (match[1] === "chg_stale") { await route.fulfill(json(409, {error: {code: "settings_changed_since", message: "the setting changed again"}})); return; }
      if (match[1] === "chg_c") { await route.fulfill(json(500, {error: {code: "storage", message: "no"}})); return; }
      reverted.add(match[1]);
      await route.fulfill(json(200, {ok: true}));
      return;
    }
    await route.fulfill(json(404, {error: {code: "not_found", message: "not stubbed"}}));
  });
  await page.goto(`${origin}/${basename(fixture)}/index.html`);
  await page.locator("#n-live button").waitFor();

  // The line is worded here, from the catalog key and the two values.
  assert.equal((await page.locator("#n-live [data-settings-change] > span").first().innerText()).trim(), "Timezone: America/Los_Angeles → Asia/Shanghai");
  assert.equal(await page.locator("#n-unknown [data-settings-change] > span").first().innerText(), "Plain fallback line", "an unknown catalog key falls back to the server's line");
  assert.equal(await page.locator("#n-done button").count(), 0, "a reverted notice offers no Undo");
  assert.match(await page.locator("#n-done").innerText(), /Undone/);

  await page.locator("#n-live button").click();
  await page.locator("#n-live").getByText("Undone").waitFor();
  assert.equal(await page.locator("#n-live button").count(), 0);
  assert.deepEqual(reverts, ["chg_live"]);
  console.log("PASS notice: Undo calls the revert route and then says Undone");

  await page.locator("#n-stale button").click();
  await page.locator("#n-stale [role=alert]").waitFor();
  assert.equal((await page.locator("#n-stale [role=alert]").innerText()).trim(), "· Changed again since");
  assert.equal(await page.locator("#n-stale button").count(), 1, "after a refusal Undo stays");
  console.log("PASS notice: a setting changed again is said so");

  // The Maintenance card.
  const card = page.locator("#card");
  await card.getByText("Timezone").first().waitFor();
  assert.equal(await card.locator(".settings-row-v2").count(), 3);
  assert.match(await card.innerText(), /Mochi/);
  assert.match(await card.innerText(), /Can't be reverted here/);
  assert.ok((await card.innerText()).includes("A very long instruction text A very long instruction text"), "a long value shows");
  assert.ok(!(await card.innerText()).includes("A very long instruction text ".repeat(6)), "a long value is clipped");
  await card.getByRole("button", {name: "Revert"}).first().click();
  await card.getByText("Reverted", {exact: true}).waitFor();
  assert.deepEqual(reverts, ["chg_live", "chg_stale", "chg_a"]);
  assert.equal(await card.getByRole("button", {name: "Revert"}).count(), 1, "the reverted row has no button");
  await card.getByRole("button", {name: "Revert"}).click();
  await card.getByRole("alert").waitFor();
  assert.match(await card.getByRole("alert").innerText(), /Couldn't revert/);
  console.log("PASS maintenance card: list, revert, reload, not-revertable and failure lines");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
