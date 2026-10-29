// Rendered acceptance for the Primary Assistant role and the Follow-ups (preview) page.
// Mounts the real components in a Vite fixture with every /api/* call stubbed (synthetic data) and drives them with
// Playwright: the Settings selector, the delete / hide dialogs that force a successor, the sidebar badge, and the
// preview page with its review buttons. Needs PLAYWRIGHT_MODULE.
import {selectValue, selectLabels, pickSelect} from "./lib/select.mjs";
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".primary-followups-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useCallback,useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {ApiError,api} from '../src/api';
import {TimezoneProvider} from '../src/UserTimezone';
import {DeleteConversationDialog} from '../src/DeleteConversationDialog';
import {PrimaryBadge} from '../src/PrimaryAssistant';
import {PrimaryAssistantSetting} from '../src/PrimaryAssistantSetting';
import {ReassignPrimaryDialog} from '../src/ReassignPrimaryDialog';
import {FollowupsSettings} from '../src/FollowupsSettings';
const query=new URLSearchParams(location.search);
const scene=query.get('scene')||'selector';
function useBots(){
 const [bots,setBots]=useState<any[]>([]);
 const reload=useCallback(async()=>{setBots((await api.bots()).bots)},[]);
 useEffect(()=>{void reload()},[reload]);
 return {bots,reload};
}
function Selector(){
 const {bots,reload}=useBots();
 return <div className="workspace"><div className="settings-page-body" data-page="general"><div className="settings-stack"><PrimaryAssistantSetting bots={bots} onChanged={reload}/></div></div></div>;
}
function Followups(){
 return <div className="workspace"><div className="settings-page-body" data-page="followups"><FollowupsSettings/></div></div>;
}
function Delete(){
 const {bots}=useBots();
 const target={id:'bot_research',conversationId:'c1',name:'Research Bot',kind:'bot' as const,isPrimary:query.get('primary')==='1'};
 return bots.length?<DeleteConversationDialog target={target} bots={query.get('alone')==='1'?bots.filter((b:any)=>b.id===target.id):bots} onClose={()=>{(window as any).__closed=true}} onDelete={async(t,successor)=>{(window as any).__deleted={id:t.id,successor};if(query.get('stale')==='1')throw new ApiError(409,'stale','primary_reassign_required')}}/>:null;
}
function Hide(){
 const {bots}=useBots();
 const bot=bots.find((b:any)=>b.id==='bot_research');
 return bot?<ReassignPrimaryDialog bot={bot} bots={bots} onClose={()=>{(window as any).__closed=true}} onConfirm={async id=>{(window as any).__hidden={successor:id}}}/>:null;
}
function Badge(){return <div className="workspace"><div className="contact-copy"><strong><span className="contact-name">A very long Bot name that has to be truncated before the tag</span><PrimaryBadge/></strong></div></div>}
const scenes:Record<string,()=>JSX.Element|null>={selector:Selector,followups:Followups,delete:Delete,hide:Hide,badge:Badge};
const Scene=scenes[scene];
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><Scene/></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const state = {};
  const requests = [];
  const reset = () => {
    state.primary = "bot_research";
    state.bots = [{id: "bot_research", name: "Research Bot"}, {id: "bot_inbox", name: "Inbox Bot"}, {id: "bot_notes", name: "Notes Bot"}, {id: "bot_old", name: "Retired Bot", archived: true}];
    state.modes = {bot_research: "off", bot_inbox: "off", bot_notes: "shadow"};
    state.verdicts = {};
    state.budgetPaused = false;
    state.modelConfigured = true;
    requests.length = 0;
  };
  reset();
  const loops = () => [
    {id: "loop_1", bot_id: "bot_research", bot_name: "Research Bot", source: "chat", kind: "awaiting_other", topic: "Pest control to call about the roof bees", counterparty_name: "Elyse", category: "home", importance: 90, band: "nudge", expected_by: "2026-10-15T17:00:00Z", suggested_next: "Draft a short follow-up to Elyse", quote: "Elyse says pest control will call me", resolution_phrases: [], state: state.verdicts.loop_1 === "handled" ? "handled_by_owner" : "open", verdict: state.verdicts.loop_1 ?? "", created_at: "2026-10-13T15:00:00Z"},
    {id: "loop_2", bot_id: "bot_notes", bot_name: "Notes Bot", source: "chat", kind: "owner_promise", topic: "Send Marc the draft", category: "work", importance: 60, band: "nudge", expected_by: "2026-10-30T16:00:00Z", quote: "I will send Marc the draft by 2026-10-30", resolution_phrases: [], state: "open", verdict: "", created_at: "2026-10-13T15:10:00Z"},
    {id: "loop_3", bot_id: "bot_research", bot_name: "Research Bot", source: "chat", kind: "owner_promise", topic: "Call the dentist", category: "health", importance: 45, band: "digest", expected_by: "2026-10-20T16:00:00Z", quote: "I need to call the dentist sometime", resolution_phrases: [], state: "digest_only", verdict: "", created_at: "2026-10-13T15:20:00Z"},
  ];
  const answer = (url, method, body) => {
    const path = url.pathname;
    if (path === "/api/bots") return {bots: state.bots.map(bot => ({...bot, is_primary: bot.id === state.primary}))};
    if (path === "/api/primary-assistant" && method === "PUT") {
      const target = state.bots.find(bot => bot.id === body.bot_id && !bot.archived);
      if (!target) return [400, {error: {code: "primary_invalid", message: "no"}}];
      state.primary = target.id; return {bot_id: target.id};
    }
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/followups" && method === "GET") return {
      account: {max_nudges_per_day: 3, quiet_start: "22:00", quiet_end: "08:00", weekend_min_importance: 80, digest_weekday: "mon", digest_time: "09:00", daily_budget_usd: 0.5},
      primary_bot_id: state.primary, delivery_available: false, budget_paused: state.budgetPaused, model_configured: state.modelConfigured, day: "2026-10-13",
      bots: state.bots.filter(bot => !bot.archived).map(bot => ({bot_id: bot.id, name: bot.name, is_primary: bot.id === state.primary, mode: state.modes[bot.id], source_chat: true, source_email: true, last_sweep_at: state.modes[bot.id] === "shadow" ? "2026-10-13T16:30:00Z" : undefined})),
      stats: {sweeps: 2, loops_created: 3, rejected_invalid: 1, rejected_band: 1, merged: 0, spent_usd: 0.004, would_nudge: 2, would_digest: 1, useful: 0, annoying: 0, handled: 0},
      loops: loops()};
    let match = /^\/api\/followups\/bots\/([^/]+)$/.exec(path);
    if (match && method === "PUT") { if (body.mode === "on") return [409, {error: {code: "followup_delivery_unavailable", message: "no"}}]; state.modes[match[1]] = body.mode; return {bot_id: match[1], mode: body.mode, source_chat: true, source_email: true}; }
    match = /^\/api\/followups\/loops\/([^/]+)\/feedback$/.exec(path);
    if (match && method === "POST") { state.verdicts[match[1]] = body.verdict; return {loop_id: match[1], verdict: body.verdict}; }
    if (path === "/api/followups/account" && method === "PUT") return {max_nudges_per_day: body.max_nudges_per_day ?? 3};
    return undefined;
  };
  async function open(params, {width = 1100, height = 800, theme = "light", locale = "en-US"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: "reduce", colorScheme: theme, locale});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      let body = {};
      try { body = JSON.parse(request.postData() || "{}"); } catch {}
      if (request.method() !== "GET") requests.push(`${request.method()} ${url.pathname}${url.search} ${request.postData() ?? ""}`.trim());
      const result = answer(url, request.method(), body);
      if (result === undefined) { errors.push(`unstubbed ${request.method()} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {code: "not_found", message: "not stubbed"}})}); return; }
      const [status, payload] = Array.isArray(result) ? result : [200, result];
      await route.fulfill({status, contentType: "application/json", body: JSON.stringify(payload)});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams(params)}`);
    return {page, errors, close: () => context.close()};
  }

  // ---- 1. Settings selector: shows the current Primary, lists active Bots only, applies at once.
  {
    reset();
    const {page, errors, close} = await open({scene: "selector"});
    const select = page.locator("#primary-assistant");
    await select.waitFor({timeout: 15000});
    assert.equal(await selectValue(select), "bot_research", "selector shows the current Primary");
    assert.deepEqual(await selectLabels(select), ["Research Bot", "Inbox Bot", "Notes Bot"], "archived Bots are not offered");
    assert.ok(await page.getByText("Primary Assistant", {exact: true}).first().isVisible(), "section title");
    await pickSelect(select, "bot_inbox");
    await page.waitForFunction(() => document.querySelector("#primary-assistant")?.dataset.value === "bot_inbox");
    assert.equal(state.primary, "bot_inbox", "the choice reached the server");
    assert.deepEqual(requests, ['PUT /api/primary-assistant {"bot_id":"bot_inbox"}'], "one write, no other");
    assert.deepEqual(errors, [], `page errors: ${errors}`);
    await close();
    console.log("PASS selector: current Primary, active Bots only, applies at once");
  }

  // ---- 2. Delete dialog: the Primary needs a successor; others do not.
  {
    reset();
    let t = await open({scene: "delete", primary: "1"});
    const submit = t.page.locator(".delete-dialog-submit");
    await submit.waitFor({timeout: 15000});
    const successor = t.page.locator(".primary-successor .ui-select-trigger");
    assert.ok(await successor.isVisible(), "the Primary's delete dialog asks for a successor");
    assert.equal(await submit.isDisabled(), true, "delete stays disabled until a successor is chosen");
    assert.deepEqual(await selectLabels(successor), ["Choose a Bot", "Inbox Bot", "Notes Bot"], "successors are the other active Bots, never the Bot itself or an archived one");
    await submit.click({force: true}).catch(() => {});
    assert.equal(await t.page.evaluate(() => window.__deleted), undefined, "a disabled delete does nothing");
    await pickSelect(successor, "bot_notes");
    assert.equal(await submit.isDisabled(), false);
    await submit.click();
    await t.page.waitForFunction(() => window.__closed === true);
    assert.deepEqual(await t.page.evaluate(() => window.__deleted), {id: "bot_research", successor: "bot_notes"});
    await t.close();

    t = await open({scene: "delete", primary: "0"});
    await t.page.locator(".delete-dialog-submit").waitFor();
    assert.equal(await t.page.locator(".primary-successor").count(), 0, "a non-Primary Bot is deleted without a successor");
    assert.equal(await t.page.locator(".delete-dialog-submit").isDisabled(), false);
    await t.page.locator(".delete-dialog-submit").click();
    await t.page.waitForFunction(() => window.__closed === true);
    assert.deepEqual(await t.page.evaluate(() => window.__deleted), {id: "bot_research", successor: undefined});
    await t.close();

    t = await open({scene: "delete", primary: "1", alone: "1"});
    await t.page.locator(".delete-dialog-submit").waitFor();
    assert.equal(await t.page.locator(".primary-successor").count(), 0, "the last Bot has nobody to hand over to");
    assert.equal(await t.page.locator(".delete-dialog-submit").isDisabled(), false);
    await t.close();

    t = await open({scene: "delete", primary: "1", stale: "1"});
    await t.page.locator(".primary-successor .ui-select-trigger").waitFor();
    await pickSelect(t.page.locator(".primary-successor .ui-select-trigger"), "bot_inbox");
    await t.page.locator(".delete-dialog-submit").click();
    await t.page.getByRole("alert").waitFor();
    assert.match(await t.page.getByRole("alert").innerText(), /Choose a new Primary Assistant first/, "a stale client sees the reassign message from the server code");
    await t.close();
    console.log("PASS delete dialog: successor required for the Primary only, last Bot exempt, server refusal explained");
  }

  // ---- 3. Hide dialog (archiving the Primary) asks for a successor too; Escape and Cancel leave nothing behind.
  {
    reset();
    let t = await open({scene: "hide"});
    const confirm = t.page.locator(".delete-dialog-actions .primary-button");
    await confirm.waitFor({timeout: 15000});
    assert.equal(await confirm.isDisabled(), true);
    await pickSelect(t.page.locator(".primary-successor .ui-select-trigger"), "bot_inbox");
    await confirm.click();
    await t.page.waitForFunction(() => window.__closed === true);
    assert.deepEqual(await t.page.evaluate(() => window.__hidden), {successor: "bot_inbox"});
    await t.close();
    t = await open({scene: "hide"});
    await t.page.locator(".delete-dialog").waitFor();
    await t.page.keyboard.press("Escape");
    await t.page.waitForFunction(() => window.__closed === true);
    assert.equal(await t.page.evaluate(() => window.__hidden), undefined, "Escape cancels");
    await t.close();
    console.log("PASS hide dialog: successor required, Escape cancels");
  }

  // ---- 4. Sidebar badge: the tag stays visible next to a long, truncated name.
  {
    const t = await open({scene: "badge"}, {width: 260, height: 200});
    await t.page.locator(".primary-badge").waitFor({timeout: 15000});
    const report = await t.page.evaluate(() => {
      const badge = document.querySelector(".primary-badge").getBoundingClientRect(), row = document.querySelector(".contact-copy").getBoundingClientRect(), name = document.querySelector(".contact-name");
      return {badgeRight: badge.right, rowRight: row.right, truncated: name.scrollWidth > name.clientWidth, text: document.querySelector(".primary-badge").textContent};
    });
    assert.equal(report.text, "Primary");
    assert.ok(report.badgeRight <= report.rowRight + 0.5, "badge stays inside the row");
    assert.equal(report.truncated, true, "the name truncates, not the tag");
    await t.close();
    console.log("PASS badge: visible beside a truncated name");
  }

  // ---- 5. Follow-ups (preview): preview-only banner, per-Bot mode, would-be items, review buttons persist.
  {
    reset();
    const t = await open({scene: "followups"});
    await t.page.getByText("Preview only. Nothing is sent.").waitFor({timeout: 15000});
    const body = (await t.page.locator(".followups-page").innerText()).replace(/\s+/g, " ");
    assert.match(body, /Would remind you/);
    assert.match(body, /Weekly digest/);
    assert.match(body, /Pest control to call about the roof bees/);
    assert.match(body, /Waiting on someone/);
    assert.match(body, /You promised/);
    assert.match(body, /Call the dentist/);
    assert.match(body, /3 a day/, "daily cap shown as the account limit");
    assert.match(body, /22:00–08:00/);
    assert.doesNotMatch(body, /Delivering|Turn on reminders/, "no way to switch delivery on in this phase");
    const modeOptions = await selectLabels(t.page.locator("#followup-mode-bot_research"));
    assert.deepEqual(modeOptions, ["Off", "Preview"], "only off and preview are offered");
    assert.equal(await selectValue(t.page.locator("#followup-mode-bot_notes")), "shadow");
    // Primary tag on the Primary's row.
    assert.equal(await t.page.locator(".followup-bot-name .primary-badge").count(), 1);
    await pickSelect(t.page.locator("#followup-mode-bot_research"), "shadow");
    await t.page.waitForFunction(() => document.querySelector("#followup-mode-bot_research")?.dataset.value === "shadow");
    assert.equal(state.modes.bot_research, "shadow");
    // Review buttons.
    const first = t.page.locator(".followup-item", {hasText: "Pest control to call about the roof bees"});
    await first.getByRole("button", {name: "Useful"}).click();
    await t.page.waitForFunction(() => document.querySelector(".followup-item .followup-verdict")?.textContent === "Useful");
    assert.equal(state.verdicts.loop_1, "useful");
    await first.getByRole("button", {name: "Already handled"}).click();
    await t.page.getByText("Reviewed", {exact: true}).waitFor();
    assert.equal(state.verdicts.loop_1, "handled");
    assert.deepEqual(requests, [
      'PUT /api/followups/bots/bot_research {"mode":"shadow"}',
      'POST /api/followups/loops/loop_1/feedback {"verdict":"useful"}',
      'POST /api/followups/loops/loop_1/feedback {"verdict":"handled"}']);
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS follow-ups preview: banner, modes, items by band, review buttons persist");
  }

  // ---- 6. Warnings: no model, budget paused.
  {
    reset();
    state.modelConfigured = false; state.budgetPaused = true;
    const t = await open({scene: "followups"});
    await t.page.getByText("No model connected").waitFor({timeout: 15000});
    await t.page.getByText("Paused for today").waitFor();
    await t.close();
    console.log("PASS follow-ups preview: no-model and budget-pause banners");
  }

  // ---- 7. Seven locales: the new surfaces are translated and fit at 390px.
  for (const [lang, locale, title, primary] of [["zh-CN", "zh-CN", "仅预览，不会发送任何内容。", "主助理"], ["zh-TW", "zh-TW", "僅預覽，不會傳送任何內容。", "主要助理"], ["ja", "ja-JP", "プレビューのみです。何も送信されません。", "メインアシスタント"], ["ko", "ko-KR", "미리보기 전용입니다. 아무것도 전송되지 않습니다.", "기본 어시스턴트"], ["de", "de-DE", "Nur Vorschau. Es wird nichts gesendet.", "Hauptassistent"], ["fr", "fr-FR", "Aperçu uniquement. Rien n’est envoyé.", "Assistant principal"]]) {
    reset();
    let t = await open({scene: "followups", lang}, {width: 390, height: 844, locale});
    await t.page.getByText(title).waitFor({timeout: 15000});
    const overflow = await t.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    assert.ok(overflow <= 1, `${lang}: followups page overflows by ${overflow}px`);
    await t.close();
    t = await open({scene: "selector", lang}, {width: 390, height: 844, locale});
    await t.page.getByText(primary).first().waitFor({timeout: 15000});
    await t.close();
  }
  console.log("PASS locales: zh-CN, zh-TW, ja, ko, de, fr render both surfaces without horizontal overflow");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
