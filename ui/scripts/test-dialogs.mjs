// Modal dialogs on Radix Dialog / AlertDialog (docs/design/migration.md step 3, ui/src/overlays/ModalDialog.tsx).
//  1. Bundle (production build): Radix is only in lazy chunks, never in the entry files (D31); the dialogs that used to
//     sit in the entry (ReassignPrimaryDialog, DialogShell) are lazy too; menus and dialogs share one copy of Radix's
//     layer and focus primitives (ds/integrate).
//  2. Real app (production build, synthetic data, Chromium): the delete dialog takes focus on Cancel, keeps Tab and
//     Shift+Tab inside, hides the page from screen readers (aria-hidden around it, the workspace inert), carries
//     aria-modal, closes on Escape and on a press on the scrim but on neither while busy, and gives focus back to the
//     header "More" button; Escape with a non-modal panel open closes only the dialog; the desktop app's menu shortcuts
//     stand still while it is open (App.tsx reads aria-modal); opening it costs at most a frame or two (D02).
//  3. Harness (dev server, real components and CSS): a Radix sheet over a non-Radix modal and a dialog over the sheet:
//     each Escape closes only the top one and focus returns layer by layer, the modal behind never sees the key;
//     a locked sheet ignores Escape and the scrim; the AlertDialog-based confirmation ignores the scrim; a dialog
//     whose keyboard belongs to a remote computer lets Tab and Escape through to it; a tooltip and a toast shown
//     while a native <dialog> is open (top layer) render inside it and are on top (tokens.md Z7); a dialog opened
//     and closed with the mouse leaves no focus ring; the two confirmations that left the settings page (they were
//     native <dialog>s inside it) keep its heading, button and font styles.
// Run: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:dialogs   (no SKIP: fails without it)
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

if (!process.env.PLAYWRIGHT_MODULE) { console.error("test:dialogs needs PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs"); process.exit(1); }
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const results = [], failures = [];
// TOFI_SKIP_TIMING=1 (CI): leave out the checks that read a clock; shared runners are too noisy for them. Run them locally.
const timingCheck = name => process.env.TOFI_SKIP_TIMING === "1" && /\bwithin \d+ ms\b/.test(name);
const check = async (name, run) => {
  if (timingCheck(name)) { console.log(`skipped (timing check, CI mode) - ${name}`); return; }
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n").slice(0, 4).join("\n    ")}`); }
};
const truthy = (value, message) => { if (!value) throw new Error(message); };
// DIALOGS_ONLY=app or DIALOGS_ONLY=harness runs one half (used while proving each check fails on a reverted tree).
const only = process.env.DIALOGS_ONLY || "app,harness";
const RADIX_MARKERS = ["focusScope.autoFocusOnMount", "dismissableLayer.pointerDownOutside", "data-scroll-locked"];

