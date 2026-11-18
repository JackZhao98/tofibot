// The look of a Bot and the theme live on the server (docs/design/bot-settings-and-manual.md, slice S7). This test holds:
//   the server's catalog of looks (internal/app/avatar_catalog.json) equals the library the picker draws from;
//   avatarSync and avatarStore use the same storage key and event;
//   the first load after the update hands a look this browser holds up to the server, once;
//   a look stored on the server reaches this browser (cache, event), and an equal look changes nothing;
//   the theme is pulled from the server, handed up once when the server has none, and a choice made here is pushed.
// The browser part mounts the real modules in a Vite fixture with /api answered by a stub. Needs PLAYWRIGHT_MODULE.
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));

// ---- 1. The server's catalog is the library's catalog.
const goCatalog = JSON.parse(await readFile(join(ui, "..", "internal/app/avatar_catalog.json"), "utf8"));
const lib = await import(pathToFileURL(join(ui, "src/lib/tofi-avatar/index.js")));
assert.deepEqual(goCatalog.shapes, lib.catalog.shapes.map(shape => shape.id), "shapes: internal/app/avatar_catalog.json equals the picker's library");
assert.deepEqual(Object.keys(goCatalog.patterns).sort(), lib.catalog.patterns.map(pattern => pattern.id).sort(), "patterns equal");
for (const pattern of lib.catalog.patterns) assert.deepEqual(goCatalog.patterns[pattern.id], lib.paletteOptions(pattern.id).map(palette => palette.id), `palettes of ${pattern.id} equal`);
console.log("PASS the server's catalog of looks equals the picker's library");

// ---- 2. One key and one event on both sides.
const store = await readFile(join(ui, "src/avatarStore.ts"), "utf8");
const sync = await readFile(join(ui, "src/avatarSync.ts"), "utf8");
const constant = (text, name) => new RegExp(`const ${name} = "([^"]+)"`).exec(text)?.[1];
assert.equal(constant(sync, "LOCAL_PREFIX"), constant(store, "STORAGE_PREFIX"), "storage key");
assert.equal(constant(sync, "CHANGE_EVENT"), constant(store, "AVATAR_CHANGE_EVENT"), "change event");
assert.equal(constant((await readFile(join(ui, "src/appearanceSync.ts"), "utf8")), "KEY"), "tofi:appearance", "theme key");
assert.ok(!/from "\.\/api"/.test(sync) && !/from "\.\/api"/.test(await readFile(join(ui, "src/appearanceSync.ts"), "utf8")), "the sync modules take `request` as an argument (a lazy chunk that imports api.ts reshuffles the entry bundle)");
console.log("PASS avatarSync and avatarStore share the key and the event");

