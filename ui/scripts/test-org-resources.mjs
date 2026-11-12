// Settings > Administration > Organization resources (user groups, design 5.1 and 4.8). Mounts the real SettingsShell +
// SettingsPages as an administrator in a Vite fixture with the Administration API stubbed (scripts/lib/org-fixture.mjs): the
// page says these keys belong to the server and are not the administrator's own; AI model cards (Claude with its workspace ID,
// OpenAI, the OpenAI-compatible endpoint) save a key, show only the masked hint, Test and Remove; the E2B card and its template
// image build state with Retry build; the VM row; the Search & fetch chains (the member's chain editor over the organization endpoints: key, order,
// switch, address, Test); the "provided resources are missing" banner; refusals worded from their code. The key itself must never
// come back: after every save the page holds no part of it beyond the server's hint. Synthetic data only. Needs PLAYWRIGHT_MODULE.
//   UG_SHOTS=<dir>  also saves screenshots (light and dark, 1440x900 and 390x844).
import assert from "node:assert/strict";
import {mkdir, rm} from "node:fs/promises";
import {dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
import {selectItems} from "./lib/select.mjs";
import {createOrgStub, openSettings, startFixture} from "./lib/org-fixture.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium, webkit} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
// UG_BROWSER=webkit runs the same suite in WebKit (the shots and the rendering checks).
const engine = process.env.UG_BROWSER === "webkit" ? webkit : chromium;
const shots = process.env.UG_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

const CLAUDE_KEY = "sk-ant-api03-ZXCVBNMASDFGHJKLQWERTYUIOP1234567890abcd";
const OPENAI_KEY = "sk-proj-1234567890abcdefWXYZ";
const TAVILY_KEY = "tvly-dev-abcdefghijklmnop9876";
const E2B_KEY = "e2b_0123456789abcdef0123456789abcdef01234567";

let fixture, browser;
try {
  fixture = await startFixture(ui, "org-resources");
  browser = await engine.launch(engine === chromium ? {executablePath: process.env.TOFI_TEST_CHROME || undefined} : {});
  const stub = createOrgStub();
  const errors = [];
  const open = (params = {}, options = {}) => openSettings({browser, fixture, stub, errors, params: {tab: "admin-resources", ...params}, ...options});
  const pane = page => page.locator('.settings-page-body[data-page="admin-resources"]');
  const shot = async (page, name) => { if (shots) await page.screenshot({path: join(shots, `${name}.png`)}); };
  const text = async locator => (await locator.innerText()).replace(/\s+/g, " ").trim();
  const card = (page, id) => pane(page).locator(`[data-resource="${id}"]`);
  // The cards draw at once and read the resources behind a busy mark: ready means that read is done.
  const ready = async page => { await pane(page).locator('[data-resource="provider:openai"]').waitFor(); await page.waitForFunction(() => !document.querySelector('.admin-resources [aria-busy="true"]')); };
  const holdsNoKey = async (page, ...keys) => {
    const html = await page.content();
    const inputs = await page.$$eval("input", list => list.map(input => input.value));
    for (const key of keys) {
      for (let i = 0; i + 8 <= key.length; i += 4) assert.ok(!html.includes(key.slice(i, i + 8)), `the page never holds the key (${key.slice(0, 4)}…, from ${i})`);
      assert.ok(!inputs.some(value => value.includes(key)), "no input keeps the key after saving");
    }
  };

  // ---- 1. The page: what it is, and its sections.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    assert.equal((await page.locator(".settings-page-title h2").textContent()).trim(), "Organization resources");
    assert.match(await text(pane(page).locator(".admin-intro")), /These keys belong to this server and serve accounts whose group provides them\. They are not your personal keys; yours are in Settings › AI Models, Environments and Search & fetch\./);
    assert.deepEqual(await pane(page).locator("[data-section]").evaluateAll(items => items.map(item => item.dataset.section).filter(Boolean)), ["models", "computers", "storage", "backups", "web"]);
    assert.deepEqual(await pane(page).locator("[data-resource]").evaluateAll(items => items.map(item => item.dataset.resource)), ["provider:anthropic", "provider:openai", "provider:compatible", "computer:e2b", "file_store", "backup_store"]);
    assert.match(await text(card(page, "provider:anthropic")), /^Claude.*Needs setup/);
    assert.equal(await card(page, "provider:anthropic").locator("input[type=password]").count(), 1, "a key field while nothing is stored");
    assert.match(await text(pane(page).locator(".org-vm")), /Server VM This server can run VMs/);
    assert.equal(await pane(page).locator(".web-row").count(), 5, "the organization's search and fetch sources (no Bot computer)");
    assert.equal(await pane(page).locator('.web-row[data-id="native"], .web-row[data-id="builtin"], .web-row[data-id="computer"]').count(), 0, "an organization provides no model search, built-in fetch or Bot computer");
    assert.ok(!/\b(admin\.org|policy)\.[a-z_.]+/.test(await pane(page).innerText()), "no raw i18n keys");
    await shot(page, "resources-empty-light-1440");
    await close();
    console.log("PASS page: says the keys belong to the server, five sections, nothing stored yet");
  }

  // ---- 2. Claude: key and workspace ID are both required; the card then shows only the masked hint; the key leaves the page.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    const claude = card(page, "provider:anthropic");
    const save = claude.getByRole("button", {name: "Save"});
    assert.equal(await save.isDisabled(), true);
    await claude.locator('input[name="provider:anthropic-key"]').fill(CLAUDE_KEY);
    assert.equal(await save.isDisabled(), true, "the workspace ID is required for Claude");
    await claude.locator('input[name="org-anthropic-workspace-id"]').fill("wrkspc_0123456789");
    stub.writes.length = 0;
    await save.click();
    await claude.locator(".provider-key-mask").waitFor();
    assert.deepEqual(stub.writes, [`PUT /api/admin/org/resources/provider:anthropic ${JSON.stringify({key: CLAUDE_KEY, workspace_id: "wrkspc_0123456789"})}`]);
    assert.equal(await claude.locator(".provider-key-mask").innerText(), "sk-a••••••••abcd", "prefix, dots and the last four only");
    assert.match(await text(claude), /Verified/);
    assert.equal(await claude.locator("input[type=password]").count(), 0, "the key field is gone once a key is stored");
    await holdsNoKey(page, CLAUDE_KEY);
    await shot(page, "resources-claude-stored-light-1440");
    // Replace shows the field again, empty; Cancel closes it without a request.
    await claude.getByRole("button", {name: "Replace key"}).click();
    assert.equal(await claude.locator('input[name="provider:anthropic-key"]').inputValue(), "", "the stored key is never put back in the field");
    stub.writes.length = 0;
    await claude.getByRole("button", {name: "Cancel"}).click();
    assert.equal(stub.writes.length, 0);
    // A key the provider refuses is worded, and nothing is stored.
    stub.state.rejectKeys.add("provider:openai");
    const openai = card(page, "provider:openai");
    await openai.locator('input[name="provider:openai-key"]').fill(OPENAI_KEY);
    await openai.getByRole("button", {name: "Save"}).click();
    await openai.getByRole("alert").getByText("The provider rejected this key.").waitFor();
    assert.match(await text(openai), /Needs setup/);
    // Too short a key never reaches the provider: the server asks for the full key.
    stub.state.rejectKeys.clear();
    await openai.locator('input[name="provider:openai-key"]').fill("sk-short");
    await openai.getByRole("button", {name: "Save"}).click();
    await openai.getByRole("alert").getByText("The provider rejected this key.").waitFor();
    await close();
    console.log("PASS Claude and OpenAI cards: required fields, masked hint only, the key leaves the page, refusals are worded");
  }

  // ---- 3. Test re-checks a stored key (verified, then rejected); Remove asks first and the missing banner follows.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    const openai = card(page, "provider:openai");
    await openai.locator('input[name="provider:openai-key"]').fill(OPENAI_KEY);
    await openai.getByRole("button", {name: "Save"}).click();
    await openai.locator(".provider-key-mask").waitFor();
    assert.equal(await openai.locator(".provider-key-mask").innerText(), "••••••••WXYZ", "a key under 32 characters shows no prefix");
    stub.writes.length = 0;
    await openai.locator('[data-action="test"]').click();
    await openai.getByText("Key verified.").waitFor();
    assert.deepEqual(stub.writes, ["POST /api/admin/org/resources/provider:openai/test"]);
    stub.state.rejectKeys.add("provider:openai");
    await openai.locator('[data-action="test"]').click();
    await openai.getByRole("alert").getByText("The provider rejected this key.").waitFor();
    await openai.locator('.provider-key-meta[data-status="rejected"]').waitFor();
    assert.match(await text(openai), /Rejected: The provider rejected this key\./);
    // The Provided group needs a model: removing the only one raises the banner; the VM needs none.
    assert.equal(await page.locator('[data-testid="org-missing"]').count(), 1, "something is already missing: search and fetch");
    stub.writes.length = 0;
    await openai.getByRole("button", {name: "Remove"}).click();
    assert.equal(stub.writes.length, 0, "Remove asks first");
    await openai.getByRole("group", {name: "Remove the organization's OpenAI key?"}).waitFor();
    await openai.getByRole("button", {name: "Cancel"}).click();
    await openai.getByRole("button", {name: "Remove"}).click();
    await openai.locator(".inline-confirmation .danger-link").click();
    await openai.locator("input[type=password]").waitFor();
    assert.deepEqual(stub.writes, ["DELETE /api/admin/org/resources/provider:openai"]);
    const banner = page.locator('[data-testid="org-missing"]');
    assert.match(await text(banner), /Provided resources are missing/);
    assert.match(await text(banner), /AI models: Provided/);
    assert.match(await text(banner), /Web search: Provided/);
    assert.match(await text(banner), /Web fetch: Provided/);
    assert.ok(!/Bot computer: /.test(await text(banner)), "a VM on a KVM host needs no key");
    await shot(page, "resources-missing-light-1440");
    await banner.getByRole("button", {name: "Open Groups"}).click();
    await page.locator('.settings-tabs [data-tab="admin-groups"][aria-current="page"]').waitFor();
    await close();
    console.log("PASS Test, Remove with confirmation, and the banner that lists what groups provide without a resource");
  }

  // ---- 4. The OpenAI-compatible endpoint: address, optional key, model IDs; a blank key keeps the stored one.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    const compat = card(page, "provider:compatible");
    await compat.getByLabel("Endpoint address").fill("https://models.example.com/v1");
    await compat.getByLabel("API key (optional)").fill("tok-1234567890abcdefghij");
    await compat.getByLabel("Model IDs").fill("llama-3.3-70b, qwen3-32b");
    stub.writes.length = 0;
    await compat.getByRole("button", {name: "Save"}).click();
    await compat.locator(".provider-key-mask").first().waitFor();
    assert.deepEqual(stub.writes, [`PUT /api/admin/org/resources/provider:compatible ${JSON.stringify({base_url: "https://models.example.com/v1", models: ["llama-3.3-70b", "qwen3-32b"], key: "tok-1234567890abcdefghij"})}`]);
    assert.match(await text(compat), /https:\/\/models\.example\.com\/v1.*ghij.*Stored, not verified/);
    assert.match(await text(compat), /Models: llama-3\.3-70b, qwen3-32b/);
    await holdsNoKey(page, "tok-1234567890abcdefghij");
    await compat.getByRole("button", {name: "Edit"}).click();
    assert.equal(await compat.getByLabel("Endpoint address").inputValue(), "https://models.example.com/v1", "Edit starts from the stored address");
    assert.equal(await compat.getByLabel("API key (optional)").inputValue(), "", "and never from the stored key");
    stub.writes.length = 0;
    await compat.getByLabel("Model IDs").fill("llama-3.3-70b");
    await compat.getByRole("button", {name: "Save"}).click();
    await compat.getByText("Models: llama-3.3-70b", {exact: true}).waitFor();
    assert.ok(!JSON.parse(stub.writes[0].replace(/^PUT \S+ /, "")).key, "no key is sent when the field is blank, so the server keeps its own");
    await compat.getByRole("button", {name: "Edit"}).click();
    await compat.getByLabel("Endpoint address").fill("ftp://nope");
    await compat.getByRole("button", {name: "Save"}).click();
    await compat.getByRole("alert").getByText("Enter a valid endpoint address.").waitFor();
    await close();
    console.log("PASS compatible endpoint: save, masked key, Edit never refills the key, refusals are worded");
  }

  // ---- 5. E2B: key and an optional template; a custom template is used as supplied and never rebuilt.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    const e2b = card(page, "computer:e2b");
    assert.match(await text(e2b), /^E2B cloud computer.*Needs setup/);
    await e2b.getByLabel("E2B API key").fill(E2B_KEY);
    await e2b.getByLabel("Template ID (optional)").fill("tpl_shared_1");
    stub.writes.length = 0;
    await e2b.getByRole("button", {name: "Save"}).click();
    await e2b.locator(".provider-key-mask").waitFor();
    assert.deepEqual(stub.writes, [`PUT /api/admin/org/resources/computer:e2b ${JSON.stringify({key: E2B_KEY, template_id: "tpl_shared_1"})}`]);
    assert.equal(await e2b.locator(".provider-key-mask").innerText(), "e2b_••••••••4567");
    assert.equal(await e2b.locator(".org-template").getAttribute("data-custom"), "true");
    assert.match(await text(e2b.locator(".org-template")), /^Custom template tpl_shared_1\. Used as supplied and never rebuilt\.$/);
    assert.equal(await e2b.locator('[data-action="retry-build"]').count(), 0, "a custom template is never rebuilt: no Retry build");
    await holdsNoKey(page, E2B_KEY);
    await e2b.getByRole("button", {name: "Remove"}).click();
    await e2b.locator(".inline-confirmation .danger-link").click();
    await e2b.locator("input[type=password]").waitFor();
    // Without a KVM host the VM row says so.
    await close();
    stub.reset({kvm: false});
    const none = await open();
    await ready(none.page);
    assert.match(await text(none.page.locator(".org-vm")), /This server cannot run VMs \(no KVM host\)\./);
    await none.close();
    console.log("PASS E2B card: key and template, masked, custom template note with no rebuild, VM row follows the host");

  // ---- 5b. The image build: phase, named failures with what to do, minimum version, Retry build (not for a custom template).
  {
    const e2bRow = (extra = {}) => ({id: "computer:e2b", kind: "computer_provider", status: "verified", key_prefix: "e2b_", key_hint: "…4567", verified_at: "2026-11-08T20:00:00Z", updated_at: "t0", config: {}, ...extra});
    const openBuild = async build => {
      stub.reset({build: {version: "0.9.1", updated_at: "2026-11-08T19:00:00Z", ...build}});
      stub.state.resources = {"computer:e2b": e2bRow()};
      const view = await open();
      await ready(view.page);
      return view;
    };
    const note = view => view.page.locator('[data-resource="computer:e2b"] .org-template');

    // Ready: the version, nothing to retry.
    let view = await openBuild({phase: "ready"});
    assert.match(await text(note(view)), /^Computer image ready, version 0\.9\.1\.$/);
    assert.equal(await note(view).locator('[data-action="retry-build"]').count(), 0);
    await view.close();

    // Not built yet: says when it builds and offers Build now.
    view = await openBuild({phase: "none"});
    assert.match(await text(note(view)), /No computer image is built for version 0\.9\.1 yet\. It builds when a member first needs a computer, or now with Build now\./);
    await note(view).getByRole("button", {name: "Build now"}).waitFor();
    await view.close();

    // Minimum guest version, when the server enforces one.
    view = await openBuild({phase: "ready", min_guest_version: "0.8.0"});
    assert.match(await text(note(view).locator("[data-min-guest]")), /Minimum computer image version: 0\.8\.0\. Computers on an older image must be upgraded before they run\./);
    await view.close();

    // Preparing: no action; the page reads again by itself and shows the result.
    view = await openBuild({phase: "preparing"});
    assert.match(await text(note(view)), /Building the computer image, version 0\.9\.1\./);
    assert.equal(await note(view).locator("button").count(), 0, "nothing to press while it builds");
    await shot(view.page, "build-preparing-light-1440");
    stub.state.build.phase = "ready";
    await note(view).getByText("Computer image ready, version 0.9.1.").waitFor({timeout: 12000});
    await view.close();

    // Failed, each named code with what to do; Retry build starts one build and shows it running.
    const named = {
      template_build_failed: /E2B couldn't build the computer image\./,
      image_missing: /This server build ships no computer image for E2B\./,
      invalid_key: /E2B rejected the organization's API key\. Replace the key\./,
      e2b_unreachable: /This server can't reach E2B\. Retry the build in a moment\./,
      e2b_limit: /The E2B account limit is reached\. Free capacity in E2B, then retry the build\./,
      template_stale_in_team: /The E2B team already holds a template named tofi-computer-0\.9\.1 whose last build failed.*Delete that template in the E2B dashboard \(the team owner can\), then retry the build\./,
    };
    for (const [code, pattern] of Object.entries(named)) {
      view = await openBuild({phase: "failed", error_code: code});
      assert.match(await text(note(view)), /The computer image for version 0\.9\.1 failed to build\./);
      assert.match(await text(note(view).locator("[data-error-code]")), pattern, code);
      assert.equal(await note(view).locator("[data-error-code]").getAttribute("data-error-code"), code);
      if (code === "template_stale_in_team") await shot(view.page, "build-stale-in-team-light-1440");
      if (code === "template_build_failed") {
        stub.writes.length = 0;
        await note(view).getByRole("button", {name: "Retry build"}).click();
        await note(view).getByText("Building the computer image, version 0.9.1.").waitFor();
        assert.deepEqual(stub.writes, ["POST /api/admin/org/resources/computer:e2b/build"]);
        assert.equal(await note(view).locator("button").count(), 0, "one build at a time: no second Retry while it runs");
      }
      await view.close();
    }

    // A refusal from the server is worded from its code.
    view = await openBuild({phase: "failed", error_code: "template_build_failed"});
    stub.state.failNext = Object.assign(/\/build$/, {code: "custom_template"});
    await note(view).getByRole("button", {name: "Retry build"}).click();
    await note(view).getByRole("alert").getByText("This computer uses a template you supplied. It is never rebuilt.").waitFor();
    await view.close();

    // The page in dark and on a phone: no overflow.
    for (const [width, height, touch, theme] of [[1440, 900, false, "dark"], [390, 844, true, "light"], [390, 844, true, "dark"]]) {
      stub.reset({build: {phase: "failed", version: "0.9.1", error_code: "template_stale_in_team", updated_at: "2026-11-08T19:00:00Z", min_guest_version: "0.8.0"}});
      stub.state.resources = {"computer:e2b": e2bRow()};
      const small = await open({}, {width, height, theme, touch});
      if (width < 700) await small.page.locator('.settings-mhome [data-tab="admin-resources"]').click().catch(() => {});
      await ready(small.page);
      await note(small).waitFor();
      const overflow = await small.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert.ok(overflow <= 0, `${theme} ${width}: no horizontal overflow (${overflow}px)`);
      await note(small).scrollIntoViewIfNeeded();
      await shot(small.page, `build-stale-${theme}-${width}`);
      await small.close();
    }
    console.log("PASS image build: phase, custom note, every named failure with its action, minimum version, Retry build, polling while it builds");
  }
  }

  // ---- 6. Search & fetch: the member's chain editor over the organization endpoints.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    const row = (chain, id) => pane(page).locator(`.web-row[data-chain="${chain}"][data-id="${id}"]`);
    assert.deepEqual(await pane(page).locator('.web-row[data-chain="search"]').evaluateAll(rows => rows.map(r => r.dataset.id)), ["tavily", "brave", "searxng"]);
    assert.deepEqual(await pane(page).locator('.web-row[data-chain="fetch"]').evaluateAll(rows => rows.map(r => r.dataset.id)), ["firecrawl", "brightdata"]);
    assert.match(await text(row("search", "tavily")), /Needs a key/);
    // A key: set up, write-only.
    await row("search", "tavily").getByRole("button", {name: "Set up"}).click();
    await row("search", "tavily").locator("input[type=password]").fill(TAVILY_KEY);
    stub.writes.length = 0;
    await row("search", "tavily").getByRole("button", {name: "Save"}).click();
    await row("search", "tavily").locator('[data-status="ready"], .status-badge.is-ok').first().waitFor();
    assert.deepEqual(stub.writes, [`PUT /api/admin/org/resources/webtool:tavily ${JSON.stringify({key: TAVILY_KEY})}`]);
    assert.match(await text(row("search", "tavily")), /ends in …9876|…9876/);
    await holdsNoKey(page, TAVILY_KEY);
    // Test: ok, then the provider's refusal as a stable code.
    stub.writes.length = 0;
    await row("search", "tavily").getByRole("button", {name: "Test"}).click();
    await row("search", "tavily").getByText("Working").waitFor();
    assert.deepEqual(stub.writes, ["POST /api/admin/org/resources/webtool:tavily/test"]);
    stub.state.rejectKeys.add("webtool:tavily");
    await row("search", "tavily").getByRole("button", {name: "Test"}).click();
    await row("search", "tavily").locator('[data-code="auth_failed"]').waitFor();
    // Order: one PUT with the new order of that chain.
    stub.writes.length = 0;
    await row("search", "tavily").getByRole("button", {name: "Move Tavily down"}).click();
    await page.waitForFunction(() => document.querySelector('.admin-resources .web-row[data-chain="search"]').dataset.id === "brave");
    assert.match(stub.writes[0], /^PUT \/api\/admin\/org\/web-tools \{"search_order":\["brave","tavily","searxng"\]\}$/);
    // Switch: the disabled list names chain/id.
    stub.writes.length = 0;
    await row("fetch", "firecrawl").getByRole("switch").click();
    await pane(page).locator('.web-row[data-chain="fetch"][data-id="firecrawl"][data-status="needs_key"], .web-row[data-chain="fetch"][data-id="firecrawl"]').first().waitFor();
    assert.deepEqual(stub.writes, ['PUT /api/admin/org/web-tools {"disabled":["fetch/firecrawl"]}']);
    // Address: SearXNG is an address, saved through web-tools.
    await row("search", "searxng").getByRole("button", {name: "Set up"}).click();
    await row("search", "searxng").locator("input[type=url]").fill("http://searx.internal:8080");
    stub.writes.length = 0;
    await row("search", "searxng").getByRole("button", {name: "Save"}).click();
    await row("search", "searxng").getByText("http://searx.internal:8080").waitFor();
    assert.deepEqual(stub.writes, ['PUT /api/admin/org/web-tools {"searxng_url":"http://searx.internal:8080"}']);
    // The daily cap is the organization's, in the same control.
    stub.writes.length = 0;
    await page.locator("#web-limit-search").click();
    await page.locator('.ui-select-item[data-value="100"]').click();
    await page.waitForFunction(() => document.querySelector("#web-limit-search")?.dataset.value === "100");
    assert.deepEqual(stub.writes, ['PUT /api/admin/org/web-tools {"daily_search":100}']);
    await shot(page, "resources-web-light-1440");
    await close();
    console.log("PASS Search & fetch: key, Test, order, switch, address and daily cap go to the organization endpoints");
  }

  // ---- 7. The full page, populated, in both themes at desktop and phone width; no overflow, touch-sized.
  for (const theme of ["light", "dark"]) {
    stub.reset({members: {acct_two: "provided"}});
    stub.state.resources = {
      "provider:anthropic": {id: "provider:anthropic", kind: "model_provider", status: "verified", key_prefix: "sk-a", key_hint: "…abcd", verified_at: "2026-11-08T20:00:00Z", updated_at: "t0", config: {}},
      "provider:openai": {id: "provider:openai", kind: "model_provider", status: "rejected", key_prefix: "", key_hint: "…WXYZ", error_code: "invalid_key", updated_at: "t0", config: {}},
      "computer:e2b": {id: "computer:e2b", kind: "computer_provider", status: "verified", key_prefix: "e2b_", key_hint: "…4567", verified_at: "2026-11-08T20:00:00Z", updated_at: "t0", config: {}},
      "webtool:tavily": {id: "webtool:tavily", kind: "web_provider", status: "stored", key_prefix: "", key_hint: "…9876", updated_at: "t0", config: {}},
    };
    for (const [width, height, touch] of [[1440, 900, false], [390, 844, true]]) {
      const {page, close} = await open({}, {width, height, theme, touch});
      if (width < 700) await page.locator('.settings-mhome [data-tab="admin-resources"]').click().catch(() => {});
      await ready(page);
      await pane(page).locator(".web-row").first().waitFor();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert.ok(overflow <= 0, `${theme} ${width}: no horizontal overflow (${overflow}px)`);
      const clipped = await pane(page).locator(".provider-card").evaluateAll(cards => cards.filter(item => item.scrollWidth > item.clientWidth + 1).map(item => item.dataset.resource));
      assert.deepEqual(clipped, [], `${theme} ${width}: no card clips its content`);
      if (width < 700) {
        const small = await pane(page).locator("button.primary-button:visible, button.secondary-button:visible").evaluateAll(buttons => buttons.filter(button => button.getBoundingClientRect().height < 36).map(button => button.textContent.trim() || button.getAttribute("aria-label")));
        assert.deepEqual(small, [], `${theme} ${width}: primary and secondary buttons are touch-sized`);
      }
      await shot(page, `resources-${theme}-${width}`);
      await close();
    }
  }
  console.log("PASS populated page in both themes at 1440 and 390: no overflow, no clipped cards");

  // ---- 9. The member side of the same policy (Settings is the same page for everyone): models provided, the info banner, the
  //         administrator's own pages stay personal.
  {
    const capabilities = (ai, extra = {}) => JSON.stringify({ai_models: ai, computer: {mode: "host", source: "own"}, web_search: {mode: "own", source: "own"}, web_fetch: {mode: "own", source: "own"}, ...extra});
    const provided = {mode: "provided", source: "organization"};
    const organizationProviders = (configured = {openai: true}) => [
      {id: "codex", label: "Codex", kind: "oauth", configured: false, status: "disconnected", verified_at: "", error: "", source: "organization"},
      {id: "openai", label: "OpenAI", kind: "api_key", configured: Boolean(configured.openai), verified_at: "", error: "", source: "organization"},
      {id: "anthropic", label: "Claude", kind: "api_key", configured: Boolean(configured.anthropic), verified_at: "", error: "", source: "organization"},
      {id: "compatible", label: "OpenAI-compatible endpoint", kind: "endpoint", configured: false, verified_at: "", error: "", source: "organization"},
    ];
    const models = page => page.locator('.settings-page-body[data-page="models"]');

    // Provided and available: a read-only card, the providers with their state, no key input, no Codex.
    stub.reset();
    stub.state.providers = organizationProviders();
    const view = await open({tab: "models", policy: capabilities(provided)});
    await models(view.page).locator(".provided-card").waitFor();
    assert.match(await text(models(view.page).locator(".provided-card")), /Models provided by your organization.*Managed by policy.*Bots run on your organization's model accounts\./);
    assert.deepEqual(await models(view.page).locator(".provided-list li").evaluateAll(items => items.map(item => `${item.dataset.provider}:${item.dataset.configured}`)), ["openai:true", "anthropic:false", "compatible:false"], "providers the organization provides, without Codex");
    assert.equal(await models(view.page).locator("input[type=password], input[type=url], .provider-choices, [data-method]").count(), 0, "no key, address or sign-in choice");
    assert.equal(await models(view.page).locator(".provided-card").getByRole("button").count(), 0, "no dead button on the provided card");
    assert.ok(!/Codex|ChatGPT|Replace key|Remove/.test(await models(view.page).locator(".model-providers").innerText()), "nothing about a personal sign-in or key");
    assert.equal(await models(view.page).locator(".settings-banner").count(), 0, "a model is available: no waiting banner");
    // The model list is empty here: under a provided policy the page names who can fix it and offers no action the member cannot take.
    await models(view.page).locator('[data-managed="ai_models"]').waitFor();
    assert.match(await text(models(view.page).locator('[data-managed="ai_models"]')), /^No models available yet\. Your organization hasn't provisioned any\. Contact an administrator\.$/);
    assert.equal(await models(view.page).getByRole("button", {name: "Open settings"}).count(), 0, "no 'connect Codex or add a key' action under provided");
    assert.ok(!/Connect Codex|OpenAI or Claude API key/.test(await models(view.page).innerText()), "no instruction the member cannot follow");
    await shot(view.page, "member-models-provided-light-1440");
    await view.close();
    const phone = await open({tab: "models", policy: capabilities(provided)}, {width: 390, height: 844, theme: "dark", touch: true});
    await phone.page.locator('.settings-mhome [data-tab="models"]').click().catch(() => {});
    await models(phone.page).locator(".provided-card").waitFor();
    assert.ok((await phone.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)) <= 0, "no horizontal overflow on a phone");
    await shot(phone.page, "member-models-provided-dark-390");
    await phone.close();

    // Provided but the organization holds no model: the page says so (org_resource_missing from the Bot's side).
    stub.state.providers = organizationProviders({});
    const waiting = await open({tab: "models", policy: capabilities(provided)});
    await models(waiting.page).locator(".provided-card").waitFor();
    await models(waiting.page).locator(".settings-banner.is-warn").waitFor();
    assert.match(await text(models(waiting.page).locator(".settings-banner")), /Waiting for your administrator.*has not provided a model yet/);
    await shot(waiting.page, "member-models-waiting-light-1440");
    await waiting.close();

    // Own models (the default): the administrator's own Settings are the personal cards, with nothing that shares them.
    stub.state.providers = [{id: "codex", label: "Codex", kind: "oauth", configured: false, status: "disconnected", verified_at: "", error: ""}, {id: "openai", label: "OpenAI", kind: "api_key", configured: false, verified_at: "", error: ""}, {id: "anthropic", label: "Claude", kind: "api_key", configured: false, verified_at: "", error: ""}, {id: "compatible", label: "OpenAI-compatible endpoint", kind: "endpoint", configured: false, verified_at: "", error: ""}];
    const own = await open({tab: "models"});
    await models(own.page).locator(".provider-card").first().waitFor();
    assert.equal(await models(own.page).locator(".provided-card").count(), 0);
    assert.ok(!/global|organization|everyone|all accounts/i.test(await models(own.page).locator(".model-providers").innerText()), "the administrator's own Models page has no 'make global' and no mention of sharing");
    assert.equal(await own.page.locator(".policy-banner").count(), 0);
    await own.close();

    // General no longer carries a "provided by your organization" banner: the managed rows say it themselves (settings-simplify P1).
    const general = await open({tab: "general", policy: capabilities(provided)});
    await general.page.locator('.settings-page-body[data-page="general"] [data-section="account"]').waitFor();
    assert.equal(await general.page.locator('[data-testid="policy-banner"]').count(), 0, "no banner on General");
    await general.close();
    console.log("PASS member side: provided models are read-only with no key or sign-in, a missing model says so, General has no banner, the administrator's own pages stay personal");
  }

  // ---- 9b. Own models alongside the organization's (D79, P1): under provided the member may also sign in with ChatGPT unless the
  //          administrator forbids it. Three states, nothing but the ChatGPT card in "Your own", no dead button.
  {
    const capabilities = ownAllowed => JSON.stringify({ai_models: {mode: "provided", source: "organization", ...(ownAllowed === undefined ? {} : {own_allowed: ownAllowed})}, computer: {mode: "host", source: "own"}, web_search: {mode: "own", source: "own"}, web_fetch: {mode: "own", source: "own"}});
    const organization = [
      {id: "codex", label: "Codex", kind: "oauth", configured: false, status: "disconnected", verified_at: "", error: "", source: "organization"},
      {id: "openai", label: "OpenAI", kind: "api_key", configured: true, verified_at: "", error: "", source: "organization"},
      {id: "anthropic", label: "Claude", kind: "api_key", configured: false, verified_at: "", error: "", source: "organization"},
      {id: "compatible", label: "OpenAI-compatible endpoint", kind: "endpoint", configured: false, verified_at: "", error: "", source: "organization"},
    ];
    const models = page => page.locator('.settings-page-body[data-page="models"]');
    const section = page => models(page).locator(".provided-own");
    const noOwnKeys = async page => assert.equal(await models(page).locator("input[type=password], input[type=url], .provider-choices, .provider-openai, .provider-anthropic, .provider-compatible").count(), 0, "Claude, OpenAI and compatible cards stay hidden");
    const primaries = async page => assert.ok((await models(page).locator("button.primary-button:visible").count()) <= 1, "at most one orange primary button");

    // Allowed, not signed in: the organization's card stays, "Your own" holds only the ChatGPT card with one way in.
    stub.reset();
    stub.state.providers = organization;
    const out = await open({tab: "models", policy: capabilities(true)});
    await models(out.page).locator(".provided-card").waitFor();
    await section(out.page).locator('[data-provider="codex"]').waitFor();
    assert.match(await text(models(out.page).locator(".provided-card")), /Models provided by your organization/);
    assert.equal(await section(out.page).locator(".provider-card").count(), 1, "one card in Your own: the ChatGPT sign-in");
    assert.match(await text(section(out.page)), /^Your own .*ChatGPT.*Sign in with your ChatGPT account$/);
    assert.equal(await models(out.page).locator(".provided-own-off").count(), 0, "no 'organization manages' line while allowed");
    await noOwnKeys(out.page); await primaries(out.page);
    assert.equal(await models(out.page).locator(".settings-banner").count(), 0);
    await shot(out.page, "member-own-allowed-signed-out-light-1440");
    // Signing in goes through the Codex endpoints and shows the state.
    await section(out.page).locator('[data-method="chatgpt"]').click();
    await section(out.page).getByText("Codex connected").waitFor({timeout: 15000});
    assert.equal(stub.state.codex.connected, true);
    await out.close();

    // Allowed, signed in: connected state with Disconnect; the organization's card is unchanged.
    stub.state.codex = {connected: true, needs_reconnect: false};
    stub.state.defaultModel = "gpt-5";
    stub.state.models = [
      {id: "gpt-5", name: "GPT-5", provider: "openai", reasoning_efforts: ["low", "medium"], default_reasoning: "medium"},
      {id: "codex-gpt-6-luna", name: "Codex · GPT-6 Luna", provider: "codex", reasoning_efforts: ["medium"], default_reasoning: "medium"},
    ];
    const signed = await open({tab: "models", policy: capabilities(true)});
    await section(signed.page).getByText(/Codex connected/).waitFor();
    assert.equal(await models(signed.page).locator(".provided-card").count(), 1);
    assert.equal(await section(signed.page).getByRole("button", {name: "Disconnect Codex"}).count(), 1);
    // The default-model picker shows the saved default (the empty select in the first screenshot was a fixture with no model-settings) and lists Codex beside the organization's models.
    const picker = models(signed.page).locator(".ui-select-trigger").first();
    await picker.waitFor();
    assert.match(await text(picker), /GPT-5/, "the saved default is shown");
    assert.deepEqual((await selectItems(picker)).map(item => item.value).sort(), ["codex-gpt-6-luna", "gpt-5"], "Codex models are listed beside the organization's");
    await noOwnKeys(signed.page); await primaries(signed.page);
    await shot(signed.page, "member-own-allowed-signed-in-light-1440");
    await signed.close();
    const signedPhone = await open({tab: "models", policy: capabilities(true)}, {width: 390, height: 844, theme: "dark", touch: true});
    await signedPhone.page.locator('.settings-mhome [data-tab="models"]').click().catch(() => {});
    await section(signedPhone.page).locator('[data-provider="codex"]').waitFor();
    assert.ok((await signedPhone.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)) <= 0, "no horizontal overflow on a phone");
    await shot(signedPhone.page, "member-own-allowed-signed-in-dark-390");
    await signedPhone.close();
    stub.state.codex = {connected: false, needs_reconnect: false};
    stub.state.defaultModel = undefined; stub.state.models = undefined;
    const outPhone = await open({tab: "models", policy: capabilities(true)}, {width: 390, height: 844, theme: "dark", touch: true});
    await outPhone.page.locator('.settings-mhome [data-tab="models"]').click().catch(() => {});
    await section(outPhone.page).locator('[data-provider="codex"]').waitFor();
    assert.ok((await outPhone.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)) <= 0, "no horizontal overflow on a phone");
    await shot(outPhone.page, "member-own-allowed-signed-out-dark-390");
    await outPhone.close();

    // Not allowed (and an older server that does not say): the organization's card and one muted line, no sign-in, no button.
    for (const [label, flag] of [["forbidden", false], ["unsaid", undefined]]) {
      const off = await open({tab: "models", policy: capabilities(flag)});
      await models(off.page).locator(".provided-card").waitFor();
      assert.equal(await section(off.page).count(), 0, `${label}: no Your own section`);
      assert.equal(await models(off.page).locator('[data-managed="ai_models-own"]').innerText(), "Your organization manages the models for this account.");
      assert.equal(await models(off.page).locator("[data-method], .provider-chatgpt").count(), 0, `${label}: no ChatGPT sign-in`);
      await noOwnKeys(off.page);
      if (label === "forbidden") await shot(off.page, "member-own-forbidden-light-1440");
      await off.close();
    }
    const offPhone = await open({tab: "models", policy: capabilities(false)}, {width: 390, height: 844, theme: "dark", touch: true});
    await offPhone.page.locator('.settings-mhome [data-tab="models"]').click().catch(() => {});
    await models(offPhone.page).locator('[data-managed="ai_models-own"]').waitFor();
    await shot(offPhone.page, "member-own-forbidden-dark-390");
    await offPhone.close();
    console.log("PASS member side, own models alongside: Your own holds only the ChatGPT card when allowed, one muted line when not");
  }

  // A successful HTTP response with a missing status is still an error, never a render crash.
  {
    stub.reset();
    const {status, ...withoutStatus} = stub.handle(new URL("http://fixture.test/api/admin/backups"), "GET")[1];
    const malformed = {...stub, handle: (url, method, body) => new URL(url).pathname === "/api/admin/backups"
      ? [200, withoutStatus] : stub.handle(url, method, body)};
    const view = await openSettings({browser, fixture, stub: malformed, errors, params: {tab: "admin-resources"}});
    await card(view.page, "backup_store").getByRole("alert").filter({hasText: "The backup storage did not answer."}).waitFor();
    await view.close();
    console.log("PASS malformed backup view: the card shows its error state without a page error");
  }

  // ---- 8. Every language renders the page without raw keys.
  for (const language of ["zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
    stub.reset();
    const view = await open({}, {language});
    await ready(view.page);
    assert.ok(!/\b(admin\.org|policy|webtools)\.[a-z_.]+/.test(await pane(view.page).innerText()), `${language}: no raw i18n keys`);
    await view.close();
  }
  console.log("PASS all seven languages render without raw keys");

  assert.deepEqual(errors, [], `page errors: ${errors.join("; ")}`);
  assert.ok(!JSON.stringify(stub.state).includes(CLAUDE_KEY), "the stub itself never stored a key in a view");
  console.log("org resources: PASS");
} finally {
  await browser?.close();
  await fixture?.server.close();
  if (fixture) await rm(fixture.fixture, {recursive: true, force: true});
}
