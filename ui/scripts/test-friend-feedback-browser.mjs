// Real production App and components, with synthetic HTTP responses only.
// Every request is restricted to this disposable loopback preview server.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer, preview } from "vite";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const conversationRemoval = process.argv.includes("--conversation-removal");
const output = resolve(process.env.TOFI_FEEDBACK_OUTPUT || (conversationRemoval ? "/tmp/tofi-friend-feedback-removal-evidence-20261005" : "/tmp/tofi-friend-feedback-evidence-20261005"));
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
await mkdir(output, { recursive: true });
// The focused removal regression transforms current source without rebuilding
// or rerunning the already-passing production-build acceptance scenarios.
const server = conversationRemoval
  ? await createServer({ root, server: { host: "127.0.0.1", port: 0, strictPort: false, open: false } })
  : await preview({ root, preview: { host: "127.0.0.1", port: 0, strictPort: false, open: false } });
if (conversationRemoval) await server.listen();
const origin = server.resolvedUrls.local[0].replace(/\/$/, "");
assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const stamp = "2026-10-05T00:00:00Z";
const botID = "10000000-0000-4000-8000-000000000001", dmID = "20000000-0000-4000-8000-000000000001", groupID = "30000000-0000-4000-8000-000000000001";
const results = [];
const image = "data:image/svg+xml;base64," + Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800"><rect width="1280" height="800" fill="#e5ebe9"/><rect x="80" y="60" width="1120" height="680" rx="16" fill="white"/><rect x="80" y="60" width="1120" height="54" fill="#cad9d4"/><text x="110" y="95" fill="#243d36" font-size="24">Synthetic browser workspace</text><text x="140" y="210" fill="#243d36" font-size="40">Local acceptance fixture</text></svg>').toString("base64");
try {
  for (const [layout, width, height, touch, reduce, native] of [
    ["desktop", 1280, 900, false, false, false],
    ["mobile", 390, 844, true, false, false],
    ["reduced", 1280, 900, false, true, false],
    ["native-bridge", 1280, 900, false, false, true],
  ]) {
    if (conversationRemoval && layout !== "desktop") continue;
    const context = await browser.newContext({ viewport: { width, height }, hasTouch: touch, isMobile: touch, reducedMotion: reduce ? "reduce" : "no-preference" });
    const page = await context.newPage();
    const errors = [], unexpected = [], actions = [], writes = [];
    let activeOwner = false, captureFailure = false, computerState = "ready", infoFailure = false, groupAvailable = true;
    const bot = { id: botID, name: "Fixture Cat", instructions: "Synthetic role", model: "fixture-a", reasoning_effort: "medium", dm_conversation_id: dmID, created_at: stamp };
    const chats = [{ id: dmID, kind: "dm", name: bot.name, bot_id: botID, bot_ids: [botID], updated_at: stamp }, { id: groupID, kind: "group", name: "Fixture Group", bot_ids: [botID], updated_at: stamp, user_visible: true }];
    const memory = { id: "memory-fixture", conversation_id: dmID, title: "Saved fact", description: "Synthetic memory", content: "Saved memory survives model choice.", revision: 1, created_at: stamp, updated_at: stamp };
    page.on("pageerror", error => errors.push(error.message));
    await page.addInitScript(({ native }) => {
      // No live event source or MSE stream is used in this component/UI fixture.
      window.MediaSource = undefined;
      window.EventSource = class {
        static OPEN = 1; static CLOSED = 2; readyState = 1;
        constructor() { setTimeout(() => this.onopen?.(), 0); }
        addEventListener() {} removeEventListener() {} close() { this.readyState = 2; }
      };
      if (native) window.tofiDesktop = {
        platform: "darwin", themePreference: "light", onCommand: () => () => {}, onWindowState: () => () => {}, onFlushState: () => () => {},
        readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {}, appearance: async () => {}, remoteInputFocus: () => {},
        getPermissionStatus: async () => ({ supported: false }),
      };
    }, { native });
    await context.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), path = url.pathname;
      if (url.hostname === "fonts.googleapis.com" && path === "/css2") {
        // Preserve the incumbent request but never contact a remote font host.
        return route.fulfill({ status: 200, contentType: "text/css", body: "" });
      }
      if (url.origin !== origin) { unexpected.push(request.url()); return route.abort(); }
      if (!path.startsWith("/api/")) return route.continue();
      const reply = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
      if (path === "/api/server-info") return reply({ service: "tofi", protocol_version: 1, instance_id: "40000000-0000-4000-8000-000000000001", auth: { mode: "none" } });
      if (path === "/api/preferences") return reply({ timezone: "UTC", timezone_configured: true });
      if (path === "/api/config") return reply({ model_configured: true, default_model: "fixture-a", provider: "synthetic" });
      if (path === "/api/secret-inputs") return reply({ requests: [] });
      if (path === "/api/questions") return reply({ questions: [] });
      if (path === "/api/mail-drafts") return reply({ drafts: [] });
      if (path === "/api/bots") return reply({ bots: [bot] });
      if (path === "/api/conversations") return reply({ conversations: chats.filter(chat => groupAvailable || chat.id !== groupID) });
      if (path === "/api/computer/desktop-ownership") return reply({ owner: activeOwner ? { kind: "bot", bot_id: botID, run_id: "synthetic-run", conversation_id: dmID } : null, waiting: [] });
      if (path === "/api/computers/firecracker/info") return infoFailure ? reply({ error: { code: "unavailable", message: "Synthetic outage" } }, 503) : reply({ kind: "synthetic", state: computerState });
      if (path === "/api/computers/firecracker/actions") {
        const data = request.postDataJSON(); actions.push(data);
        if (data.action === "desktop.capture") return captureFailure ? reply({ error: { code: "computer_busy", message: "Synthetic busy" } }, 409) : reply({ ok: true, result: { image_url: image } });
        if (data.action === "desktop.control.acquire") return reply({ ok: true, result: { control_id: "synthetic-lease" } });
        if (data.action === "desktop.control.release") return reply({ ok: true, result: {} });
        if (data.action === "desktop.control.input" || data.action === "desktop.control.renew") return reply({ ok: true, result: {} });
        unexpected.push(data.action); return reply({ ok: false, error: "Unexpected action" }, 400);
      }
      if (path === `/api/bots/${botID}` && request.method() === "PATCH") { const data = request.postDataJSON(); writes.push(data); Object.assign(bot, data); return reply(bot); }
      if (path === "/api/models") return reply({ source: "fixture", models: ["fixture-a", "fixture-b"].map(id => ({ id, name: id, reasoning_efforts: ["medium"], default_reasoning: "medium" })) });
      if (/\/messages$/.test(path)) return reply({ messages: [{ id: "synthetic-message", conversation_id: path.split("/")[3], seq: 1, role: "assistant", content: "Saved chat history.", created_at: stamp }], drafts: [], has_more: false, event_cursor: 0 });
      if (/\/runs$/.test(path)) return reply({ runs: [] });
      if (/\/memories$/.test(path)) return reply({ memories: [{ ...memory, conversation_id: path.split("/")[3] }] });
      if (/\/tools$/.test(path)) return reply({ activities: [], summaries: [] });
      if (/\/read$/.test(path)) return reply({ read_seq: 1 });
      if (/\/schedules$/.test(path)) return reply({ schedules: [] });
      if (/\/work-items$/.test(path)) return reply({ work_items: [] });
      if (/\/inspector$/.test(path)) return reply({});
      unexpected.push(`${request.method()} ${path}`); return reply({ error: { message: "Unexpected fixture request" } }, 404);
    });
    const record = name => { results.push({ layout, name, pass: true }); console.log(`${layout}: ${name} PASS`); };
    const noControl = () => assert.equal(actions.some(item => item.action.includes("control.acquire") || item.action === "desktop.stop" || item.action === "desktop.start"), false);
    const launcher = () => page.locator(".desktop-presence-launcher");
    const dialog = () => page.locator('.desktop-floating-shell[role="dialog"]');
    const capture = async (name, selector) => {
      // Capture the settled surface, excluding indefinite avatar animations.
      await page.locator(selector).evaluate(async element => {
        const finite = element.getAnimations({ subtree: true }).filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime ?? Infinity));
        await Promise.all(finite.map(animation => animation.finished.catch(() => {})));
      });
      await page.screenshot({ path: join(output, `${layout}-${name}.png`) });
    };
    if (conversationRemoval) {
      await page.goto(`${origin}/g/${groupID}`);
      await page.getByRole("button", { name: "打开共享电脑", exact: true }).click();
      await dialog().waitFor();
      await page.locator(".desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
      assert.equal(await page.locator('.chat-pane[inert]').count(), 1);
      assert.equal(await page.locator('.contact-pane[inert]').count(), 1);

      // Another client's deletion is delivered by the normal index refresh.
      groupAvailable = false;
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      await page.getByRole("heading", { name: "无法打开这个会话", exact: true }).waitFor();
      await dialog().waitFor({ state: "detached" });
      const state = await page.evaluate(() => ({
        desktopCount: document.querySelectorAll(".desktop-floating-shell").length,
        chatInert: Boolean(document.querySelector(".chat-pane")?.inert),
        sidebarInert: Boolean(document.querySelector(".contact-pane")?.inert),
        routeNotice: document.querySelector(".conversation-empty")?.textContent,
      }));
      assert.equal(state.desktopCount, 0);
      assert.equal(state.chatInert, false);
      assert.equal(state.sidebarInert, false);
      const recover = page.getByRole("button", { name: "返回消息列表", exact: true });
      await recover.focus();
      assert.equal(await recover.evaluate(element => element === document.activeElement), true);
      await capture("removed-active-conversation", ".conversation-empty");
      await recover.click();
      assert.equal(await page.locator(".contact-pane.open").count(), 1);

      // Returning data cannot revive a presentation the deletion invalidated.
      groupAvailable = true;
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
      const openDesktop = page.getByRole("button", { name: "打开共享电脑", exact: true });
      await openDesktop.waitFor();
      assert.equal(await openDesktop.getAttribute("aria-pressed"), "false");
      assert.equal(await dialog().count(), 0);
      noControl();
      assert.deepEqual(errors, []); assert.deepEqual(unexpected, []);
      await writeFile(join(output, "transition-result.json"), JSON.stringify({ state, recoveredWithoutReopening: true, unexpected, errors }, null, 2));
      record("removed-active-group-unlocks-recovery-and-clears-viewer");
      await context.close();
      continue;
    }
    await page.goto(`${origin}/b/${botID}`);
    await page.locator(".message-content").filter({ hasText: "Saved chat history." }).waitFor();
    const composer = page.getByRole("textbox", { name: `发送给 ${bot.name}` });
    await composer.fill("Unsent draft"); await composer.focus();
    activeOwner = true;
    await launcher().waitFor();
    assert.equal(await composer.evaluate(element => element === document.activeElement), true);
    assert.equal(await page.locator(".computer-detail").count(), 0);
    assert.equal((await launcher().boundingBox()).width, 48);
    noControl(); record("activity-icon-no-focus-or-viewer");
    if (!touch) {
      await launcher().hover();
      await page.locator(".desktop-presence-peek .desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
      assert.equal(await composer.evaluate(element => element === document.activeElement), true);
      assert.equal(await page.locator('.desktop-presence-peek [inert]').count(), 1);
      assert.equal(await page.locator('.desktop-presence-peek [role="switch"]').count(), 0);
      noControl(); record("hover-is-passive");
      await capture("preview", ".desktop-presence-peek");
      if (reduce) assert.equal(await page.locator(".desktop-presence-peek").evaluate(element => getComputedStyle(element).animationName), "none");
      await page.mouse.move(30, 40); await composer.focus();
      await page.locator(".desktop-presence-peek").waitFor({ state: "detached" });
      await launcher().focus();
      await page.locator(".desktop-presence-peek").waitFor();
      await page.keyboard.press("Escape");
      await page.locator(".desktop-presence-peek").waitFor({ state: "detached" });
      assert.equal(await launcher().evaluate(element => element === document.activeElement), true);
      noControl(); record("keyboard-preview-escape");
      await composer.focus();
      await launcher().hover();
      await page.locator(".desktop-presence-peek-hint").click();
      await dialog().waitFor();
      noControl();
      await page.keyboard.press("Escape");
      await dialog().waitFor({ state: "detached" });
      await page.waitForFunction(() => document.activeElement?.closest(".composer"));
      record("preview-click-opens-without-control-and-restores-composer");
      await page.mouse.move(30, 40);
      if (layout === "desktop") {
        computerState = "stopped";
        await launcher().focus();
        await page.locator(".desktop-presence-peek .desktop-bot-starting").waitFor();
        await page.waitForTimeout(1700);
        noControl(); assert.deepEqual(unexpected, []);
        record("passive-preview-never-boots-stopped-machine");
        computerState = "ready";
        await page.locator(".desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
      }
    } else {
      await launcher().focus();
      assert.equal(await page.locator(".desktop-presence-peek").count(), 0);
      record("mobile-focus-no-hover-preview");
    }
    if (touch) await launcher().tap(); else await launcher().press("Enter");
    await dialog().waitFor();
    await page.locator(".desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
    const canvasBounds = await page.locator(".computer-detail.is-expanded").boundingBox();
    assert.ok(canvasBounds.x >= -1 && canvasBounds.x + canvasBounds.width <= width + 1, `${layout} expanded canvas must fit the viewport`);
    noControl(); record("explicit-open-is-expanded-observation");
    assert.equal(await page.locator('.chat-pane[inert]').count(), 1);
    await page.keyboard.press("Shift+Tab");
    assert.equal(await dialog().evaluate(element => element.contains(document.activeElement)), true);
    await page.keyboard.press("Tab");
    assert.equal(await dialog().evaluate(element => element.contains(document.activeElement)), true);
    record("dialog-traps-focus");
    await page.keyboard.press("Escape");
    await dialog().waitFor({ state: "detached" });
    await page.waitForFunction(() => document.activeElement?.classList.contains("desktop-presence-launcher"));
    assert.equal(await page.locator(".desktop-presence-peek").count(), 0);
    record("escape-restores-icon-without-preview-reopening");
    if (touch) await launcher().tap(); else await launcher().press("Enter");
    await dialog().waitFor();
    // Keeping the pointer/focus elsewhere cannot fold a user-opened view.
    activeOwner = false;
    await page.waitForTimeout(1150);
    assert.equal(await dialog().count(), 1);
    noControl(); record("explicit-open-persists-after-owner-completes");
    await capture("expanded", ".computer-detail.is-expanded");
    captureFailure = true;
    await page.locator(".desktop-frame-status.is-stale").waitFor({ timeout: 9000 });
    assert.match(await page.locator(".desktop-frame-status").innerText(), /非实时/);
    assert.equal(await page.locator(".computer-screen").count(), 1);
    record("retained-stale-frame-visibly-not-live");
    await capture("stale", ".computer-detail.is-expanded");
    captureFailure = false;
    infoFailure = true;
    await page.locator(".error-banner").filter({ hasText: "连接暂时不可用" }).waitFor();
    assert.equal(await dialog().count(), 1);
    assert.match(await page.locator(".desktop-frame-status").innerText(), /非实时/);
    infoFailure = false;
    await page.locator(".desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
    noControl(); record("connection-outage-keeps-explicit-view-and-recovers");
    await page.keyboard.press("Escape");
    await dialog().waitFor({ state: "detached" });
    await page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "打开共享电脑");
    noControl(); record("escape-restores-header-focus");
    await page.getByRole("button", { name: "打开共享电脑", exact: true }).click();
    await dialog().waitFor();
    await page.getByRole("button", { name: "隐藏共享电脑", exact: true }).click();
    await dialog().waitFor({ state: "detached" });
    noControl(); record("hide-leaves-vm-running");
    const menu = async label => { await page.getByRole("button", { name: "更多操作", exact: true }).click(); await page.getByRole("button", { name: label, exact: true }).click(); };
    await menu("这个 Bot 的记忆");
    await page.getByRole("heading", { name: "这个 Bot 的记忆", exact: true }).waitFor();
    assert.match(await page.locator(".detail-content").filter({ has: page.getByRole("heading", { name: "这个 Bot 的记忆", exact: true }) }).innerText(), /不同模型/);
    await page.getByRole("button", { name: "关闭记忆", exact: true }).click();
    await menu("Bot 设置");
    await page.getByRole("button", { name: /名称、职能与模型/ }).click();
    await page.getByText("模型与思考", { exact: true }).click();
    await page.locator(".model-fields select").first().selectOption("fixture-b");
    assert.match(await page.locator(".model-fields").innerText(), /保留已保存的记忆和聊天记录/);
    const saved = page.waitForResponse(response => response.request().method() === "PATCH" && new URL(response.url()).pathname === `/api/bots/${botID}`);
    await page.getByRole("button", { name: "保存", exact: true }).click();
    assert.equal((await saved).status(), 200);
    assert.equal(writes.length, 1); assert.equal(writes[0].model, "fixture-b");
    await page.locator(".bot-v2-panel").waitFor({ state: "detached" });
    assert.equal(await page.locator(".message-content").filter({ hasText: "Saved chat history." }).count(), 1);
    assert.equal(await composer.inputValue(), "Unsent draft");
    await menu("这个 Bot 的记忆");
    assert.equal(await page.locator(".memory-title").innerText(), memory.title);
    assert.equal(await page.locator(".memory-details p").textContent(), memory.content);
    record("model-change-keeps-ui-chat-draft-and-scoped-memory");
    await page.getByRole("button", { name: "关闭记忆", exact: true }).click();
    await page.goto(`${origin}/g/${groupID}`);
    await page.locator(".chat-header h1").filter({ hasText: "Fixture Group" }).waitFor();
    await menu("本群共享记忆");
    await page.getByRole("heading", { name: "本群共享记忆", exact: true }).waitFor();
    assert.match(await page.locator(".detail-content").filter({ has: page.getByRole("heading", { name: "本群共享记忆", exact: true }) }).innerText(), /本群成员共享/);
    await capture("group-memory", ".detail-pane.visible");
    record("group-memory-accurate-scope");
    await page.getByRole("button", { name: "关闭记忆", exact: true }).click();
    await page.getByRole("button", { name: "打开共享电脑", exact: true }).click();
    await dialog().waitFor();
    await page.locator(".desktop-frame-status").filter({ hasText: "截图查看" }).waitFor();
    noControl();
    const takeoverStart = actions.length;
    await page.locator(".remote-desktop-surface").click();
    await page.locator(".remote-desktop.in-control").waitFor();
    if (touch) await page.getByRole("button", { name: "打开远程键盘", exact: true }).click();
    await page.getByRole("textbox", { name: "远程桌面键盘输入", exact: true }).focus();
    const released = page.waitForResponse(response => {
      const request = response.request();
      return request.method() === "POST" && new URL(response.url()).pathname.endsWith("/actions") && request.postDataJSON().action === "desktop.control.release";
    });
    await page.keyboard.press("Escape");
    await dialog().waitFor({ state: "detached" });
    assert.equal((await released).status(), 200);
    assert.equal(actions.slice(takeoverStart).filter(item => item.action === "desktop.control.acquire").length, 1);
    assert.equal(actions.some(item => item.action === "desktop.stop" || item.action === "desktop.start"), false);
    assert.equal(actions.slice(takeoverStart).some(item => item.args?.events?.some(event => event.key === "Escape")), false);
    record("explicit-takeover-escape-releases-control-without-vm-stop");
    assert.deepEqual(errors, []); assert.deepEqual(unexpected, []);
    await context.close();
  }
  await writeFile(join(output, "results.json"), JSON.stringify({ browser: "Chromium (installed Chrome)", browserVersion: browser.version(), synthetic: true, liveMediaSource: false, externalFonts: "suppressed", results }, null, 2));
  console.log(`friend feedback browser checks: PASS (${results.length} ${conversationRemoval ? "focused conversation-removal regression" : "scenarios across desktop, mobile touch emulation, reduced motion and synthetic native bridge"}; ${output})`);
} finally {
  await browser.close();
  if (conversationRemoval) await server.close();
  else await new Promise(resolve => server.httpServer.close(resolve));
}