// ---- 1 + 2: the production app
if (only.includes("app")) {
const { createFixture } = await import("./lib/chat-fixture.mjs");
const fixture = await createFixture();

await check("Radix ships only in lazy chunks; the entry files carry none of it and no dialog code", async () => {
  const get = async path => (await fetch(`${fixture.origin}/${path}`)).text();
  const html = await get("");
  const eager = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map(match => match[1]);
  truthy(eager.length, "no entry scripts found in index.html");
  // Crawl every chunk reachable from the entry (static and dynamic imports).
  const seen = new Map(), queue = [...eager];
  while (queue.length) {
    const file = queue.shift();
    if (seen.has(file)) continue;
    const text = await get(file);
    seen.set(file, text);
    for (const match of text.matchAll(/(?:assets\/|\.\/)([\w.-]+\.js)/g)) queue.push(`assets/${match[1]}`);
  }
  const radix = [...seen].filter(([, text]) => RADIX_MARKERS.some(marker => text.includes(marker))).map(([file]) => file);
  truthy(radix.length, `no chunk carries Radix (looked for ${RADIX_MARKERS.join(", ")} in ${seen.size} chunks)`);
  const eagerRadix = radix.filter(file => eager.includes(file));
  truthy(!eagerRadix.length, `Radix is in the entry: ${eagerRadix.join(", ")}`);
  for (const name of ["ReassignPrimaryDialog", "DeleteConversationDialog", "DialogShell", "ModalDialog"]) {
    const files = [...seen.keys()].filter(file => file.startsWith(`assets/${name}-`));
    truthy(files.length, `${name} is not a chunk of its own`);
    truthy(!files.some(file => eager.includes(file)), `${name} is loaded with the entry`);
  }
  // Menus (RadixOverlays, step 2) and dialogs (ModalDialog, step 3) must share one copy of Radix's layer and focus
  // primitives: two copies keep two layer stacks, and Escape in a menu over a dialog would close both.
  for (const marker of ["dismissableLayer.pointerDownOutside", "focusScope.autoFocusOnMount"]) {
    const copies = [...seen].reduce((count, [, text]) => count + text.split(marker).length - 1, 0);
    truthy(copies === 1, `${marker} is bundled ${copies} times; the Radix packages must resolve to one copy of each primitive`);
  }
  const owner = radix.find(file => seen.get(file).includes("dismissableLayer.pointerDownOutside"));
  for (const name of ["RadixOverlays", "ModalDialog"]) {
    const [file] = [...seen.keys()].filter(path => path.startsWith(`assets/${name}-`));
    truthy(file && (file === owner || seen.get(file).includes(basename(owner))), `${name} does not use the shared Radix primitives in ${owner}`);
  }
  console.log(`  ${seen.size} chunks; Radix in ${radix.join(", ")}`);
});

const menuDelete = async page => {
  await page.locator(".header-more > button").click();
  await page.locator(".more-menu[data-state=open] .delete-menu-action").click();
  await page.locator(".delete-dialog").waitFor();
};
const inDialog = page => page.evaluate(() => Boolean(document.querySelector(".delete-dialog")?.contains(document.activeElement)));

await check("delete dialog: focus on Cancel, Tab stays inside, page hidden, aria-modal, Escape closes, focus back on More", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 }, motion: "reduce" });
  await menuDelete(page);
  const state = await page.evaluate(() => {
    const dialog = document.querySelector(".delete-dialog");
    return { role: dialog.getAttribute("role"), modal: dialog.getAttribute("aria-modal"), marked: dialog.hasAttribute("data-tofi-modal"), layer: dialog.closest("#tofi-layers") !== null,
      focus: document.activeElement?.textContent, inert: document.querySelector(".workspace").inert,
      labelled: Boolean(document.getElementById(dialog.getAttribute("aria-labelledby") ?? "")?.textContent) };
  });
  truthy(state.role === "alertdialog" && state.modal === "true" && state.marked, `role/aria-modal/data-tofi-modal: ${JSON.stringify(state)}`);
  truthy(state.layer, "the dialog is not in #tofi-layers");
  truthy(state.focus === "Cancel", `focus starts on "${state.focus}", not Cancel`);
  // Radix hides the rest of the page from screen readers (aria-hidden on everything outside the dialog except live
  // regions and their ancestors), in an effect after the first paint; the app also makes the workspace inert.
  const BEHIND = [".header-more > button", ".composer textarea", ".message-scroll", ".sidebar-search, .search-box input"];
  const unhidden = () => page.evaluate(BEHIND => BEHIND.filter(selector => { const el = document.querySelector(selector); return el && !el.closest('[aria-hidden="true"]'); }), BEHIND);
  await page.waitForFunction(BEHIND => BEHIND.every(selector => { const el = document.querySelector(selector); return !el || el.closest('[aria-hidden="true"]'); }), BEHIND, { timeout: 1000 }).catch(() => {});
  const visibleBehind = await unhidden();
  truthy(!visibleBehind.length && state.inert, `the page behind is not hidden: screen readers still reach ${visibleBehind.join(", ") || "-"}; workspace inert=${state.inert}`);
  truthy(state.labelled, "aria-labelledby does not name the heading");
  for (const key of [...Array(7).fill("Tab"), ...Array(7).fill("Shift+Tab")]) { await page.keyboard.press(key); truthy(await inDialog(page), `${key} moved focus out of the dialog`); }
  await page.keyboard.press("Escape");
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  await page.waitForFunction(() => document.activeElement?.matches(".header-more > button"), null, { timeout: 2000 }).catch(() => {});
  const after = await page.evaluate(() => ({ focus: document.activeElement?.outerHTML.slice(0, 80), hidden: document.querySelectorAll('#root [aria-hidden="true"][data-aria-hidden]').length, inert: document.querySelector(".workspace").inert }));
  truthy(after.focus?.includes("ghost-button"), `focus did not return to More: ${after.focus}`);
  truthy(after.hidden === 0 && !after.inert, `the page stays hidden after closing: ${JSON.stringify(after)}`);
  // A click on blank space inside the card does not strand Escape (components.md C13).
  await menuDelete(page);
  const box = await page.locator(".delete-dialog h2").boundingBox();
  await page.mouse.click(box.x + box.width - 2, box.y + box.height / 2);
  truthy(await page.locator(".delete-dialog").count(), "a click inside the card closed it");
  await page.keyboard.press("Escape");
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  // A press on the scrim closes it.
  await menuDelete(page);
  await page.mouse.click(8, 8);
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

