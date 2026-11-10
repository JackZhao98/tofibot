// Settings > Administration > Groups, and the Group column and Overrides on Accounts (user groups, design 5.1). Mounts the real
// SettingsShell + SettingsPages as an administrator in a Vite fixture with the Administration API stubbed as a small stateful
// gateway (scripts/lib/org-fixture.mjs): the navigation group, the group list and editor (one row per capability, VM only on a
// KVM host, built-in Self-serve read-only), creating, renaming and deleting a group (group_in_use refused), batch membership,
// the missing-resource note, the Group column with an inline menu, multi-select with "Move N to group…", and an account's
// Group and Overrides ("Reset to group"). Synthetic data only. Needs PLAYWRIGHT_MODULE.
//   UG_SHOTS=<dir>  also saves screenshots (light and dark, 1440x900 and 390x844).
import assert from "node:assert/strict";
import {mkdir, rm} from "node:fs/promises";
import {dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";
import {pickSelect, selectItems, selectValue} from "./lib/select.mjs";
import {createOrgStub, openSettings, startFixture} from "./lib/org-fixture.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium, webkit} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
// UG_BROWSER=webkit runs the same suite in WebKit (the shots and the rendering checks).
const engine = process.env.UG_BROWSER === "webkit" ? webkit : chromium;
const shots = process.env.UG_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

let fixture, browser;
try {
  fixture = await startFixture(ui, "admin-groups");
  browser = await engine.launch(engine === chromium ? {executablePath: process.env.TOFI_TEST_CHROME || undefined} : {});
  const stub = createOrgStub();
  const errors = [];
  const open = (params = {}, options = {}) => openSettings({browser, fixture, stub, errors, params, ...options});
  const body = (page, tab) => page.locator(`.settings-page-body[data-page="${tab}"]`);
  const shot = async (page, name) => { if (shots) await page.screenshot({path: join(shots, `${name}.png`)}); };
  const text = async locator => (await locator.innerText()).replace(/\s+/g, " ").trim();
  const radio = (scope, name) => scope.getByRole("radio", {name});
  // The AI models row is a select with three choices (own, provided, provided_only), not a segmented control.
  const aiTrigger = scope => scope.locator('[data-capability="ai_models"] .ui-select-trigger');

  // ---- 1. The Administration group: its own group after System, three pages, admin only.
  {
    stub.reset();
    const {page, close} = await open({tab: "admin-groups"});
    assert.deepEqual(await page.$$eval(".settings-tabs .settings-nav-group>p", items => items.map(item => item.textContent)), ["You", "Bots can use", "Workspace", "System", "Administration"]);
    assert.deepEqual(await page.$$eval('.settings-tabs [data-group="admin"] button[data-tab]', items => items.map(item => item.dataset.tab)), ["admin", "admin-groups", "admin-resources"]);
    assert.deepEqual(await page.$$eval('.settings-tabs [data-group="admin"] .settings-nav-label', items => items.map(item => item.textContent)), ["Accounts", "Groups", "Organization resources"]);
    assert.equal(await page.locator('.settings-tabs [data-group="system"] [data-tab="admin"]').count(), 0, "Accounts left System");
    await body(page, "admin-groups").locator(".ag-item").first().waitFor();
    assert.equal((await page.locator(".settings-page-title h2").textContent()).trim(), "Groups");
    await close();
    console.log("PASS navigation: Administration is its own group with Accounts, Groups and Organization resources");
  }

  // ---- 2. The list and the editor: built-in badges, counts, a row per capability, Self-serve read-only, Provided editable.
  {
    stub.reset({members: {acct_two: "provided"}});
    const {page, close} = await open({tab: "admin-groups"});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    assert.deepEqual(await pane.locator(".ag-item").evaluateAll(items => items.map(item => item.dataset.group)), ["self_serve", "provided"]);
    assert.match(await text(pane.locator('.ag-item[data-group="self_serve"]')), /Self-serve Built in 3 members/);
    assert.match(await text(pane.locator('.ag-item[data-group="provided"]')), /Provided Built in 1 member$/);
    assert.deepEqual(stub.reads, [], "the groups page needs no per-account policy read either");
    assert.equal(await pane.locator('.ag-item[aria-current="true"]').getAttribute("data-group"), "self_serve", "the first group is open");
    assert.deepEqual(await pane.locator(".ag-policy-row").evaluateAll(rows => rows.map(row => row.dataset.capability)), ["ai_models", "computer", "web_search", "web_fetch"], "one row per capability");
    assert.match(await text(pane.locator(".ag-editor")), /Self-serve is today's behavior and is read-only/);
    assert.equal(await selectValue(aiTrigger(pane)), "own");
    assert.equal(await aiTrigger(pane).isDisabled(), true, "Self-serve cannot be edited");
    assert.equal(await pane.locator('.ag-editor [data-action="reset"], .ag-editor .danger-zone').count(), 0, "a built-in group has no delete");
    await shot(page, "groups-selfserve-light-1440");

    await pane.locator('.ag-item[data-group="provided"]').click();
    assert.equal(await pane.locator(".ag-editor").getAttribute("data-group"), "provided");
    assert.equal(await selectValue(aiTrigger(pane)), "provided", "a stored provided row without allow_own reads as provided, members may also use their own");
    assert.equal(await aiTrigger(pane).isDisabled(), false, "Provided is editable");
    assert.equal(await pane.locator('[data-capability="computer"] .ui-select-trigger').getAttribute("data-value"), "provided");
    assert.deepEqual(await pane.locator('[data-capability="computer"] .policy-control [role="radio"]').allInnerTexts(), ["VM", "E2B"], "a KVM host offers the VM source");
    assert.equal(await radio(pane.locator('.ag-policy-row[data-capability=\"computer\"]'), "VM").getAttribute("aria-checked"), "true");
    assert.deepEqual((await selectItems(pane.locator('[data-capability="computer"] .ui-select-trigger'))).map(item => item.text), ["Server VM", "Own E2B key", "Organization", "None"]);
    assert.match(await text(pane.locator(".ag-members")), /Sam Example/);
    assert.match(await text(pane.locator('[data-section="members"] .settings-sec-head')), /Members \(1\)/);
    await shot(page, "groups-provided-light-1440");
    await close();
    console.log("PASS list and editor: badges, counts, one row per capability, Self-serve read-only, VM source on a KVM host");
  }

  // ---- 3. No KVM host: the VM source is not offered. Editing sends the whole policy in one PATCH and shows it saved.
  {
    stub.reset({kvm: false});
    stub.state.groups.find(group => group.id === "provided").policies.computer = {mode: "provided", source: "e2b", params: {}};
    const {page, close} = await open({tab: "admin-groups"});
    const calls = stub.writes;
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    await pane.locator('.ag-item[data-group="provided"]').click();
    assert.deepEqual(await pane.locator('[data-capability="computer"] .policy-control [role="radio"]').allInnerTexts(), ["E2B"], "no VM source without a KVM host");
    stub.writes.length = 0;
    await radio(pane.locator('.ag-policy-row[data-capability=\"web_search\"]'), "Own keys").click();
    await pane.locator(".ag-saved").getByText("Saved").waitFor();
    assert.equal(calls.length, 1);
    const patch = JSON.parse(calls[0].replace(/^PATCH \/api\/admin\/groups\/provided /, ""));
    assert.deepEqual(Object.keys(patch.policies).sort(), ["ai_models", "computer", "web_fetch", "web_search"], "the four rows are replaced as a whole");
    assert.deepEqual(patch.policies.web_search, {mode: "own", params: {}});
    assert.deepEqual(patch.policies.computer, {mode: "provided", source: "e2b", params: {}});
    assert.equal(await radio(pane.locator('.ag-policy-row[data-capability=\"web_search\"]'), "Own keys").getAttribute("aria-checked"), "true");
    // The computer select: choosing Organization from None keeps a source the host can run.
    await pickSelect(pane.locator('[data-capability="computer"] .ui-select-trigger'), "none");
    await pane.locator('[data-capability="computer"] [data-mode="none"]').waitFor();
    await pickSelect(pane.locator('[data-capability="computer"] .ui-select-trigger'), "provided");
    await pane.locator('[data-capability="computer"] [data-mode="provided"]').waitFor();
    assert.equal(JSON.parse(stub.writes.at(-1).replace(/^PATCH \/api\/admin\/groups\/provided /, "")).policies.computer.source, "e2b", "E2B when the host has no VM");
    await close();
    console.log("PASS editing: one PATCH with all four rows, no VM source without KVM");
  }

  // ---- 3b. The AI models row: three choices stored as a mode plus allow_own; an account override takes the same three.
  {
    stub.reset({members: {acct_two: "provided"}});
    const {page, close} = await open({tab: "admin-groups"});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    await pane.locator('.ag-item[data-group="provided"]').click();
    assert.deepEqual((await selectItems(aiTrigger(pane))).map(item => [item.value, item.text]), [["own", "Members bring their own"], ["provided", "Provided; members may also use their own"], ["provided_only", "Provided only"]]);
    const stored = () => JSON.parse(stub.writes.at(-1).replace(/^PATCH \/api\/admin\/groups\/provided /, "")).policies.ai_models;
    await pickSelect(aiTrigger(pane), "provided_only");
    await pane.locator(".ag-saved").getByText("Saved").waitFor();
    assert.deepEqual(stored(), {mode: "provided", params: {allow_own: false}}, "Provided only stores allow_own=false");
    assert.equal(await selectValue(aiTrigger(pane)), "provided_only");
    await pickSelect(aiTrigger(pane), "provided");
    await pane.locator(".ag-saved").getByText("Saved").waitFor();
    assert.deepEqual(stored(), {mode: "provided", params: {allow_own: true}}, "Provided, members may also use their own stores allow_own=true");
    await pickSelect(aiTrigger(pane), "own");
    await pane.locator(".ag-saved").getByText("Saved").waitFor();
    assert.deepEqual(stored(), {mode: "own", params: {}}, "own carries no parameter");
    await close();
    const overrides = await open({tab: "admin"});
    const detailPane = body(overrides.page, "admin");
    await detailPane.locator(".admin-row").first().waitFor();
    await detailPane.locator('.admin-row[data-account="acct_two"]').click();
    const detail = detailPane.locator('[data-testid="admin-detail"]');
    await detail.locator('.ag-override-row[data-capability="ai_models"]').waitFor();
    stub.writes.length = 0;
    await pickSelect(aiTrigger(detail), "provided_only");
    await detail.locator('[data-capability="ai_models"][data-overridden="true"]').waitFor();
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/accounts/acct_two/policy {"overrides":{"ai_models":{"mode":"provided","params":{"allow_own":false}}}}']);
    assert.match(await text(detail.locator('[data-capability="ai_models"] .settings-row-copy')), /Overridden · Provided only \(group: Own keys|group: Members bring their own|group: Provided; members may also use their own\)/);
    await overrides.close();
    console.log("PASS AI models row: three choices stored as mode + allow_own, group and account override");
  }

  // ---- 4. Create, rename, add members (batch), delete is refused while members remain, then allowed.
  {
    stub.reset();
    const {page, close} = await open({tab: "admin-groups"});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    await pane.getByRole("button", {name: "New group"}).click();
    const sheet = page.getByRole("dialog", {name: "New group"});
    await sheet.getByLabel("Group name").fill("Interns");
    stub.writes.length = 0;
    await sheet.getByRole("button", {name: "Create group"}).click();
    await sheet.waitFor({state: "detached"});
    assert.deepEqual(stub.writes, ['POST /api/admin/groups {"name":"Interns"}']);
    assert.equal(await pane.locator('.ag-item[aria-current="true"] strong').innerText(), "Interns", "the new group opens");
    assert.equal(await pane.locator('.ag-item[data-group="provided"] .settings-tag').count(), 1);
    assert.equal(await pane.locator('.ag-item[aria-current="true"] .settings-tag').count(), 0, "a custom group has no Built in badge");
    assert.equal(await selectValue(aiTrigger(pane)), "own", "a new group starts as Self-serve");

    // A duplicate name is refused with words, not a code.
    await pane.getByRole("button", {name: "New group"}).click();
    await page.getByRole("dialog", {name: "New group"}).getByLabel("Group name").fill("Provided");
    await page.getByRole("dialog", {name: "New group"}).getByRole("button", {name: "Create group"}).click();
    await page.getByRole("dialog", {name: "New group"}).getByRole("alert").getByText("A group with that name already exists.").waitFor();
    await page.getByRole("dialog", {name: "New group"}).getByRole("button", {name: "Cancel"}).click();

    // Rename.
    stub.writes.length = 0;
    await pane.getByLabel("Name", {exact: true}).fill("Summer interns");
    await pane.getByRole("button", {name: "Rename"}).click();
    await pane.locator('.ag-item[aria-current="true"] strong').getByText("Summer interns").waitFor();
    assert.match(stub.writes[0], /^PATCH \/api\/admin\/groups\/grp_1 \{"name":"Summer interns"\}$/);

    // Add members: only accounts from other groups, a batch of two in one call.
    await pane.getByRole("button", {name: "Add members"}).click();
    const add = page.getByRole("dialog", {name: "Add members to Summer interns"});
    assert.deepEqual(await add.locator(".ag-pick-row").evaluateAll(rows => rows.map(row => row.dataset.account)), ["acct_sample", "acct_two", "acct_three", "acct_four"]);
    assert.match(await text(add.locator('[data-account="acct_two"]')), /Now in Self-serve/);
    assert.equal(await add.getByRole("button", {name: /^Add/}).isDisabled(), true, "nothing picked");
    await add.locator('[data-account="acct_two"] input').check();
    await add.locator('[data-account="acct_three"] input').check();
    stub.writes.length = 0;
    await add.getByRole("button", {name: "Add 2"}).click();
    await add.waitFor({state: "detached"});
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/groups/grp_1/members {"account_ids":["acct_two","acct_three"]}']);
    await pane.locator('[data-section="members"] .settings-sec-head').getByText("Members (2)").waitFor();
    assert.deepEqual(await pane.locator(".ag-member").evaluateAll(rows => rows.map(row => row.dataset.account)), ["acct_two", "acct_three"]);

    // Delete is refused while members remain (the control says why), allowed once they are moved.
    const del = pane.locator(".danger-zone");
    assert.equal(await del.getByRole("button", {name: "Delete group"}).isDisabled(), true);
    assert.match(await text(del), /Move its 2 members to another group first\./);
    await pane.locator('.ag-member[data-account="acct_two"] .group-menu-trigger').click();
    await page.locator('.group-menu [data-group-option="self_serve"]').click();
    await pane.locator(".ag-member").nth(1).waitFor({state: "detached"});
    await pane.locator('.ag-member[data-account="acct_three"] .group-menu-trigger').click();
    await page.locator('.group-menu [data-group-option="provided"]').click();
    await pane.locator(".ag-member").first().waitFor({state: "detached"});
    assert.match(await text(pane.locator(".ag-members")), /No members\./);
    await del.getByRole("button", {name: "Delete group"}).click();
    const confirm = page.getByRole("dialog", {name: "Delete Summer interns?"});
    assert.match(await text(confirm), /can't be undone/);
    await confirm.getByRole("button", {name: "Cancel"}).click();
    await confirm.waitFor({state: "detached"});
    assert.equal(await pane.locator('.ag-item[data-group="grp_1"]').count(), 1, "Cancel deletes nothing");
    stub.writes.length = 0;
    await del.getByRole("button", {name: "Delete group"}).click();
    await page.getByRole("dialog", {name: "Delete Summer interns?"}).getByRole("button", {name: "Delete group"}).click();
    await pane.locator('.ag-item[data-group="grp_1"]').waitFor({state: "detached"});
    assert.deepEqual(stub.writes, ["DELETE /api/admin/groups/grp_1"]);
    assert.equal(await pane.locator('.ag-item[aria-current="true"]').count(), 1, "another group opens");
    await close();
    console.log("PASS create, rename, batch add, delete refused while members remain and then allowed");
  }

  // ---- 5. A server refusal shows named words (group_in_use) and changes nothing on the page.
  {
    stub.reset();
    const {page, close} = await open({tab: "admin-groups"});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    await pane.locator('.ag-item[data-group="provided"]').click();
    stub.state.failNext = Object.assign(/\/api\/admin\/groups\/provided$/, {code: "invalid_policy"});
    await radio(pane.locator('.ag-policy-row[data-capability=\"web_fetch\"]'), "Own keys").click();
    await pane.getByRole("alert").getByText("That policy is not valid.").waitFor();
    assert.equal(await radio(pane.locator('.ag-policy-row[data-capability=\"web_fetch\"]'), "Organization").getAttribute("aria-checked"), "true", "the row keeps the stored value");
    await close();
    console.log("PASS refusals are worded from the code and leave the stored value on screen");
  }

  // ---- 6. A group that provides what the organization lacks says so, with a way to the resource page.
  {
    stub.reset();
    const {page, close} = await open({tab: "admin-groups"});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    await pane.locator('.ag-item[data-group="provided"]').click();
    await pane.locator('[data-missing="ai_models"]').waitFor();
    assert.deepEqual(await pane.locator("[data-missing]").evaluateAll(items => items.map(item => item.dataset.missing)), ["ai_models", "web_search", "web_fetch"], "the VM needs no organization key");
    await pane.locator('[data-missing="ai_models"]').getByRole("button", {name: "Open Organization resources"}).click();
    await body(page, "admin-resources").locator(".admin-resources").waitFor();
    assert.equal(await page.locator('.settings-tabs [data-tab="admin-resources"]').getAttribute("aria-current"), "page");
    await close();
    console.log("PASS a provided capability without an organization resource is flagged and links to Organization resources");
  }

  // ---- 7. Accounts: the Group column, an inline change, and a bulk move.
  {
    stub.reset({members: {acct_two: "provided"}});
    const {page, close} = await open({tab: "admin"});
    const pane = body(page, "admin");
    await pane.locator(".admin-row").first().waitFor();
    assert.match(await text(pane.locator(".admin-row-head")), /Group$/);
    await pane.locator('.admin-row-wrap:has([data-account="acct_two"]) .group-menu-trigger:has-text("Provided")').waitFor();
    assert.equal(await text(pane.locator('.admin-row-wrap:has([data-account="acct_three"]) .group-menu-trigger')), "Self-serve");
    assert.deepEqual(stub.reads, [], "the list carries each account's group (GET /api/admin/accounts): no per-account policy read");
    assert.equal(await pane.locator("button.admin-row").count(), 4, "the row is still one button; the checkbox and menu sit beside it");
    // One account, inline.
    stub.writes.length = 0;
    await pane.locator('.admin-row-wrap:has([data-account="acct_three"]) .group-menu-trigger').click();
    await page.locator('.group-menu [data-group-option="provided"]').click();
    await pane.locator('.admin-row-wrap:has([data-account="acct_three"]) .group-menu-trigger:has-text("Provided")').waitFor();
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/groups/provided/members {"account_ids":["acct_three"]}']);
    // Bulk: select two, move them together, selection clears.
    assert.equal(await pane.locator('[data-testid="admin-bulk"]').count(), 0, "no bar until something is selected");
    await pane.locator('[data-select="acct_two"]').check();
    await pane.locator('[data-select="acct_four"]').check();
    const bar = pane.locator('[data-testid="admin-bulk"]');
    assert.match(await text(bar), /2 selected Move 2 to group… Clear selection/);
    await shot(page, "accounts-bulk-light-1440");
    stub.writes.length = 0;
    await bar.getByRole("button", {name: "Move 2 to group…"}).click();
    await page.locator('.group-menu [data-group-option="self_serve"]').click();
    await bar.waitFor({state: "detached"});
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/groups/self_serve/members {"account_ids":["acct_two","acct_four"]}']);
    assert.equal(await text(pane.locator('.admin-row-wrap:has([data-account="acct_two"]) .group-menu-trigger')), "Self-serve");
    // A refused move changes nothing and says why.
    stub.state.failNext = Object.assign(/members$/, {code: "not_found"});
    await pane.locator('[data-select="acct_two"]').check();
    await pane.getByRole("button", {name: "Move 1 to group…"}).click();
    await page.locator('.group-menu [data-group-option="provided"]').click();
    await pane.getByRole("alert").getByText("That item no longer exists.").waitFor();
    assert.equal(await text(pane.locator('.admin-row-wrap:has([data-account="acct_two"]) .group-menu-trigger')), "Self-serve");
    assert.deepEqual(stub.reads, [], "moving accounts reads the accounts list again, never one policy per account");
    await close();
    console.log("PASS accounts: Group column, inline change, bulk move and a refused move");
  }

  // ---- 8. Account detail: Group and Overrides ("Follows group", set a value, "Reset to group").
  {
    stub.reset({members: {acct_two: "provided"}});
    const {page, close} = await open({tab: "admin"});
    const pane = body(page, "admin");
    await pane.locator(".admin-row").first().waitFor();
    await pane.locator('.admin-row[data-account="acct_two"]').click();
    const detail = pane.locator('[data-testid="admin-detail"]');
    await detail.locator('.ag-override-row[data-capability="ai_models"]').waitFor();
    assert.equal(await text(detail.locator(".group-menu-trigger")), "Provided");
    assert.match(await text(detail.locator('[data-capability="ai_models"] .settings-row-copy')), /AI models Follows group · Provided; members may also use their own/);
    assert.match(await text(detail.locator('[data-capability="computer"] .settings-row-copy')), /Follows group · Organization · VM/);
    assert.equal(await detail.locator('[data-action="reset"]').count(), 0, "nothing is overridden yet");
    await shot(page, "account-overrides-light-1440");
    // Set a value: one override row.
    stub.writes.length = 0;
    await radio(detail.locator('.ag-override-row[data-capability=\"web_search\"]'), "Off").click();
    await detail.locator('[data-capability="web_search"][data-overridden="true"]').waitFor();
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/accounts/acct_two/policy {"overrides":{"web_search":{"mode":"off","params":{}}}}']);
    assert.match(await text(detail.locator('[data-capability="web_search"] .settings-row-copy')), /Overridden · Off \(group: Organization\)/);
    assert.equal(await radio(detail.locator('.ag-override-row[data-capability=\"web_search\"]'), "Off").getAttribute("aria-checked"), "true");
    // A second override is added to the first; Reset removes only its own.
    await radio(detail.locator('.ag-override-row[data-capability=\"web_fetch\"]'), "Built-in only").click();
    await detail.locator('[data-capability="web_fetch"][data-overridden="true"]').waitFor();
    assert.deepEqual(Object.keys(JSON.parse(stub.writes.at(-1).replace(/^PUT \/api\/admin\/accounts\/acct_two\/policy /, "")).overrides).sort(), ["web_fetch", "web_search"]);
    stub.writes.length = 0;
    await detail.locator('[data-capability="web_search"] [data-action="reset"]').click();
    await detail.locator('[data-capability="web_search"][data-overridden="false"]').waitFor();
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/accounts/acct_two/policy {"overrides":{"web_fetch":{"mode":"builtin","params":{}}}}']);
    assert.match(await text(detail.locator('[data-capability="web_search"] .settings-row-copy')), /Follows group · Organization/);
    // Changing the group from the detail.
    stub.writes.length = 0;
    await detail.locator(".group-menu-trigger").click();
    await page.locator('.group-menu [data-group-option="self_serve"]').click();
    await detail.locator('.group-menu-trigger:has-text("Self-serve")').waitFor();
    assert.deepEqual(stub.writes.filter(line => line.startsWith("PUT")), ['PUT /api/admin/accounts/acct_two/policy {"group_id":"self_serve"}']);
    assert.match(await text(detail.locator('[data-capability="ai_models"] .settings-row-copy')), /Follows group · Members bring their own/);
    await close();
    console.log("PASS account detail: follows the group, overrides one capability at a time, resets, changes group");
  }

  // ---- 9. Phone width, both themes: the pages fit, the editor is a pushed page with a way back, the bulk bar stays on screen.
  for (const theme of ["light", "dark"]) {
    stub.reset({members: {acct_two: "provided"}});
    const phone = await open({tab: "admin-groups"}, {width: 390, height: 844, theme, touch: true});
    const {page} = phone;
    await page.locator('.settings-mhome [data-tab="admin-groups"]').click().catch(() => {});
    const pane = body(page, "admin-groups");
    await pane.locator(".ag-item").first().waitFor();
    assert.equal(await pane.locator(".ag-editor").isVisible(), false, "the list comes first on a phone");
    await shot(page, `groups-list-${theme}-390`);
    await pane.locator('.ag-item[data-group="provided"]').click();
    await pane.locator(".ag-policy-row").first().waitFor();
    assert.equal(await pane.locator(".ag-back").isVisible(), true);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert.ok(overflow <= 0, `the editor fits the phone (overflow ${overflow}px)`);
    const tooSmall = await pane.locator("button:visible:not([role=radio])").evaluateAll(buttons => buttons.filter(button => { const box = button.getBoundingClientRect(); return box.height < 36; }).map(button => button.textContent.trim()));
    assert.deepEqual(tooSmall, [], "buttons are at least 36px tall (the shared Segmented control keeps its own size)");
    await shot(page, `groups-editor-${theme}-390`);
    await pane.locator(".ag-back").click();
    await pane.locator(".ag-side").waitFor();
    await phone.close();
    // Accounts on a phone: the menu sits under the row, the bar is visible.
    const accounts = await open({tab: "admin"}, {width: 390, height: 844, theme, touch: true});
    await accounts.page.locator('.settings-mhome [data-tab="admin"]').click().catch(() => {});
    const list = body(accounts.page, "admin");
    await list.locator(".admin-row").first().waitFor();
    await list.locator('[data-select="acct_two"]').check();
    const barBox = await list.locator('[data-testid="admin-bulk"]').boundingBox();
    assert.ok(barBox && barBox.y + barBox.height <= 844 && barBox.x >= 0 && barBox.x + barBox.width <= 390, "the bulk bar is inside the phone screen");
    const flow = await accounts.page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert.ok(flow <= 0, `accounts fit the phone (overflow ${flow}px)`);
    await shot(accounts.page, `accounts-bulk-${theme}-390`);
    await accounts.close();
  }
  console.log("PASS phone width in both themes: list then pushed editor, no overflow, touch-sized, bulk bar on screen");

  // ---- 10. Dark at desktop width, and a non-English language renders without raw keys.
  {
    stub.reset({members: {acct_two: "provided"}});
    const dark = await open({tab: "admin-groups"}, {theme: "dark"});
    await body(dark.page, "admin-groups").locator(".ag-item").first().waitFor();
    await body(dark.page, "admin-groups").locator('.ag-item[data-group="provided"]').click();
    await shot(dark.page, "groups-provided-dark-1440");
    await dark.close();
    for (const language of ["zh-CN", "ja", "de", "fr", "ko", "zh-TW"]) {
      const view = await open({tab: "admin-groups"}, {language});
      const pane = body(view.page, "admin-groups");
      await pane.locator(".ag-item").first().waitFor();
      await pane.locator('.ag-item[data-group="provided"]').click();
      assert.ok(!/\b(admin|policy)\.[a-z_.]+/.test(await pane.innerText()), `${language}: no raw i18n keys`);
      await view.close();
    }
    console.log("PASS dark theme and all seven languages render without raw keys");
  }

  assert.deepEqual(errors, [], `page errors: ${errors.join("; ")}`);
  console.log("admin groups: PASS");
} finally {
  await browser?.close();
  await fixture?.server.close();
  if (fixture) await rm(fixture.fixture, {recursive: true, force: true});
}
