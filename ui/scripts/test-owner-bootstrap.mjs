import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

// Two-step first-run setup: a setup-key gate (verified server-side before step 2),
// then the registration form with a live password checklist and per-field errors.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = fileURLToPath(new URL("../", import.meta.url));
const catalog = language => JSON.parse(readFileSync(new URL(`../src/locales/${language}/auth.json`, import.meta.url), "utf8"));
const result = await build({
  root: ui, configFile: false, plugins: [react()], logLevel: "error",
  define: { "process.env.NODE_ENV": '"production"' },
  build: { write: false, lib: { entry: fileURLToPath(new URL("../test-fixtures/acceptance/owner-bootstrap.tsx", import.meta.url)), formats: ["iife"], name: "OwnerBootstrapFixture" } },
});
const output = (Array.isArray(result) ? result[0] : result).output;
const script = output.find(item => item.type === "chunk").code;
const styles = output.filter(item => item.type === "asset" && item.fileName.endsWith(".css")).map(item => String(item.source)).join("\n").replace(/@import\s*["'][^"']*googleapis[^"']*["'];?/g, ""); // stay offline: no web fonts
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const secret = "SyntheticBootstrapAuthority123";
const password = "Orchard-Lantern-9051";

async function openFixture(context, multiAccount, state) {
  const page = await context.newPage();
  page.setDefaultTimeout(5_000);
  page.consoleMessages = []; page.pageErrors = [];
  page.on("console", message => page.consoleMessages.push(message.text()));
  page.on("pageerror", error => page.pageErrors.push(error.message));
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    assert.equal(url.origin, "https://bootstrap.example.test", "fixture must never contact another origin");
    const json = (body, status = 200) => route.fulfill({ status, json: body });
    if (url.pathname === "/") {
      await route.fulfill({ contentType: "text/html; charset=utf-8", body: '<meta charset="UTF-8"><link rel="stylesheet" href="/fixture.css"><div id="root"></div><script src="/fixture.js"></script>' });
    } else if (url.pathname === "/fixture.css") {
      await route.fulfill({ contentType: "text/css; charset=utf-8", body: styles });
    } else if (url.pathname === "/fixture.js") {
      await route.fulfill({ contentType: "text/javascript; charset=utf-8", body: script });
    } else if (url.pathname === "/api/server-info") {
      await json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: multiAccount ? "accounts" : "owner-password" } });
    } else if (url.pathname === "/api/auth/session") {
      await json({ enabled: true, setup_required: !state.authenticated, authenticated: state.authenticated, password_transport_allowed: true, multi_account: multiAccount });
    } else if (url.pathname === "/api/auth/setup/verify") {
      assert.equal(request.method(), "POST");
      assert.equal(url.search, "", "authority must not appear in a URL");
      assert.ok(!JSON.stringify(request.headers()).includes(secret), "authority must not appear in headers");
      const body = request.postDataJSON();
      assert.deepEqual(Object.keys(body), ["bootstrap_secret"], "verify sends the key and nothing else");
      state.verifyRequests.push(body);
      if (body.bootstrap_secret === secret) await json({ valid: true });
      else await json({ error: { code: "invalid_bootstrap" } }, 401);
    } else if (url.pathname === "/api/auth/setup") {
      assert.equal(request.method(), "POST");
      assert.equal(url.search, "", "authority must not appear in a URL");
      assert.ok(!JSON.stringify(request.headers()).includes(secret), "authority must not appear in headers");
      const body = request.postDataJSON();
      state.setupRequests.push(body);
      if (body.bootstrap_secret !== secret) await json({ error: { code: "invalid_bootstrap" } }, 401);
      else if (body.email === "taken@example.test") await json({ error: { code: "invalid_email", field: "email", message: "use a valid email address" } }, 400);
      else {
        assert.deepEqual(body, { username: "synthetic-admin", email: "admin@example.test", password, bootstrap_secret: secret });
        state.authenticated = true;
        await json({ enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: multiAccount });
      }
    } else {
      assert.ok(!url.pathname.startsWith("/api/"), "unexpected fixture API");
      await route.fulfill({ status: 204 });
    }
  });
  await page.goto("https://bootstrap.example.test/");
  return page;
}

const dangerColor = page => page.evaluate(() => { const probe = document.createElement("i"); probe.style.color = "var(--danger)"; document.body.append(probe); const value = getComputedStyle(probe).color; probe.remove(); return value; });
// Borders ease into red, so wait for the transition to settle before comparing.
const redBorder = (page, name, color) => page.waitForFunction(([field, expected]) => getComputedStyle(document.querySelector(`input[name="${field}"]`)).borderTopColor === expected, [name, color]);
const field = (page, name) => page.locator(`.owner-field:has(input[name="${name}"])`);
const ruleStates = page => page.locator(".pw-rules li").evaluateAll(items => Object.fromEntries(items.map(item => [item.dataset.rule, item.dataset.state])));