await check("delete dialog while busy: neither Escape nor the scrim closes it; a failure is shown and Escape closes after", async () => {
  let release;
  const held = new Promise(resolve => { release = resolve; });
  const route = async route => {
    if (route.request().method() !== "DELETE") return false;
    await held;
    await route.fulfill({ status: 500, json: { error: { code: "synthetic", message: "Synthetic failure" } } });
    return true;
  };
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 }, motion: "reduce", route });
  await menuDelete(page);
  await page.locator(".delete-dialog-submit").click();
  await page.locator('.delete-dialog[aria-busy="true"]').waitFor();
  await page.keyboard.press("Escape");
  await page.mouse.click(8, 8);
  await page.waitForTimeout(150);
  truthy(await page.locator(".delete-dialog").count(), "the busy dialog closed");
  release();
  await page.locator(".delete-dialog [role=alert]").waitFor();
  await page.keyboard.press("Escape");
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

await check("Escape closes only the top layer: the dialog first, then the non-modal panel under it", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 }, motion: "reduce" });
  await page.getByRole("button", { name: "Tasks", exact: true }).click();
  await page.waitForFunction(() => document.querySelector(".workspace-grid")?.classList.contains("sidebar-open"));
  await menuDelete(page);
  await page.keyboard.press("Escape");
  await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 2000 });
  await page.waitForTimeout(100);
  truthy(await page.evaluate(() => document.querySelector(".workspace-grid").classList.contains("sidebar-open")), "one Escape closed the dialog and the panel under it");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector(".workspace-grid").classList.contains("sidebar-open"), null, { timeout: 2000 });
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

await check("desktop app: menu shortcuts stand still while a dialog is open (App.tsx reads aria-modal)", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 }, motion: "reduce" });
  await context.addInitScript(() => {
    window.__commands = [];
    window.tofiDesktop = { platform: "darwin", themePreference: "light", onCommand: listener => { window.__commands.push(listener); return () => {}; }, onWindowState: () => () => {}, onFlushState: () => () => {},
      readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {}, appearance: async () => {}, remoteInputFocus: () => {},
      getPermissionStatus: async () => ({ supported: false, microphone: "unknown", screen: "unknown", accessibility: false }), requestMicrophoneAccess: async () => false };
  });
  await page.reload();
  await page.locator(".message-block").first().waitFor();
  const collapsed = () => page.evaluate(() => document.querySelector(".workspace-grid").classList.contains("sidebar-collapsed"));
  const toggle = () => page.evaluate(() => { for (const listener of window.__commands) listener("toggle-sidebar"); });
  truthy(await page.evaluate(() => window.__commands.length > 0), "the app registered no desktop command listener");
  const before = await collapsed();
  await toggle();
  await page.waitForTimeout(100);
  truthy(await collapsed() !== before, "control: toggle-sidebar does nothing even without a dialog");
  await toggle();
  await page.waitForTimeout(100);
  await menuDelete(page);
  const open = await collapsed();
  await toggle();
  await page.waitForTimeout(150);
  truthy(await collapsed() === open, "toggle-sidebar acted behind the open delete dialog");
  truthy(await page.locator(".delete-dialog").count(), "the dialog closed");
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

