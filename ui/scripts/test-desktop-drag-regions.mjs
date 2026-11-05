// In the macOS desktop app, `-webkit-app-region: drag` areas move the window and swallow clicks. Electron builds the
// drag region from the page in layout-tree order (Blink LocalFrameView::CollectDraggableRegions): each `drag` box is added,
// each `no-drag` box is cut out; elements without an app-region change nothing, so a panel drawn over the header is still
// a drag area under it (2026-11-04: the enlarged Bot computer's close button sat in the header strip and could not be
// clicked in the app, while the browser was fine).
// In the app's Chromium app-region is INHERITED (Blink's window-drag surrogate), pseudo-elements included, and an element's
// ::after comes after all its children in that order. rc.4 set drag on .chat-header, whose ::after blur layer then covered
// every header button again (2026-11-05; confirmed with ELECTRON_DEBUG_DRAGGABLE_REGIONS in Electron 44 / Chromium 152:
// one solid 1280x94 drag block). Playwright's older Chromium does not inherit it, so the walk below applies the
// inheritance itself and includes ::before/::after; the conversation check also asserts the header still moves the window.
// Do not set `app-region: none` to stop the inheritance: Electron 44 treats it as no-drag. Keep drag off elements that
// have pseudo-element layers instead (the chat header drags through its own .chat-header-drag strip).
//
// This mounts the real workspace with the desktop bridge stubbed, opens every surface that can cover the title strips,
// rebuilds the drag region the way Electron does, and fails when a visible control is inside it.
import assert from "node:assert/strict";
import { installStubs, startUi, launch } from "./computer-window-fixture.mjs";

// 1110 px: the drawer still overlays the chat (up to 1120 px) while the layout already counts as wide (from 1100 px).
// 1200 px: between 1121 and 1279 the old drawer track rule applied to Settings.
const SIZES = [{ width: 1280, height: 800 }, { width: 1200, height: 760 }, { width: 1110, height: 760 }, { width: 860, height: 640 }];

const { server, origin } = await startUi();
const browser = await launch();
const failures = [];
let checked = 0;

async function open(size) {
  const page = await browser.newPage({ viewport: size });
  const state = { info: { kind: "firecracker", state: "hibernated", workspace_root: "/workspace", browser: "Google Chrome", hibernation: true }, requests: [] };
  await installStubs(page, state);
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    const json = body => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    const allocation = { vcpus: 4, memory_mib: 8192, disk_gib: 40 };
    if (path === "/api/computer/resources") return json({ current: allocation, desired: allocation, pending: false, state: "ready", host: { cpus: 16, memory_total_mib: 65536, memory_available_mib: 32768, disk_available_gib: 500 }, limits: { max_vcpus: 8, max_memory_mib: 16384, max_disk_gib: 100 } });
    return route.fallback();
  });
  await page.addInitScript(() => {
    const off = () => () => {};
    window.tofiDesktop = {
      platform: "darwin", themePreference: "light",
      onCommand: off, onWindowState: off, onFlushState: off,
      appearance: async () => {}, requestMicrophoneAccess: async () => false,
      getPermissionStatus: async () => ({ supported: true, microphone: "not-determined", screen: "denied", accessibility: false }),
      requestPermission: async () => false, openPrivacySettings: async () => true, openDesktopSettings: async () => true,
      startAccessibilityDrag: () => {}, remoteInputFocus: () => {},
      readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
      prepareAttachment: async () => ({ handle: "h", name: "n" }), saveAttachment: async () => true, revealAttachment: async () => {}, dragAttachment: () => {},
    };
  });
  await page.goto(origin + "/");
  await page.locator(".chat-header").waitFor();
  assert.equal(await page.evaluate(() => document.documentElement.dataset.desktop), "darwin", "the workspace runs as the macOS desktop app");
  return page;
}

