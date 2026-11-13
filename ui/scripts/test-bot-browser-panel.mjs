// Real workspace, synthetic API and preload only; native view drawing belongs to tofi-app.
import assert from 'node:assert/strict';
import {installStubs, startUi, launch} from './computer-window-fixture.mjs';
import {source} from './test-bot-browser-pip.mjs';

const {server, origin} = await startUi();
let browser;
try {
  browser = await launch();
  const page = await browser.newPage({viewport: {width: 1440, height: 900}, locale: 'en-US'});
  const iso = ms => new Date(Date.now() - ms).toISOString();
  const messages = [{id: 'u-live', seq: 1, conversation_id: 'conv-synthetic', role: 'user', content: 'Open the page.', created_at: iso(9000), run_id: 'run-live'}];
  const runs = [{id: 'run-live', bot_id: 'bot-synthetic', conversation_id: 'conv-synthetic', origin_conversation_id: 'conv-synthetic', queue_seq: 1, model: 'synthetic-model', status: 'running', trigger_message_id: 'u-live', created_at: iso(8000), updated_at: iso(0)}];
  const tools = [{conversation_id: 'conv-synthetic', bot_id: 'bot-synthetic', run_id: 'run-live', call_id: 'live-1', name: 'computer_browser', arguments: JSON.stringify({computer_id: 'mac-synthetic', action: 'navigate'}), result: '', status: 'running', truncated: false, started_at: iso(4000), updated_at: iso(0)}];
  await installStubs(page, {info: {state: 'ready'}, capture: 'image', requests: [], messages});
  await page.route('**/api/**', route => {
    const p = new URL(route.request().url()).pathname;
    const json = body => route.fulfill({contentType: 'application/json', body: JSON.stringify(body)});
    if (p === '/api/server-info') return json({service: 'tofi', protocol_version: 1, instance_id: '00000000-0000-4000-8000-000000000001', auth: {mode: 'none'}, bot_browser: {version: 1, upload: 'websocket-json', view: 'ndjson-jpeg'}});
    if (p.endsWith('/runs')) return json({runs});
    if (p.endsWith('/tools')) return json({activities: tools, summaries: [], tool_count: 1, has_more: false});
    if (p === '/api/config') return json({computer: {backend: 'none', available: false, capabilities: []}});
    if (p === '/api/browser-sources') return json({v: 1, sources: [source()]});
    return route.fallback();
  });
  await page.addInitScript(() => {
    const off = () => () => {};
    let listener;
    window.__panelCalls = [];
    window.__popupCalls = [];
    window.open = (...args) => { window.__popupCalls.push(args); return null; };
    window.__panelPush = state => listener?.(state);
    window.tofiDesktop = {
      platform: 'darwin', themePreference: 'light', onCommand: off, onWindowState: off, onFlushState: off,
      appearance: async () => {}, remoteInputFocus: () => {}, readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
      botBrowser: {
        panel: async open => { window.__panelCalls.push(open); if (window.__panelFail) throw new Error(window.__panelFail); },
        panelState: async () => ({open: true, width: 400}),
        onPanel: fn => { listener = fn; return () => { if (listener === fn) listener = undefined; }; },
      },
    };
  });
  await page.goto(origin);
  const toggle = page.getByRole('button', {name: 'Bot browser', exact: true});
  await toggle.waitFor();
  await page.waitForFunction(() => document.querySelector('[data-native-browser-toggle]')?.getAttribute('aria-pressed') === 'true');
  // The Mac app narrows this page's viewport while the panel is open: the page itself reserves no room.
  const fills = () => page.evaluate(() => Math.round(document.querySelector('.workspace').getBoundingClientRect().width) === innerWidth && !document.documentElement.hasAttribute('data-native-side-panel'));
  assert.equal(await fills(), true, 'the page fills its own viewport');
  await toggle.click();
  assert.deepEqual(await page.evaluate(() => window.__panelCalls), [false]);
  assert.equal(await toggle.getAttribute('aria-pressed'), 'true', 'requests wait for native state');
  await page.evaluate(() => window.__panelPush({open: false, width: 400}));
  await page.waitForFunction(() => document.querySelector('[data-native-browser-toggle]').getAttribute('aria-pressed') === 'false');
  await toggle.click();
  assert.deepEqual(await page.evaluate(() => window.__panelCalls), [false, true]);
  await page.evaluate(() => window.__panelPush({open: true, width: 520}));
  await page.waitForFunction(() => document.querySelector('[data-native-browser-toggle]').getAttribute('aria-pressed') === 'true');
  assert.equal(await fills(), true);

  // A panel that cannot open says why, in words, and the message goes away by itself.
  await page.evaluate(() => { window.__panelFail = "Error invoking remote method 'desktop:bot-browser-panel': Error: no_connection"; window.__panelPush({open: false, width: 0}); });
  await page.waitForFunction(() => document.querySelector('[data-native-browser-toggle]').getAttribute('aria-pressed') === 'false');
  await toggle.click();
  const failure = page.locator('[data-browser-panel-failure]');
  await failure.waitFor();
  assert.equal(await failure.getAttribute('data-browser-panel-failure'), 'no_connection');
  assert.match(await failure.innerText(), /still connecting to the server/);
  await page.evaluate(() => { window.__panelFail = 'Error: browser_not_enabled'; });
  await toggle.click();
  await page.waitForFunction(() => document.querySelector('[data-browser-panel-failure]')?.textContent.includes('Enable the Bot browser'));
  await page.evaluate(() => { window.__panelFail = null; });
  await failure.waitFor({state: 'detached', timeout: 8000});
  await page.evaluate(() => { window.__panelCalls = []; });

  await page.evaluate(() => window.__panelPush({open: false, width: 0}));
  const look = page.locator('.computer-look').first();
  await look.waitFor();
  await look.click();
  assert.deepEqual(await page.evaluate(() => window.__panelCalls), [true], 'mac_browser look opens the panel');
  assert.equal(await page.locator('.bot-browser-panel').count(), 0);
  assert.equal(await page.locator('.desktop-floating-shell').count(), 0);
  assert.deepEqual(await page.evaluate(() => window.__popupCalls), []);
  console.log('PASS native panel mount, push, toggle, named failure, no page-side reservation');

  // The Bot computer in the Mac app is the same widget as on the web: docked in the widget column, never a separate window.
  // Below 1100 px it floats inside the window like the web mini window.
  for (const [width, docked] of [[1440, true], [1000, false]]) {
    const vm = await browser.newPage({viewport: {width, height: 900}, locale: 'en-US'});
    await installStubs(vm, {info: {state: 'ready'}, capture: 'image', requests: []});
    await vm.addInitScript(() => {
      const off = () => () => {};
      window.__popupCalls = [];
      window.open = (...args) => { window.__popupCalls.push(args); return null; };
      window.tofiDesktop = {
        platform: 'darwin', themePreference: 'light', onCommand: off, onWindowState: off, onFlushState: off,
        appearance: async () => {}, remoteInputFocus: () => {}, readWorkspaceState: async () => ({}), writeWorkspaceState: async () => {},
        botBrowser: {panel: async () => {}, panelState: async () => ({open: false, width: 0}), onPanel: () => () => {}},
      };
    });
    await vm.goto(origin);
    if (width < 1100) await vm.locator('.conversation-row').first().click().catch(() => {});
    await vm.locator('[data-computer-toggle]').first().click();
    await vm.locator('.desktop-floating-shell').waitFor();
    const placed = await vm.evaluate(() => {
      const shell = document.querySelector('.desktop-floating-shell');
      const box = shell.getBoundingClientRect();
      return {docked: shell.classList.contains('is-docked'), inColumn: Boolean(shell.closest('.widget-column')), inside: box.left >= 0 && box.top >= 0 && box.right <= innerWidth && box.bottom <= innerHeight, popups: window.__popupCalls.length};
    });
    assert.deepEqual(placed, {docked, inColumn: docked, inside: true, popups: 0}, `Bot computer at ${width} px`);
    if (docked) {
      // Press and drag the screen: the widget column's own reorder engine lifts it (no window drag, no popup).
      await vm.locator('[data-desktop-frame].desktop-live-screen').waitFor({timeout: 15000});
      const screen = await vm.evaluate(() => { const e = document.querySelector('.widget-column .desktop-enlarge'); const r = e?.getBoundingClientRect(); return r && {x: r.x, y: r.y, width: r.width, height: r.height, n: document.querySelectorAll('.desktop-enlarge').length}; });
      assert.ok(screen?.width > 0, `the docked screen is pressable: ${JSON.stringify(screen)}`);
      await vm.mouse.move(screen.x + screen.width / 2, screen.y + screen.height / 2);
      await vm.mouse.down();
      await vm.mouse.move(screen.x + screen.width / 2, screen.y + screen.height / 2 + 40, {steps: 5});
      assert.equal(await vm.evaluate(() => document.documentElement.hasAttribute('data-widget-dragging') && Boolean(document.querySelector('.widget-column [data-dragging] .desktop-floating-shell'))), true, 'the docked computer lifts for a drag');
      await vm.keyboard.press('Escape');
      await vm.mouse.up();
    }
    await vm.close();
  }
  console.log('PASS Mac app Bot computer: docked widget in the column, in-window mini window below 1100 px, no popup');
} finally {
  await browser?.close();
  await server.close();
}
