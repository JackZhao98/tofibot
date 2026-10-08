import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { build } from "vite";
import react from "@vitejs/plugin-react";
import { symlink } from "node:fs/promises";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-integration-catalog-"));
async function browserRenderer() {
  await build({
    configFile: false, root, plugins: [react()], logLevel: "error",
    build: { outDir: join(output, "browser"), emptyOutDir: false, lib: { entry: join(root, "src/IntegrationBrowser.tsx"), formats: ["es"], fileName: "browser" }, rollupOptions: { external: ["react", "react/jsx-runtime", "react/jsx-dev-runtime"] } },
  });
  await symlink(join(root, "node_modules"), join(output, "node_modules"), "dir");
  const { IntegrationBrowser } = await import(pathToFileURL(join(output, "browser/browser.js")));
  return (query = "", servers = []) => renderToStaticMarkup(createElement(IntegrationBrowser, { query, servers, onQueryChange() {}, onBack() {}, onSelect() {}, onCustom() {} }));
}

function assertRetiredNotion(html) {
  const row = html.match(/<li[^>]*data-held-integration-id="notion-token"[\s\S]*?<\/li>/)?.[0];
  assert.ok(row, "historical Notion token reference must remain a held row");
  assert.ok(row.includes("历史提供方项目") && row.includes("已停止维护"), "retired vendor origin must be disclosed in the actual row");
  assert.ok(row.includes("不在首批支持范围") && row.includes("历史项目文档"));
  assert.ok(!row.includes("提供方维护"), "retired source cannot claim current provider maintenance");
  assert.ok(!row.includes("<button") && !row.includes("<input"), "historical reference cannot collect credentials or offer setup");
  assert.ok(html.includes('data-integration-id="notion"'), "hosted OAuth remains separately addable");
  assert.ok(!html.includes('data-integration-id="notion-token"'));
  assert.ok(html.includes('aria-label="暂不可添加的服务"'), "retired reference must not be presented as pending first-wave support");
}