// Controls that a person can see and reach (the topmost element at their centre), sampled at five points each, tested
// against the drag region rebuilt from every element's computed app-region in document order.
async function blocked(page) {
  return page.evaluate(() => {
    const regions = [];
    const modeOf = style => style.webkitAppRegion || style.getPropertyValue("-webkit-app-region");
    const add = (mode, box) => { if ((mode === "drag" || mode === "no-drag") && box.width && box.height) regions.push({ drag: mode === "drag", box }); };
    // A rendered ::before/::after: its own box when it is positioned with resolved sizes, else (pessimistically) its element's box.
    // Inherited mode: an element's own drag/no-drag, else its parent's (what the app's Chromium computes).
    const own = style => { const mode = modeOf(style); return mode === "drag" || mode === "no-drag" ? mode : null; };
    const pseudo = (element, which, inherited) => {
      const style = getComputedStyle(element, which);
      if (style.content === "none" || style.content === "normal" || /^(none|inline|contents)$/.test(style.display) || style.visibility !== "visible") return;
      const box = element.getBoundingClientRect();
      const px = value => (/^-?[\d.]+px$/.test(value) ? parseFloat(value) : null);
      const [left, top, width, height] = [px(style.left), px(style.top), px(style.width), px(style.height)];
      const placed = /absolute|fixed/.test(style.position) && left !== null && top !== null && width && height;
      add(own(style) ?? inherited, placed ? new DOMRect(box.left + left, box.top + top, width, height) : box);
    };
    const walk = (element, parentMode) => {
      const style = getComputedStyle(element);
      const mode = own(style) ?? parentMode;
      // Blink only adds boxes (not inline or display:contents) that are visible; their children are still walked.
      if (style.visibility === "visible" && !/^(inline|contents)$/.test(style.display)) add(mode, element.getBoundingClientRect());
      pseudo(element, "::before", mode);
      for (const child of element.children) walk(child, mode);
      pseudo(element, "::after", mode);
    };
    for (const child of document.body.children) walk(child, null);
    const inDrag = (x, y) => {
      let drag = false;
      for (const { drag: add, box } of regions) if (x >= box.left && x < box.right && y >= box.top && y < box.bottom) drag = add;
      return drag;
    };
    const controls = document.querySelectorAll("button, a[href], input, select, textarea, [role=button], [role=menuitem], [role=switch], [role=tab], [role=option]");
    const out = [];
    for (const control of controls) {
      const box = control.getBoundingClientRect();
      if (box.width < 4 || box.height < 4 || control.closest("[inert], [aria-hidden=true]")) continue;
      const cx = box.left + box.width / 2, cy = box.top + box.height / 2;
      if (cx < 0 || cy < 0 || cx >= innerWidth || cy >= innerHeight) continue;
      const top = document.elementFromPoint(cx, cy);
      if (!top || !(top === control || control.contains(top))) continue;
      const points = [[cx, cy], [box.left + 3, box.top + 3], [box.right - 3, box.top + 3], [box.left + 3, box.bottom - 3], [box.right - 3, box.bottom - 3]];
      const hit = points.filter(([x, y]) => inDrag(x, y) && (document.elementFromPoint(x, y) === control || control.contains(document.elementFromPoint(x, y))));
      if (hit.length) out.push(`${control.getAttribute("aria-label") || control.textContent.trim().slice(0, 40) || control.className} @${Math.round(box.left)},${Math.round(box.top)}`);
    }
    // The chat header must still move the window: the empty middle between its two pills is drag, the message list just under it is not.
    const header = document.querySelector(".chat-header");
    let headerDrag = null;
    if (header && header.getBoundingClientRect().width) {
      const h = header.getBoundingClientRect(), title = header.querySelector(".chat-title").getBoundingClientRect(), actions = header.querySelector(".header-actions").getBoundingClientRect();
      const y = h.top + h.height / 2;
      headerDrag = { middle: inDrag((title.right + actions.left) / 2, y), below: inDrag((title.right + actions.left) / 2, h.bottom + 12) };
    }
    return { out, total: controls.length, drags: regions.filter(region => region.drag).length, headerDrag };
  });
}

async function check(page, surface, size) {
  // Panels finish their entrance motion (finite animations only; spinners loop forever).
  await page.waitForFunction(() => document.getAnimations().every(a => a.playState !== "running" || a.effect?.getTiming().iterations === Infinity), null, { timeout: 5000 });
  const { out, drags, headerDrag } = await blocked(page);
  // Without a drag box the check would pass on nothing (a rule that stopped applying, or a renamed header).
  assert.ok(drags > 0, `${size.width}x${size.height} ${surface}: no macOS drag area found; the check would be empty`);
  if (surface === "conversation") {
    if (!headerDrag?.middle) out.push("the empty middle of the chat header does not move the window");
    if (headerDrag?.below) out.push("the message list just under the chat header moves the window");
  }
  checked++;
  for (const label of out) failures.push(`${size.width}x${size.height} ${surface}: ${label}`);
  console.log(`${out.length ? "FAIL" : "PASS"} ${size.width}x${size.height} ${surface}${out.length ? ": " + out.join("; ") : ""}`);
}

