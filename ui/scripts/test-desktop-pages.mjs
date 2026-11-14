// Built artifact acceptance, synthetic local shell only. Owner runs this outside the restricted sandbox.
import assert from "node:assert/strict";
import { build } from "vite";
import { createServer } from "node:http";
import { mkdtempSync, readFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve, extname } from "node:path";
import { fileURLToPath } from "node:url";
import { launch } from "./computer-window-fixture.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const out = mkdtempSync(join(tmpdir(), "tofi-desktop-pages-"));
let server, browser;
const posts = [], updates = [], apiRequests = [];
let setup = {error: "", values: {connectionMode: "https", serverURL: "https://synthetic.example"}, checking: false};
let mismatch = {reason: "app_too_old", appVersion: "1.2.3", uiContract: 2, serverContract: 3, minClient: "2.0.0", serverURL: "https://synthetic.example", updaterAvailable: true};
const pages = ["setup", "update-required", "local-run", "permission-guide"];
try {
  await build({root: ui, configFile: join(ui, "vite.desktop-pages.config.ts"), logLevel: "error", build: {outDir: out, emptyOutDir: true}});
  for (const name of pages) {
    const html = readFileSync(join(out, `desktop-${name}.html`), "utf8");
    for (const [, asset] of html.matchAll(/(?:src|href)="\/(assets\/[^"]+)"/g)) assert.ok(existsSync(join(out, asset)), asset);
    assert.match(readFileSync(join(ui, `src/desktop-${name}/main.tsx`), "utf8"), /^import "\.\.\/cascade\.css";/);
  }
  server = createServer(async (request, response) => {
    const url = new URL(request.url, "http://localhost");
    const json = data => { response.writeHead(200, {"content-type": "application/json", "cache-control": "no-store"}); response.end(JSON.stringify(data)); };
    if (url.pathname === "/__desktop/setup/state") return json(setup);
    if (url.pathname === "/__desktop/update-required/state") return json(mismatch);
    if (request.method === "POST") {
      let body = ""; for await (const chunk of request) body += chunk;
      if (url.pathname === "/__desktop/setup") {
        posts.push(Object.fromEntries(new URLSearchParams(body)));
        response.writeHead(303, {location: "/__desktop/setup"}); return response.end();
      }
      if (url.pathname === "/__desktop/update") {
        assert.equal(request.headers["x-tofi-connection-info"], "1");
        updates.push(JSON.parse(body)); return json({state: "downloaded", message: "Synthetic update ready"});
      }
    }
    const name = pages.find(name => url.pathname === `/__desktop/${name}`);
    if (url.pathname === "/__desktop/app-icon.png") { response.writeHead(200, {"content-type": "image/png"}); return response.end(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aC1sAAAAASUVORK5CYII=", "base64")); }
    const path = name ? join(out, `desktop-${name}.html`) : resolve(out, `.${decodeURIComponent(url.pathname)}`);
    if (!path.startsWith(out + "/") || !existsSync(path)) { response.writeHead(404); return response.end(); }
    const mime = {".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2"};
    response.writeHead(200, {"content-type": mime[extname(path)] || "application/octet-stream"}); response.end(readFileSync(path));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  browser = await launch();
  const context = await browser.newContext({locale: "en-US", viewport: {width: 800, height: 900}});
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("request", request => { if (new URL(request.url()).pathname.startsWith("/api/")) apiRequests.push(request.url()); });
  await page.addInitScript(() => {
    window.calls = []; window.mode = "native";
    const record = (name, ...args) => window.calls.push([name, ...args]);
    const selected = () => window.mode === "native" ? {mode: "native", selectionID: "selection-native", root: "/synthetic/project", name: "Synthetic project", authorized: true} : {selectionID: "selection-snapshot", name: "Synthetic snapshot", files: [{path: "main.txt", bytes: 3}], totalBytes: 3, excludedCount: 2};
    window.tofiDesktop = {
      platform: "darwin", themePreference: "system",
      appUpdate: {install: async () => record("install")},
      startAccessibilityDrag: () => record("drag"),
      getLocalRunStatus: async () => { record("status"); return {mode: window.mode === "native" ? "native" : undefined, available: true, running: false}; },
      chooseLocalRunDirectory: async () => { record("choose"); return selected(); },
      authorizeLocalRunDirectory: async id => { record("authorize", id); return selected(); },
      localRun: input => { record("run", input); return new Promise(resolve => { window.finishRun = resolve; }); },
      localRunStop: async () => { record("stop"); return true; },
      localRunSaveFile: async handle => { record("save", handle); return true; },
      onLocalRunEvent: listener => { window.emit = listener; return () => { window.emit = null; }; }
    };
  });
  const go = name => page.goto(`${origin}/__desktop/${name}`);
  const button = name => page.getByRole("button", {name, exact: true});
  await go("setup");
  await page.getByLabel("Server address", {exact: true}).fill("https://another.example");
  await button("Connect to server").click();
  await page.waitForURL(`${origin}/__desktop/setup`);
  await page.getByLabel("Server address", {exact: true}).waitFor();
  assert.deepEqual(posts[0], {connectionMode: "https", serverURL: "https://another.example"});
  await button("Connect through SSH").first().click();
  await page.getByLabel("SSH alias", {exact: true}).fill("synthetic-host");
  await page.getByLabel("Server port", {exact: true}).fill("8333");
  await page.locator('form input[value="ssh"]').locator("..").getByRole("button").click();
  await page.waitForURL(`${origin}/__desktop/setup`);
  await page.getByLabel("Server address", {exact: true}).waitFor();
  assert.deepEqual(posts[1], {connectionMode: "ssh", sshAlias: "synthetic-host", remotePort: "8333"});
  setup = {error: "Synthetic connection failure", checking: true, values: {connectionMode: "ssh", sshAlias: "saved-host", remotePort: "9000"}};
  await go("setup");
  await page.getByRole("alert").waitFor();
  assert.equal(await page.getByLabel("SSH alias", {exact: true}).inputValue(), "saved-host");
  assert.ok(await page.getByLabel("SSH alias", {exact: true}).isDisabled());
  assert.equal(await page.getByRole("button", {name: "Connecting…", exact: true}).count(), 2);

  await go("update-required");
  for (const text of ["This app", "Bundled UI contract", "Server contract", "Server requires app", "1.2.3", "2.0.0 or newer"]) await page.getByText(text, {exact: true}).waitFor();
  await button("Update").click();
  await button("Restart and install").waitFor();
  assert.deepEqual(updates, [{action: "check"}]);
  await button("Restart and install").click();
  await page.waitForFunction(() => window.calls.some(call => call[0] === "install"));
  mismatch = {...mismatch, reason: "server_too_old", serverURL: "javascript:alert(1)"};
  await go("update-required");
  await page.getByRole("heading", {name: "This Tofi server is older than this app supports"}).waitFor();
  assert.equal(await button("Update").count(), 0);
  assert.equal(await page.getByRole("link", {name: "Open in browser"}).count(), 0);

  await go("local-run");
  await button("Choose directory…").click();
  await page.getByText("/synthetic/project", {exact: true}).waitFor();
  await page.getByLabel("Shell command", {exact: true}).fill("echo synthetic");
  assert.ok(await button("Run").isDisabled(), "choose is never authorization");
  await button("Authorize this directory…").click();
  await button("Run").click();
  await page.waitForFunction(() => typeof window.finishRun === "function");
  await page.evaluate(() => {
    window.emit({type: "state", mode: "native", runID: "run-native", phase: "running"});
    window.emit({type: "output", mode: "native", runID: "run-native", stream: "stdout", text: "a".repeat(70000) + "尾"});
    window.emit({type: "output", mode: "native", runID: "wrong-run", stream: "stdout", text: "WRONG"});
    window.emit({type: "output", mode: "native", runID: "run-native", stream: "stderr", text: "synthetic stderr"});
  });
  await page.waitForFunction(() => document.querySelector('[data-stream="stderr"]').textContent === "synthetic stderr");
  assert.ok(await page.locator('[data-stream="stdout"]').evaluate(node => new TextEncoder().encode(node.textContent).length <= 65536 && node.textContent.endsWith("尾") && !node.textContent.includes("WRONG")));
  await button("Stop").click();
  await page.waitForFunction(() => window.calls.some(call => call[0] === "stop"));
  await page.evaluate(() => window.finishRun({runID: "run-native", mode: "native", effects: "direct", containment: "unverified", status: "stopped", cleanup: {scope: "managed-process-group", status: "absent"}, stdout: "final stdout", stderr: "final stderr", exitCode: 0}));
  await page.getByText("The managed process group has exited.", {exact: false}).waitFor();
  await page.waitForFunction(() => document.querySelector('[data-stream="stdout"]').textContent === "final stdout");
  const calls = await page.evaluate(() => window.calls);
  assert.deepEqual(calls.find(call => call[0] === "authorize"), ["authorize", "selection-native"]);
  assert.deepEqual(calls.find(call => call[0] === "run"), ["run", {selectionID: "selection-native", command: "echo synthetic"}]);
  await page.evaluate(() => { window.mode = "snapshot"; });
  await button("Refresh status").click();
  await button("Choose directory…").click();
  await page.getByText("main.txt", {exact: true}).waitFor();
  await page.getByLabel("Generated file paths", {exact: false}).fill("out.txt");
  await button("Run").click();
  await page.waitForFunction(() => window.calls.filter(call => call[0] === "run").length === 2);
  assert.deepEqual(await page.evaluate(() => window.calls.filter(call => call[0] === "run")[1]), ["run", {selectionID: "selection-snapshot", command: "echo synthetic", exportPaths: ["out.txt"]}]);
  await page.evaluate(() => window.finishRun({runID: "run-snapshot", status: "completed", files: [{path: "out.txt", bytes: 5, handle: "file-handle"}]}));
  await button("Save as…").click();
  await page.waitForFunction(() => window.calls.some(call => call[0] === "save" && call[1] === "file-handle"));

  await page.setViewportSize({width: 420, height: 94});
  await page.goto(`${origin}/__desktop/permission-guide?packaged=1&theme=dark`);
  await page.locator(".permission-tile").waitFor();
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  await page.locator(".permission-tile").dispatchEvent("dragstart");
  assert.deepEqual(await page.evaluate(() => window.calls), [["drag"]]);
  await page.evaluate(() => window.setPermissionGuideTheme(false));
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light");
  assert.equal(await page.locator("body").evaluate(node => getComputedStyle(node).backgroundColor), "rgba(0, 0, 0, 0)");
  for (const selector of [".permission-tile", ".permission-close"]) assert.equal(await page.locator(selector).evaluate(node => getComputedStyle(node).getPropertyValue("-webkit-app-region")), "no-drag");
  await page.evaluate(() => { window.close = () => { window.closedByTest = true; }; });
  await button("Close").click();
  assert.equal(await page.evaluate(() => window.closedByTest), true);
  await page.goto(`${origin}/__desktop/permission-guide?packaged=0&theme=light`);
  await page.getByText("Add Electron.app (development) to the permissions list").waitFor();
  // The main process can change theme before localization resolves and React mounts.
  const early = await context.newPage();
  let releaseCatalogs;
  const catalogs = new Promise(resolve => { releaseCatalogs = resolve; });
  await early.route("**/assets/en-*.js", async route => { await catalogs; await route.continue(); });
  try {
    await early.goto(`${origin}/__desktop/permission-guide?packaged=1&theme=light`, {waitUntil: "commit"});
    await early.waitForFunction(() => typeof window.setPermissionGuideTheme === "function");
    await early.evaluate(() => window.setPermissionGuideTheme(true));
  } finally { releaseCatalogs(); }
  await early.locator(".permission-tile").waitFor();
  assert.equal(await early.evaluate(() => document.documentElement.dataset.theme), "dark");
  await early.close();
  assert.deepEqual(apiRequests, []);
  assert.deepEqual(errors, []);
  await context.close();
  console.log("PASS built desktop pages: native POST, update, local IPC, streams, exports, guide drag and theme");
} finally {
  await browser?.close();
  if (server) await new Promise(resolve => server.close(resolve));
  rmSync(out, {recursive: true, force: true});
}