// ---- 2b. The ground (schemaVersion 2): one list on both sides, one key and event, and every boundary in the UI keeps it.
{
  const goAvatar = await readFile(join(ui, "..", "internal/app/avatar.go"), "utf8");
  const goGrounds = /var avatarGrounds = \[\]string\{([^}]*)\}/.exec(goAvatar)?.[1].split(",").map(item => item.trim().replace(/"/g, ""));
  const mem = new Map();
  const events = [];
  globalThis.localStorage = {getItem: key => mem.has(key) ? mem.get(key) : null, setItem: (key, value) => void mem.set(key, String(value)), removeItem: key => void mem.delete(key)};
  globalThis.window = Object.assign(new EventTarget(), {dispatchEvent(event) { events.push(event.type); return EventTarget.prototype.dispatchEvent.call(this, event); }});
  globalThis.CustomEvent = class extends Event { constructor(type, init) { super(type); this.detail = init?.detail; } };
  const {openUiModules} = await import("./ui-modules.mjs");
  const modules = await openUiModules({language: "en"});
  try {
    const gs = await modules.load("/src/avatarStore.ts");
    assert.deepEqual([...gs.GROUNDS], goGrounds, "the ground list equals internal/app/avatar.go");
    assert.equal(constant(sync, "GROUND_PREFIX"), constant(store, "GROUND_PREFIX"), "ground storage key");
    assert.equal(constant(sync, "GROUND_EVENT"), constant(store, "GROUND_CHANGE_EVENT"), "ground change event");
    // The default: stable, spread over the list, never stored.
    const ids = Array.from({length: 60}, (_, i) => `bot_${i}`);
    for (const id of ids) assert.equal(gs.defaultBotGround(id), gs.defaultBotGround(id), "same id, same ground");
    assert.ok(new Set(ids.map(gs.defaultBotGround)).size >= 4, "the default spreads over the list");
    assert.equal(gs.getBotGround("bot_a"), gs.defaultBotGround("bot_a"));
    assert.equal(gs.getStoredBotGround("bot_a"), undefined);
    assert.equal(mem.size, 0, "reading the default stores nothing");
    gs.saveBotGround("bot_a", "lime");
    assert.equal(gs.getBotGround("bot_a"), "lime");
    mem.set("tofi:bot-ground:bot_b", "pink");
    assert.equal(gs.getStoredBotGround("bot_b"), undefined, "a damaged copy falls back to the default");
    // Looks: version 1 and 2 read, an invalid ground and an unknown version refused.
    assert.deepEqual(gs.parseLook({schemaVersion: 1, shape: "loaf", pattern: "solid", palette: "ivory"}), {config: {schemaVersion: 1, shape: "loaf", pattern: "solid", palette: "ivory"}});
    assert.equal(gs.parseLook({schemaVersion: 2, shape: "loaf", pattern: "solid", palette: "ivory", ground: "teal"}).ground, "teal");
    assert.throws(() => gs.parseLook({schemaVersion: 2, shape: "loaf", pattern: "solid", palette: "ivory", ground: "pink"}), /ground/);
    assert.throws(() => gs.parseLook({schemaVersion: 3, shape: "loaf", pattern: "solid", palette: "ivory"}), /schemaVersion/);
    // Export: the stored ground rides along as version 2; none stays version 1.
    mem.clear();
    gs.saveBotAvatarConfig("bot_c", {shape: "loaf", pattern: "solid", palette: "ivory"});
    assert.equal(gs.exportBotLook("bot_c").schemaVersion, 1);
    assert.equal("ground" in gs.exportBotLook("bot_c"), false, "an unset ground is not exported");
    gs.saveBotGround("bot_c", "grape");
    assert.deepEqual(gs.exportBotLook("bot_c"), {schemaVersion: 2, shape: "loaf", pattern: "solid", palette: "ivory", ground: "grape"});
    // The Bot package: build then parse keeps the ground; an old package without one still parses; a bad ground is refused.
    const pkg = await modules.load("/src/botPackage.ts");
    const built = pkg.buildBotPackage({id: "bot_c", name: "Synthetic", instructions: "x", model: "m", dm_conversation_id: "d", created_at: "2026-01-01T00:00:00Z"});
    assert.equal(built.bot.avatar.ground, "grape");
    assert.equal(pkg.parseBotPackage(JSON.stringify(built)).bot.avatar.ground, "grape", "a package round-trips the ground");
    const old = {...built, bot: {...built.bot, avatar: {schemaVersion: 1, shape: "loaf", pattern: "solid", palette: "ivory"}}};
    assert.equal(pkg.parseBotPackage(JSON.stringify(old)).bot.avatar.ground, undefined, "a package from before the ground still imports");
    const bad = {...built, bot: {...built.bot, avatar: {...built.bot.avatar, ground: "pink"}}};
    assert.throws(() => pkg.parseBotPackage(JSON.stringify(bad)));
    // Sync: the server's ground reaches the copy and the event; none removes a stale copy; the held v1 look is handed up without touching it.
    mem.clear(); events.length = 0;
    const sent = [];
    const send = async (path, init) => { sent.push(`${init.method} ${path} ${init.body}`); return {}; };
    const sy = await modules.load("/src/avatarSync.ts");
    const served = {schemaVersion: 2, shape: "loaf", pattern: "solid", palette: "ivory", ground: "mustard"};
    sy.hydrateBotAvatars(send, [{id: "bot_d", avatar: served}]);
    assert.equal(gs.getStoredBotGround("bot_d"), "mustard", "the server's ground reaches this browser");
    assert.ok(events.includes("tofi:ground-change"), "and is announced");
    events.length = 0;
    sy.hydrateBotAvatars(send, [{id: "bot_d", avatar: served}]);
    assert.ok(!events.includes("tofi:ground-change"), "an equal ground does not flap");
    sy.hydrateBotAvatars(send, [{id: "bot_d", avatar: {...served, schemaVersion: 1, ground: undefined}}]);
    assert.equal(gs.getStoredBotGround("bot_d"), undefined, "a Bot with a look and no ground drops a stale copy");
    assert.equal(mem.get("tofi:bot-avatar:bot_d") !== undefined, true, "the look copy is kept");
    assert.deepEqual(sent, [], "hydrating a stored look sends nothing");
    // A removed ground is announced, and a subscriber falls back to the default (event and storage event alike).
    sy.hydrateBotAvatars(send, [{id: "bot_f", avatar: served}]);
    const heard = [];
    const off = gs.subscribeBotGround("bot_f", ground => heard.push(ground));
    sy.hydrateBotAvatars(send, [{id: "bot_f", avatar: {...served, schemaVersion: 1, ground: undefined}}]);
    assert.deepEqual(heard, [gs.defaultBotGround("bot_f")], "removal reaches subscribers as the default");
    const storageEvent = new Event("storage"); storageEvent.key = "tofi:bot-ground:bot_f"; storageEvent.newValue = null;
    globalThis.window.dispatchEvent(storageEvent);
    assert.deepEqual(heard, [gs.defaultBotGround("bot_f"), gs.defaultBotGround("bot_f")], "a storage removal falls back to the default");
    off();
    // A cleared look (the server has none) drops the ground copy and announces it; a held look is still handed up.
    sy.hydrateBotAvatars(send, [{id: "bot_g", avatar: served}]);
    assert.equal(gs.getStoredBotGround("bot_g"), "mustard");
    events.length = 0; sent.length = 0;
    sy.hydrateBotAvatars(send, [{id: "bot_g", avatar: null}]);
    assert.equal(gs.getStoredBotGround("bot_g"), undefined, "no look on the server, no ground copy");
    assert.ok(events.includes("tofi:ground-change"), "the clear is announced");
    assert.equal(gs.exportBotLook("bot_g").ground, undefined, "an export no longer carries the cleared ground");
    // A v1 look pushed from here carries no ground key (the server keeps the stored one).
    sent.length = 0;
    mem.set("tofi:bot-avatar:bot_e", JSON.stringify({schemaVersion: 1, shape: "curl", pattern: "solid", palette: "ivory"}));
    sy.hydrateBotAvatars(send, [{id: "bot_e", avatar: null}]);
    assert.ok(sent.length === 1 && !sent[0].includes("ground"), "the held look goes up as version 1");
    // Two picker changes in a row both reach the server, the last with the last look; a later load with no server look sends no third.
    sent.length = 0;
    const lookA = {schemaVersion: 1, shape: "loaf", pattern: "solid", palette: "ivory"};
    const lookB = {schemaVersion: 1, shape: "tall", pattern: "solid", palette: "honey"};
    for (const next of [lookA, lookB]) { const saved = gs.saveBotAvatarConfig("bot_h", next); sy.pushBotAvatar(send, "bot_h", saved); }
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(sent.length, 2, "every picker change is sent");
    assert.equal(sent[1], `PATCH /api/bots/bot_h ${JSON.stringify({avatar: lookB})}`, "the last send holds the last look");
    sy.hydrateBotAvatars(send, [{id: "bot_h", avatar: null}]);
    assert.equal(sent.length, 2, "a following load with no server look does not hand it up again");
  } finally {
    await modules.close();
    delete globalThis.localStorage; delete globalThis.window; delete globalThis.CustomEvent;
  }
  console.log("PASS the ground: one list, one key, a stable default, and every UI boundary keeps it");
}

if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP browser part: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const fixture = await mkdtemp(join(ui, ".avatar-store-"));
let server, browser;
try {
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><div id="root"></div><script type="module" src="./main.ts"></script>');
  await writeFile(join(fixture, "main.ts"), `import {request} from '../src/api';
import * as avatarStore from '../src/avatarStore';
import * as avatarSync from '../src/avatarSync';
import * as appearanceSync from '../src/appearanceSync';
(window as any).__t = {request, avatarStore, avatarSync, appearanceSync};
(window as any).__ready = true;
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});
  const page = await (await browser.newContext()).newPage();
  page.on("pageerror", error => console.log("pageerror:", error.message));
  const calls = [];
  let patchStatus = 200;
  let serverTheme = "";
  const json = (status, body) => ({status, contentType: "application/json", body: JSON.stringify(body)});
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    if (url.origin !== origin) { await route.abort(); return; }
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    calls.push(`${request.method()} ${url.pathname} ${request.postData() ?? ""}`.trim());
    if (url.pathname === "/api/preferences" && request.method() === "GET") { await route.fulfill(json(200, {appearance: serverTheme})); return; }
    if (url.pathname === "/api/preferences" && request.method() === "PUT") { serverTheme = JSON.parse(request.postData()).appearance; await route.fulfill(json(200, {appearance: serverTheme})); return; }
    if (url.pathname.startsWith("/api/bots/") && request.method() === "PATCH") { await route.fulfill(patchStatus === 200 ? json(200, {}) : json(patchStatus, {error: {code: "storage", message: "no"}})); return; }
    await route.fulfill(json(404, {error: {code: "not_found", message: "not stubbed"}}));
  });
  await page.goto(`${origin}/${basename(fixture)}/index.html`);
  await page.waitForFunction(() => window.__ready === true);

  const look = (shape, pattern, palette) => ({schemaVersion: 1, shape, pattern, palette});
  const local = id => page.evaluate(id => localStorage.getItem(`tofi:bot-avatar:${id}`), id);

  // ---- 3. The first load after the update: a look this browser holds goes up, once.
  await page.evaluate(config => localStorage.setItem("tofi:bot-avatar:b1", JSON.stringify(config)), look("loaf", "tabby", "tabby"));
  await page.evaluate(() => { const t = window.__t; t.avatarSync.hydrateBotAvatars(t.request, [{id: "b1", avatar: null}]); });
  await page.waitForTimeout(150);
  assert.deepEqual(calls.filter(call => call.startsWith("PATCH")), [`PATCH /api/bots/b1 {"avatar":${JSON.stringify(look("loaf", "tabby", "tabby"))}}`], "the held look is sent to the server");
  await page.evaluate(() => { const t = window.__t; t.avatarSync.hydrateBotAvatars(t.request, [{id: "b1", avatar: null}]); });
  await page.waitForTimeout(100);
  assert.equal(calls.filter(call => call.startsWith("PATCH")).length, 1, "a second load does not send it again");
  console.log("PASS the first load hands a held look up once");

  // ---- 4. A refused push is tried again on the next load.
  calls.length = 0; patchStatus = 500;
  await page.evaluate(config => localStorage.setItem("tofi:bot-avatar:b3", JSON.stringify(config)), look("rice", "solid", "honey"));
  await page.evaluate(() => { const t = window.__t; t.avatarSync.hydrateBotAvatars(t.request, [{id: "b3", avatar: null}]); });
  await page.waitForTimeout(150);
  patchStatus = 200;
  await page.evaluate(() => { const t = window.__t; t.avatarSync.hydrateBotAvatars(t.request, [{id: "b3", avatar: null}]); });
  await page.waitForTimeout(150);
  assert.equal(calls.filter(call => call.startsWith("PATCH /api/bots/b3")).length, 2, "after a refusal the next load sends it again");
  console.log("PASS a refused push is retried on the next load");

  // ---- 5. Another device's look reaches this browser: cache and event. An equal look changes nothing.
  const result = await page.evaluate(async serverLook => {
    const t = window.__t;
    const seen = [];
    const off = t.avatarStore.subscribeBotAvatar("b2", config => seen.push(config));
    localStorage.setItem("tofi:bot-avatar:b2", JSON.stringify({schemaVersion: 1, shape: "curl", pattern: "solid", palette: "ivory"}));
    t.avatarSync.hydrateBotAvatars(t.request, [{id: "b2", avatar: serverLook}]);
    const after = t.avatarStore.getBotAvatarConfig("b2");
    t.avatarSync.hydrateBotAvatars(t.request, [{id: "b2", avatar: serverLook}]); // equal now: no second event
    off();
    return {seen, after};
  }, look("stand", "calico", "calico"));
  assert.equal(result.seen.length, 1, "one event for one change");
  assert.deepEqual(result.seen[0], look("stand", "calico", "calico"));
  assert.deepEqual(result.after, look("stand", "calico", "calico"), "the next paint reads the server's look");
  assert.deepEqual(JSON.parse(await local("b2")), look("stand", "calico", "calico"));
  assert.ok(!calls.some(call => call.startsWith("PATCH /api/bots/b2")), "a Bot with a stored look is never overwritten from this browser");
  // A Bot with a stored look and an empty cache gets the cache filled.
  await page.evaluate(serverLook => { const t = window.__t; t.avatarSync.hydrateBotAvatars(t.request, [{id: "b4", avatar: serverLook}]); }, look("tall", "stripes", "ocean"));
  assert.deepEqual(JSON.parse(await local("b4")), look("tall", "stripes", "ocean"));
  console.log("PASS a look stored on the server reaches this browser and does not flap");

  // ---- 6. The theme.
  calls.length = 0;
  await page.evaluate(() => localStorage.removeItem("tofi:appearance"));
  serverTheme = "dark";
  const storageEvents = [];
  await page.exposeFunction("__noteStorage", value => storageEvents.push(value));
  await page.evaluate(() => window.addEventListener("storage", event => { if (event.key === "tofi:appearance") window.__noteStorage(event.newValue); }));
  await page.evaluate(() => { const t = window.__t; return t.appearanceSync.pullTheme(t.request); });
  assert.equal(await page.evaluate(() => localStorage.getItem("tofi:appearance")), "dark", "the server's theme is applied");
  assert.deepEqual(storageEvents, ["dark"], "open useAppearance hooks hear it");
  assert.ok(!calls.some(call => call.startsWith("PUT")), "applying the server's theme does not send it back");

  serverTheme = "";
  calls.length = 0;
  await page.evaluate(() => localStorage.setItem("tofi:appearance", "light"));
  await page.evaluate(() => { const t = window.__t; return t.appearanceSync.pullTheme(t.request); });
  assert.deepEqual(calls.filter(call => call.startsWith("PUT")), [`PUT /api/preferences {"appearance":"light"}`], "with no theme chosen on the server, the one this browser holds goes up");

  calls.length = 0;
  await page.evaluate(() => { const t = window.__t; t.appearanceSync.startThemeSync(t.request); });
  await page.waitForTimeout(150);
  calls.length = 0;
  await page.evaluate(() => { localStorage.setItem("tofi:appearance", "dark"); window.dispatchEvent(new StorageEvent("storage", {key: "tofi:appearance", newValue: "dark"})); });
  await page.waitForTimeout(150);
  assert.deepEqual(calls.filter(call => call.startsWith("PUT")), [`PUT /api/preferences {"appearance":"dark"}`], "a choice made here is pushed");
  console.log("PASS the theme is pulled, handed up once and pushed");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
