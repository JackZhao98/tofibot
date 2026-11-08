// Settings > Administration > Organization resources (user groups, design 5.1 and 4.8). Mounts the real SettingsShell +
// SettingsPages as an administrator in a Vite fixture with the Administration API stubbed (scripts/lib/org-fixture.mjs): the
// page says these keys belong to the server and are not the administrator's own; AI model cards (Claude with its workspace ID,
// OpenAI, the OpenAI-compatible endpoint) save a key, show only the masked hint, Test and Remove; the E2B card and its template
// placeholder; the VM row; the Search & fetch chains (the member's chain editor over the organization endpoints: key, order,
// switch, address, Test); the "provided resources are missing" banner; refusals worded from their code. The key itself must never
// come back: after every save the page holds no part of it beyond the server's hint. Synthetic data only. Needs PLAYWRIGHT_MODULE.
//   UG_SHOTS=<dir>  also saves screenshots (light and dark, 1440x900 and 390x844).
import assert from "node:assert/strict";
import {mkdir, rm} from "node:fs/promises";
import {dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
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

  // ---- 1. The page: what it is, and the three sections.
  {
    stub.reset();
    const {page, close} = await open();
    await ready(page);
    assert.equal((await page.locator(".settings-page-title h2").textContent()).trim(), "Organization resources");
    assert.match(await text(pane(page).locator(".admin-intro")), /These keys belong to this server and serve accounts whose group provides them\. They are not your personal keys; yours are in Settings › AI Models, Environments and Search & fetch\./);
    assert.deepEqual(await pane(page).locator("[data-section]").evaluateAll(items => items.map(item => item.dataset.section).filter(Boolean)), ["models", "computers", "web"]);
    assert.deepEqual(await pane(page).locator("[data-resource]").evaluateAll(items => items.map(item => item.dataset.resource)), ["provider:anthropic", "provider:openai", "provider:compatible", "computer:e2b"]);
    assert.match(await text(card(page, "provider:anthropic")), /^Claude.*Needs setup/);
    assert.equal(await card(page, "provider:anthropic").locator("input[type=password]").count(), 1, "a key field while nothing is stored");
    assert.match(await text(pane(page).locator(".org-vm")), /Server VM This server can run VMs/);
    assert.equal(await pane(page).locator(".web-row").count(), 5, "the organization's search and fetch sources (no Bot computer)");
    assert.equal(await pane(page).locator('.web-row[data-id="native"], .web-row[data-id="builtin"], .web-row[data-id="computer"]').count(), 0, "an organization provides no model search, built-in fetch or Bot computer");
    assert.ok(!/\b(admin\.org|policy)\.[a-z_.]+/.test(await pane(page).innerText()), "no raw i18n keys");
    await shot(page, "resources-empty-light-1440");
    await close();
    console.log("PASS page: says the keys belong to the server, three sections, nothing stored yet");
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

  // ---- 5. E2B: key and an optional template; the build status is a named placeholder until shared computers exist.
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
    assert.equal(await e2b.locator(".org-template").getAttribute("data-slice"), "shared-computers", "the placeholder is named for what fills it");
    assert.match(await text(e2b.locator(".org-template")), /Template: tpl_shared_1/);
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
    console.log("PASS E2B card: key and template, masked, placeholder for the template build, VM row follows the host");
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

    // The Settings banner: shown while something is managed, opens the page that explains it, dismissible and remembered.
    const banner = await open({tab: "general", policy: capabilities(provided)});
    await banner.page.locator('[data-testid="policy-banner"]').waitFor();
    assert.match(await text(banner.page.locator('[data-testid="policy-banner"]')), /Some resources are provided by your organization.*Review/);
    await shot(banner.page, "member-banner-light-1440");
    await banner.page.locator('[data-testid="policy-banner"]').getByRole("button", {name: "Review"}).click();
    await banner.page.locator('.settings-tabs [data-tab="models"][aria-current="page"]').waitFor();
    await banner.page.locator('.settings-tabs [data-tab="general"]').click();
    await banner.page.getByRole("button", {name: "Dismiss"}).click();
    await banner.page.locator('[data-testid="policy-banner"]').waitFor({state: "detached"});
    await banner.page.reload();
    await banner.page.locator(".settings-shell").first().waitFor();
    await banner.page.locator('.settings-page-body[data-page="general"] [data-section="account"]').waitFor();
    assert.equal(await banner.page.locator('[data-testid="policy-banner"]').count(), 0, "a dismissed banner stays dismissed in this browser");
    // A change in what is managed shows it again.
    await banner.context.close();
    const again = await open({tab: "general", policy: capabilities(provided, {web_search: {mode: "off", source: "none"}})});
    await again.page.locator('[data-testid="policy-banner"]').waitFor();
    await again.close();
    console.log("PASS member side: provided models are read-only with no key or sign-in, a missing model says so, the banner links and is dismissible, the administrator's own pages stay personal");
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
