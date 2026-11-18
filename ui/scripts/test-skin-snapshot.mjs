// test:skin-snapshot (V3 refactor, PLAN 7.5): proves a "no visual change" PR keeps every computed style.
// It renders the same pages from a base git ref and from the working tree, and compares, element by element, every
// standard computed property, including ::before and ::after when they render. Custom properties are not compared:
// renaming them is what these PRs do; what they resolve to shows up in the standard properties. transition-* is not
// compared either: it shapes a change, not a frame, and libraries switch it inline by timing (dnd-kit sortable rows);
// test:motion-tokens holds the durations. Nor is animation-delay: the boot logo derives its phase from the clock, so
// looping animations are captured at their first frame with no delay. Each tree has its own dev server, so the page's
// origin is removed from url() values.
//
// The cat artwork (svg[data-tofi-avatar], animated from JS) is compared at its root only, as in test:skin-sensitivity.
// Coverage (PLAN 7.5): every catalog state; every Settings tab and Administration page on the org fixture; sign-in and
// owner setup (the session gate); the Mac entry pages; the OAuth completion page. Each page is captured at 1280 and
// 390 px, light and dark, under normal, reduced-motion and forced-colours emulation. Running animations are paused at
// their start (infinite) or finished (finite) before each capture, so a capture never lands mid-transition.
//
// Usage: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:skin-snapshot
//   SKIN_SNAPSHOT_BASE=<git ref>       what "before" is (default origin/main)
//   SKIN_SNAPSHOT_ONLY=catalog,settings,entry,mac,oauth   some groups only
//   SKIN_SNAPSHOT_STATE=section/state  catalog states only (comma-separated; "section/inline" for a section's inline states)
//   SKIN_SNAPSHOT_DETAIL=1             print the differing properties (slow; use with SKIN_SNAPSHOT_STATE)
//   SKIN_SNAPSHOT_OUT=/abs/file.json   write every difference, not just the first ones printed
// Stateful widgets are not deterministic (where focus lands, a video that attaches late), so one capture proves nothing
// either way. Each tree is captured, the base twice, and pages that differ are captured again on both sides, up to
// SKIN_SNAPSHOT_RETRIES (default 3) rounds. An element differs only if no value seen in the head was ever seen in the base.
// Synthetic data only. Not part of CI: it builds two trees and takes several minutes.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createOrgStub, startFixture } from "./lib/org-fixture.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const repo = dirname(ui);
if (!process.env.PLAYWRIGHT_MODULE) { console.error("PLAYWRIGHT_MODULE is not set"); process.exit(2); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const BASE = process.env.SKIN_SNAPSHOT_BASE || "origin/main";
const ONLY = (process.env.SKIN_SNAPSHOT_ONLY || "catalog,settings,entry,mac,oauth").split(",");
const ONLY_STATES = process.env.SKIN_SNAPSHOT_STATE?.split(",");
const DETAIL = process.env.SKIN_SNAPSHOT_DETAIL === "1";
const LOAD_MS = 120_000;
const WIDTHS = [{ width: 1280, height: 800 }, { width: 390, height: 760 }];
const THEMES = ["light", "dark"];
const MEDIA = [{ id: "normal", reducedMotion: "no-preference", forcedColors: "none" }, { id: "reduce", reducedMotion: "reduce", forcedColors: "none" }, { id: "forced", reducedMotion: "no-preference", forcedColors: "active" }];
const MAC_PAGES = ["desktop-settings", "desktop-setup", "desktop-permission-guide", "desktop-local-run", "desktop-update-required", "desktop-bot-browser"];
const SETTINGS_TABS = ["general", "connections", "skills", "permissions", "models", "computer", "devices", "usage", "advanced", "admin", "admin-groups", "admin-resources", "admin-design"];
const SESSIONS = {
  "sign-in": { enabled: true, setup_required: false, authenticated: false, password_transport_allowed: true },
  "owner-setup": { enabled: true, setup_required: true, authenticated: false, password_transport_allowed: true },
};

// The "before" tree: the base ref's ui/ and the OAuth template, sharing this tree's node_modules (sequential runs only).
const work = mkdtempSync(join(tmpdir(), "tofi-skin-snapshot-"));
execFileSync("sh", ["-c", `git -C "${repo}" archive "${BASE}" ui internal/app/oauth_completion.go | tar -x -C "${work}"`]);
symlinkSync(join(ui, "node_modules"), join(work, "ui", "node_modules"));
const trees = [{ name: `base (${BASE})`, ui: join(work, "ui"), repo: work }, { name: "head (working tree)", ui, repo }];

// In the page: pause or finish animations, then hash (or keep, with DETAIL) every standard property of every element.
function snapshot(detail) {
  for (const animation of document.getAnimations()) {
    try { const end = animation.effect?.getComputedTiming?.().endTime; if (Number.isFinite(end)) animation.finish(); else { animation.effect?.updateTiming?.({ delay: 0 }); animation.pause(); animation.currentTime = 0; } } catch { /* an animation that cannot be finished is left as is */ }
  }
  const props = [...getComputedStyle(document.documentElement)].filter(name => !name.startsWith("--") && !name.startsWith("transition") && name !== "animation-delay").sort();
  const hash = text => { let h = 2166136261; for (let i = 0; i < text.length; i += 1) { h ^= text.charCodeAt(i); h = Math.imul(h, 16777619); } return (h >>> 0).toString(36); };
  const styleOf = (element, pseudo) => { const style = getComputedStyle(element, pseudo); return props.map(name => style.getPropertyValue(name)).join(";").split(location.origin).join(""); };
  const result = {};
  const walk = (element, path) => {
    // display:none paints nothing, and animations under it keep following the clock without being listed.
    if (getComputedStyle(element).display === "none") { result[path] = "none"; return; }
    let full = styleOf(element);
    for (const pseudo of ["::before", "::after"]) { const content = getComputedStyle(element, pseudo).content; if (content !== "none" && content !== "normal") full += `|${pseudo}:${styleOf(element, pseudo)}`; }
    result[path] = detail ? full : hash(full);
    // The cat (lib/tofi-avatar) is artwork drawn frame by frame from JS; its root svg is compared, its insides are not.
    if (element.hasAttribute("data-tofi-avatar")) return;
    const counts = {};
    for (const child of element.children) {
      const tag = child.tagName.toLowerCase();
      // Nothing that never paints: the head (the dev server adds <style> tags as lazy CSS lands), scripts, templates.
      if (["head", "script", "style", "link", "meta", "template", "noscript"].includes(tag)) continue;
      counts[tag] = (counts[tag] ?? 0) + 1; walk(child, `${path}>${tag}${counts[tag]}`);
    }
  };
  walk(document.documentElement, "html");
  return detail ? { props, result } : { result };
}

async function settle(page) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.evaluate(() => new Promise(done => requestAnimationFrame(() => requestAnimationFrame(done))));
}

