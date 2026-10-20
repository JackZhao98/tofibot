// Idle cat animation budget: N mounted small cats must share one animation-frame pump and step at ~24 fps,
// instead of one 60 fps rAF loop (full SVG path recompute) per cat. Measured in real Chromium.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:avatar-frame-budget
import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await pw.chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
try {
  const page = await browser.newPage({ viewport: { width: 800, height: 600 } });
  await page.route("**/blank.html", route => route.fulfill({ status: 200, contentType: "text/html", body: "<!doctype html><title>blank</title><body></body>" }));
  await page.goto(`${origin}/blank.html`);
  const result = await page.evaluate(async () => {
    let registered = 0;
    const raf = window.requestAnimationFrame.bind(window);
    window.requestAnimationFrame = (fn) => { registered++; return raf(fn); };
    const { mountCat } = await import("/src/lib/tofi-avatar/index.js");
    const root = document.createElement("div");
    root.style.cssText = "position:fixed;left:0;top:0";
    document.body.append(root);
    const hosts = [];
    for (let i = 0; i < 20; i++) {
      const host = document.createElement("span");
      host.style.cssText = "display:inline-block;width:25px;height:25px";
      root.append(host);
      hosts.push(host);
    }
    const cats = hosts.map(host => mountCat(host, { initialState: "awake" }));
    let steps = 0;
    const observer = new MutationObserver(records => { for (const r of records) if (r.target === hosts[0].querySelector("path")) steps++; });
    observer.observe(hosts[0], { attributes: true, attributeFilter: ["d"], subtree: true });
    await new Promise(r => setTimeout(r, 300));
    registered = 0; steps = 0;
    const t0 = performance.now();
    await new Promise(r => setTimeout(r, 2000));
    const seconds = (performance.now() - t0) / 1000;
    cats.forEach(cat => cat.destroy());
    return { rafPerSecond: registered / seconds, firstPathUpdatesPerSecond: steps / seconds };
  });
  console.log(JSON.stringify(result));
  assert.ok(result.rafPerSecond <= 75, `20 cats must share one frame pump, got ${result.rafPerSecond.toFixed(0)} rAF/s`);
  assert.ok(result.firstPathUpdatesPerSecond <= 30, `a 25 px cat must step at <= ~24 fps, got ${result.firstPathUpdatesPerSecond.toFixed(1)}/s`);
  console.log("avatar frame budget: PASS");
} finally {
  await browser.close();
  await server.close();
}