async function run() {
  execFileSync(join(root, "node_modules/.bin/tsc"), ["src/integrationCatalog.ts", "src/mcpTokenHeaders.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--strict"], { cwd: root });
  const { integrationCatalog, getIntegrationPreset, heldIntegrations, matchesIntegration } = await import(pathToFileURL(join(output, "integrationCatalog.js")));
  const { mcpTokenHeaders } = await import(pathToFileURL(join(output, "mcpTokenHeaders.js")));
  if (process.argv.includes("--retired-notion")) {
    const render = await browserRenderer();
    assertRetiredNotion(render("Notion"));
    assert.equal(getIntegrationPreset("notion-token"), undefined);
    console.log("Retired Notion actual browser row: PASS (historical vendor origin, retirement, first-wave exclusion, no setup/credential controls, hosted OAuth separate)");
    return;
  }
  const ids = new Set();
  for (const item of integrationCatalog) {
    assert.ok(!ids.has(item.id), `duplicate preset: ${item.id}`);
    ids.add(item.id);
    assert.equal(new URL(item.url).protocol, "https:");
    assert.equal(new URL(item.docsURL).protocol, "https:");
    assert.ok(item.setup.length);
    assert.ok(!("token" in item) && !("headers" in item) && !("env" in item), "presets contain public configuration only");
    assert.equal(getIntegrationPreset(item.id), item);
    if (item.category === "service") assert.equal(item.upstream, "vendor", `service provenance must be explicit: ${item.id}`);
    if (item.readOnly) assert.equal(new URL(item.url).pathname.endsWith("/readonly"), true);
  }
  const readonly = getIntegrationPreset("github-readonly");
  assert.equal(readonly.url, "https://api.githubcopilot.com/mcp/readonly");
  assert.equal(readonly.upstream, "vendor");
  assert.equal(readonly.auth, "token");
  assert.equal(readonly.readOnly, true);
  assert.deepEqual(mcpTokenHeaders("{}", " synthetic-pat ", readonly), { Authorization: "Bearer synthetic-pat" });
  assert.equal(getIntegrationPreset("notion").auth, "oauth", "hosted Notion cannot be advertised as a PAT endpoint");
  const linearReadOnly = getIntegrationPreset("linear-readonly");
  assert.equal(linearReadOnly.url, "https://mcp.linear.app/mcp/readonly");
  assert.equal(linearReadOnly.upstream, "vendor");
  assert.equal(linearReadOnly.readOnly, true);
  assert.deepEqual(mcpTokenHeaders("{}", "synthetic-linear-key", linearReadOnly), { Authorization: "Bearer synthetic-linear-key" });
  for (const id of ["notion-token", "discord", "slack", "yahoo-finance"]) {
    assert.equal(getIntegrationPreset(id), undefined, `unproven provider must not become an installable preset: ${id}`);
  }
  assert.deepEqual(mcpTokenHeaders("{}", "synthetic-key", getIntegrationPreset("context7")), { "Context7-API-Key": "synthetic-key" });
  assert.deepEqual(mcpTokenHeaders('{"Authorization":"••••••••"}', " "), { Authorization: "••••••••" }, "blank token preserves retention marker");
  assert.deepEqual(mcpTokenHeaders('{"Authorization":"••••••••"}', "replacement"), { Authorization: "Bearer replacement" });
  assert.deepEqual(mcpTokenHeaders("{}", ""), {}, "explicitly deleting a header remains deletion");
  assert.deepEqual(mcpTokenHeaders('{"X-Public":"fixture"}', ""), { "X-Public": "fixture" });
  assert.deepEqual(mcpTokenHeaders("{}", "fixture", { tokenHeader: "X-API-Key", tokenPrefix: "Token " }), { "X-API-Key": "Token fixture" });
  for (const invalid of ["null", "[]", '{"X-Key":123}', "not-json"]) assert.throws(() => mcpTokenHeaders(invalid, ""));
  assert.deepEqual(heldIntegrations.map(item => item.id), ["notion-token", "discord", "slack", "yahoo-finance"]);
  for (const item of heldIntegrations) {
    assert.ok(!ids.has(item.id), "held provider cannot appear as an addable preset");
    assert.equal(new URL(item.docsURL).protocol, "https:");
    assert.ok(item.reason);
    for (const key of ["url", "token", "headers", "env", "command", "package"]) assert.ok(!(key in item), `held status cannot carry setup payload: ${key}`);
  }
  assert.equal(heldIntegrations.find(item => item.id === "discord").upstream, "community");
  assert.equal(heldIntegrations.find(item => item.id === "yahoo-finance").upstream, "community");
  assert.equal(heldIntegrations.find(item => item.id === "yahoo-finance").auth, "none");
  assert.equal(heldIntegrations.find(item => item.id === "slack").auth, "oauth");
  assert.equal(matchesIntegration(heldIntegrations.find(item => item.id === "discord"), "  DISCORD  "), true);

  // Render the actual browser component. No DOM, network, account or settings
  // request is needed to verify that held entries have no setup control.
  const render = await browserRenderer();
  const all = render();
  const heldSection = all.match(/<section[^>]*aria-label="暂不可添加的服务"[\s\S]*?<\/section>/)?.[0];
  assert.ok(heldSection);
  assert.ok(!heldSection.includes("<button"), "held providers must have no install/setup button");
  assert.ok(!heldSection.includes("<input"), "held providers must have no credential entry");
  assert.ok(heldSection.includes("社区维护") && heldSection.includes("提供方维护"));
  assert.ok(all.includes("OAuth 授权") && all.includes("访问令牌") && all.includes("无需密钥") && all.includes("只读工具"));
  for (const id of ["notion-token", "discord", "slack", "yahoo-finance"]) {
    const html = render(heldIntegrations.find(item => item.id === id).name);
    assert.ok(html.includes(`data-held-integration-id="${id}"`));
    assert.ok(!html.includes(`data-integration-id="${id}"`));
  }
  const notion = render("Notion");
  assert.ok(notion.includes('data-integration-id="notion"') && notion.includes('data-held-integration-id="notion-token"'));
  assertRetiredNotion(notion);
  const installed = render("GitHub", [{ url: readonly.url }]);
  assert.match(installed, /<button[^>]*data-integration-id="github-readonly"[^>]*disabled=""/);
  assert.ok(render("synthetic-missing-provider").includes('role="status"'));
  console.log("Integration catalog, private token headers and actual browser rendering: PASS (source/auth labels, read-only presets, held-provider setup fence, search, installed state, preserve/replace/delete)");
}
try { await run(); } finally { await rm(output, { recursive: true, force: true }); }