async function captureAll(page) {
  const out = {};
  for (const size of WIDTHS) {
    await page.setViewportSize(size);
    await settle(page);
    for (const theme of THEMES) {
      await page.evaluate(value => { document.documentElement.dataset.theme = value; document.documentElement.style.colorScheme = value; }, theme);
      for (const media of MEDIA) {
        await page.emulateMedia({ reducedMotion: media.reducedMotion, forcedColors: media.forcedColors });
        out[`${size.width} ${theme} ${media.id}`] = await page.evaluate(snapshot, DETAIL);
      }
    }
  }
  await page.emulateMedia({ reducedMotion: "no-preference", forcedColors: "none" });
  return out;
}

const oauthPage = async tree => {
  const { oauthAssetsPlugin } = await import(pathToFileURL(join(tree.ui, "oauthAssetsPlugin.ts")).href);
  const assets = [];
  oauthAssetsPlugin().generateBundle.call({ emitFile: asset => assets.push(asset) });
  const css = assets.find(asset => asset.fileName === "oauth/connection.css").source;
  const go = readFileSync(join(tree.repo, "internal/app/oauth_completion.go"), "utf8");
  const template = /template\.Must\(template\.New\("oauth-connection"\)\.Parse\(`([\s\S]*?)`\)\)/.exec(go)[1];
  return template.replace('data-state="{{.State}}"', 'data-state="pending"').replace(/<script src="\/oauth\/connection\.js"><\/script>/, "")
    .replace(/<link rel="stylesheet" href="\/oauth\/connection\.css">/, `<style>${css}</style>`).replace(/\{\{[^}]*\}\}/g, "");
};

