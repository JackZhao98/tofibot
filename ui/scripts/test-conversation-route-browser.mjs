// Built production App + real disposable AccountGateway HTTP/SSE/SQLite.
// Start TestConversationRouteBrowserFixture first; no live account is used.
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const fixture = JSON.parse(readFileSync(process.env.TOFI_ROUTE_BROWSER_MANIFEST, "utf8"));
const { origin, accounts, password } = fixture;
assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/, "only disposable loopback fixtures are allowed");
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const output = resolve(process.env.TOFI_ROUTE_BROWSER_OUTPUT || "../docs/acceptance/conversation-routing");
mkdirSync(output, { recursive: true });
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const results = [];
const state = async () => {
  const response = await fetch(origin + "/acceptance/state");
  assert.equal(response.status, 200);
  return response.json();
};
const taskCounts = value => ({ engine_calls: value.engine_calls, tasks: value.counts.map(({ runs, schedules, work_items }) => ({ runs, schedules, work_items })) });
const initial = taskCounts(await state());

try {
  for (const [layout, width, height, native] of [["desktop", 1280, 900, false], ["narrow", 390, 844, false], ["native-bridge", 1280, 900, true]]) {
    const context = await browser.newContext({ viewport: { width, height }, permissions: ["clipboard-read", "clipboard-write"] });
    const [alpha, bravo] = accounts;
    const [one, two, archived, deleted] = alpha.bots;
    const page = await context.newPage();
    const errors = [], requests = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("request", request => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/")) requests.push({ path: url.pathname + url.search, method: request.method() });
    });
    await context.addInitScript(({ native, savedID }) => {
      window.routeNotifications = [];
      window.Notification = class {
        static permission = "granted";
        static async requestPermission() { return "granted"; }
        constructor(title) { this.title = title; window.routeNotifications.push(this); }
        close() { this.closed = true; }
      };
      if (!native) return;
      let state = JSON.parse(sessionStorage.getItem("synthetic-native-state") || "null") || { activeConversation: savedID };
      window.nativeWrites = [];
      window.tofiDesktop = {
        platform: "darwin", themePreference: "light",
        onCommand: () => () => {}, onWindowState: () => () => {}, onFlushState: () => () => {},
        readWorkspaceState: async () => state,
        writeWorkspaceState: async (instance, next) => { state = next; sessionStorage.setItem("synthetic-native-state", JSON.stringify(next)); window.nativeWrites.push({ instance, state: next }); },
        appearance: async () => {}, remoteInputFocus: () => {},
        getPermissionStatus: async () => ({ supported: false, microphone: "unknown", screen: "unknown", accessibility: false }),
        requestMicrophoneAccess: async () => false,
      };
    }, { native, savedID: one.dm_conversation_id });

    const login = async account => {
      await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
      await page.getByLabel("用户名或邮箱").fill(account.username);
      await page.getByLabel("密码", { exact: true }).fill(password);
      await page.getByRole("button", { name: "登录", exact: true }).click();
    };
    const chat = async (path, name, history) => {
      await page.waitForURL(origin + path);
      await page.locator(".chat-header h1").filter({ hasText: name }).waitFor();
      if (history) await page.locator(".message-content").filter({ hasText: history }).first().waitFor();
      assert.equal(await page.locator(".chat-header h1").innerText(), name);
      assert.equal(await page.locator(".chat-pane").isVisible(), true);
    };
    const record = async label => {
      assert.deepEqual(errors, [], errors.join("\n"));
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), label + " layout overflow");
      assert.deepEqual(taskCounts(await state()), initial, label + " must create zero work");
      results.push({ layout, label, path: new URL(page.url()).pathname, pass: true });
    };
    const sidebar = async name => {
      if (width < 600 && !await page.locator(".contact-pane").isVisible()) await page.getByRole("button", { name: "返回消息列表", exact: true }).click();
      await page.locator(".conversation-row").filter({ has: page.locator("strong", { hasText: name }) }).click();
    };
    const unavailable = async path => {
      const start = requests.length;
      await page.goto(origin + path);
      await page.getByRole("heading", { name: "无法打开这个会话" }).waitFor();
      assert.equal(new URL(page.url()).pathname, path);
      assert.equal(await page.locator(".chat-header").count(), 0);
      assert.equal(await page.locator(".composer").count(), 0);
      assert.equal(requests.slice(start).filter(request => request.path.startsWith("/api/conversations/")).length, 0, "unavailable IDs must never fetch a chat snapshot/SSE");
    };

    // Explicit target survives the real login form and beats native last choice.
    await page.goto(`${origin}/b/${two.id}`);
    await page.getByRole("heading", { name: "欢迎回来" }).waitFor();
    assert.equal(requests.some(request => request.path.startsWith("/api/bots") || request.path.startsWith("/api/conversations")), false, "login gate must protect the index");
    await login(alpha);
    await chat(`/b/${two.id}`, two.name, alpha.username + " HISTORY Two");
    assert.equal(requests.some(request => request.path.includes(`/conversations/${one.dm_conversation_id}/messages`)), false, "explicit link must not load default/native fallback first");
    await record("direct-link-login-return-native-precedence");

    const composer = () => page.getByRole("textbox", { name: "发送给 " + two.name });
    await composer().fill("SYNTHETIC UNSENT BOT DRAFT");
    await page.reload();
    await chat(`/b/${two.id}`, two.name, alpha.username + " HISTORY Two");
    assert.equal(await composer().inputValue(), "SYNTHETIC UNSENT BOT DRAFT");
    await record("reload-retains-target-history-and-draft");

    await sidebar(one.name);
    await chat(`/b/${one.id}`, one.name, alpha.username + " HISTORY One");
    const before = await page.evaluate(() => history.length);
    await sidebar(one.name);
    assert.equal(await page.evaluate(() => history.length), before, "same selection must not add history");
    await sidebar(alpha.group.name);
    await chat(`/g/${alpha.group.id}`, alpha.group.name, alpha.username + " GROUP HISTORY");
    const groupComposer = () => page.getByRole("textbox", { name: "发送给 " + alpha.group.name });
    await groupComposer().fill("SYNTHETIC UNSENT GROUP DRAFT");
    await page.goBack();
    await chat(`/b/${one.id}`, one.name);
    await page.goBack();
    await chat(`/b/${two.id}`, two.name);
    assert.equal(await composer().inputValue(), "SYNTHETIC UNSENT BOT DRAFT");
    await page.goForward();
    await chat(`/b/${one.id}`, one.name);
    await page.goForward();
    await chat(`/g/${alpha.group.id}`, alpha.group.name);
    assert.equal(await groupComposer().inputValue(), "SYNTHETIC UNSENT GROUP DRAFT");
    await page.reload();
    await chat(`/g/${alpha.group.id}`, alpha.group.name, alpha.username + " GROUP HISTORY");
    assert.equal(await groupComposer().inputValue(), "SYNTHETIC UNSENT GROUP DRAFT");
    await record("sidebar-repeat-back-forward-group-reload-drafts");
    await page.screenshot({ path: `${output}/${layout}-group.png` });

    await page.getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("button", { name: "复制会话链接", exact: true }).click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), `${origin}/g/${alpha.group.id}`);
    const copied = await context.newPage();
    await copied.goto(`${origin}/g/${alpha.group.id}`);
    await copied.locator(".chat-header h1").waitFor();
    assert.equal(await copied.locator(".chat-header h1").innerText(), alpha.group.name);
    await copied.close();
    await record("copy-link-opens-authorized-group-in-new-tab");

    for (const [label, path] of [
      ["missing-bot", "/b/00000000-0000-0000-0000-000000000000"],
      ["deleted-bot", `/b/${deleted.id}`], ["internal-group", `/g/${alpha.internal}`],
      ["malformed-id", "/b/%25"], ["wrong-kind", `/g/${one.dm_conversation_id}`],
      ["cross-account-bot", `/b/${bravo.bots[0].id}`], ["cross-account-group", `/g/${bravo.group.id}`],
    ]) {
      await unavailable(path);
      assert.equal((await page.locator("body").innerText()).includes(bravo.username), false, "other account content must not render");
      assert.equal(requests.some(request => request.path.includes(bravo.bots[0].dm_conversation_id) || request.path.includes(bravo.group.id)), false, "other account IDs must not reach workspace API");
      await record(label);
    }
    await page.screenshot({ path: `${output}/${layout}-unavailable.png` });
    await page.getByRole("button", { name: "返回消息列表", exact: true }).click();
    await sidebar(one.name);
    await chat(`/b/${one.id}`, one.name);
    await record("unavailable-recovery-by-sidebar");

    // Real assistant message causes the existing notification path to refresh.
    await page.request.post(origin + "/acceptance/notification");
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await page.waitForFunction(() => window.routeNotifications.some(notice => notice.title.endsWith("Two")));
    await page.evaluate(() => window.routeNotifications.findLast(notice => notice.title.endsWith("Two")).onclick());
    await chat(`/b/${two.id}`, two.name, "SYNTHETIC NOTIFICATION");
    assert.equal(await composer().inputValue(), "SYNTHETIC UNSENT BOT DRAFT");
    await page.reload();
    await chat(`/b/${two.id}`, two.name, "SYNTHETIC NOTIFICATION");
    await record("notification-selection-reload-retains-url-and-draft");

    await page.goto(`${origin}/b/${archived.id}`);
    await chat(`/b/${archived.id}`, archived.name, alpha.username + " HISTORY Archived");
    assert.equal(await page.getByRole("textbox", { name: "发送给 " + archived.name }).isDisabled(), true);
    await record("archived-direct-link-is-read-only");

    // Logout while a direct link is open, then sign into the other account.
    await page.request.post(origin + "/api/auth/logout", { data: {} });
    await page.reload();
    await login(bravo);
    await page.getByRole("heading", { name: "无法打开这个会话" }).waitFor();
    assert.equal(new URL(page.url()).pathname, `/b/${archived.id}`);
    assert.equal((await page.locator("body").innerText()).includes(alpha.username), false);
    assert.equal(await page.locator("textarea").count(), 0);
    await record("same-tab-account-switch-keeps-target-unavailable-no-leak");

    // Native root still accepts a saved selection only if it belongs to this index.
    await page.goto(origin + "/");
    await page.waitForURL(/\/[bg]\/[^/]+$/);
    assert.equal((await page.locator("body").innerText()).includes(alpha.username), false);
    await record("root-default-validates-native-saved-selection-in-current-account");
    const writes = requests.filter(request => !["GET", "HEAD"].includes(request.method) && !request.path.startsWith("/api/auth/") && !request.path.endsWith("/read"));
    assert.deepEqual(writes, [], "opening links and history must perform no execution/create/update/delete writes");

    // Deliberate synthetic creation/deletion also uses the same navigation path.
    // These are explicit UI actions after the zero-write navigation assertions.
    await page.getByRole("button", { name: "新建 Bot 或群", exact: true }).click();
    await page.getByRole("button", { name: "新建 Bot", exact: true }).click();
    await page.waitForURL(/\/b\/[^/]+$/);
    await page.waitForFunction(previous => location.pathname !== previous, new URL(results.at(-1).path, origin).pathname);
    const newBotPath = new URL(page.url()).pathname;
    await page.locator(".chat-header h1").waitFor();
    const newBotName = await page.locator(".chat-header h1").innerText();
    await page.reload();
    await chat(newBotPath, newBotName);
    await record("created-bot-selection-and-reload");
    if (width < 600) await page.getByRole("button", { name: "返回消息列表", exact: true }).click();
    await page.getByRole("button", { name: "新建 Bot 或群", exact: true }).click();
    await page.getByRole("button", { name: "新建群", exact: true }).click();
    const createdName = layout + " Created Group";
    await page.getByPlaceholder("群名称").fill(createdName);
    for (const bot of bravo.bots.slice(0, 2)) await page.locator(".member-option").filter({ hasText: bot.name }).getByRole("checkbox").check();
    await page.getByRole("button", { name: "创建群", exact: true }).click();
    await page.waitForURL(/\/g\/[^/]+$/);
    const createdPath = new URL(page.url()).pathname;
    await chat(createdPath, createdName);
    await page.reload();
    await chat(createdPath, createdName);
    await record("created-group-selection-and-reload");
    await page.getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("button", { name: "删除群聊", exact: true }).click();
    await page.getByRole("alertdialog").getByRole("button", { name: "删除群聊", exact: true }).click();
    await page.getByRole("heading", { name: "无法打开这个会话" }).waitFor();
    assert.equal(new URL(page.url()).pathname, createdPath, "active deletion must not silently select another chat");
    await page.goBack();
    await chat(newBotPath, newBotName);
    await page.goForward();
    await page.getByRole("heading", { name: "无法打开这个会话" }).waitFor();
    await page.reload();
    await page.getByRole("heading", { name: "无法打开这个会话" }).waitFor();
    await record("active-deletion-back-forward-reload-remains-unavailable");
    await context.close();
  }
  writeFileSync(`${output}/browser-results.json`, JSON.stringify({ browser: await browser.version(), productionBuild: true, realAccountGateway: true, baseline: initial, final: taskCounts(await state()), results }, null, 2) + "\n");
  console.log(`PASS: ${results.length} rendered navigation cases on desktop/narrow/native bridge; real login/accounts/SSE/SQLite; zero task creation and execution writes`);
} finally {
  await browser.close();
}
