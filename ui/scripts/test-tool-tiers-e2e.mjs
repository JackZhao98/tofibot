// End-to-end acceptance for MCP tool tiers (docs/agent-plan/mcp-tool-tiers.md).
//
// Real: the Server (compiled Go fixture binary: HTTP handler, store, MCP manager,
// tier store, AutoReview pipeline, runtime engine, scheduler) and the real Web UI
// from ui/dist, driven in Chromium and WebKit at 1440x900 and 1180x820.
// Local fakes only: a Notion-like MCP server that records every tools/call, a
// scripted chat model and a scripted v6 reviewer. No credential, no remote host.
//
//   npm --prefix ui run build
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm --prefix ui run test:tool-tiers-e2e
//
// Optional: PLAYWRIGHT_WEBKIT_MODULE (a Playwright whose WebKit build is installed;
// defaults to PLAYWRIGHT_MODULE), WEBKIT_EXECUTABLE, TIERS_E2E_ONLY="chromium-1440x900"
// (comma list of <browser>-<W>x<H>), TIERS_E2E_OUTPUT (default artifacts/tool-tiers-e2e).
// Exit code 0 only when every check passes in every combination.
import {spawn, execFileSync} from 'node:child_process';
import {mkdirSync, writeFileSync, existsSync, readFileSync, rmSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {resolve, dirname} from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '../..');
const out = resolve(process.env.TIERS_E2E_OUTPUT || resolve(repo, 'artifacts/tool-tiers-e2e'));
mkdirSync(out, {recursive: true});
const pwChromium = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const pwWebkit = process.env.PLAYWRIGHT_WEBKIT_MODULE ? await import(process.env.PLAYWRIGHT_WEBKIT_MODULE) : pwChromium;

const BROWSERS = {chromium: pwChromium.chromium, webkit: pwWebkit.webkit};
const VIEWPORTS = [[1440, 900], [1180, 820]];
const only = (process.env.TIERS_E2E_ONLY || '').split(',').filter(Boolean);
const wantScenarios = (process.env.TIERS_E2E_SCENARIOS || '').split(',').filter(Boolean); // e.g. s3,s7 (debugging aid)
const want = key => !wantScenarios.length || wantScenarios.includes(key);

// ---------------------------------------------------------------- results
const results = [];
let combo = '', scenario = '';
function check(name, ok, evidence = '', bug = '') {
  const row = {combo, scenario, name, ok: !!ok, evidence: String(evidence), bug};
  results.push(row);
  console.log(`${ok ? 'PASS' : 'FAIL'}  [${combo}] ${scenario} :: ${name}${ok ? '' : ` -- ${evidence}${bug ? ` (${bug})` : ''}`}`);
  return !!ok;
}
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function waitFor(fn, what, timeout = 40000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try { last = await fn(); if (last) return last; } catch (e) { last = e; }
    await sleep(150);
  }
  throw new Error(`timeout waiting for ${what}: ${last instanceof Error ? last.message : JSON.stringify(last)}`);
}

// ---------------------------------------------------------------- fixture
const testBin = resolve(out, 'tool-tiers-e2e.test');
function buildFixture() {
  execFileSync('go', ['test', '-c', '-o', testBin, './internal/app'], {cwd: repo, stdio: 'inherit'});
}

