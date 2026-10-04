import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = fileURLToPath(new URL("../", import.meta.url));
const result = await build({
  root: ui, configFile: false, plugins: [react()], logLevel: "error",
  define: { "process.env.NODE_ENV": '"production"' },
  build: { write: false, lib: { entry: fileURLToPath(new URL("../test-fixtures/acceptance/owner-bootstrap.tsx", import.meta.url)), formats: ["iife"], name: "OwnerBootstrapFixture" } },
});
const script = (Array.isArray(result) ? result[0] : result).output.find(item => item.type === "chunk").code;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
try {
  for (const multiAccount of [false, true]) {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(5_000);
    const secret = "SyntheticBootstrapAuthority123";
    const password = "SyntheticPassword123!";
    const consoleMessages = [], pageErrors = [], setupRequests = [];
    let authenticated = false;
    page.on("console", message => consoleMessages.push(message.text()));
    page.on("pageerror", error => pageErrors.push(error.message));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      assert.equal(url.origin, "https://bootstrap.example.test", "fixture must never contact another origin");
      if (url.pathname === "/") {
        await route.fulfill({ contentType: "text/html; charset=utf-8", body: '<meta charset="UTF-8"><div id="root"></div><script src="/fixture.js"></script>' });
      } else if (url.pathname === "/fixture.js") {
        await route.fulfill({ contentType: "text/javascript; charset=utf-8", body: script });
      } else if (url.pathname === "/api/server-info") {
        await route.fulfill({ json: { service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: multiAccount ? "accounts" : "owner-password" } } });
      } else if (url.pathname === "/api/auth/session") {
        await route.fulfill({ json: { enabled: true, setup_required: !authenticated, authenticated, password_transport_allowed: true, multi_account: multiAccount } });
      } else if (url.pathname === "/api/auth/setup") {
        assert.equal(request.method(), "POST");
        assert.equal(url.search, "", "authority must not appear in a URL");
        assert.ok(!JSON.stringify(request.headers()).includes(secret), "authority must not appear in headers");
        const body = request.postDataJSON();
        setupRequests.push(body);
        if (body.bootstrap_secret !== secret) {
          await route.fulfill({ status: 401, json: { error: { code: "invalid_bootstrap" } } });
        } else {
          assert.deepEqual(body, { username: "synthetic-admin", email: "admin@example.test", password, bootstrap_secret: secret });
          authenticated = true;
          await route.fulfill({ json: { enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: multiAccount } });
        }
      } else {
        // All other requests (such as artwork) are fulfilled locally too.
        assert.ok(!url.pathname.startsWith("/api/"), "unexpected fixture API");
        await route.fulfill({ status: 204 });
      }
    });
    await page.goto("https://bootstrap.example.test/");
    const input = page.locator('input[name="bootstrap_secret"]');
    await input.waitFor({ timeout: 5_000 }).catch(cause => {
      throw new Error(`Bootstrap fixture failed to render: ${JSON.stringify({ pageErrors, consoleMessages })}`, { cause });
    });
    assert.equal(await input.getAttribute("type"), "password");
    assert.equal(await input.getAttribute("autocomplete"), "off");
    assert.equal(await input.evaluate(element => element.required), true);
    await page.locator('input[name="username"]').fill("synthetic-admin");
    await page.locator('input[name="email"]').fill("admin@example.test");
    await page.locator('input[name="password"]').fill(password);
    const submit = page.locator("form .primary-button");
    assert.equal(await submit.innerText(), "创建账号并继续");
    await submit.click();
    assert.equal(setupRequests.length, 0, "missing secret must block browser submission");
    await input.fill("wrong-synthetic-secret");
    await submit.click();
    await page.getByRole("alert").filter({ hasText: "初始化密钥无效或已使用。" }).waitFor();
    await input.fill(secret);
    await submit.click();
    await page.getByText("Synthetic authenticated workspace").waitFor();
    assert.equal(setupRequests.length, 2);
    assert.equal(await input.count(), 0, "successful setup must retire the secret field");
    const saved = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage }, url: location.href }));
    assert.ok(!JSON.stringify(saved).includes(secret), "authority must not persist in browser state");
    assert.ok(!consoleMessages.join("\n").includes(secret), "authority must not be logged");
    assert.deepEqual(pageErrors, []);
    await context.close();
    console.log(`PASS ${multiAccount ? "multi-account" : "single-owner"} bootstrap: required masked secret, POST body, rejection, cleared form, no persistence/logging`);
  }
} finally {
  await browser.close();
}
