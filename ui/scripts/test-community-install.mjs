// Rendered acceptance for installing a community MCP package yourself (decision 0047).
// Mounts the real MCPSettings (with the local Runner panel) in a Vite fixture with every /api/* call
// stubbed (synthetic data only) and drives it with Playwright. Needs PLAYWRIGHT_MODULE.
import assert from "node:assert/strict";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

const PACKAGE = "@discord-mcp/cli", VERSION = "0.32.1";
const DIGEST = "sha512-rIdvu+/Eia+YVtEy4OBAzrVpshFn9Q2BTVlIqDHdP5ptgR9U0AsM3sVfuxPnoz/kgNnlA+p0zp/6F/5Y8+c5mQ==";
const SECRET = "synthetic-bot-token-0123456789-do-not-show";
const fixture = await mkdtemp(join(ui, ".community-install-"));
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
import {MCPSettings} from '../src/MCPSettings';
const query=new URLSearchParams(location.search);
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<div className="workspace"><MCPSettings/></div>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  const installs = [], unexpected = [], requested = [];
  const plugins = [{id: "discord", kind: "npm", package: PACKAGE, version: VERSION, state: "sleeping", tool_count: 0}];
  const stub = (url, method, body) => {
    const path = url.pathname;
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      {name: "local_discord", url: "http://runner.example.test/mcp/discord", status: {checked_at: "2026-10-22T09:30:00Z", ok: true, tool_count: 10}, provenance: {origin: "community", kind: "npm", package: PACKAGE, version: VERSION}},
      {name: "curated_docs", url: "https://mcp.example.test/docs", status: {checked_at: "2026-10-22T09:30:00Z", ok: true, tool_count: 3}},
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/tools$/.test(path) && method === "GET") return {tools: [], read_only: 0, write: 0, high_risk: 0};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") return {ok: true, tool_count: 3};
    if (path === "/api/extensions/repo-stars") return {stars: {}};
    if (path === "/api/standing-allowances") return {allowances: [], remembered: []};
    if (/^\/api\/extensions\/mcp\/[^/]+\/bots$/.test(path)) return {all: true, bots: []};
    if (path === "/api/extensions/google") return {client: {configured: false}, apis: []};
    if (path === "/api/extensions/oauth-options") return {vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/local-mcp" && method === "GET") return {available: true, plugins};
    if (path === "/api/extensions/local-mcp/install" && method === "POST") { installs.push(JSON.parse(body)); return {ok: true}; }
    return undefined;
  };
  async function open(lang) {
    const context = await browser.newContext({viewport: {width: 1200, height: 1400}});
    const page = await context.newPage();
    page.on("pageerror", error => unexpected.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      requested.push(`${request.method()} ${url.pathname}${url.search}`);
      const answer = stub(url, request.method(), request.postData());
      if (answer === undefined) { unexpected.push(`${request.method()} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {message: "not stubbed", code: "not_found"}})}); return; }
      await route.fulfill({contentType: "application/json", body: JSON.stringify(answer)});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?lang=${lang}`);
    await page.locator(".mcp-settings").waitFor({timeout: 20000});
    return {page, close: () => context.close()};
  }

  const en = await open("en");
  const row = en.page.locator('.mcp-service[aria-label="discord"]');
  await row.waitFor();
  await row.locator(".mcp-service-open").click();
  const detail = en.page.locator(".mcp-detail");
  await detail.waitFor();
  assert.ok((await detail.locator(".mcp-detail-title p").textContent()).includes(`Community · ${PACKAGE} ${VERSION}`), "the page names the package and version");
  assert.ok((await detail.textContent()).includes("its tools ask for approval until you mark them read-only"), "the page says how its tools are treated");
  await detail.getByRole("button", {name: "Back to Connections"}).click();
  assert.equal(await en.page.locator('.mcp-service[aria-label="curated_docs"]').count(), 1);

  await en.page.getByRole("button", {name: "Add connection"}).click();
  const dialog = en.page.locator('[role="dialog"].connect-dialog');
  await dialog.locator('[data-integration-id="custom-package"]').click();
  const form = dialog.locator(".connect-form");
  const field = label => form.locator("label", {hasText: label}).locator("input,textarea,select");
  await field("Plugin ID").fill("discord2");
  await field("Package name").fill(PACKAGE);
  await field("Pinned version").fill(VERSION);
  await field("Executable").fill("discord-mcp");
  await field("Arguments").fill("serve");
  const install = dialog.getByRole("button", {name: "Install plugin"});
  await dialog.locator(".connect-env-row input").nth(0).fill("DISCORD_TOKEN");
  const secretInput = dialog.locator(".connect-env-row input").nth(1);
  assert.equal(await secretInput.getAttribute("type"), "password", "a variable's value is never shown while typing");
  await secretInput.fill(SECRET);
  await field("Package digest").fill("sha512-too-short");
  assert.equal(await install.isDisabled(), true, "a malformed digest blocks the install");
  await field("Package digest").fill(DIGEST);
  assert.equal(await install.isDisabled(), false);
  await dialog.getByRole("button", {name: "Add variable"}).click();
  assert.equal(await dialog.locator(".connect-env-row").count(), 2, "more variables can be added");
  await install.click();
  await dialog.waitFor({state: "detached"});
  assert.deepEqual(installs, [{id: "discord2", kind: "npm", package: PACKAGE, version: VERSION, binary: "discord-mcp", args: ["serve"], env: {DISCORD_TOKEN: SECRET}, integrity: DIGEST}], "the exact pinned package, digest and secret go to the Runner install route");
  const html = await en.page.content();
  assert.ok(!html.includes(SECRET), "the secret appears nowhere in the page");
  assert.ok(!requested.some(line => line.includes(SECRET)), "the secret appears in no URL");
  await en.close();

  const zh = await open("zh-CN");
  const zhRow = zh.page.locator('.mcp-service[aria-label="discord"]');
  await zhRow.waitFor();
  await zhRow.locator(".mcp-service-open").click();
  assert.match(await zh.page.locator(".mcp-detail .mcp-detail-title p").textContent(), new RegExp(`社区 · ${PACKAGE} ${VERSION}`));
  await zh.close();
  assert.deepEqual(unexpected, [], "no unstubbed request and no page error");
  console.log("PASS community install: the connection page names package and version, masked variable values, digest validation, exact install payload, secret never in the page, zh-CN copy");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
