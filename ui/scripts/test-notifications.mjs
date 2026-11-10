// Web outlet of server-side notifications (cloud-services 10, ui/src/attentionNotifier.ts, ChatsSettings.tsx).
// Real modules in a Vite fixture, /api stubbed as a small server (synthetic data), Chromium via Playwright:
//  1. A plain browser (no tofiDesktop.notifications): the notifier starts at the tail, then fires Notification once per
//     delivered item, never for the conversation in view, never again for an item already shown, silent for a quiet
//     one, and not for an item older than ten minutes. The delivery view is the only source (no conversation list).
//  2. A server without `notifications` in /api/server-info: not one /api/notifications request, and the settings page
//     says the server needs an update.
//  3. A stub tofiDesktop.notifications bridge: the page never calls Notification and never reads the view, even with
//     items waiting; it reports the conversation in view through bridge.setView; the device rows show the bridge's
//     state in every state and the switches call bridge.update.
//  4. The four account switches render and save in a browser; the "This device" rows exist only with the bridge.
//  5. All seven languages render the rows (browser and bridge) at 390 and 1440 px without raw keys or overflow.
//
//   PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:notifications   (no SKIP: fails without it)
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

if (!process.env.PLAYWRIGHT_MODULE) { console.error("test:notifications needs PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs"); process.exit(1); }
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".notifications-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18n,i18nReady,setLanguage} from '../src/i18n';
import {ChatsSettings} from '../src/ChatsSettings';
import {TimezoneProvider} from '../src/UserTimezone';
import {browserNotifier} from '../src/attentionNotifier';
const query=new URLSearchParams(location.search);
const w=window as any;
w.__opened=[];w.__active=null;
w.__notifier=browserNotifier({
  watching:(id:string)=>document.visibilityState==='visible'&&id===w.__active,
  t:(key:string,options?:any)=>i18n.t(key as never,options as never) as unknown as string,
  open:(item:any)=>w.__opened.push(item.conversation_id??item.kind),
});
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><div className="workspace"><div className="settings-page-body"><ChatsSettings/></div></div></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const state = {};
  const reset = () => Object.assign(state, {
    requests: [], delivery: true, queue: [], tail: 5, prefs: {notify_chats: true, notify_tasks: true, notify_needs_you: true, notify_spend_alerts: true}, putBodies: [],
  });
  reset();
  const ago = ms => new Date(Date.now() - ms).toISOString();
  const item = (id, kind, conv, extra = {}) => ({id, kind, detail: "", conversation_id: conv, bot_id: "bot-1", message_seq: id, count: 1, quiet: false, title: `Chat ${conv}`, bot_name: "Ada", preview: `preview ${id}`, created_at: ago(2000), ...extra});

  async function open(params = {}, {width = 1100, height = 900, bridge = null, permission = "granted"} = {}) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: "reduce", locale: "en-US"});
    await context.addInitScript(({bridgeStatus, permission}) => {
      window.__notices = [];
      window.__bridgeCalls = [];
      function FakeNotification(title, options) { window.__notices.push({title, options}); this.close = () => {}; }
      FakeNotification.permission = permission;
      FakeNotification.requestPermission = async () => permission;
      window.Notification = FakeNotification;
      if (bridgeStatus) {
        let status = bridgeStatus;
        const listeners = new Set();
        window.__setBridgeStatus = next => { status = next; listeners.forEach(l => l(status)); };
        window.tofiDesktop = {
          platform: "darwin", themePreference: "system",
          onCommand: () => () => {}, onWindowState: () => () => {}, onFlushState: () => () => {}, remoteInputFocus: () => {},
          appearance: async () => {}, writeWorkspaceState: async () => {}, readWorkspaceState: async () => ({}),
          notifications: {
            status: async () => status,
            update: async patch => { window.__bridgeCalls.push(["update", patch]); status = {...status, settings: {...status.settings, ...patch}}; return status; },
            setView: view => window.__bridgeCalls.push(["setView", view]),
            onStatus: listener => { listeners.add(listener); return () => listeners.delete(listener); },
          },
        };
      }
    }, {bridgeStatus: bridge, permission});
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      const body = request.postData() ? JSON.parse(request.postData()) : undefined;
      state.requests.push(`${request.method()} ${url.pathname}${url.search}`);
      const json = (data, status = 200) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(data)});
      if (url.pathname === "/api/preferences") {
        if (request.method() === "PUT") { state.putBodies.push(body); Object.assign(state.prefs, body); }
        return json({timezone: "America/Los_Angeles", timezone_configured: true, ...state.prefs});
      }
      if (url.pathname === "/api/server-info") return json(state.delivery ? {service: "tofi", notifications: {delivery: 1, max_wait: 25}} : {service: "tofi"});
      if (url.pathname === "/api/notifications") {
        if (url.searchParams.get("tail") === "1") return json({items: [], next_after: state.tail, language: ""});
        const after = Number(url.searchParams.get("after"));
        const items = state.queue.filter(entry => entry.id > after);
        return json({items, next_after: items.length ? items[items.length - 1].id : after, language: ""});
      }
      return json({}, 404);
    });
    await page.goto(`${origin}/${fixture.split("/").pop()}/index.html?${new URLSearchParams(params)}`);
    await page.locator(".settings-page-body [data-setting='notify-chats']").waitFor();
    return {page, context, errors, close: () => context.close()};
  }
  const feedRequests = () => state.requests.filter(line => line.includes("/api/notifications"));

  // ---- 1. A plain browser.
  reset();
  {
    const t = await open();
    await t.page.evaluate(() => window.__notifier.poll());
    assert.deepEqual(feedRequests(), ["GET /api/notifications?view=delivery&tail=1"], "the first read only takes the tail");
    assert.equal(await t.page.evaluate(() => window.__notices.length), 0, "nothing from before the page is shown");
    state.queue = [
      item(6, "reply", "c1"),                       // in view: not shown
      item(7, "reply", "c2", {count: 3, bot_name: "Ada", title: "Group"}),
      item(8, "needs_you", "c3", {message_seq: null, preview: ""}),
      item(9, "spend_alert", undefined, {detail: "spend_cap", message_seq: null, preview: "", title: ""}),
      item(10, "reply", "c4", {quiet: true}),
      item(11, "reply", "c5", {created_at: ago(11 * 60_000)}), // too old
    ];
    await t.page.evaluate(() => { window.__active = "c1"; });
    await t.page.evaluate(() => window.__notifier.poll());
    const notices = await t.page.evaluate(() => window.__notices);
    assert.deepEqual(notices.map(n => n.title), ["Group", "Chat c3", "Tofi", "Chat c4"], "one Notification per delivered item; none for the conversation in view or the stale row");
    assert.equal(notices[0].options.body, "3 new messages · Ada: preview 7");
    assert.equal(notices[1].options.body, "Waiting for your answer");
    assert.equal(notices[2].options.body, "This month's spend reached your limit");
    assert.equal(notices[0].options.tag, "tofi-c2");
    assert.equal(notices[3].options.silent, true, "a quiet item replaces the banner without sound");
    assert.equal(notices[0].options.silent, undefined);
    assert.equal(feedRequests().at(-1), "GET /api/notifications?view=delivery&after=5");
    await t.page.evaluate(() => window.__notifier.poll());
    assert.equal(feedRequests().at(-1), "GET /api/notifications?view=delivery&after=11", "the cursor moved past every row, shown or not");
    assert.equal(await t.page.evaluate(() => window.__notices.length), 4, "an item is never shown twice");
    assert.ok(!state.requests.some(line => line.includes("/api/conversations")), "no conversation list is read for notifications");
    assert.deepEqual(t.errors, []);
    await t.close();
    console.log("PASS browser outlet: tail first, one Notification per delivered item, nothing for the chat in view, quiet is silent");
  }

  // ---- 1b. No permission: nothing is read.
  reset();
  {
    const t = await open({}, {permission: "default"});
    await t.page.evaluate(() => window.__notifier.poll());
    assert.deepEqual(feedRequests(), [], "without permission the page asks the server nothing");
    await t.close();
  }

  // ---- 2. A server that cannot deliver.
  reset(); state.delivery = false;
  {
    const t = await open();
    await t.page.evaluate(() => window.__notifier.poll());
    await t.page.evaluate(() => window.__notifier.poll());
    assert.deepEqual(feedRequests(), [], "an older server is never asked for the delivery view");
    assert.equal(await t.page.evaluate(() => window.__notifier.state()), "server_unsupported");
    assert.match(await t.page.locator("[data-setting='notify-server-unsupported']").innerText(), /server needs an update to send notifications/i);
    await t.close();
    console.log("PASS an older server is named and not asked");
  }

  // ---- 3. The Mac app's bridge.
  reset(); state.queue = [item(6, "reply", "c2")];
  const status = (state, extra = {}) => ({state, settings: {enabled: true, content: true, openAtLogin: false}, ...extra});
  {
    const t = await open({}, {bridge: status("on")});
    await t.page.evaluate(() => window.__notifier.poll());
    await t.page.evaluate(() => window.__notifier.poll());
    assert.deepEqual(feedRequests(), [], "with the bridge the page never reads the delivery view");
    assert.equal(await t.page.evaluate(() => window.__notices.length), 0, "with the bridge the page never calls Notification");
    await t.page.evaluate(() => window.__notifier.setView("c9"));
    await t.page.evaluate(() => window.__notifier.setView(null));
    assert.deepEqual(await t.page.evaluate(() => window.__bridgeCalls), [["setView", {conversationId: "c9"}], ["setView", {conversationId: null}]]);
    // Device rows: only here, one text per state, and the switches reach the bridge.
    const rows = await t.page.locator("[data-setting^='notify-device-']:not([data-setting='notify-device-status'])").count();
    assert.equal(rows, 3);
    const expected = {
      on: /Notifications are on/, off: /off on this Mac/, not_connected: /No server is set up/, server_unsupported: /server needs an update to send notifications/,
      signed_out: /Signed out/, offline: /Can't reach the server/, blocked_by_system: /macOS does not allow notifications/,
    };
    for (const [name, pattern] of Object.entries(expected)) {
      await t.page.evaluate(next => window.__setBridgeStatus(next), status(name));
      await t.page.waitForFunction(value => document.querySelector("[data-setting='notify-device-status']")?.dataset.state === value, name);
      assert.match(await t.page.locator("[data-setting='notify-device-status']").innerText(), pattern, name);
    }
    await t.page.evaluate(next => window.__setBridgeStatus(next), status("offline", {lastSuccessAt: Date.now() - 60_000}));
    await t.page.waitForFunction(() => /Last success/.test(document.querySelector("[data-setting='notify-device-status']")?.textContent ?? ""));
    await t.page.evaluate(next => window.__setBridgeStatus(next), status("offline", {reason: "too_many_waiters"}));
    await t.page.locator("[data-setting='notify-device-reason'][data-reason='too_many_waiters']").waitFor();
    await t.page.evaluate(next => window.__setBridgeStatus(next), status("offline", {reason: "some_new_reason"}));
    await t.page.waitForFunction(() => !document.querySelector("[data-setting='notify-device-reason']"));
    await t.page.locator("[data-setting='notify-device-content'] [role=switch]").click();
    await t.page.locator("[data-setting='notify-device-openAtLogin'] [role=switch]").click();
    assert.deepEqual((await t.page.evaluate(() => window.__bridgeCalls)).filter(call => call[0] === "update"), [["update", {content: false}], ["update", {openAtLogin: true}]]);
    assert.deepEqual(t.errors, []);
    await t.close();
    console.log("PASS bridge: no reads, no Notification, view reported, seven device states, switches reach the main process");
  }

  // ---- 4. Account switches, and no device rows without the bridge.
  reset();
  {
    const t = await open();
    assert.equal(await t.page.locator("[data-setting='notify-device']").count(), 0, "no device rows in a browser");
    for (const key of ["tasks", "chats", "needs_you", "spend_alerts"]) assert.equal(await t.page.locator(`[data-setting='notify-${key}'] [role=switch]`).isChecked(), true, `${key} defaults on`);
    await t.page.locator("[data-setting='notify-needs_you'] [role=switch]").click();
    await t.page.waitForFunction(() => document.querySelector("[data-setting='notify-needs_you'] [role=switch]")?.getAttribute("aria-checked") === "false");
    assert.deepEqual(state.putBodies, [{notify_needs_you: false}]);
    await t.page.locator("[data-setting='notify-spend_alerts'] [role=switch]").click();
    await t.page.waitForFunction(() => document.querySelector("[data-setting='notify-spend_alerts'] [role=switch]")?.getAttribute("aria-checked") === "false");
    assert.deepEqual(state.putBodies, [{notify_needs_you: false}, {notify_spend_alerts: false}]);
    assert.equal(await t.page.locator("[data-setting='notify-chats'] [role=switch]").isChecked(), true, "the others stay");
    await t.close();
    console.log("PASS four account switches default on and save on their own");
  }

  // ---- 5. Seven languages, two widths.
  const languages = [["en", "en-US"], ["zh-CN", "zh-CN"], ["zh-TW", "zh-TW"], ["ja", "ja-JP"], ["ko", "ko-KR"], ["de", "de-DE"], ["fr", "fr-FR"]];
  for (const [lang] of languages) {
    for (const width of [390, 1440]) {
      for (const withBridge of [false, true]) {
        reset();
        const t = await open({lang}, {width, height: 1000, bridge: withBridge ? status("offline", {lastSuccessAt: Date.now() - 3_600_000, reason: "too_many_waiters"}) : null});
        if (withBridge) await t.page.waitForFunction(() => document.querySelector("[data-setting='notify-device-status']")?.dataset.state === "offline");
        const label = `${lang} ${width}px ${withBridge ? "bridge" : "browser"}`;
        const text = await t.page.locator(".settings-page-body").innerText();
        assert.ok(!/settings:|chats\.|device_|notify_|reason_/.test(text), `${label}: raw key on the page: ${text.slice(0, 200)}`);
        for (const key of ["tasks", "chats", "needs_you", "spend_alerts"]) assert.equal(await t.page.locator(`[data-setting='notify-${key}']`).count(), 1, `${label}: ${key} row`);
        if (withBridge) {
          assert.equal(await t.page.locator("[data-setting^='notify-device-']:not([data-setting='notify-device-status']) [role=switch]").count(), 3, `${label}: device switches`);
          assert.ok(!/\{\{/.test(text), `${label}: untranslated or uninterpolated text`);
        }
        const overflow = await t.page.evaluate(() => {
          const out = [];
          if (document.documentElement.scrollWidth > window.innerWidth + 1) out.push("page");
          for (const el of document.querySelectorAll(".settings-page-body .debug-toggle, .settings-page-body .field-note, .settings-page-body .device-notifications-title")) {
            const box = el.getBoundingClientRect();
            if (el.scrollWidth > el.clientWidth + 1 || box.right > window.innerWidth + 1 || box.left < -1) out.push(`${el.getAttribute("data-setting") || el.className}: scroll ${el.scrollWidth} client ${el.clientWidth} right ${Math.round(box.right)}`);
          }
          return out;
        });
        assert.deepEqual(overflow, [], `${label}: overflow`);
        await t.close();
      }
    }
  }
  console.log("PASS all seven languages render the rows at 390 and 1440 px, with and without the bridge, no raw keys or overflow");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