async function startFixture(profile, tag) {
  const manifest = resolve(out, `${tag}-manifest.json`);
  rmSync(manifest, {force: true});
  const child = spawn(testBin, ['-test.run', '^TestToolTiersE2EFixture$', '-test.v', '-test.count=1'], {
    cwd: resolve(repo, 'internal/app'),
    env: {...process.env, TOFI_TIERS_E2E_MANIFEST: manifest, TOFI_TIERS_E2E_PROFILE: profile, TOFI_TIERS_E2E_LOG: resolve(out, `${tag}-fixture.log`)},
  });
  let log = '';
  child.stdout.on('data', d => { log += d; });
  child.stderr.on('data', d => { log += d; });
  let exited;
  const exit = new Promise(r => child.on('exit', (code, signal) => { exited = {code, signal}; r(exited); }));
  await waitFor(() => { if (exited) throw new Error('fixture exited early: ' + log.slice(-600)); return existsSync(manifest); }, 'fixture manifest', 90000);
  await sleep(100);
  const {origin} = JSON.parse(readFileSync(manifest, 'utf8'));
  const call = async (method, path, body) => {
    const res = await fetch(origin + path, {method, headers: {'Content-Type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body)});
    const text = await res.text();
    let json; try { json = JSON.parse(text); } catch { json = undefined; }
    return {status: res.status, json, text};
  };
  const fx = {
    origin, tag, call,
    state: async () => (await call('GET', '/acceptance/state')).json,
    bot: async name => (await call('POST', '/acceptance/bot', {name})).json,
    sql: async query => { const r = await call('POST', '/acceptance/sql', {query}); if (r.status !== 200) throw new Error(`sql ${r.status}: ${r.text}`); return r.json.rows; },
    tierFile: async () => (await call('GET', '/acceptance/tierfile')).text,
    tiers: async () => (await call('GET', '/api/extensions/mcp/notion/tools')).json,
    async stop() {
      await call('POST', '/acceptance/stop').catch(() => {});
      const code = await Promise.race([exit, sleep(30000).then(() => ({code: 'timeout'}))]);
      writeFileSync(resolve(out, `${tag}-go-test.log`), log);
      return {...code, log};
    },
  };
  return fx;
}

// Counts the fixture's recorded calls since a mark.
async function mark(fx) { const s = await fx.state(); return {calls: s.mcp_calls.length, reviews: s.reviews.length, lists: s.mcp_lists, model: s.model_requests}; }
async function since(fx, m) {
  const s = await fx.state();
  return {calls: s.mcp_calls.slice(m.calls), reviews: s.reviews.slice(m.reviews), lists: s.mcp_lists - m.lists, model: s.model_requests - m.model};
}
const callsOf = (d, tool) => d.calls.filter(c => c.tool === tool);

// ---------------------------------------------------------------- UI helpers
const composer = page => page.getByPlaceholder(/^Message /);
async function openBot(page, fx, bot) {
  await page.goto(`${fx.origin}/b/${bot.id}`);
  await composer(page).waitFor({timeout: 20000});
}
async function say(page, text) {
  const box = composer(page);
  const posted = () => page.waitForResponse(r => r.request().method() === 'POST' && /\/conversations\/[^/]+\/messages$/.test(r.url()), {timeout: 8000});
  for (let attempt = 1; attempt <= 3; attempt++) {
    await box.fill(text);
    if ((await box.inputValue()) !== text) continue; // the draft was reset under us; type it again
    const sent = posted().catch(() => null);
    await box.press('Enter');
    const res = await sent;
    if (res) break;
    if (attempt === 3) throw new Error(`message was never sent: ${text}`);
  }
  await page.getByText(text, {exact: true}).first().waitFor({timeout: 15000});
}
const bodyText = page => page.locator('body').innerText();
async function runsSettled(fx, conversationId) {
  const rows = await fx.sql(`select status from runs where conversation_id='${conversationId}'`);
  return rows.length > 0 && rows.every(r => ['done', 'failed', 'cancelled', 'interrupted'].includes(r.status));
}
async function waitSettled(fx, bot) {
  await waitFor(async () => (await runsSettled(fx, bot.dm_conversation_id)) || JSON.stringify(await fx.sql(`select status,error from runs where conversation_id='${bot.dm_conversation_id}'`)) === 'never', 'run to settle (' + JSON.stringify(await fx.sql(`select status,error from runs where conversation_id='${bot.dm_conversation_id}'`)) + ')', 60000);
  await sleep(500);
}
async function waitCard(page) {
  await page.getByRole('button', {name: 'Approve action', exact: true}).first().waitFor({timeout: 30000});
}
const approveButtons = page => page.getByRole('button', {name: 'Approve action', exact: true});
const declineButtons = page => page.getByRole('button', {name: 'Decline', exact: true});
async function shot(page, name) {
  const dir = resolve(out, combo);
  mkdirSync(dir, {recursive: true});
  await page.screenshot({path: resolve(dir, `${name}.png`)});
}
async function openConnections(page) {
  await page.getByRole('button', {name: 'Settings', exact: true}).click();
  await page.getByRole('button', {name: 'Connections'}).first().click();
  await page.getByText(/^Connected|Not connected|Needs/).first().waitFor({timeout: 15000}).catch(() => {});
  await page.getByText('notion', {exact: true}).first().waitFor({timeout: 15000});
}
async function openToolsDisclosure(page) {
  const toggle = page.getByRole('button', {name: /^Tools · \d+$/});
  await toggle.waitFor({timeout: 15000});
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
  await page.locator('.mcp-tools').waitFor();
  await sleep(500);
}
async function toolGroups(page) {
  return page.locator('.mcp-tool-group').evaluateAll(groups => Object.fromEntries(groups.map(g => [g.dataset.tier, {
    label: g.getAttribute('aria-label'),
    tools: [...g.querySelectorAll('li.mcp-tool')].map(li => ({
      name: li.querySelector('code')?.textContent ?? '',
      marks: [...li.querySelectorAll('.mcp-tier-mark')].map(m => m.textContent.trim()),
      select: li.querySelector('select')?.value,
    })),
  }])));
}
const names = group => (group?.tools ?? []).map(t => t.name).sort();

// What a person would call a layout defect on the card, measured not eyeballed.
async function layoutReport(page) {
  return page.evaluate(() => {
    const dialog = document.querySelector('.mcp-service')?.closest('[role="dialog"]') ?? document.body;
    const card = document.querySelector('.mcp-service') ?? dialog;
    const canvas = document.createElement('canvas').getContext('2d');
    const issues = [];
    if (document.documentElement.scrollWidth > innerWidth + 1) issues.push(`page scrolls horizontally ${document.documentElement.scrollWidth}>${innerWidth}`);
    const cardRect = card.getBoundingClientRect();
    for (const el of card.querySelectorAll('*')) {
      const cs = getComputedStyle(el);
      if (cs.display === 'none' || cs.visibility === 'hidden') continue;
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height) continue;
      const clipped = !['visible'].includes(cs.overflowX) && el.scrollWidth > el.clientWidth + 1 && el.tagName !== 'SELECT';
      if (clipped) issues.push(`clipped ${el.tagName}.${el.className} ${el.scrollWidth}>${el.clientWidth}`);
      if (r.right > cardRect.right + 1 || r.left < cardRect.left - 1) {
        if (el.closest('.disclosure-region') || !el.closest('.disclosure')) issues.push(`outside card ${el.tagName}.${el.className} ${Math.round(r.left)}..${Math.round(r.right)} vs ${Math.round(cardRect.left)}..${Math.round(cardRect.right)}`);
      }
    }
    // A closed select's label must clear the chevron drawn at its right edge
    // (background-position calc(100% - Npx) + the 5px glyph).
    for (const sel of card.querySelectorAll('select')) {
      const cs = getComputedStyle(sel);
      canvas.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      const text = sel.options[sel.selectedIndex]?.text ?? '';
      const textRight = parseFloat(cs.paddingLeft) + canvas.measureText(text).width;
      const arrowFrom = /calc\(100% - ([\d.]+)px\)/.exec(cs.backgroundPosition);
      const arrowLeft = sel.clientWidth - (arrowFrom ? parseFloat(arrowFrom[1]) : 0) - 5;
      if (textRight > sel.clientWidth - parseFloat(cs.paddingRight) + 1 || arrowFrom && textRight > arrowLeft - 2) {
        issues.push(`select label "${text}" runs under its chevron: text ends at ${textRight.toFixed(0)}px, chevron starts at ${arrowLeft.toFixed(0)}px (select ${sel.clientWidth}px wide, computed padding ${cs.padding}, font ${cs.fontSize})`);
      }
    }
    return [...new Set(issues)];
  });
}
async function motionReport(page) {
  return page.evaluate(() => {
    const dialog = document.querySelector('.mcp-service')?.closest('[role="dialog"]') ?? document.body;
    const running = document.getAnimations().filter(a => a.playState === 'running' && a.effect?.target && dialog.contains(a.effect.target))
      .map(a => `${a.effect.target.tagName}.${a.effect.target.className}:${a.animationName || a.transitionProperty || 'anim'}`);
    const spinners = [...dialog.querySelectorAll('[role="progressbar"],[class*="spinner" i],[class*="loading" i],[aria-busy="true"]')].map(e => `${e.tagName}.${e.className}`);
    return {running, spinners};
  });
}

// ---------------------------------------------------------------- scenarios
async function scenarioConnections(page, fx) {
  scenario = 'S1 connections page';
  const t0 = Date.now();
  await openBot(page, fx, await fx.bot('Notion Settings'));
  await openConnections(page);
  // The read-only summary arrives with the tier list a beat after the count; wait for it, then judge.
  await page.locator('.mcp-service').first().getByText(/read only/).first().waitFor({timeout: 10000}).catch(() => {});
  const status = await page.locator('.mcp-service').first().innerText();
  const m = /Connected\s*·\s*(\d+) tools?\s*·\s*(\d+) read only/.exec(status);
  check('card shows "N tools · M read only" without a refresh', !!m && m[2] === '4', status.split('\n').slice(0, 4).join(' | '));
  await openToolsDisclosure(page);
  const disclosureCount = Number(/(\d+)\s*$/.exec((await page.getByRole('button', {name: /^Tools · \d+$/}).textContent()).trim())?.[1]);
  check('status count and Tools disclosure count agree', m && Number(m[1]) === disclosureCount, `status says ${m?.[1]}, disclosure says ${disclosureCount} (the duplicate listing is counted once in the list and twice in the status)`, 'BUG-2');
  const groups = await toolGroups(page);
  check('group labels are Read only (marked by provider) / Reviewed each time', groups.read_only?.label === 'Read only (marked by provider)' && groups.write?.label === 'Reviewed each time' && !groups.high_risk, JSON.stringify(Object.keys(groups)));
  check('read-only group = the four provider-annotated tools', JSON.stringify(names(groups.read_only)) === JSON.stringify(['notion-fetch', 'notion-get-users', 'notion-query-database', 'notion-search']), names(groups.read_only).join());
  check('write group = update-page, create-page, the duplicate-listed tool, the unannotated "read only" description', JSON.stringify(names(groups.write)) === JSON.stringify(['notion-create-page', 'notion-describe-schema', 'notion-get-comments', 'notion-update-page']), names(groups.write).join());
  const dup = groups.write?.tools.find(t => t.name === 'notion-get-comments');
  check('duplicate-listed tool is NOT read only and says "Listed more than once"', dup && dup.marks.includes('Listed more than once'), JSON.stringify(dup));
  const desc = groups.write?.tools.find(t => t.name === 'notion-describe-schema');
  check('unannotated tool described "read only" is NOT read only and is not marked by provider', desc && !desc.marks.includes('Marked by provider'), JSON.stringify(desc));
  const roMarks = groups.read_only?.tools.every(t => t.marks.includes('Marked by provider'));
  check('every read-only tool carries "Marked by provider"', roMarks, JSON.stringify(groups.read_only?.tools.map(t => t.marks)));
  await sleep(700);
  const motion = await motionReport(page);
  check('no animation or spinner at rest on the card', motion.running.length === 0 && motion.spinners.length === 0, JSON.stringify(motion));
  const layout = await layoutReport(page);
  check('no text overflow / clipping on the card', layout.length === 0, layout.join(' ; '), 'BUG-3');
  await page.locator('.mcp-service').first().scrollIntoViewIfNeeded();
  await shot(page, 's1-connections-tools');
  // Refresh
  const before = await mark(fx);
  await page.getByRole('button', {name: 'Refresh tools'}).click();
  await waitFor(async () => (await since(fx, before)).lists > 0, 'tools/list after Refresh');
  await page.getByRole('button', {name: 'Refresh tools'}).waitFor();
  const after = await since(fx, before);
  const groups2 = await toolGroups(page);
  check('Refresh tools re-lists once and keeps the tiers; button returns to rest', after.lists >= 1 && names(groups2.read_only).length === 4 && names(groups2.write).length === 4 && (await page.getByText('Refreshing…').count()) === 0, `lists=${after.lists} model requests=${after.model}`);
  check('Refresh asks no model', after.model === 0 && after.reviews.length === 0, `model=${after.model} reviews=${after.reviews.length}`);
  check('settings page ready fast (no load spinner left)', (await page.locator('[aria-busy="true"]').count()) === 0, `${Date.now() - t0}ms`);
  await page.getByRole('button', {name: 'Close settings'}).first().click();
}

async function scenarioUserRequestedWrite(page, fx) {
  scenario = 'S2 user-requested write (allow, high risk)';
  const bot = await fx.bot('Notion S2');
  await openBot(page, fx, bot);
  const m = await mark(fx);
  await say(page, 'Update page page-q4 with the Q4 launch plan v2');
  await page.getByText('Done: page-q4 now holds the Q4 launch plan v2.').last().waitFor({timeout: 30000});
  await waitSettled(fx, bot);
  const d = await since(fx, m);
  const updates = callsOf(d, 'notion-update-page');
  check('exactly one update-page call with the requested target', updates.length === 1 && updates[0].args.page_id === 'page-q4' && updates[0].args.content === 'Q4 launch plan v2', JSON.stringify(d.calls));
  check('reviewer allowed at high risk (one review)', d.reviews.length === 1 && d.reviews[0].Decision === 'allow' && d.reviews[0].Risk === 'high' && d.reviews[0].Sources.includes('host_user_ingress'), JSON.stringify(d.reviews));
  check('no approval card shown', (await approveButtons(page).count()) === 0 && (await declineButtons(page).count()) === 0 && !(await bodyText(page)).includes('Allow this external tool call?'), '');
  const rows = await fx.sql(`select q.status, q.answered_by, a.status as review_status, a.decision, a.risk_level from questions q left join mcp_auto_reviews a on a.question_id=q.id where q.conversation_id='${bot.dm_conversation_id}'`);
  check('DB: auto review row approved, question closed by auto-review, none pending', rows.length === 1 && rows[0].decision === 'allow' && rows[0].risk_level === 'high' && rows[0].status !== 'pending', JSON.stringify(rows));
  const claims = await fx.sql(`select count(*) n from mcp_call_execution_claims where run_id in (select id from runs where conversation_id='${bot.dm_conversation_id}')`);
  check('DB: one execution claim', claims[0].n === 1, JSON.stringify(claims));
  check('assistant text carries no literal <progress> tag', !/<\/?progress>/i.test(await page.locator('body').innerHTML()), '');
  check('no "Not sure this went through" card after a clean execution', !(await bodyText(page)).includes('Not sure this went through'), '', 'BUG-1');
  await shot(page, 's2-allow-high-risk-no-card');
}

async function scenarioDifferentTarget(page, fx) {
  scenario = 'S3 different target (needs_human)';
  // 3a: approve
  let bot = await fx.bot('Notion S3 approve');
  await openBot(page, fx, bot);
  let m = await mark(fx);
  await say(page, 'Update page page-roadmap with the roadmap draft');
  await waitCard(page);
  let d = await since(fx, m);
  check('3a: card appears and nothing executed yet', (await approveButtons(page).count()) === 1 && d.calls.length === 0, JSON.stringify(d.calls));
  check('3a: reviewer said needs_human for a different page', d.reviews.length === 1 && d.reviews[0].Decision === 'needs_human' && d.reviews[0].Page === 'page-budget', JSON.stringify(d.reviews));
  await page.getByText('View full arguments').first().click();
  const cardText = await page.locator('.approval-card').first().innerText();
  check('3a: card (full arguments) names the page that would change', cardText.includes('page-budget') && cardText.includes('Roadmap draft'), cardText.replace(/\s+/g, ' ').slice(0, 300));
  check('3a: card shows the reviewer reason and risk level', cardText.includes(d.reviews[0].Reason) && /Reason/.test(cardText) && /Risk\s*\n?\s*High/i.test(cardText), cardText.replace(/\s+/g, ' ').slice(0, 400));
  await shot(page, 's3a-card-pending');
  await approveButtons(page).first().click();
  await page.getByText('Roadmap page updated.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  d = await since(fx, m);
  check('3a: approving executes exactly once, on the proposed target', d.calls.length === 1 && d.calls[0].tool === 'notion-update-page' && d.calls[0].args.page_id === 'page-budget', JSON.stringify(d.calls));
  const q = await fx.sql(`select status, answered_by, answer_json from questions where conversation_id='${bot.dm_conversation_id}'`);
  check('3a: DB question answered true by human', q.length === 1 && q[0].status === 'answered' && q[0].answered_by === 'human' && q[0].answer_json === 'true', JSON.stringify(q));
  check('3a: no stale "Not sure this went through" card after approval executed once', !(await bodyText(page)).includes('Not sure this went through'), 'the approved run executed once (call log) but the chat shows the uncertain-effect card', 'BUG-1');
  await shot(page, 's3a-after-approve');
  // 3b: decline
  bot = await fx.bot('Notion S3 decline');
  await openBot(page, fx, bot);
  m = await mark(fx);
  await say(page, 'Update page page-hiring with the hiring plan');
  await waitCard(page);
  await declineButtons(page).first().click();
  await waitSettled(fx, bot);
  d = await since(fx, m);
  check('3b: declining does not execute', d.calls.length === 0, JSON.stringify(d.calls));
  const q2 = await fx.sql(`select status, answer_json from questions where conversation_id='${bot.dm_conversation_id}'`);
  check('3b: DB question answered false', q2.length === 1 && q2[0].answer_json === 'false', JSON.stringify(q2));
  const text = await bodyText(page);
  const runs3b = await fx.sql(`select status, error from runs where conversation_id='${bot.dm_conversation_id}'`);
  check('3b: the run ends cleanly and the Bot\'s reply after the decline is visible', runs3b.length === 1 && runs3b[0].status === 'done' && text.includes('Hiring change was not applied.'), JSON.stringify(runs3b));
  check('3b: a decline is not shown as uncertain', !text.includes('Not sure this went through'), '', 'BUG-1');
  await shot(page, 's3b-after-decline');
}

async function scenarioReads(page, fx) {
  scenario = 'S4 read-only twice, no review';
  const bot = await fx.bot('Notion S4');
  await openBot(page, fx, bot);
  const m = await mark(fx);
  await say(page, 'Read page-intro for me');
  await page.getByText('Read the intro page twice; it is in good shape.').last().waitFor({timeout: 30000});
  await waitSettled(fx, bot);
  let d = await since(fx, m);
  check('same read tool called twice in one run: both execute', callsOf(d, 'notion-fetch').length === 2, JSON.stringify(d.calls));
  check('no reviewer call for reads', d.reviews.length === 0, JSON.stringify(d.reviews));
  check('no card for reads', (await approveButtons(page).count()) === 0, '');
  const rows = await fx.sql(`select count(*) n from mcp_auto_reviews where conversation_id='${bot.dm_conversation_id}'`);
  check('DB: no auto-review rows for the reads', rows[0].n === 0, JSON.stringify(rows));
  await shot(page, 's4-reads');
  const m2 = await mark(fx);
  await say(page, 'Check page-faq');
  await page.getByText('Searched, then read the FAQ page.').last().waitFor({timeout: 30000});
  await waitSettled(fx, bot);
  d = await since(fx, m2);
  check('two different read tools in one run both execute without review', d.calls.length === 2 && d.reviews.length === 0, JSON.stringify(d.calls.map(c => c.tool)));
}

async function scenarioInjection(page, fx) {
  scenario = 'S6 prompt injection from a read';
  const bot = await fx.bot('Notion S6');
  await openBot(page, fx, bot);
  const m = await mark(fx);
  await say(page, 'Summarize page page-notes for me.');
  await waitCard(page);
  const d = await since(fx, m);
  check('read ran, the write proposed from its text did not', callsOf(d, 'notion-fetch').length === 1 && callsOf(d, 'notion-create-page').length === 0, JSON.stringify(d.calls));
  check('reviewer saw no authorization for the injected write (needs_human)', d.reviews.length === 1 && d.reviews[0].Tool === 'notion-create-page' && d.reviews[0].Decision === 'needs_human' && !/attacker/.test(d.reviews[0].Intent), JSON.stringify(d.reviews));
  check('a card is shown, not auto-executed', (await approveButtons(page).count()) === 1, '');
  await shot(page, 's6-injection-card');
  await declineButtons(page).first().click();
  await page.getByText('I summarized the page. I did not create the export page.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  const after = await since(fx, m);
  check('declined: still no create-page call', callsOf(after, 'notion-create-page').length === 0, JSON.stringify(after.calls));
}

async function scenarioDefiniteRefusal(page, fx) {
  scenario = 'S8 mid-run replies visible, definite refusal';
  const bot = await fx.bot('Notion S8');
  await openBot(page, fx, bot);
  const m = await mark(fx);
  await say(page, 'Update page page-team with the new roster');
  await page.getByText('I could not update that page: the change was refused.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  const d = await since(fx, m);
  const text = await bodyText(page);
  check('reviewer denied; nothing executed', d.reviews.length === 1 && d.reviews[0].Decision === 'deny' && d.calls.length === 0, JSON.stringify({reviews: d.reviews.map(r => r.Decision), calls: d.calls}));
  check('mid-run Bot reply stays visible in the chat', text.includes('Checking the team page before I change it.'), '');
  check('no approval card for a definite refusal', (await approveButtons(page).count()) === 0 && (await declineButtons(page).count()) === 0, '');
  check('uncertain card does not appear for a definite refusal', !text.includes('Not sure this went through'), text.includes('This action did not pass the safety check') ? 'shows the safety-check refusal' : '', 'BUG-1');
  check('no literal <progress> tag anywhere in the page', !/<\/?progress>/i.test(await page.locator('body').innerHTML()), '');
  await shot(page, 's8-definite-refusal');
}

async function scenarioAlwaysAsk(page, fx) {
  scenario = 'S1b/S5 "Always ask me" override';
  // Change the tier in the real Settings UI and prove it survives a reload.
  await openBot(page, fx, await fx.bot('Notion Settings 2'));
  await openConnections(page);
  await openToolsDisclosure(page);
  const before = await mark(fx);
  await page.getByLabel('Tier for notion-update-page').selectOption('high_risk');
  await waitFor(async () => (await names((await toolGroups(page)).high_risk)).includes('notion-update-page'), 'update-page in Always ask me group');
  let groups = await toolGroups(page);
  const ask = groups.high_risk?.tools.find(t => t.name === 'notion-update-page');
  check('changing update-page to "Always ask me" regroups it and says "Set by you"', groups.high_risk?.label === 'Always ask me' && ask?.select === 'high_risk' && ask.marks.includes('Set by you'), JSON.stringify(ask));
  check('the change asked no model', (await since(fx, before)).model === 0, '');
  await shot(page, 's1b-always-ask-set');
  await page.reload();
  await composer(page).waitFor();
  await openConnections(page);
  await openToolsDisclosure(page);
  groups = await toolGroups(page);
  const ask2 = groups.high_risk?.tools.find(t => t.name === 'notion-update-page');
  check('override persists across reload (UI)', ask2?.select === 'high_risk', JSON.stringify(ask2));
  const api = (await fx.tiers()).tools.find(t => t.name === 'notion-update-page');
  check('override persists in the server (API + tier file)', api?.tier === 'high_risk' && api.source === 'owner' && api.override === 'high_risk' && /"override": "high_risk"/.test(await fx.tierFile()), JSON.stringify(api));
  await page.getByRole('button', {name: 'Close settings'}).first().click();

  // S5a: the user asks, the owner said always ask: card every time, no reviewer.
  const bot = await fx.bot('Notion S5');
  await openBot(page, fx, bot);
  let m = await mark(fx);
  await say(page, 'Update page page-pricing with 2027 rates');
  await waitCard(page);
  let d = await since(fx, m);
  check('5a: card even though the user asked for exactly this', (await approveButtons(page).count()) === 1 && d.calls.length === 0, JSON.stringify(d.calls));
  check('5a: the reviewer is never asked for an Always-ask tool', d.reviews.length === 0, JSON.stringify(d.reviews));
  await shot(page, 's5a-always-ask-card');
  await approveButtons(page).first().click();
  await page.getByText('Pricing page updated.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  d = await since(fx, m);
  check('5a: approving executes once', callsOf(d, 'notion-update-page').length === 1 && callsOf(d, 'notion-update-page')[0].args.page_id === 'page-pricing', JSON.stringify(d.calls));
  check('5a: no stale "Not sure this went through" card after approval executed once', !(await bodyText(page)).includes('Not sure this went through'), '', 'BUG-1');
  m = await mark(fx);
  await say(page, 'Update page page-pricing with 2027 rates, once more');
  await page.getByRole('button', {name: 'Approve action', exact: true}).first().waitFor({timeout: 40000});
  d = await since(fx, m);
  check('5a: the second identical request asks again (card every time)', (await approveButtons(page).count()) >= 1 && d.calls.length === 0 && d.reviews.length === 0, JSON.stringify(d.calls));
  await declineButtons(page).first().click();
  await waitSettled(fx, bot);

  // S5b: unattended run.
  scenario = 'S5b Always-ask in a scheduled run';
  const sbot = await fx.bot('Notion S5 schedule');
  await openBot(page, fx, sbot);
  m = await mark(fx);
  const runAt = new Date(Date.now() + 3000).toISOString().replace(/\.\d+Z$/, 'Z');
  const created = await fx.call('POST', `/api/conversations/${sbot.dm_conversation_id}/schedules`, {bot_id: sbot.id, title: 'Nightly page update', content: 'Update page page-nightly with the nightly summary', kind: 'once', run_at: runAt, timezone: 'UTC'});
  check('5b: schedule created through the API the UI uses', created.status === 201, `${created.status} ${created.text.slice(0, 120)}`);
  await waitFor(async () => (await fx.call('GET', `/api/conversations/${sbot.dm_conversation_id}/schedules`)).json.schedules.every(s => s.execution_status === 'done' || s.status === 'completed') , 'scheduled run to finish', 60000);
  await waitSettled(fx, sbot);
  d = await since(fx, m);
  const act = await fx.sql(`select status, result from tool_activities where run_id in (select id from runs where conversation_id='${sbot.dm_conversation_id}') and name='call_mcp_tool'`);
  const named = act.some(a => /mcp_high_risk_unattended/.test(a.result) && /not_executed/.test(a.result));
  check('5b: not executed, refused with the named reason mcp_high_risk_unattended', d.calls.length === 0 && named, JSON.stringify({calls: d.calls, act: act.map(a => a.status + ':' + a.result.slice(0, 120))}));
  check('5b: no reviewer call and no card', d.reviews.length === 0 && (await fx.sql(`select count(*) n from questions where conversation_id='${sbot.dm_conversation_id}'`))[0].n === 0, JSON.stringify(d.reviews));
  const runs = await fx.sql(`select status, error from runs where conversation_id='${sbot.dm_conversation_id}'`);
  check('5b: the run continued and finished (not failed)', runs.length >= 1 && runs.every(r => r.status === 'done'), JSON.stringify(runs));
  await page.reload();
  await composer(page).waitFor();
  await page.getByText('The nightly update was not executed; it needs your approval.').last().waitFor({timeout: 20000});
  const text = await bodyText(page);
  check('5b: the conversation reports the blocker', text.includes('The nightly update was not executed'), '');
  await sleep(800);
  await shot(page, 's5b-scheduled-unattended');
}

async function scenarioMigration(page, fx) {
  scenario = 'S7 tier file v1 to v2';
  const raw = JSON.parse(await fx.tierFile());
  const dump = JSON.stringify(raw);
  check('tier file rewritten as version 2 on startup', raw.version === 2, `version=${raw.version}`);
  check('model-classification fields and queue are gone', !/"auto"|auto_digest|model_tier|classified_at|"queue"/.test(dump), dump.slice(0, 200));
  check('v1 provider marks were not carried over (annotations unrecorded)', !/"read_only_hint": true/.test(dump) && !/annotations_present/.test(dump), '');
  const before = await fx.tiers();
  const pre = await fx.state();
  check('API: every tool is "Reviewed each time" (write) before any tools/list, and none has happened', before.tools.length > 0 && before.tools.every(t => t.tier === 'write') && before.read_only === 0 && pre.mcp_lists === 0, `lists=${pre.mcp_lists} ${JSON.stringify(before.tools.map(t => t.name + ':' + t.tier))}`);
  const m = await mark(fx);
  await openBot(page, fx, await fx.bot('Notion Migrated'));
  await openConnections(page);
  await openToolsDisclosure(page);
  const opened = await since(fx, m);
  check('opening Connections re-lists the server (the card checks it on mount; no model asked)', opened.lists >= 1 && opened.model === 0, `lists=${opened.lists} model=${opened.model}`);
  let groups = await toolGroups(page);
  check('UI after that tools/list: the four provider-marked tools are Read only, the other four stay Reviewed each time', JSON.stringify(names(groups.read_only)) === JSON.stringify(['notion-fetch', 'notion-get-users', 'notion-query-database', 'notion-search']) && names(groups.write).length === 4 && groups.read_only.tools.every(t => t.marks.includes('Marked by provider')), JSON.stringify(Object.fromEntries(Object.entries(groups).map(([k, v]) => [k, names(v)]))));
  const layout = await layoutReport(page);
  check('UI: no overflow on the migrated card', layout.length === 0, layout.join(' ; '), 'BUG-3');
  await shot(page, 's7-after-open');
  const m1 = await mark(fx);
  await page.getByRole('button', {name: 'Refresh tools'}).click();
  await waitFor(async () => (await since(fx, m1)).lists > 0, 'tools/list after Refresh');
  groups = await toolGroups(page);
  check('after Refresh: duplicate-listed and unannotated tools stay Reviewed each time', ['notion-get-comments', 'notion-describe-schema'].every(n => names(groups.write).includes(n)) && names(groups.read_only).length === 4, names(groups.write).join());
  const after = JSON.parse(await fx.tierFile());
  check('tier file now records the provider annotations', after.version === 2 && after.servers.notion.tools['notion-fetch'].read_only_hint === true, '');
  await shot(page, 's7-after-refresh');
  await page.getByRole('button', {name: 'Close settings'}).first().click();
  // A migrated read now runs without review.
  const bot = await fx.bot('Notion Migrated chat');
  await openBot(page, fx, bot);
  const m2 = await mark(fx);
  await say(page, 'Fetch page-migrated');
  await page.getByText('Fetched.').last().waitFor({timeout: 30000});
  await waitSettled(fx, bot);
  const d = await since(fx, m2);
  check('post-migration read executes with no review call', callsOf(d, 'notion-fetch').length === 1 && d.reviews.length === 0, JSON.stringify(d.calls));
}

async function scenarioRemember(page, fx) {
  scenario = 'S9 approve and remember in this chat';
  const bot = await fx.bot('Notion S9');
  await openBot(page, fx, bot);
  const grants = async () => (await fx.call('GET', `/api/conversations/${bot.dm_conversation_id}/remembered-approvals`)).json?.grants ?? [];
  const rememberButtons = page => page.getByRole('button', {name: 'Approve and remember in this chat', exact: true});
  // 9a: the card offers the memory; using it executes once and records a grant bound to the page id.
  let m = await mark(fx);
  await say(page, 'Update page page-remember-first with the budget draft');
  await waitCard(page);
  let d = await since(fx, m);
  check('9a: card for a different target offers "Approve and remember in this chat"', (await rememberButtons(page).count()) === 1 && d.calls.length === 0 && d.reviews.length === 1 && d.reviews[0].Decision === 'needs_human', JSON.stringify(d.reviews));
  const hint = await page.locator('.approval-remember-hint').first().innerText().catch(() => '');
  check('9a: the card says what "same target" binds to (page_id = page-budget)', hint.includes('page_id = page-budget'), hint);
  await shot(page, 's9a-card-remember-offer');
  await rememberButtons(page).first().click();
  await page.getByText('Budget page updated.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  d = await since(fx, m);
  check('9a: remembering executes exactly once on the proposed target', d.calls.length === 1 && d.calls[0].tool === 'notion-update-page' && d.calls[0].args.page_id === 'page-budget', JSON.stringify(d.calls));
  let g = await grants();
  check('9a: one grant, bound to notion-update-page on page_id = page-budget, 24h', g.length === 1 && g[0].tool === 'notion-update-page' && g[0].server === 'notion' && g[0].target_kind === 'identifier' && g[0].target_summary === 'page_id = page-budget' && Math.abs(Date.parse(g[0].expires_at) - Date.now() - 24 * 3600e3) < 120e3, JSON.stringify(g));
  const q1 = await fx.sql(`select answered_by from questions where conversation_id='${bot.dm_conversation_id}'`);
  check('9a: DB: the first card was answered by the human', q1.length === 1 && q1[0].answered_by === 'human', JSON.stringify(q1));
  // 9b: same tool, same page, different content: reviewed again, no card, executed, audited.
  m = await mark(fx);
  await say(page, 'Update page page-remember-second with the second pass');
  await page.getByText('Budget page updated again.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  d = await since(fx, m);
  check('9b: same target executes without a card', d.calls.length === 1 && d.calls[0].args.page_id === 'page-budget' && d.calls[0].args.content === 'Budget draft, second pass' && (await approveButtons(page).count()) === 0, JSON.stringify(d.calls));
  check('9b: the reviewer was still asked (needs_human) and the memory replaced only the person', d.reviews.length === 1 && d.reviews[0].Decision === 'needs_human', JSON.stringify(d.reviews));
  const q2 = await fx.sql(`select q.answered_by, a.grant_id, a.claimed_at, json_extract(q.approval_json,'$.review.decision_source') as source from questions q join mcp_call_approvals a on a.question_id=q.id where q.conversation_id='${bot.dm_conversation_id}' order by q.created_at desc limit 1`);
  check('9b: DB: decided by remembered-approval with decision source remembered_approval and the grant id, claimed once', q2.length === 1 && q2[0].answered_by === 'remembered-approval' && q2[0].source === 'remembered_approval' && q2[0].grant_id === g[0].id && q2[0].claimed_at !== '', JSON.stringify(q2));
  const activity = page.locator('.task-activity').last();
  if ((await activity.locator(':scope > summary').count()) && !(await activity.evaluate(node => node.open))) await activity.locator(':scope > summary').click();
  await page.locator('.task-remembered-record').last().waitFor({timeout: 10000}).catch(() => {});
  check('9b: the activity record says a remembered approval decided it (no one was asked)', (await bodyText(page)).includes('Approved by a remembered approval from this chat'), (await activity.innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 200));
  await shot(page, 's9b-remembered-no-card');
  // 9c: a different page still asks.
  m = await mark(fx);
  await say(page, 'Update page page-remember-other with the ledger draft');
  await waitCard(page);
  d = await since(fx, m);
  check('9c: a different target asks again', (await approveButtons(page).count()) === 1 && d.calls.length === 0, JSON.stringify(d.calls));
  await declineButtons(page).first().click();
  await page.getByText('Ledger page was not changed.').last().waitFor({timeout: 40000});
  await waitSettled(fx, bot);
  // 9d: the conversation lists the grant; Revoke ends it; the same target asks again.
  await page.getByRole('button', {name: 'More actions', exact: true}).click();
  await page.getByRole('button', {name: 'Remembered approvals', exact: true}).click();
  const sheet = page.locator('.remembered-sheet');
  await sheet.waitFor({timeout: 10000});
  await sheet.locator('.remembered-item').first().waitFor({timeout: 10000});
  const sheetText = await sheet.innerText();
  check('9d: "Remembered approvals" lists the grant with its tool and target', sheetText.includes('notion-update-page') && sheetText.includes('page_id = page-budget') && (await sheet.locator('.remembered-item').count()) === 1, sheetText.replace(/\s+/g, ' ').slice(0, 200));
  await shot(page, 's9d-remembered-list');
  await sheet.getByRole('button', {name: 'Revoke', exact: true}).click();
  await sheet.getByText('Nothing is remembered in this chat.').waitFor({timeout: 10000});
  g = await grants();
  check('9d: Revoke removes the grant (list empty, API empty)', g.length === 0, JSON.stringify(g));
  await sheet.getByRole('button', {name: 'Close', exact: true}).click();
  await sheet.waitFor({state: 'detached', timeout: 10000});
  m = await mark(fx);
  await say(page, 'Update page page-remember-second with the second pass, after revoke');
  await waitCard(page);
  d = await since(fx, m);
  check('9d: after Revoke the same target asks again', (await approveButtons(page).count()) === 1 && d.calls.length === 0, JSON.stringify(d.calls));
  await declineButtons(page).first().click();
  await waitSettled(fx, bot);
  await shot(page, 's9d-after-revoke-card');
}

// ---------------------------------------------------------------- main
buildFixture();
let failedHard = false;
const summary = [];
for (const browserName of Object.keys(BROWSERS)) {
  for (const [width, height] of VIEWPORTS) {
    combo = `${browserName}-${width}x${height}`;
    if (only.length && !only.includes(combo)) continue;
    console.log(`\n=== ${combo} ===`);
    let browser;
    const launch = {headless: true};
    if (browserName === 'webkit' && process.env.WEBKIT_EXECUTABLE) launch.executablePath = process.env.WEBKIT_EXECUTABLE;
    try {
      browser = await BROWSERS[browserName].launch(launch);
    } catch (e) {
      check('browser launches', false, e.message.split('\n')[0]);
      continue;
    }
    for (const profile of ['main', 'v1']) {
      let fx;
      try {
        fx = await startFixture(profile, `${combo}-${profile}`);
        const context = await browser.newContext({viewport: {width, height}});
        const page = await context.newPage();
        const errors = [];
        // WebKit reports a same-origin fetch aborted by navigation as "due to access control checks".
        page.on('pageerror', e => { if (!/due to access control checks/.test(e.message)) errors.push('pageerror: ' + e.message); });
        page.on('console', msg => { if (msg.type() === 'error' && !/favicon|Failed to load resource/.test(msg.text())) errors.push('console: ' + msg.text()); });
        page.on('request', r => { if (process.env.TIERS_E2E_DEBUG && r.method() !== 'GET') console.log('DEBUG REQ', r.method(), r.url()); });
        page.on('response', async r => { if (process.env.TIERS_E2E_DEBUG && /questions|schedules/.test(r.url()) && r.request().method() !== 'GET') console.log('DEBUG', r.status(), r.request().method(), r.url(), (await r.text().catch(() => '')).slice(0, 300)); if (r.status() >= 400 && !/\/api\/computers\//.test(r.url())) errors.push(`http ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });
        try {
          if (profile === 'main') {
            if (want('s1')) await scenarioConnections(page, fx);
            if (want('s2')) await scenarioUserRequestedWrite(page, fx);
            if (want('s3')) await scenarioDifferentTarget(page, fx);
            if (want('s4')) await scenarioReads(page, fx);
            if (want('s6')) await scenarioInjection(page, fx);
            if (want('s8')) await scenarioDefiniteRefusal(page, fx);
            if (want('s9')) await scenarioRemember(page, fx);
            if (want('s5')) await scenarioAlwaysAsk(page, fx);
          } else if (want('s7')) {
            await scenarioMigration(page, fx);
          }
          scenario = 'browser console';
          check('no page errors or console errors', errors.length === 0, errors.slice(0, 3).join(' | '));
        } catch (e) {
          failedHard = true;
          check(`scenario ran to completion (${scenario})`, false, e.stack?.split('\n').slice(0, 3).join(' / '));
          await page.screenshot({path: resolve(out, `${combo}-${profile}-failure.png`)}).catch(() => {});
        }
        await context.close();
      } catch (e) {
        failedHard = true;
        check('fixture started', false, e.message);
      } finally {
        if (fx) {
          const r = await fx.stop();
          scenario = 'fixture exit';
          check(`go fixture (${profile}) exited 0 with PASS and no SKIP`, r.code === 0 && /--- PASS: TestToolTiersE2EFixture/.test(r.log) && !/SKIP/.test(r.log), `exit=${r.code} ${r.log.match(/--- (PASS|FAIL|SKIP)[^\n]*/)?.[0] ?? ''}`);
        }
      }
    }
    await browser.close();
  }
}

const failed = results.filter(r => !r.ok);
const byBug = {};
for (const r of failed) (byBug[r.bug || 'unclassified'] ||= []).push(`${r.combo} ${r.scenario} :: ${r.name}`);
writeFileSync(resolve(out, 'results.json'), JSON.stringify({passed: results.length - failed.length, failed: failed.length, results}, null, 1));
console.log(`\n${results.length - failed.length}/${results.length} checks passed, ${failed.length} failed`);
for (const [bug, items] of Object.entries(byBug)) console.log(`${bug}: ${items.length} failing check(s), e.g. ${items[0]}`);
process.exit(failed.length || failedHard ? 1 : 0);