const openCost = async (page, cdp) => page.evaluate(() => new Promise(resolve => {
  const long = [];
  const observer = new PerformanceObserver(list => { for (const entry of list.getEntries()) long.push(entry.duration); });
  observer.observe({ type: "longtask" });
  const item = document.querySelector(".more-menu[data-state=open] .delete-menu-action");
  const start = performance.now();
  item.click();
  const tick = () => {
    const dialog = document.querySelector(".delete-dialog");
    if (dialog && dialog.getBoundingClientRect().width > 0) { const visible = performance.now() - start; setTimeout(() => { observer.disconnect(); resolve({ visible, long: Math.max(0, ...long) }); }, 120); }
    else requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}));
for (const cpu of [1, 4]) {
  await check(`opening the delete dialog shows it within ${cpu === 1 ? 32 : 100} ms (median of 5, ${cpu}x CPU, chunks warm), no long task over ${cpu === 1 ? 50 : 150} ms`, async () => {
    const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 } });
    // The idle warm-up fetches the dialog chunks (lazyPanels.tsx); wait for the dialog and Radix to be in.
    await page.waitForFunction(() => ["ModalDialog", "DeleteConversationDialog"].every(name => performance.getEntriesByType("resource").some(entry => entry.name.includes(`/assets/${name}-`))), null, { timeout: 10000 });
    const cdp = await context.newCDPSession(page);
    if (cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: cpu });
    const samples = [];
    for (let i = 0; i < 5; i++) {
      await page.locator(".header-more > button").click();
      await page.locator(".more-menu[data-state=open] .delete-menu-action").waitFor();
      await page.waitForTimeout(250);
      samples.push(await openCost(page, cdp));
      await page.keyboard.press("Escape");
      await page.locator(".delete-dialog").waitFor({ state: "detached", timeout: 3000 });
      await page.waitForTimeout(300);
    }
    const visible = samples.map(sample => sample.visible).sort((a, b) => a - b);
    const median = visible[2], worstLong = Math.max(...samples.map(sample => sample.long));
    console.log(`  ${cpu}x: click to visible ${visible.map(v => v.toFixed(1)).join(", ")} ms (median ${median.toFixed(1)}), longest task ${worstLong.toFixed(0)} ms`);
    truthy(median <= (cpu === 1 ? 32 : 100), `median click-to-visible ${median.toFixed(1)} ms`);
    truthy(worstLong <= (cpu === 1 ? 50 : 150), `a long task of ${worstLong.toFixed(0)} ms while opening`);
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });
}
await fixture.close();
}

