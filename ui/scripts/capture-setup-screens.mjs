// Captures the first-run setup states (key step, key error, register empty/errors/valid)
// at desktop and phone width in light and dark. Synthetic data only; output is gitignored.
//   PLAYWRIGHT_MODULE=... node scripts/capture-setup-screens.mjs [outDir]
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = fileURLToPath(new URL("../", import.meta.url));
const out = process.argv[2] || fileURLToPath(new URL("../../artifacts/setup-redesign/", import.meta.url));
mkdirSync(out, { recursive: true });
const result = await build({
  root: ui, configFile: false, plugins: [react()], logLevel: "error",
  define: { "process.env.NODE_ENV": '"production"' },
  build: { write: false, lib: { entry: fileURLToPath(new URL("../test-fixtures/acceptance/owner-bootstrap.tsx", import.meta.url)), formats: ["iife"], name: "SetupShots" } },
});
const files = (Array.isArray(result) ? result[0] : result).output;
const script = files.find(item => item.type === "chunk").code;
const styles = files.filter(item => item.type === "asset" && item.fileName.endsWith(".css")).map(item => String(item.source)).join("\n");
const secret = "SyntheticBootstrapAuthority123";
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
try {
  for (const [size, viewport] of [["1440x900", { width: 1440, height: 900 }], ["390x844", { width: 390, height: 844 }]]) {
    for (const scheme of ["light", "dark"]) {
      const context = await browser.newContext({ viewport, colorScheme: scheme, locale: "en-US", deviceScaleFactor: 2 });
      const page = await context.newPage();
      await page.route("**/*", async route => {
        const url = new URL(route.request().url());
        if (url.origin !== "https://bootstrap.example.test") return route.continue(); // web fonts only
        const json = (body, status = 200) => route.fulfill({ status, json: body });
        if (url.pathname === "/") await route.fulfill({ contentType: "text/html; charset=utf-8", body: '<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/fixture.css"><div id="root"></div><script src="/fixture.js"></script>' });
        else if (url.pathname === "/fixture.css") await route.fulfill({ contentType: "text/css", body: styles });
        else if (url.pathname === "/fixture.js") await route.fulfill({ contentType: "text/javascript", body: script });
        else if (url.pathname === "/api/server-info") await json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "accounts" } });
        else if (url.pathname === "/api/auth/session") await json({ enabled: true, setup_required: true, authenticated: false, password_transport_allowed: true, multi_account: true });
        else if (url.pathname === "/api/auth/setup/verify") await (route.request().postDataJSON().bootstrap_secret === secret ? json({ valid: true }) : json({ error: { code: "invalid_bootstrap" } }, 401));
        else await route.fulfill({ status: 204 });
      });
      await page.goto("https://bootstrap.example.test/");
      const key = page.locator('input[name="bootstrap_secret"]');
      await key.waitFor();
      await page.evaluate(() => document.fonts.ready);
      const shot = async name => { await page.waitForTimeout(350); await page.screenshot({ path: `${out}/${name}-${size}-${scheme}.png`, fullPage: true }); };
      await shot("1-key-step");
      await key.fill("wrong-synthetic-secret");
      await page.locator("form .primary-button").click();
      await page.locator(".field-error").waitFor();
      await shot("2-key-error");
      await key.fill(secret);
      await page.locator("form .primary-button").click();
      await page.locator('input[name="username"]').waitFor();
      await shot("3-register-empty");
      await page.locator('input[name="username"]').fill("a");
      await page.locator('input[name="email"]').fill("not-an-email");
      await page.locator('input[name="password"]').fill("PassWord1234");
      await page.locator('input[name="confirm_password"]').fill("different-value");
      await page.locator('input[name="confirm_password"]').blur();
      await page.locator("form .primary-button").click();
      await page.locator(".field-error").first().waitFor();
      await shot("4-register-errors");
      await page.locator('input[name="username"]').fill("synthetic-admin");
      await page.locator('input[name="email"]').fill("admin@example.test");
      await page.locator('input[name="password"]').fill("Orchard-Lantern-9051");
      await page.locator('input[name="confirm_password"]').fill("Orchard-Lantern-9051");
      await shot("5-register-valid");
      await context.close();
    }
  }
  console.log(`screenshots written to ${out}`);
} finally {
  await browser.close();
}