async function captureTree(browser, tree, only) {
  const pages = {};
  const fx = await startFixture(tree.ui, "skin-snapshot");
  const stub = createOrgStub();
  stub.reset?.();
  try {
    const context = await browser.newContext({ viewport: WIDTHS[0], locale: "en-US" });
    // One clock for every run: relative times ("3 min ago") change the width of their text otherwise.
    await context.clock.setFixedTime(new Date("2026-11-17T18:00:00Z"));
    await context.addInitScript(() => { try { localStorage.setItem("tofi:appearance", "light"); } catch { /* storage blocked */ } });
    let session = null;
    await context.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method(), path = url.pathname;
      if (url.origin !== fx.origin) return route.abort();
      if (!path.startsWith("/api/") && path !== "/health") return route.continue();
      const json = (status, body) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
      if (path === "/api/auth/session") return json(200, session ?? { enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: { id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin" } });
      if (path === "/api/server-info") return json(200, { service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: { mode: "owner" } });
      if (path === "/api/bots") return json(200, { bots: [{ id: "bot_research", name: "Research Bot", archived: false }] });
      if (path === "/api/preferences") return json(200, { timezone: "America/Los_Angeles", timezone_configured: true });
      if (path === "/api/models") return json(200, { models: stub.state.models ?? [] });
      let body = {};
      try { body = JSON.parse(request.postData() || "{}"); } catch { /* a GET */ }
      const answer = stub.handle(url, method, body);
      return answer === undefined ? json(200, {}) : json(answer[0], answer[1]);
    });
    const visit = async (key, url, ready) => {
      if (only && !only.has(key)) return;
      const page = await context.newPage();
      await page.setViewportSize(WIDTHS[0]);
      try {
        await page.goto(url);
        if (ready) await ready(page);
        pages[key] = await captureAll(page);
      } catch (error) { pages[key] = { error: String(error.message).split("\n")[0] }; }
      finally { await page.close(); }
      process.stdout.write(".");
    };

    if (ONLY.includes("catalog")) {
      const shell = await context.newPage();
      await shell.goto(`${fx.origin}/catalog.html`);
      await shell.waitForFunction(() => Array.isArray(window.__catalog), null, { timeout: LOAD_MS });
      const index = await shell.evaluate(() => window.__catalog);
      await shell.close();
      for (const section of index) {
        const jobs = [];
        if (section.states.some(state => state.kind === "inline")) jobs.push(null);
        for (const state of section.states.filter(state => state.kind !== "inline")) jobs.push(state);
        for (const state of jobs) {
          const id = `${section.id}/${state?.id ?? "inline"}`;
          if (ONLY_STATES && !ONLY_STATES.includes(id)) continue;
          const query = new URLSearchParams({ frame: section.id, theme: "light", lng: "en" });
          if (state) query.set("state", state.id);
          await visit(`catalog ${id}`, `${fx.origin}/catalog.html?${query}`, page => page.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: LOAD_MS }));
        }
      }
    }
    if (ONLY.includes("settings")) {
      for (const tab of SETTINGS_TABS) {
        session = null;
        await visit(`settings ${tab}`, `${fx.origin}${fx.path}?${new URLSearchParams({ lang: "en", host: "kvm", tab })}`, page => page.locator(".settings-shell").first().waitFor({ timeout: LOAD_MS }));
      }
    }
    if (ONLY.includes("entry")) {
      for (const [name, value] of Object.entries(SESSIONS)) {
        session = value;
        await visit(`entry ${name}`, `${fx.origin}${fx.path}?lang=en`, page => page.locator("h1").first().waitFor({ timeout: LOAD_MS }));
      }
      session = null;
    }
    if (ONLY.includes("mac")) for (const name of MAC_PAGES) await visit(`mac ${name}`, `${fx.origin}/${name}.html`, null);
    if (ONLY.includes("oauth")) {
      writeFileSync(join(fx.fixture, "oauth.html"), await oauthPage(tree));
      await visit("oauth completion", `${fx.origin}/${basename(fx.fixture)}/oauth.html`, null);
    }
    await context.close();
  } finally {
    await fx.server.close();
    rmSync(fx.fixture, { recursive: true, force: true });
  }
  return pages;
}