try {
  for (const multiAccount of [false, true]) {
    // Automatic language: the browser locale picks the catalog (zh-CN, then en-US).
    const locale = multiAccount ? "en-US" : "zh-CN";
    const c = catalog(multiAccount ? "en" : "zh-CN");
    const context = await browser.newContext({ locale });
    const state = { authenticated: false, verifyRequests: [], setupRequests: [] };
    const page = await openFixture(context, multiAccount, state);
    const key = page.locator('input[name="bootstrap_secret"]');
    await key.waitFor().catch(cause => { throw new Error(`Bootstrap fixture failed to render: ${JSON.stringify({ pageErrors: page.pageErrors, consoleMessages: page.consoleMessages })}`, { cause }); });
    const danger = await dangerColor(page);

    // Step 1 is only the key; the registration fields do not exist yet.
    assert.equal(await key.getAttribute("type"), "password");
    assert.equal(await key.getAttribute("autocomplete"), "off");
    assert.equal(await page.locator('input[name="username"]').count(), 0, "registration must wait for the key");
    assert.match(await page.locator(".owner-hint").innerText(), /sudo tofi setup-secret/);
    const next = page.locator("form .primary-button");
    assert.equal(await next.innerText(), c.setup.continue);
    await next.click();
    await page.locator("form .field-error").filter({ hasText: c.setup.error.key_required }).waitFor();
    assert.equal(state.verifyRequests.length, 0, "an empty key is not sent");

    // Wrong key: verified by the server, inline red error on the field, still step 1.
    await key.fill("wrong-synthetic-secret");
    await next.click();
    await page.locator("form .field-error").filter({ hasText: c.error.invalid_bootstrap }).waitFor();
    assert.equal(await key.getAttribute("aria-invalid"), "true");
    assert.ok((await key.getAttribute("aria-describedby")).includes(await page.locator("form .field-error").getAttribute("id")));
    await redBorder(page, "bootstrap_secret", danger);
    assert.equal(await page.locator('input[name="username"]').count(), 0);
    assert.equal(state.setupRequests.length, 0);
    await key.fill(secret + "x");
    assert.equal(await page.locator("form .field-error").count(), 0, "editing the key clears its error");

    // Right key: step 2 appears, the key field retires, nothing was consumed.
    await key.fill(secret);
    await next.click();
    const username = page.locator('input[name="username"]');
    await username.waitFor();
    assert.equal(await key.count(), 0);
    assert.equal(state.setupRequests.length, 0, "verifying must not create the account");
    assert.deepEqual(state.verifyRequests.map(item => item.bootstrap_secret), ["wrong-synthetic-secret", secret]);

    // Errors wait for blur: a first keystroke shows nothing.
    await username.pressSequentially("a");
    assert.equal(await field(page, "username").locator(".field-error").count(), 0, "no error on first keystroke");
    await username.blur();
    await field(page, "username").locator(".field-error").filter({ hasText: c.error.invalid_username }).waitFor();
    assert.equal(await username.getAttribute("aria-invalid"), "true");
    await redBorder(page, "username", danger);
    assert.ok((await username.getAttribute("aria-describedby")).includes(await field(page, "username").locator(".field-error").getAttribute("id")));
    await username.fill("");
    await username.blur();
    await field(page, "username").locator(".field-error").filter({ hasText: c.setup.error.username_required }).waitFor();

    // The checklist is live: neutral, then each keystroke.
    const pw = page.locator('input[name="password"]');
    assert.deepEqual(await ruleStates(page), { length: "idle", common: "idle", identity: "idle" });
    assert.equal(await field(page, "password").locator(".field-error").count(), 0);
    await pw.pressSequentially("pass");
    assert.deepEqual(await ruleStates(page), { length: "bad", common: "ok", identity: "ok" });
    assert.equal(await field(page, "password").locator(".field-error").count(), 0, "checklist is live, the error line waits for blur");
    await pw.fill("PassWord1234");
    assert.deepEqual(await ruleStates(page), { length: "ok", common: "bad", identity: "ok" }, "common passwords are rejected case-insensitively");
    await pw.fill("Zq7!mK2$vXp");
    assert.equal((await ruleStates(page)).length, "bad", "11 characters fail");
    await username.fill("orchard");
    await page.locator('input[name="email"]').fill("ops@example.test");
    await pw.fill(password);
    assert.deepEqual(await ruleStates(page), { length: "ok", common: "ok", identity: "bad" }, "password containing the username");
    await username.fill("synthetic-admin");
    assert.deepEqual(await ruleStates(page), { length: "ok", common: "ok", identity: "ok" });
    await page.locator('input[name="email"]').fill("admin@example.test");
    await pw.fill("short");
    await pw.blur();
    await field(page, "password").locator(".field-error").filter({ hasText: c.setup.error.password_rules }).waitFor();
    await redBorder(page, "password", danger);
    await pw.fill(password);
    assert.equal(await field(page, "password").locator(".field-error").count(), 0, "a shown error follows the live state and clears once the password is valid");

    // Confirm must match.
    const confirm = page.locator('input[name="confirm_password"]');
    await confirm.fill("not-the-same-value");
    await confirm.blur();
    await field(page, "confirm_password").locator(".field-error").filter({ hasText: c.setup.error.confirm_mismatch }).waitFor();
    assert.equal(await confirm.getAttribute("aria-invalid"), "true");
    await redBorder(page, "confirm_password", danger);
    await confirm.fill(password);
    assert.equal(await field(page, "confirm_password").locator(".field-error").count(), 0, "matching confirm clears the error");

    // A server field error lands on its own field and keeps the account uncreated.
    const submit = page.locator("form .primary-button");
    assert.equal(await submit.innerText(), c.gate.submit.create_account);
    await page.locator('input[name="email"]').fill("taken@example.test");
    await submit.click();
    await field(page, "email").locator(".field-error").filter({ hasText: c.error.invalid_email }).waitFor();
    assert.equal(await page.locator('input[name="email"]').getAttribute("aria-invalid"), "true");
    assert.equal(await field(page, "username").locator(".field-error").count(), 0);
    assert.equal(await field(page, "password").locator(".field-error").count(), 0);
    assert.equal(state.setupRequests.length, 1);

    // Submitting with a blank field shows every error and sends nothing.
    await page.locator('input[name="email"]').fill("");
    await submit.click();
    await field(page, "email").locator(".field-error").filter({ hasText: c.setup.error.email_required }).waitFor();
    assert.equal(state.setupRequests.length, 1);

    await page.locator('input[name="email"]').fill("admin@example.test");
    await submit.click();
    await page.getByText("Synthetic authenticated workspace").waitFor();
    assert.equal(state.setupRequests.length, 2);
    assert.deepEqual(state.setupRequests[1], { username: "synthetic-admin", email: "admin@example.test", password, bootstrap_secret: secret });
    const saved = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage }, url: location.href }));
    assert.ok(!JSON.stringify(saved).includes(secret), "authority must not persist in browser state");
    assert.ok(!page.consoleMessages.join("\n").includes(secret), "authority must not be logged");
    assert.deepEqual(page.pageErrors, []);
    await context.close();
    console.log(`PASS ${multiAccount ? "multi-account" : "single-owner"} two-step setup (${locale}): key gate, wrong-key error, step 2, live checklist, mismatch, red borders, server field error, no persistence/logging`);
  }

  // Language switch on the setup screen: default follows the browser; the picker changes the catalog.
  const context = await browser.newContext({ locale: "en-US" });
  const state = { authenticated: false, verifyRequests: [], setupRequests: [] };
  const page = await openFixture(context, true, state);
  await page.locator('input[name="bootstrap_secret"]').waitFor();
  const picker = page.locator(".auth-language select");
  assert.equal(await picker.inputValue(), "auto");
  for (const language of ["zh-CN", "de", "fr", "ja"]) {
    await picker.selectOption(language);
    const c = catalog(language);
    await page.locator("form .primary-button").filter({ hasText: c.setup.continue }).waitFor();
    assert.equal(await page.locator("html").getAttribute("lang"), language);
    assert.ok((await page.locator(".owner-card h1").innerText()).includes(c.gate.title.welcome_new));
  }
  await picker.selectOption("zh-CN");
  await page.locator('input[name="bootstrap_secret"]').fill(secret);
  await page.locator("form .primary-button").click();
  await page.locator('input[name="username"]').waitFor();
  await picker.selectOption("de");
  const de = catalog("de");
  await page.locator(".pw-rules li").first().filter({ hasText: de.setup.rule.length }).waitFor();
  await page.locator('input[name="password"]').fill("Zq7");
  assert.equal(await page.locator(".pw-rules li").first().getAttribute("data-state"), "bad");
  assert.equal(await page.locator('input[name="username"]').count(), 1, "switching language keeps step 2 and its state");
  await context.close();
  console.log("PASS language switch on setup: auto -> zh-CN, de, fr, ja; step 2 survives a switch");
} finally {
  await browser.close();
}