// ---- 3: harness with the real components
if (only.includes("harness")) {
const pw = await import(process.env.PLAYWRIGHT_MODULE);
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");
const { focusRingProbe } = await import("./focus-audit.mjs");
const harness = await mkdtemp(join(ui, ".dialogs-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(harness, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(harness, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><div id="tofi-layers"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(harness, "main.tsx"), `${styles}
import '../src/settings/settings-components.css';import '../src/computer-resources.css';import '../src/extensions-system.css';
import React,{useEffect,useRef,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady} from '../src/i18n';
import {installFocusModality} from '../src/focusModality';
import {ActionHints} from '../src/InteractionSystem';
import {WorkspaceToast} from '../src/WorkspaceToast';
import {AdminSheet} from '../src/AdminSheets';
import {ModalDialog,ConfirmDialog} from '../src/overlays/ModalDialog';
installFocusModality();
const w=window as any;w.__paneEscapes=0;w.__remote=[];w.__closed=[];
function Native({onClose,onToast}:{onClose:()=>void;onToast:()=>void}){
 const ref=useRef<HTMLDialogElement>(null);
 useEffect(()=>{ref.current?.showModal();return()=>ref.current?.close()},[]);
 return <dialog ref={ref} className="file-preview" onCancel={event=>{event.preventDefault();onClose()}}><button id="n-hint" data-hint="Native hint">i</button><button id="n-toast" onClick={onToast}>toast</button></dialog>;
}
function Harness(){
 const [sheet,setSheet]=useState(false),[inner,setInner]=useState(false),[locked,setLocked]=useState(false),[confirm,setConfirm]=useState(false),[remote,setRemote]=useState(false),[native,setNative]=useState(false),[toast,setToast]=useState(false),[restart,setRestart]=useState(false);
 const keep=useRef<HTMLButtonElement>(null);
 // A non-Radix modal behind, like the settings window: its own document Escape listener that honours defaultPrevented.
 useEffect(()=>{const key=(event:KeyboardEvent)=>{if(event.key==='Escape'&&!event.defaultPrevented)w.__paneEscapes++};document.addEventListener('keydown',key);return()=>document.removeEventListener('keydown',key)},[]);
 return <div className="workspace"><ActionHints/><aside className="pane" role="dialog" aria-modal="true" aria-label="Pane">
  <button id="open-sheet" onClick={()=>setSheet(true)}>Sheet</button><button id="open-confirm" onClick={()=>setConfirm(true)}>Confirm</button><button id="open-remote" onClick={()=>setRemote(true)}>Remote</button><button id="open-native" onClick={()=>setNative(true)}>Native</button><button id="open-restart" onClick={()=>setRestart(true)}>Restart</button><button id="show-toast" onClick={()=>setToast(true)}>Toast</button>
  {/* Where the two confirmations used to render as native <dialog>s: inside a settings page, in the settings shell. */}
  <div className="settings-shell"><div className="settings-page-body"><div className="settings-section"><h3 id="ref-h3">Reference heading</h3><button className="primary-button" id="ref-primary">Reference</button><button id="ref-plain">Reference</button></div></div></div>
 </aside>
 {sheet&&<AdminSheet title="Synthetic sheet" onClose={()=>{w.__closed.push('sheet');setSheet(false)}} locked={locked}><input id="field" aria-label="Field"/><button id="open-inner" onClick={()=>setInner(true)}>Inner</button><button id="lock" onClick={()=>setLocked(value=>!value)}>{locked?'Unlock':'Lock'}</button>
  {inner&&<ModalDialog onClose={()=>{w.__closed.push('inner');setInner(false)}} overlayClassName="sheet-overlay"><div className="sheet" role="dialog" aria-label="Inner" tabIndex={-1}><button id="inner-a">A</button><button id="inner-b">B</button></div></ModalDialog>}
 </AdminSheet>}
 {confirm&&<ConfirmDialog onClose={()=>setConfirm(false)} initialFocus={keep} overlayClassName="settings-leave-overlay"><div className="settings-leave-dialog" aria-labelledby="c-title"><h3 id="c-title">Synthetic confirmation</h3><div className="settings-leave-actions"><button ref={keep} id="c-keep" onClick={()=>setConfirm(false)}>Keep</button><button id="c-go">Go</button></div></div></ConfirmDialog>}
 {remote&&<ModalDialog onClose={()=>setRemote(false)} closeOnEscape={false} closeOnOutside={false} overlayClassName="mcp-oauth-overlay"><div className="mcp-oauth-dialog" role="dialog" aria-label="Remote" tabIndex={-1}><button id="r-cancel" onClick={()=>setRemote(false)}>Cancel</button>
  <textarea id="r-keys" aria-label="Remote keyboard" onKeyDown={event=>{event.stopPropagation();if(event.key==='Tab'||event.key==='Escape'){event.preventDefault();w.__remote.push(event.key)}}}/></div></ModalDialog>}
 {restart&&<ConfirmDialog onClose={()=>setRestart(false)} overlayClassName="resource-restart-overlay"><div className="resource-restart-dialog" aria-labelledby="rr-title"><h3 id="rr-title">Synthetic restart</h3><p>Synthetic impact.</p><div className="resource-restart-actions"><button className="secondary-button" onClick={()=>setRestart(false)}>Cancel</button><button className="primary-button" id="rr-primary">Restart</button></div></div></ConfirmDialog>}
 {native&&<Native onClose={()=>setNative(false)} onToast={()=>setToast(true)}/>}
 {/* The app's own workspace toast (WorkspaceToast), rendered like App.tsx does: in the workspace, outside any dialog. */}
 {toast&&<WorkspaceToast onRetry={()=>{w.__retries=(w.__retries||0)+1}}/>}
 </div>;
}
void i18nReady.then(()=>createRoot(document.getElementById('root')!).render(<Harness/>));
`);
  server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await pw.chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
  const openHarness = async () => {
    const context = await browser.newContext({ viewport: { width: 1180, height: 820 }, locale: "en-US", reducedMotion: "reduce" });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error" && !/Download the React DevTools/.test(message.text())) errors.push(message.text()); });
    await page.goto(`${origin}/${basename(harness)}/index.html`);
    await page.locator("#open-sheet").waitFor();
    return { page, context, errors };
  };
  const active = page => page.evaluate(() => document.activeElement?.id || document.activeElement?.getAttribute("aria-label") || document.activeElement?.tagName);

  await check("stacked dialogs: each Escape closes only the top one, focus returns layer by layer, the modal behind never sees it", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#open-sheet").click();
    await page.locator(".admin-sheet").waitFor();
    truthy(await page.evaluate(() => document.activeElement?.classList.contains("admin-sheet")), `focus starts on ${await active(page)}, not the sheet`);
    // An Escape in the very first frame of a new dialog must not close the one underneath (Radix registers the new
    // top layer in an effect); ModalDialog drops that key.
    await page.evaluate(() => new Promise(resolve => {
      const observer = new MutationObserver(() => {
        if (!document.querySelector('[aria-label="Inner"]')) return;
        observer.disconnect();
        document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
        resolve();
      });
      observer.observe(document.body, { childList: true, subtree: true });
      const button = document.getElementById("open-inner");
      button.focus();
      button.click();
    }));
    await page.waitForTimeout(150);
    truthy(await page.locator(".admin-sheet").count() === 1, `an Escape in the inner dialog's first frame closed the sheet under it (onClose calls: ${JSON.stringify(await page.evaluate(() => window.__closed))})`);
    if (!await page.locator('[aria-label="Inner"]').count()) await page.locator("#open-inner").click();
    await page.locator('[aria-label="Inner"]').waitFor();
    await page.waitForTimeout(100);
    const hidden = await page.evaluate(() => ({ outer: document.querySelector(".admin-sheet").closest("[aria-hidden=true]") !== null, root: document.getElementById("root").getAttribute("aria-hidden") }));
    truthy(hidden.outer && hidden.root === "true", `under the inner dialog the sheet and the page must be hidden from screen readers: ${JSON.stringify(hidden)}`);
    await page.keyboard.press("Escape");
    await page.locator('[aria-label="Inner"]').waitFor({ state: "detached", timeout: 2000 });
    truthy(await page.locator(".admin-sheet").count() === 1, `the first Escape also closed the sheet (onClose calls: ${JSON.stringify(await page.evaluate(() => window.__closed))})`);
    await page.waitForFunction(() => document.activeElement?.id === "open-inner", null, { timeout: 2000 }).catch(() => {});
    truthy(await active(page) === "open-inner", `focus went to ${await active(page)}, not back to the Inner button`);
    await page.keyboard.press("Escape");
    await page.locator(".admin-sheet").waitFor({ state: "detached", timeout: 2000 });
    await page.waitForFunction(() => document.activeElement?.id === "open-sheet", null, { timeout: 2000 }).catch(() => {});
    truthy(await active(page) === "open-sheet", `focus went to ${await active(page)}, not back to the Sheet button`);
    truthy(await page.evaluate(() => window.__paneEscapes) === 0, "the modal behind handled an Escape meant for a dialog");
    await page.keyboard.press("Escape");
    truthy(await page.evaluate(() => window.__paneEscapes) === 1, "control: with no dialog above it the modal behind should see Escape");
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("Tab and Shift+Tab stay inside the sheet; a locked sheet ignores Escape and the scrim", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#open-sheet").click();
    await page.locator(".admin-sheet").waitFor();
    for (const key of [...Array(6).fill("Tab"), ...Array(6).fill("Shift+Tab")]) {
      await page.keyboard.press(key);
      truthy(await page.evaluate(() => Boolean(document.querySelector(".admin-sheet")?.contains(document.activeElement))), `${key} left the sheet for ${await active(page)}`);
    }
    await page.locator("#lock").click();
    await page.keyboard.press("Escape");
    await page.mouse.click(6, 6);
    await page.waitForTimeout(150);
    truthy(await page.locator(".admin-sheet").count() === 1, "the locked sheet closed");
    await page.locator("#lock").click();
    await page.mouse.click(6, 6);
    await page.locator(".admin-sheet").waitFor({ state: "detached", timeout: 2000 });
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("confirmation (AlertDialog): role alertdialog, aria-modal, focus on the safe choice, the scrim does not close it, Escape does, no focus ring after a mouse round trip", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#open-confirm").click();
    await page.locator(".settings-leave-dialog").waitFor();
    const state = await page.evaluate(() => { const d = document.querySelector(".settings-leave-dialog"); return { role: d.getAttribute("role"), modal: d.getAttribute("aria-modal"), focus: document.activeElement?.id }; });
    truthy(state.role === "alertdialog" && state.modal === "true" && state.focus === "c-keep", JSON.stringify(state));
    await page.mouse.click(6, 6);
    await page.waitForTimeout(150);
    truthy(await page.locator(".settings-leave-dialog").count() === 1, "a press on the scrim closed the confirmation");
    await page.keyboard.press("Escape");
    await page.locator(".settings-leave-dialog").waitFor({ state: "detached", timeout: 2000 });
    await page.waitForFunction(() => document.activeElement?.id === "open-confirm", null, { timeout: 2000 }).catch(() => {});
    truthy(await active(page) === "open-confirm", `focus went to ${await active(page)}`);
    const ring = await page.evaluate(focusRingProbe);
    truthy(!ring.ring, `after a mouse-opened dialog closes, ${ring.focused} draws ${ring.ring?.ring}`);
    truthy(await page.evaluate(() => window.__paneEscapes) === 0, "the modal behind handled the confirmation's Escape");
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("portaled out of the settings page, the two confirmations keep the look they had in it (fonts, button metrics)", async () => {
    const { page, context, errors } = await openHarness();
    const look = (selector, props) => page.evaluate(([selector, props]) => { const cs = getComputedStyle(document.querySelector(selector)); return Object.fromEntries(props.map(p => [p, cs.getPropertyValue(p)])); }, [selector, props]);
    const FONT = ["font-family", "font-size", "font-weight", "line-height"], BUTTON = ["font-size", "min-height", "padding", "border-radius"];
    const ref = { h3: await look("#ref-h3", FONT), primary: await look("#ref-primary", BUTTON), plain: await look("#ref-plain", ["font-family"]) };
    await page.locator("#open-restart").click();
    await page.locator(".resource-restart-dialog").waitFor();
    const h3 = await look("#rr-title", FONT), primary = await look("#rr-primary", BUTTON);
    truthy(JSON.stringify(h3) === JSON.stringify(ref.h3), `restart heading ${JSON.stringify(h3)} != settings-page heading ${JSON.stringify(ref.h3)}`);
    truthy(JSON.stringify(primary) === JSON.stringify(ref.primary), `restart button ${JSON.stringify(primary)} != settings-page button ${JSON.stringify(ref.primary)}`);
    await page.keyboard.press("Escape");
    await page.locator("#open-confirm").click();
    await page.locator(".settings-leave-dialog").waitFor();
    const plain = await look("#c-keep", ["font-family"]);
    truthy(JSON.stringify(plain) === JSON.stringify(ref.plain), `leave-dialog button font ${JSON.stringify(plain)} != settings-shell button font ${JSON.stringify(ref.plain)}`);
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("the workspace toast above an open sheet stays clickable, and pressing it does not close the sheet", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#show-toast").click();
    await page.locator("#open-sheet").click();
    await page.locator(".admin-sheet").waitFor();
    await page.waitForTimeout(100);
    await page.locator(".workspace-toast button").click({ timeout: 3000 });
    await page.waitForTimeout(150);
    const state = await page.evaluate(() => ({ retries: window.__retries ?? 0, open: Boolean(document.querySelector(".admin-sheet")) }));
    truthy(state.retries === 1, `Retry on the toast did not run (${state.retries})`);
    truthy(state.open, "pressing the toast closed the sheet");
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("a dialog whose keyboard belongs to a remote computer: Tab and Escape reach the receiver, nothing closes", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#open-remote").click();
    await page.locator("#r-keys").click();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(150);
    const state = await page.evaluate(() => ({ focus: document.activeElement?.id, keys: window.__remote, open: Boolean(document.querySelector('[aria-label="Remote"]')), pane: window.__paneEscapes }));
    truthy(state.open, "Escape closed the dialog");
    truthy(state.focus === "r-keys", `Tab moved focus to ${state.focus}`);
    truthy(JSON.stringify(state.keys) === '["Tab","Escape"]', `the receiver saw ${JSON.stringify(state.keys)}`);
    truthy(state.pane === 0, "the modal behind handled the remote Escape");
    await page.locator("#r-cancel").click();
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });

  await check("tooltip and toast shown over a native <dialog> (top layer) render inside it and are on top (Z7)", async () => {
    const { page, context, errors } = await openHarness();
    await page.locator("#open-native").click();
    await page.locator("dialog[open]").waitFor();
    await page.locator("#n-toast").click();
    await page.locator(".workspace-toast").waitFor();
    await page.locator("#n-hint").hover();
    await page.locator(".action-hint").waitFor({ timeout: 3000 });
    const placed = await page.evaluate(() => {
      const top = el => { const r = el.getBoundingClientRect(); const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return hit === el || el.contains(hit); };
      const hint = document.querySelector(".action-hint"), toast = document.querySelector(".workspace-toast");
      hint.style.pointerEvents = "auto"; // hit-testing skips pointer-events:none

      return { hintIn: Boolean(hint.closest("dialog[open]")), toastIn: Boolean(toast.closest("dialog[open]")), hintTop: top(hint), toastTop: top(toast) };
    });
    truthy(placed.hintIn && placed.hintTop, `the hint is not inside the dialog or not on top: ${JSON.stringify(placed)}`);
    truthy(placed.toastIn && placed.toastTop, `the toast is not inside the dialog or not on top: ${JSON.stringify(placed)}`);
    await page.keyboard.press("Escape");
    await page.locator("dialog[open]").waitFor({ state: "detached", timeout: 2000 });
    await page.waitForFunction(() => { const toast = document.querySelector(".workspace-toast"); return toast && !toast.closest("dialog"); }, null, { timeout: 2000 }).catch(() => {});
    truthy(await page.evaluate(() => { const toast = document.querySelector(".workspace-toast"); return Boolean(toast && !toast.closest("dialog")); }), "the toast did not come back to the page when the dialog closed");
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });
} finally {
  await browser?.close();
  await server?.close();
  await rm(harness, { recursive: true, force: true });
}
}

if (failures.length) { console.log(`dialogs: FAIL (${failures.length} of ${results.length + failures.length})`); process.exit(1); }
console.log(`dialogs: PASS (${results.length} checks)`);
