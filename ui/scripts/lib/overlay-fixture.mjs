// Synthetic workspace for the menu and popover checks (scripts/test-overlays.mjs): builds the production bundle (or uses
// `dist`), serves it, and opens a page whose /api is answered in the page. Two Bots, their DMs and a group ("Ops Room")
// whose last own message was read by both Bots; one connected MCP server for the Settings > Connections menu.
// Synthetic data only.
import http from "node:http";
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

export async function createOverlayFixture({ engineName = "chromium", dist } = {}) {
  const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
  const engine = engineName === "webkit" ? pw.webkit : pw.chromium;
  const ui = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
  const own = !dist;
  const outDir = dist || mkdtempSync(join(tmpdir(), "tofi-overlays-"));
  if (own) await build({ configFile: false, root: ui, plugins: [react()], build: { outDir, emptyOutDir: true }, logLevel: "error" });
  const mime = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".json": "application/json", ".woff2": "font/woff2", ".png": "image/png" };
  const delays = { pattern: null, ms: 0 };
  const server = http.createServer((req, res) => {
    const path = new URL(req.url, "http://x").pathname;
    let file = join(outDir, path);
    if (!existsSync(file) || statSync(file).isDirectory()) file = join(outDir, "index.html");
    const send = () => { res.writeHead(200, { "content-type": mime[extname(file)] || "application/octet-stream" }); res.end(readFileSync(file)); };
    if (delays.pattern && delays.pattern.test(path)) setTimeout(send, delays.ms); else send();
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await engine.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

  const T0 = Date.parse("2026-10-13T10:00:00Z");
  const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
  const bots = ["a", "b"].map((k, i) => ({ id: `bot_${k}`, name: ["Ledger Cat", "Simile Cat"][i], instructions: "Synthetic role that keeps the weekly ledger tidy.", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: `dm_${k}`, created_at: iso(0) }));
  const convs = [
    ...["a", "b"].map((k, i) => ({ id: `dm_${k}`, kind: "dm", name: bots[i].name, bot_id: `bot_${k}`, bot_ids: [`bot_${k}`], updated_at: iso(100 - i), read_seq: 99, user_visible: true })),
    { id: "grp", kind: "group", name: "Ops Room", bot_ids: ["bot_a", "bot_b"], updated_at: iso(90), read_seq: 99, user_visible: true },
  ];
  const msg = (conv, seq, role, content, extra = {}) => ({ id: `${conv}_m${seq}`, conversation_id: conv, seq, role, content, created_at: iso(seq * 30), ...extra });
  const history = conv => {
    if (conv === "grp") return [
      msg("grp", 1, "assistant", "Group opener with the weekly totals.", { sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: "run_g1" }),
      msg("grp", 2, "assistant", "Second note from the other cat.", { sender_bot_id: "bot_b", sender_bot_name: "Simile Cat", run_id: "run_g2" }),
      msg("grp", 3, "user", "Group hello, please check the ledger.", { client_message_id: "c-grp" }),
    ];
    return Array.from({ length: 12 }, (_, i) => {
      const seq = i + 1, user = seq % 3 === 1;
      return msg(conv, seq, user ? "user" : "assistant", user ? `Question ${seq}: can you check the weekly ledger?` : `Synthetic answer ${seq} totals the weekly entries against the ledger.`, user ? {} : { sender_bot_id: conv === "dm_a" ? "bot_a" : "bot_b", sender_bot_name: conv === "dm_a" ? "Ledger Cat" : "Simile Cat", run_id: `run_${conv}_${seq}` });
    });
  };
  const runsFor = conv => conv === "grp"
    ? ["bot_a", "bot_b"].map((bot, i) => ({ id: `run_grp_${bot}`, conversation_id: "grp", bot_id: bot, status: "done", model: "synthetic-model", origin_conversation_id: "grp", trigger_message_id: "grp_m3", queue_seq: i + 1, created_at: iso(100), updated_at: iso(101) }))
    : [];

  async function open({ width = 1280, height = 860, path = "/b/bot_a", reduced = false, touch = false, waitFor = ".message-block", renders = false } = {}) {
    const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", reducedMotion: reduced ? "reduce" : "no-preference", hasTouch: touch, isMobile: touch && width < 600, deviceScaleFactor: 1 });
    // renders: a minimal React DevTools hook that records, per commit, whether the Workspace component (the function component
    // above <div class="workspace ...">) rendered in it (React's PerformedWork flag, bit 1). Test-only; production React talks
    // to whatever hook is installed before it loads.
    if (renders) await context.addInitScript(() => {
      window.__workspaceRenders = 0;
      const find = fiber => { const stack = [fiber]; while (stack.length) { const f = stack.pop(); if (!f) continue; const cls = f.tag === 5 && f.memoizedProps?.className; if (typeof cls === "string" && cls.split(" ")[0] === "workspace") { let w = f.return; while (w && ![0, 11, 14, 15].includes(w.tag)) w = w.return; return w; } stack.push(f.sibling, f.child); } return null; };
      window.__REACT_DEVTOOLS_GLOBAL_HOOK__ = { supportsFiber: true, isDisabled: false, renderers: new Map(), inject() { return 1; }, checkDCE() {}, onScheduleFiberRoot() {}, onCommitFiberUnmount() {}, onPostCommitFiberRoot() {},
        onCommitFiberRoot(_id, root) { const w = find(root.current); if (w && (w.flags & 1)) window.__workspaceRenders++; } };
    });
    await context.addInitScript(() => {
      try { localStorage.setItem("tofi:appearance", "light"); } catch {}
      window.EventSource = class { constructor(url) { this.url = url; setTimeout(() => this.onopen?.(), 0); } addEventListener() {} close() {} };
      window.__clip = null;
      try { Object.defineProperty(navigator, "clipboard", { value: { writeText: async text => { window.__clip = text; } }, configurable: true }); } catch {}
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/api/**", async route => {
      const url = new URL(route.request().url());
      const p = url.pathname;
      const json = body => route.fulfill({ status: 200, json: body });
      if (p.endsWith("/events") || p.includes("/events/")) return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream" }, body: ": synthetic\n\n" });
      if (p === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-0000000000aa", auth: { mode: "none" } });
      if (p === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
      if (p === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
      if (p === "/api/bots") return json({ bots });
      if (p === "/api/conversations") return json({ conversations: convs });
      const conv = p.split("/")[3] ?? "";
      if (p.endsWith("/messages")) return json({ messages: history(conv), drafts: [], has_more: false, event_cursor: 0 });
      if (p.endsWith("/runs")) return json({ runs: runsFor(conv) });
      if (p.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
      if (p.endsWith("/memories")) return json({ memories: [] });
      if (p.endsWith("/schedule-occurrences")) return json({ occurrences: [] });
      if (p.endsWith("/schedules")) return json({ schedules: [] });
      if (p === "/api/extensions/mcp") return json({ servers: [{ name: "acme_tracker", url: "https://mcp.example.test/tracker", headers: {}, status: { checked_at: iso(0), ok: true, tool_count: 4 } }] });
      if (/^\/api\/extensions\/mcp\/[^/]+\/tools$/.test(p)) return json({ tools: [], read_only: 0, write: 0, high_risk: 0 });
      if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(p)) return json({ ok: true, tool_count: 4 });
      if (p === "/api/extensions/oauth-options") return json({ vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: "" });
      if (p === "/api/extensions/local-mcp") return json({ available: false, reason: "synthetic" });
      if (p === "/api/extensions/skills") return json({ skills: [] });
      if (p.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
      if (p === "/api/questions") return json({ questions: [] });
      if (p === "/api/mail-drafts") return json({ drafts: [] });
      return json({});
    });
    await page.goto(origin + path);
    if (waitFor) await page.locator(waitFor).first().waitFor();
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(500);
    return { page, context, errors };
  }
  const close = async () => { await browser.close(); server.close(); if (own) rmSync(outDir, { recursive: true, force: true }); };
  return { pw, browser, origin, open, close, outDir, delays };
}
