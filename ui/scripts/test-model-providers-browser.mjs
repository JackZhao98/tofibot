// Rendered acceptance for the model provider cards and grouped model picker.
// Needs PLAYWRIGHT_MODULE; set PROVIDER_SCREENSHOTS=<dir> to keep screenshots.
import {selectItems, selectValue, pickSelect} from "./lib/select.mjs";
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile, mkdir} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");
const shots = process.env.PROVIDER_SCREENSHOTS;
if (shots) await mkdir(shots, {recursive: true});
const fixture = await mkdtemp(join(ui, ".provider-settings-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {i18nReady,setLanguage} from '../src/i18n';import {TimezoneProvider} from '../src/UserTimezone';import {ModelProviders} from '../src/ProviderSettings';import {ModelFields} from '../src/ModelSettings';${styles}
function Fixture(){const [model,setModel]=useState(new URLSearchParams(location.search).get('model')||'');const [effort,setEffort]=useState('');return <TimezoneProvider><div className="workspace"><div className="settings-page-body"><ModelProviders refreshToken={0} codexRefresh={0} onConfigured={()=>{}} onCodexConfigured={()=>{}}/><section className="settings-section" data-testid="picker"><ModelFields model={model} effort={effort} onChange={(m,e)=>{setModel(m);setEffort(e)}}/><output data-testid="value">{model}|{effort}</output></section></div></div></TimezoneProvider>}
void i18nReady.then(()=>setLanguage('zh-CN')).then(()=>createRoot(document.getElementById('root')).render(<Fixture/>));`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME});
  const page = await browser.newPage();
  const goodKey = "sk-synthetic-PRIVATE-KEY-9f3a", badKey = "sk-synthetic-BAD-KEY-0000";
  const providers = {
    codex: {id: "codex", label: "Codex", kind: "oauth", configured: true, status: "connected"},
    openai: {id: "openai", label: "OpenAI", kind: "api_key", configured: false, key_hint: "", verified_at: "", error: ""},
    anthropic: {id: "anthropic", label: "Claude", kind: "api_key", configured: true, key_hint: "…7c21", key_prefix: "sk-a", verified_at: "2026-10-07T16:05:00Z", error: ""},
  };
  let codexConnected = true;
  const models = [
    {id: "codex-gpt-6-sol", name: "Codex · GPT-6 Sol", provider: "codex", reasoning_efforts: ["low", "medium", "high", "xhigh"], default_reasoning: "high"},
    {id: "codex-gpt-6-luna", name: "GPT-6 Luna", provider: "codex", reasoning_efforts: ["medium"], default_reasoning: "medium"},
    {id: "gpt-6-luna", name: "GPT-6 Luna", provider: "openai", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium"},
    {id: "gpt-4.1", name: "GPT-4.1", provider: "openai", reasoning_efforts: [], default_reasoning: ""},
    {id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium", "high", "xhigh", "max"], default_reasoning: "high"},
  ];
  const writes = [], unexpected = [], bodies = [];
  let catalog = {models, source: "live", warning: ""};
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method();
    if (url.origin !== origin) { unexpected.push(url.href); await route.abort(); return; }
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    const reply = (body, status = 200) => { const text = JSON.stringify(body); bodies.push(text); return route.fulfill({status, contentType: "application/json", body: text}); };
    if (url.pathname === "/api/preferences") return reply({timezone: "America/Los_Angeles", timezone_configured: true});
    if (url.pathname === "/api/auth/codex" && method === "GET") return reply({connected: codexConnected, expires_at: Date.parse("2026-11-01T00:00:00Z")});
    if (url.pathname === "/api/auth/codex" && method === "DELETE") { writes.push("DELETE codex"); codexConnected = false; providers.codex = {...providers.codex, configured: false, status: "disconnected"}; return reply({}); }
    if (url.pathname === "/api/auth/codex/verify") return reply({connected: codexConnected, expires_at: Date.parse("2026-11-01T00:00:00Z"), check: "ok"});
    if (url.pathname === "/api/providers" && method === "GET") return reply({providers: Object.values(providers)});
    if (url.pathname === "/api/models") return reply(catalog);
    if (url.pathname === "/api/model-settings" && method === "GET") return reply({model: "codex-gpt-6-luna", reasoning_effort: "medium"});
    const match = url.pathname.match(/^\/api\/providers\/(openai|anthropic)\/key$/);
    if (match && method === "PUT") {
      writes.push(`PUT ${match[1]}`);
      const {key} = JSON.parse(request.postData() || "{}");
      await new Promise(resolve => setTimeout(resolve, 400));
      if (key === badKey) return reply({error: {code: "invalid_key", message: "OpenAI rejected this API key (HTTP 401)."}}, 400);
      providers[match[1]] = {...providers[match[1]], configured: true, key_hint: `…${key.slice(-4)}`, key_prefix: key.slice(0, 4), verified_at: "2026-10-07T17:30:00Z", error: ""};
      return reply(providers[match[1]]);
    }
    if (match && method === "DELETE") { writes.push(`DELETE ${match[1]}`); providers[match[1]] = {...providers[match[1]], configured: false, key_hint: "", key_prefix: "", verified_at: ""}; return reply({}); }
    unexpected.push(`${method} ${url.pathname}`);
    return route.fulfill({status: 500, body: "unexpected"});
  });
  const at = `${origin}/${basename(fixture)}/index.html`;
  for (const viewport of [{width: 900, height: 1400}, {width: 390, height: 1600}]) {
    await page.setViewportSize(viewport);
    codexConnected = true; providers.codex = {...providers.codex, configured: true, status: "connected"};
    await page.goto(at);
    const openai = page.locator("article.provider-card").filter({has: page.getByRole("heading", {name: "OpenAI", exact: true})});
    const claude = page.locator("article.provider-card").filter({has: page.getByRole("heading", {name: "Claude", exact: true})});
    // Codex and the OpenAI key are one card: a connected ChatGPT sign-in shows only that method.
    await openai.getByText("Codex 已连接 · 已验证").waitFor({timeout: 10000});
    assert.equal(await page.locator("article.provider-card").count(), 2, "OpenAI (ChatGPT or key) and Claude");
    assert.equal(await page.getByRole("heading", {name: "Codex", exact: true}).count(), 0, "no separate Codex card");
    assert.equal(await openai.locator("input").count(), 0, "no key input while the ChatGPT sign-in is the method");
    assert.equal(await openai.locator(".provider-choice").count(), 0, "no chooser once a method is set up");
    // Claude: the key is masked (first four, dots, last four) with its verified time; no replace input until asked.
    await claude.getByText("sk-a••••••••7c21").waitFor();
    assert.match(await claude.innerText(), /已验证于/);
    assert.equal(await claude.locator("input").count(), 0, "no input while a key is set");
    assert.equal(await claude.getByText(/替换 Claude API key/).count(), 0, "no Replace heading");
    await claude.getByRole("button", {name: "替换密钥"}).click();
    await claude.getByLabel("Claude API key").waitFor();
    await claude.getByRole("button", {name: "取消"}).click();
    assert.equal(await claude.locator("input").count(), 0, "cancel hides the input again");
    // Picker: optgroups in Codex, OpenAI, Claude order with display names.
    const picker = page.getByTestId("picker");
    await picker.locator(".ui-select-trigger").first().waitFor();
    const groupedItems = await selectItems(picker.locator(".ui-select-trigger").first());
    const grouped = [];
    for (const item of groupedItems) { if (!item.group) continue; if (grouped.at(-1)?.[0] !== item.group) grouped.push([item.group, []]); grouped.at(-1)[1].push(item.text); }
    assert.deepEqual(grouped,
      [["Codex", ["Codex · GPT-6 Sol", "Codex · GPT-6 Luna"]], ["OpenAI", ["GPT-6 Luna", "GPT-4.1"]], ["Claude", ["Claude Opus 5.5"]]]);
    if (shots) await page.screenshot({path: join(shots, `providers-${viewport.width}.png`), fullPage: true});
    const modelSelect = picker.locator(".ui-select-trigger").first();
    const effortSelect = () => picker.locator("label",{hasText:"思考强度"}).locator(".ui-select-trigger");
    // A Bot with no pin follows the global model; the option names what it follows and hides effort.
    await modelSelect.getByText("跟随全局（当前：Codex · GPT-6 Luna · 中）").waitFor({timeout: 10000});
    assert.equal(await selectValue(modelSelect), "default");
    assert.equal(await picker.locator("label",{hasText:"思考强度"}).count(), 0, "effort hidden while following global");
    await pickSelect(modelSelect, "claude-opus-5-5");
    assert.deepEqual((await selectItems(effortSelect())).map(item => item.value), ["default", "low", "medium", "high", "xhigh", "max"]);
    assert.equal((await selectItems(effortSelect()))[0].text, "模型默认（高）");
    assert.equal(await page.getByTestId("value").textContent(), "claude-opus-5-5|default");
    await pickSelect(effortSelect(), "max");
    assert.equal(await page.getByTestId("value").textContent(), "claude-opus-5-5|max");
    await pickSelect(modelSelect, "gpt-4.1");
    assert.equal(await effortSelect().count(), 0, "effort hidden for non-reasoning models");
    assert.equal(await page.getByTestId("value").textContent(), "gpt-4.1|default");
    await pickSelect(modelSelect, "default");
    assert.equal(await page.getByTestId("value").textContent(), "default|default", "following global follows its effort too");
    // Switching method disconnects the current one after a confirmation, then offers both equal choices.
    await openai.getByRole("button", {name: "改用 API key"}).click();
    await openai.getByRole("group", {name: "退出 ChatGPT 并改用 API key？"}).waitFor();
    await openai.getByRole("button", {name: "切换"}).click();
    const input = openai.getByLabel("OpenAI API key");
    await input.waitFor();
    const tiles = openai.locator(".provider-choice");
    assert.equal(await tiles.count(), 2, "two equal choices");
    const [one, two] = await Promise.all([tiles.nth(0).boundingBox(), tiles.nth(1).boundingBox()]);
    assert.ok(Math.abs(one.width - two.width) < 2 && Math.abs(one.height - two.height) < 2, "the choices are the same size");
    if (viewport.width > 560) assert.ok(Math.abs(one.y - two.y) < 2, "side by side on a wide window");
    // Invalid key: disabled while verifying, inline named error, nothing echoed.
    await input.fill(badKey);
    await openai.getByRole("button", {name: "保存并验证"}).click();
    await openai.getByRole("button", {name: "验证中…"}).waitFor();
    assert(await openai.getByRole("button", {name: "验证中…"}).isDisabled());
    await openai.getByRole("alert").filter({hasText: "没有通过 OpenAI 的验证"}).waitFor();
    // The server's own reason is shown; no part of the key ever is.
    assert((await openai.innerText()).includes("HTTP 401"), "server reason is shown");
    assert(!(await openai.innerText()).includes(badKey.slice(-6)), "key stays out of the note");
    assert.equal(await input.getAttribute("type"), "password");
    // Valid key: the input goes away and the card shows only the masked key and when it was verified.
    await input.fill(goodKey);
    await openai.getByRole("button", {name: "保存并验证"}).click();
    await openai.getByText("sk-s••••••••9f3a").waitFor();
    assert.match(await openai.innerText(), /已验证于/);
    assert.equal(await openai.locator("input").count(), 0, "no replace input after saving");
    assert.equal(await openai.locator(".provider-choice").count(), 0, "the other method is not offered beside a set-up one");
    assert(!(await page.content()).includes("PRIVATE-KEY"), "the key never renders");
    if (shots && viewport.width === 900) await page.screenshot({path: join(shots, "providers-saved.png"), fullPage: true});
    // Remove asks first.
    await openai.getByRole("button", {name: "移除", exact: true}).click();
    await openai.getByRole("group", {name: "移除 OpenAI API key？"}).waitFor();
    await openai.getByRole("button", {name: "确认移除"}).click();
    await openai.locator(".provider-choice").first().waitFor();
    assert.equal(await openai.locator(".provider-choice").count(), 2, "nothing set up: both methods are offered");
    // Reset the OpenAI provider for the next viewport.
  }
  assert(bodies.every(body => !body.includes("PRIVATE-KEY")), "no response carries the key");
  // No provider: generic note pointing at the connection page.
  catalog = {models: [], source: "live", warning: ""};
  await page.goto(at);
  await page.getByText("还没有可用的模型。在设置的「模型」页连接 Codex，或添加 OpenAI / Claude API key。").waitFor({timeout: 10000});
  assert(!(await page.getByTestId("picker").innerText()).includes("暂未获取到 Codex"));
  // Unknown saved id keeps the current-configuration option.
  catalog = {models, source: "live", warning: ""};
  await page.goto(`${at}?model=claude-retired-model`);
  await page.getByTestId("picker").locator(".ui-select-trigger").first().getByText("claude-retired-model · 当前配置").waitFor({timeout: 10000});
  assert.deepEqual(writes, ["DELETE codex", "PUT openai", "PUT openai", "DELETE openai", "DELETE codex", "PUT openai", "PUT openai", "DELETE openai"]);
  assert.deepEqual(unexpected, []);
  console.log("PASS model providers: OpenAI (ChatGPT or key) and Claude cards, masked keys, switch method, grouped picker, effort per model, invalid/valid key flow without echo, confirm remove, empty and unknown-model states (desktop + narrow)");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
