import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import test from "node:test";

const root = new URL("../../", import.meta.url);
const source = path => {
  if (process.env.UX_MENU_BASELINE === "1") {
    try { return execFileSync("git", ["show", `HEAD:${path}`], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }); }
    catch { return ""; }
  }
  return readFileSync(new URL(path, root), "utf8");
};
test("account menu keeps workspace destinations and moves help to General", () => {
  const menu = source("ui/src/AccountMenu.tsx");
  for (const key of ["settings", "usage", "artifacts"]) assert.ok(menu.includes(`common:nav.${key}`));
  assert.ok(menu.includes("<OverlaySubmenu"));
  assert.doesNotMatch(menu, /onConnection|projectLinks|sidebar-account-themes|desktop_client|<small>/);
  assert.ok(menu.includes('className="sidebar-account-theme"'));
  const help = source("ui/src/settings/HelpAndFeedback.tsx");
  for (const key of ["report_issue", "send_feedback", "report_security", "github"]) assert.ok(help.includes(`"${key}"`));
  assert.match(help, /<a .*href=/);
  assert.doesNotMatch(help, /description=/);
  assert.match(source("ui/src/settings/SettingsPages.tsx"), /function GeneralPage[\s\S]*?<HelpAndFeedback\/>/);
});
test("footer omits client wording; menu fades without translation or corner scale", () => {
  const footer = source("ui/src/OwnerSession.tsx").split("export function SidebarAccount")[1];
  assert.ok(footer);
  assert.doesNotMatch(footer, /desktop_client/);
  assert.match(footer, /hosting === "self" && !isDesktop \? window.location.host : ""/);
  assert.match(footer, /address && <small>\{address\}<\/small>/);
  assert.match(footer, /<AppUpdateButton\/>/);
  const css = source("ui/src/account-menu.css");
  assert.match(css, /@keyframes account-menu-fade \{ from \{ opacity:0; \} to \{ opacity:1; \} \}/);
  assert.doesNotMatch(css, /translate|scale\(/);
  assert.match(css, /prefers-reduced-motion:reduce/);
  assert.match(css, /-webkit-backdrop-filter:blur\(32px\)/);
  assert.match(css, /var\(--surface\) 98%/);
});
test("update contract is additive and wording exists in all seven locales", () => {
  const desktop = source("ui/src/desktop.ts");
  assert.match(desktop, /appUpdate\?: AppUpdateBridge/);
  for (const state of ["idle", "checking", "available", "downloading", "downloaded", "waiting_idle", "installing", "error"]) assert.ok(desktop.includes(`"${state}"`));
  for (const language of ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
    const catalog = JSON.parse(source(`ui/src/locales/${language}/auth.json`));
    assert.ok(catalog.account.help_feedback);
    assert.ok(catalog.app_update.downloading);
    assert.match(catalog.app_update.restart, /\{\{version\}\}/);
    assert.ok(catalog.app_update.install_failed);
  }
});
