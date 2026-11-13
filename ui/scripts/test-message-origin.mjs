// Exercise the real send API in a browser, with and without the Mac bridge.
import assert from "node:assert/strict";
import {mkdtemp, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const fixture = await mkdtemp(join(ui, ".message-origin-"));
let server, browser;
try {
  await writeFile(join(fixture, "index.html"), '<!doctype html><script type="module" src="./main.ts"></script>');
  await writeFile(join(fixture, "main.ts"), `import {api} from '../src/api'; window.send = () => api.sendMessage('synthetic-chat', {content:'synthetic message',client_message_id:crypto.randomUUID()});`);
  server = await createServer({configFile:false, root:ui, server:{host:"127.0.0.1",port:0}, logLevel:"error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath:process.env.TOFI_TEST_CHROME || undefined});
  for (const mode of ["mac", "web", "old-app", "unpaired"]) {
    const context = await browser.newContext();
    await context.addInitScript(mode => {
      window.originReads = 0; window.originListeners = [];
      // The Mac app's bridge as desktop.ts uses it on load; only botBrowser differs between the modes.
      const shell = {platform: "darwin", onFlushState: () => () => {}, onWindowState: () => () => {}, remoteInputFocus: () => {}, writeWorkspaceState: async () => {}};
      if (mode === "old-app") window.tofiDesktop = shell;
      if (mode === "mac" || mode === "unpaired") window.tofiDesktop = {...shell, botBrowser: {
        status: async () => {window.originReads++; return {deviceID:mode === "mac" ? "synthetic-mac" : null};},
        onStatus: listener => {window.originListeners.push(listener); return () => {};},
      }};
    }, mode);
    const page = await context.newPage();
    const posts = [];
    await page.route("**/api/**", async route => {
      if (route.request().method() === "POST") posts.push(route.request().postDataJSON());
      await route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({message:{id:"synthetic"}})});
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html`);
    await page.waitForFunction(() => typeof window.send === "function");
    await page.evaluate(() => window.send());
    assert.equal(posts.length,1);
    assert.equal(posts[0].origin_computer_id,mode === "mac" ? "synthetic-mac" : undefined);
    assert.equal(Object.hasOwn(posts[0],"origin_computer_id"),mode === "mac");
    if (mode === "mac") {
      await page.evaluate(() => window.originListeners.forEach(listener => listener({deviceID:"repaired-mac"})));
      await page.evaluate(() => window.send());
      assert.equal(posts[1].origin_computer_id,"repaired-mac");
      await page.evaluate(() => window.originListeners.forEach(listener => listener({deviceID:null})));
      await page.evaluate(() => window.send());
      assert.equal(Object.hasOwn(posts[2],"origin_computer_id"),false);
      assert.equal(await page.evaluate(() => window.originReads),1);
    }
    await context.close();
  }
  console.log("PASS message origin: Mac, web, older app, null identity, repair, one status read");
} finally {
  await browser?.close(); await server?.close(); await rm(fixture,{recursive:true,force:true});
}