for (const size of SIZES) {
  let page = await open(size);
  await check(page, "conversation", size);

  await page.getByRole("button", { name: /^Bot computer/ }).first().click();
  await page.locator(".computer-hide-button, [aria-label='Close Bot computer']").first().waitFor();
  await check(page, "Bot computer card", size);

  await page.locator("[aria-label='Enlarge Bot computer']").last().click();
  await page.locator("[aria-label='Shrink Bot computer']").first().waitFor();
  await check(page, "enlarged Bot computer", size);
  await page.locator("[aria-label='Close Bot computer']").first().click();

  // The tasks drawer: a column beside the chat when wide, a panel over the chat (and its header) below 1120 px.
  await page.getByRole("button", { name: "Tasks" }).first().click();
  await page.locator(".detail-pane.visible").first().waitFor();
  await check(page, "tasks drawer", size);
  await page.getByRole("button", { name: "Tasks" }).first().click({ force: true }).catch(() => {});
  await page.keyboard.press("Escape");
  await page.locator(".detail-pane.visible").first().waitFor({ state: "detached", timeout: 3000 }).catch(() => {});

  await page.getByRole("button", { name: "More actions" }).click();
  await page.getByRole("menu").first().waitFor();
  await check(page, "More actions menu", size);
  await page.keyboard.press("Escape");

  // A native <dialog> has no role attribute (the image viewer, AttachmentLightbox, is one); its close button sits at the top.
  await page.evaluate(() => {
    const dialog = document.createElement("dialog");
    dialog.className = "attachment-lightbox";
    dialog.style.cssText = "position:fixed;inset:8px auto auto 50%;transform:translateX(-50%);margin:0;padding:12px;width:420px;height:300px";
    const close = document.createElement("button");
    close.type = "button"; close.setAttribute("aria-label", "Close image");
    close.style.cssText = "width:36px;height:36px";
    dialog.append(close);
    document.body.append(dialog);
    dialog.showModal();
  });
  await check(page, "native <dialog> (image viewer)", size);
  await page.evaluate(() => { const dialog = document.querySelector("dialog.attachment-lightbox"); dialog.close(); dialog.remove(); });

  await page.getByRole("button", { name: "Search" }).first().click();
  await page.getByRole("dialog").first().waitFor();
  await check(page, "search", size);
  await page.keyboard.press("Escape");
  await page.close();

  for (const tab of ["computer", "devices", "general"]) {
    page = await open(size);
    const panes = () => page.evaluate(() => [".contact-pane", ".chat-pane"].map(s => { const b = document.querySelector(s)?.getBoundingClientRect(); return b ? `${Math.round(b.left)}+${Math.round(b.width)}` : "none"; }).join(" "));
    const before = await panes();
    await page.goto(`${origin}/settings/${tab}`);
    await page.locator(".settings-tabs").waitFor();
    await check(page, `settings › ${tab}`, size);
    // Settings floats over the workspace; it must not make the grid open a drawer track (2026-11-05: the chat shrank by 336 px on desktop).
    const after = await panes();
    assert.equal(after, before, `${size.width}x${size.height} settings › ${tab}: the sidebar and chat keep their place (${before} -> ${after})`);
    const win = await page.evaluate(() => { const b = document.querySelector(".detail-pane:has(.settings-tabs)").getBoundingClientRect(); return { w: b.width / innerWidth, h: b.height / innerHeight, left: b.left, top: b.top }; });
    assert.ok(win.w <= 0.885 && win.h <= 0.865 && win.left >= 48 && win.top >= 40, `${size.width}x${size.height} settings › ${tab}: the window sits inside the viewport, clear of the traffic lights (${JSON.stringify(win)})`);
    await page.close();
  }
}

// The Mac settings page is its own document: it needs a strip the window can be dragged by, and nothing on it may sit in that strip.
for (const size of SIZES) {
  const page = await browser.newPage({ viewport: size });
  await page.addInitScript(() => {
    const status = { name: "Mac", workspaceRoot: "/Users/demo/Tofi", fileAccess: true, screenCapture: true, desktopControl: true, paired: false, permissions: { screen: false, accessibility: false },
      sandbox: { enabled: false, available: true, bound: false, root: null, connectionID: null, running: false }, bots: [{ bot_id: "b1", bot_name: "Inbox", decision: "ask" }], control: { phase: "idle" } };
    const same = async () => JSON.parse(JSON.stringify(status));
    window.tofiDesktop = { platform: "darwin", themePreference: "light", getSettings: same, openPermissionGuide: async () => true, updateSettings: same, chooseWorkspace: async () => null,
      setBotDecision: same, controlStop: same, controlResume: same, pair: same, relaunch: async () => true, chooseSandboxRoot: async () => null, authorizeSandboxRoot: same, sandboxRevoke: same };
  });
  await page.route(url => new URL(url).pathname.startsWith("/api/"), route => route.abort());
  await page.goto(origin + "/desktop-settings.html");
  await page.locator("#mac-name").waitFor();
  const strip = await page.evaluate(() => { const el = document.querySelector(".mac-settings-titlebar"); const box = el?.getBoundingClientRect(); return el && getComputedStyle(el).webkitAppRegion === "drag" && box.width === innerWidth && box.height >= 24; });
  assert.ok(strip, `${size.width}x${size.height}: the Mac settings page has a full-width drag strip`);
  await check(page, "Mac settings page", size);
  for (let y = 300; y < 3000; y += 300) { // scrolled content passes under the strip
    await page.evaluate(top => { for (const el of document.querySelectorAll("*")) if (el.scrollHeight > el.clientHeight + 20 && /auto|scroll/.test(getComputedStyle(el).overflowY)) el.scrollTop = top; }, y);
    await check(page, `Mac settings page scrolled ${y}px`, size);
  }
  await page.close();
}

await browser.close();
await server.close();
assert.ok(checked >= 27, `checked ${checked} surfaces`);
if (failures.length) {
  console.error(`\n${failures.length} drag-area problem(s) in the desktop app (controls that cannot be clicked, or a header that cannot move the window):\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
console.log(`PASS ${checked} surfaces: no visible control inside a macOS drag area`);