const RETRIES = Number(process.env.SKIN_SNAPSHOT_RETRIES ?? 3);
const baseRuns = [], headRuns = [];
const values = (runs, key, mode, path) => new Set(runs.map(run => run[key]?.[mode]?.result?.[path]));
function compare() {
  const failures = [], report = [];
  let unstable = 0;
  for (const key of new Set([...Object.keys(baseRuns[0]), ...Object.keys(headRuns[0])])) {
    const a = baseRuns[0][key], b = headRuns[0][key];
    if (!a || !b) { failures.push({ key, text: `${key}: only in ${a ? "base" : "head"}` }); continue; }
    if (a.error || b.error) { if (Boolean(a.error) !== Boolean(b.error)) failures.push({ key, text: `${key}: base ${a.error ?? "ok"}, head ${b.error ?? "ok"}` }); continue; }
    for (const mode of Object.keys(a)) {
      const paths = new Set([...baseRuns, ...headRuns].flatMap(run => Object.keys(run[key]?.[mode]?.result ?? {})));
      const differing = [];
      for (const path of paths) {
        const seenBase = values(baseRuns, key, mode, path), seenHead = values(headRuns, key, mode, path);
        if (seenBase.size > 1) unstable += 1;
        if (![...seenHead].some(value => seenBase.has(value))) differing.push(path);
      }
      if (!differing.length) continue;
      report.push({ page: key, mode, paths: differing });
      const x = a[mode].result, y = b[mode]?.result ?? {};
      const lines = differing.slice(0, 4).map(path => {
        if (!(path in x) || !(path in y)) return `    ${path}: only in ${path in x ? "base" : "head"}`;
        if (!DETAIL) return `    ${path}`;
        if (x[path] === "none" || y[path] === "none") return `    ${path}: display ${x[path] === "none" ? "none" : "shown"} -> ${y[path] === "none" ? "none" : "shown"}`;
        const props = a[mode].props, before = x[path].split("|"), after = y[path].split("|"), changes = [];
        for (let part = 0; part < Math.max(before.length, after.length); part += 1) {
          const label = (before[part] ?? after[part]).match(/^(::\w+):/)?.[1] ?? "";
          const strip = text => (text ?? "").replace(/^::\w+:/, "").split(";");
          const p = strip(before[part]), q = strip(after[part]);
          props.forEach((name, i) => { if (p[i] !== q[i]) changes.push(`${label}${label ? " " : ""}${name} ${p[i]} -> ${q[i]}`); });
        }
        return `    ${path}: ${changes.slice(0, 6).join("; ")}`;
      });
      failures.push({ key, text: `${key} [${mode}]: ${differing.length} element(s) differ\n${lines.join("\n")}` });
    }
  }
  return { failures, report, unstable };
}

const browser = await chromium.launch({ headless: true });
const [base, head] = trees;
let outcome;
try {
  const run = async (tree, list, only) => { process.stdout.write(`${tree.name} `); const started = Date.now(); list.push(await captureTree(browser, tree, only)); console.log(` ${Object.keys(list.at(-1)).length} pages in ${Math.round((Date.now() - started) / 1000)} s`); };
  await run(base, baseRuns); await run(base, baseRuns); await run(head, headRuns);
  outcome = compare();
  for (let round = 1; round <= RETRIES && outcome.failures.length; round += 1) {
    const only = new Set(outcome.failures.map(failure => failure.key));
    console.log(`round ${round}: capturing ${only.size} differing page(s) again`);
    await run(base, baseRuns, only); await run(head, headRuns, only);
    outcome = compare();
  }
} finally {
  await browser.close();
  rmSync(work, { recursive: true, force: true });
}

const { failures, report, unstable } = outcome;
const after = headRuns[0];
const pageCount = Object.keys(after).length, errors = Object.values(after).filter(page => page.error).length;
if (process.env.SKIN_SNAPSHOT_OUT) writeFileSync(process.env.SKIN_SNAPSHOT_OUT, JSON.stringify({ base: BASE, unstable, failures: failures.map(failure => failure.text), report }, null, 2));
const note = `${unstable} element capture(s) varied within the base${errors ? `; ${errors} page(s) failed to load` : ""}`;
if (failures.length) {
  const byPage = {};
  for (const entry of report) byPage[entry.page] = (byPage[entry.page] ?? 0) + entry.paths.length;
  console.log(`skin snapshot: FAIL (${failures.length} difference(s) over ${pageCount} pages; ${note})`);
  console.log(`  by page: ${Object.entries(byPage).sort((p, q) => q[1] - p[1]).map(([page, n]) => `${page} ${n}`).join(", ")}`);
  for (const failure of failures.slice(0, 40)) console.log(`  ${failure.text}`);
  process.exit(1);
}
console.log(`skin snapshot: PASS (${pageCount} pages x ${WIDTHS.length * THEMES.length * MEDIA.length} captures match ${BASE}; ${note})`);
