// Actual production editors + isolated HTTP/SQLite. No stubbed update methods.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const backend = process.env.TOFI_METADATA_API;
assert.ok(backend, "TOFI_METADATA_API must name the synthetic fixture");
const origin = process.env.TOFI_METADATA_ORIGIN || "http://127.0.0.1:5198";
const output = resolve(process.env.TOFI_METADATA_OUTPUT || "../docs/acceptance/display-metadata");
await mkdir(output, { recursive: true });
async function readFixture() {
  const response = await fetch(backend + "/acceptance/context");
  assert.equal(response.status, 200); return response.json();
}
async function externalPatch(path, input) {
  const response = await fetch(backend + path, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) });
  assert.equal(response.status, 200, await response.clone().text()); return response.json();
}
const seed = await readFixture();
const memoryID = seed.memories[0].id;
const scheduleID = seed.schedules.find(item => item.kind === "daily").id;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const results = [];
try {
  for (const kind of ["memory", "schedule"]) {
    for (const scenario of ["title-only-refresh", "description-only-refresh", "body-restored-with-title", "body-conflict-before-refresh", "title-conflict-after-refresh", "all-restored-no-op", "body-edit-unrelated-refresh"]) {
      const id = kind === "memory" ? memoryID : scheduleID;
      const path = `/api/${kind === "memory" ? "memories" : "schedules"}/${id}`;
      const baseline = await externalPatch(path, { title: `Original ${kind} title`, description: `Original ${kind} description`, content: kind === "memory" ? "原始事实，保留中文。" : "Original English execution instruction with quoted ‘上海’." });
      const fixture = await readFixture();
      const context = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
      await context.addInitScript(fixture => { window.metadataFixture = fixture; }, fixture);
      const page = await context.newPage();
      const pageErrors = []; page.on("pageerror", error => pageErrors.push(error.message));
      const requests = [];
      page.on("request", request => { if (request.method() === "PATCH" && new URL(request.url()).pathname === path) requests.push(request.postDataJSON()); });
      await page.goto(origin + "/test-fixtures/metadata.html");
      await page.getByRole("button", { name: kind === "memory" ? "记忆列表" : "日程列表", exact: true }).click();
      let row;
      if (kind === "memory") {
        row = page.locator(".memory-card").first();
      } else {
        const index = fixture.schedules.findIndex(item => item.id === id);
        row = page.locator(".work-schedule").nth(index);
        await row.locator(":scope > details > summary").click();
      }
      await row.getByRole("button", { name: "编辑", exact: true }).click();
      const title = page.getByRole("textbox", { name: kind === "memory" ? "编辑记忆标题" : "编辑日程标题", exact: true });
      const description = page.getByRole("textbox", { name: kind === "memory" ? "编辑记忆说明" : "编辑日程说明", exact: true });
      const content = page.getByRole("textbox", { name: kind === "memory" ? "编辑记忆" : "编辑日程执行指令", exact: true });
      const localTitle = `Local ${kind} title`;
      const localDescription = `Local ${kind} description`;
      const localContent = kind === "memory" ? "本地编辑的新事实。" : "Deliberately edited local English instruction.";
      const remoteContent = kind === "memory" ? "外部修正的事实，禁止回退。" : "Corrected English instruction from another editor.";
      let external;
      let changedKey;
      const conflict = scenario.includes("conflict");
      const noOp = scenario === "all-restored-no-op";
      if (scenario === "description-only-refresh") {
        await description.fill(localDescription); changedKey = "description";
        external = { title: `Remote ${kind} title`, content: remoteContent };
      } else if (scenario === "body-conflict-before-refresh") {
        await content.fill(localContent); changedKey = "content";
        external = { content: remoteContent };
      } else if (scenario === "body-edit-unrelated-refresh") {
        await content.fill(localContent); changedKey = "content";
        external = { title: `Remote ${kind} title`, description: `Remote ${kind} description` };
      } else {
        await title.fill(localTitle); changedKey = "title";
        if (scenario === "body-restored-with-title" || noOp) {
          await content.fill(localContent); await content.fill(baseline.content);
        }
        if (noOp) { await title.fill(baseline.title); changedKey = undefined; }
        external = { content: remoteContent, description: `Remote ${kind} description`, ...((noOp || scenario === "title-conflict-after-refresh") ? { title: `Remote ${kind} title` } : {}) };
      }
      const remote = await externalPatch(path, external);
      if (scenario !== "body-conflict-before-refresh") {
        const refreshed = page.waitForResponse(response => response.request().method() === "GET" && response.url().includes(kind === "memory" ? `/api/conversations/${fixture.conversation.id}/memories` : `/api/bots/${fixture.bot.id}/schedules`));
        await page.evaluate(() => window.dispatchEvent(new Event("metadata-acceptance-refresh")));
        await refreshed;
        if (kind === "memory") {
          await page.waitForFunction(revision => document.querySelector("#memories")?.getAttribute("data-revision") === String(revision), remote.revision);
        } else if (external.title) {
          await row.locator(":scope > strong").filter({ hasText: external.title }).waitFor();
        } else {
          await row.locator(":scope > .work-schedule-description").filter({ hasText: external.description }).waitFor();
        }
      }
      // Refreshes must not replace the draft or edit-open baseline.
      assert.equal(await content.inputValue(), changedKey === "content" ? localContent : baseline.content);
      const form = page.locator(kind === "memory" ? ".memory-edit" : ".schedule-edit");
      let response;
      if (noOp) {
        await form.getByRole("button", { name: "保存", exact: true }).click();
        await form.waitFor({ state: "detached" });
      } else {
        const saved = page.waitForResponse(response => response.request().method() === "PATCH" && new URL(response.url()).pathname === path);
        await form.getByRole("button", { name: "保存", exact: true }).click();
        response = await saved;
        assert.equal(response.status(), conflict ? 409 : 200);
        if (conflict) {
          assert.equal((await response.json()).error.code, "edit_conflict");
          await page.getByRole("alert").filter({ hasText: /草稿已保留/ }).first().waitFor();
          assert.equal(await page.getByRole("alert").count(), 1, "conflict should stay in the editor without a stale retry action");
          assert.equal(await content.inputValue(), changedKey === "content" ? localContent : baseline.content);
          if (changedKey === "title") assert.equal(await title.inputValue(), localTitle);
          await page.screenshot({ path: `${output}/${kind}-edit-conflict.png`, fullPage: true });
        } else {
          await form.waitFor({ state: "detached" });
        }
      }
      const expectedPatch = changedKey ? { [changedKey]: changedKey === "title" ? localTitle : changedKey === "description" ? localDescription : localContent, expected: { [changedKey]: baseline[changedKey] } } : undefined;
      assert.deepEqual(requests, expectedPatch ? [expectedPatch] : [], "untouched fields or refreshed values appeared in outgoing PATCH");
      const state = await readFixture();
      const current = state[kind === "memory" ? "memories" : "schedules"].find(item => item.id === id);
      for (const key of ["title", "description", "content"]) {
        const expected = !conflict && !noOp && key === changedKey ? expectedPatch[key] : remote[key];
        assert.equal(current[key], expected, `lost concurrent ${key} in ${kind}/${scenario}`);
      }
      if (kind === "memory") assert.equal(current.revision, remote.revision + (!conflict && !noOp ? 1 : 0), "rejected/no-op edit incremented revision");
      else for (const key of ["id", "kind", "timezone", "next_at_utc", "daily_time", "interval_seconds", "created_at"]) assert.equal(current[key], baseline[key], `edit race changed schedule ${key}`);
      assert.deepEqual(pageErrors, []);
      results.push({ kind, scenario, pass: true, patch: requests[0] || null, status: response?.status() || "no write" });
      await context.close();
    }
  }
  await writeFile(`${output}/edit-races-browser-results.json`, JSON.stringify(results, null, 2));
  console.log("PASS: 14 actual-UI refresh/edit races; exact partial PATCHes, restored/no-op omission, atomic 409 conflicts, drafts retained, revision/timing preserved");
} finally { await browser.close(); }
