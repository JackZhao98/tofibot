import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import react from "@vitejs/plugin-react";

const result = await build({
  root: fileURLToPath(new URL("../", import.meta.url)), configFile: false, plugins: [react()], logLevel: "error",
  define: { "process.env.NODE_ENV": '"production"' },
  build: { write: false, lib: { entry: fileURLToPath(new URL("../test-fixtures/acceptance/account-menu.tsx", import.meta.url)), formats: ["iife"], name: "AccountMenuFixture" } },
});
const output = (Array.isArray(result) ? result[0] : result).output;
const script = output.find(item => item.type === "chunk").code;
const styles = output.filter(item => item.type === "asset" && item.fileName.endsWith(".css")).map(item => String(item.source)).join("\n").replace(/@import\s*["'][^"']*googleapis[^"']*["'];?/g, "");
if (process.argv.includes("--build-only")) { console.log("PASS: account-menu browser fixture compiled (browser assertions not run)"); process.exit(0); }
const { chromium, webkit } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
for (const engine of [chromium, webkit]) {
  const browser = await engine.launch({ headless: true });
  try {
    for (const [native, hosting] of [["web", "self"], ["web", "cloud"], ["web", undefined], ["legacy", "self"], ["current", "self"]]) {
      const context = await browser.newContext();
      await context.addInitScript(native => {
        localStorage.setItem("tofi:appearance", "system");
        window.installCount = 0; window.unsubscribeCount = 0;
        if (native !== "web") window.tofiDesktop = {
          platform: "darwin", themePreference: "system", appearance: async () => {},
          readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
          onFlushState: () => () => {}, onWindowState: () => () => {}, remoteInputFocus: () => {},
          appUpdate: native === "current" ? {
            status: () => new Promise(resolve => { window.resolveInitialUpdate = resolve; }),
            onStatus: fn => { window.emitUpdate = fn; return () => { window.unsubscribeCount++; }; },
            install: async () => { window.installCount++; if (window.rejectInstall) throw new Error("synthetic"); },
          } : undefined,
        };
      }, native);
      const page = await context.newPage();
      await page.route("**/*", route => {
        const path = new URL(route.request().url()).pathname;
        if (path === "/") return route.fulfill({ contentType: "text/html", body: '<meta charset="utf-8"><div id="root"></div><link rel="stylesheet" href="/fixture.css"><script src="/fixture.js"></script>' });
        if (path === "/api/server-info") return route.fulfill({ json: { service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "owner-password" }, ...(hosting ? { hosting } : {}) } });
        if (path === "/api/auth/session") return route.fulfill({ json: { enabled: true, authenticated: true, setup_required: false, password_transport_allowed: true, owner: { username: "Example", email: "example@account.invalid" } } });
        if (path === "/fixture.js") return route.fulfill({ contentType: "text/javascript", body: script });
        if (path === "/fixture.css") return route.fulfill({ contentType: "text/css", body: styles });
        return route.fulfill({ json: {} });
      });
      await page.goto("https://workspace.example.test:8443");
      await page.locator(".sidebar-account-identity").waitFor();
      const address = page.locator(".sidebar-account-copy small");
      if (hosting === "self" && native === "web") {
        assert.equal(await address.innerText(), "workspace.example.test:8443");
      } else {
        assert.equal(await address.count(), 0, `${native}/${hosting ?? "old server"} has no address line`);
      }
      assert.equal(await page.locator(".sidebar-app-update").count(), 0);
      const avatar = await page.locator(".sidebar-account-monogram").boundingBox();
      const settings = await page.locator(".sidebar-account-settings:not(.sidebar-app-update)").boundingBox();
      assert.ok(Math.abs(avatar.y + avatar.height / 2 - settings.y - settings.height / 2) < 1, "settings and avatar are vertically centered");
      await page.locator(".sidebar-account-identity").click();
      const menu = page.locator('.sidebar-account-menu:not(.sidebar-account-submenu)');
      await menu.waitFor();
      assert.equal(await menu.locator(".sidebar-account-menu-heading strong").innerText(), "Example");
      assert.deepEqual(await menu.locator(":scope > [role=menuitem]").allTextContents(), ["Settings", "Usage", "Artifacts", "Language"]);
      const animation = await menu.evaluate(element => getComputedStyle(element).animationName);
      assert.equal(animation, "account-menu-fade");
      const theme = menu.locator(".sidebar-account-theme");
      for (const label of ["Light", "Dark", "System"]) {
        await theme.click();
        await page.waitForFunction(label => document.querySelector(".sidebar-account-theme")?.getAttribute("aria-label") === label, label);
        assert.equal(await menu.count(), 1);
      }
      await page.keyboard.press("Escape");
      await page.locator(".sidebar-account-settings:not(.sidebar-app-update)").click();
      await page.locator(".settings-help-links").waitFor();
      assert.deepEqual(await page.locator(".settings-help-links a").allTextContents(), ["Report an issue", "Send feedback", "Report a security issue", "GitHub"]);
      for (const link of await page.locator(".settings-help-links a").all()) {
        assert.equal(await link.getAttribute("target"), "_blank");
        assert.ok((await link.getAttribute("href")).startsWith("https://github.com/JackZhao98/tofibot"));
      }
      if (native === "current") {
        await page.waitForFunction(() => typeof window.emitUpdate === "function");
        // A pushed state wins over an older pending status() read.
        await page.evaluate(() => { window.emitUpdate({state:"downloaded",version:"0.2.5"}); window.resolveInitialUpdate({state:"idle",version:null}); });
        const update = page.locator(".sidebar-app-update");
        await page.waitForFunction(() => document.querySelector(".sidebar-app-update")?.getAttribute("data-state") === "downloaded");
        assert.equal(await update.getAttribute("title"), "Restart to update to 0.2.5");
        await page.evaluate(() => window.emitUpdate({state:"downloaded",version:null}));
        await page.waitForFunction(() => document.querySelector(".sidebar-app-update")?.getAttribute("title") === "Restart to update");
        await update.click();
        await page.waitForFunction(() => window.installCount === 1);
        for (const state of ["available", "downloading"]) {
          await page.evaluate(state => window.emitUpdate({state,version:"0.2.5"}), state);
          await page.waitForFunction(state => document.querySelector(".sidebar-app-update")?.getAttribute("data-state") === state, state);
          assert.equal(await update.getAttribute("title"), "Downloading update");
          await update.click();
          assert.equal(await page.evaluate(() => window.installCount), 1);
        }
        for (const state of ["idle", "checking", "waiting_idle", "installing", "error"]) {
          await page.evaluate(state => window.emitUpdate({state,version:null}), state);
          await update.waitFor({state:"detached"});
        }
        await page.evaluate(() => { window.rejectInstall = true; window.emitUpdate({state:"downloaded",version:"0.2.5"}); });
        await update.click();
        await page.getByRole("alert").waitFor();
        assert.equal(await page.getByRole("alert").innerText(), "Could not install the update.");
        await page.evaluate(() => window.unmountAccount());
        await page.waitForFunction(() => window.unsubscribeCount === 1);
      }
      await context.close();
    }
  } finally { await browser.close(); }
}
console.log("PASS: account menu, hosting addresses and native update states (Chromium + WebKit)");
