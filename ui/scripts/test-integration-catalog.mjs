import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules } from "./ui-modules.mjs";

// Shipped zh-CN copy; English is loaded as well for the en checks.
const ui = await openUiModules({ language: "zh-CN" });
async function browserRenderer() {
  const { IntegrationBrowser } = await ui.load("/src/IntegrationBrowser.tsx");
  return (query = "", servers = []) => renderToStaticMarkup(createElement(IntegrationBrowser, { query, servers, localConnectors: [], onQueryChange() {}, onSelect() {}, onLocal() {}, onCustom() {} }));
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
  const { integrationCatalog, getIntegrationPreset, heldIntegrations, matchesIntegration, integrationAuthLabel } = await ui.load("/src/integrationCatalog.ts");
  const { mcpTokenHeaders } = await ui.load("/src/mcpTokenHeaders.ts");
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
  const robinhood = getIntegrationPreset("robinhood");
  assert.equal(robinhood.url, "https://agent.robinhood.com/mcp/trading");
  assert.equal(robinhood.auth, "oauth", "Robinhood signs in with OAuth, not an API key");
  assert.equal(robinhood.dcr, true, "the client is registered dynamically, like Notion");
  assert.equal(robinhood.upstream, "vendor");
  assert.ok(!robinhood.metadataURL && !robinhood.scopes, "discovered from the server, nothing hard-coded");
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
  // The directory curates official connectors only: community servers are added through the custom path.
  assert.ok(heldSection.includes("提供方维护") && !heldSection.includes("社区维护"));
  assert.ok(!all.includes('data-held-integration-id="discord"') && !all.includes('data-held-integration-id="yahoo-finance"'), "community servers are not curated");
  assert.ok(all.includes("由第三方维护") && all.includes("每次都会先询问"), "the custom path states the community caveat");
  for (const item of integrationCatalog) assert.equal(item.upstream, "vendor", `${item.id}: every curated connector is published by the service itself`);
  assert.ok(all.includes("OAuth 授权") && all.includes("访问令牌") && all.includes("无需密钥") && all.includes("只读工具"));
  for (const id of ["notion-token", "slack"]) {
    const html = render(heldIntegrations.find(item => item.id === id).name);
    assert.ok(html.includes(`data-held-integration-id="${id}"`));
    assert.ok(!html.includes(`data-integration-id="${id}"`));
  }
  for (const id of ["discord", "yahoo-finance"]) assert.ok(!render(heldIntegrations.find(item => item.id === id).name).includes(`data-held-integration-id="${id}"`));
  const notion = render("Notion");
  assert.ok(notion.includes('data-integration-id="notion"') && notion.includes('data-held-integration-id="notion-token"'));
  assertRetiredNotion(notion);
  const installed = render("GitHub", [{ url: readonly.url }]);
  assert.match(installed, /<button[^>]*data-integration-id="github-readonly"[^>]*disabled=""/);
  assert.ok(render("synthetic-missing-provider").includes('role="status"'));

  // Catalog text follows the UI language at access time.
  await ui.setLanguage("en");
  try {
    assert.equal(getIntegrationPreset("github-readonly").name, "GitHub (read-only)");
    assert.equal(integrationAuthLabel("none"), "No key needed");
    assert.ok(matchesIntegration(heldIntegrations.find(item => item.id === "notion-token"), "legacy"));
    const english = render("Notion");
    assert.ok(english.includes("Former provider project") && english.includes("No longer maintained") && english.includes('aria-label="Services not available yet"'));
    assert.ok(!/[\p{sc=Han}]/u.test(render()), "English rendering has no Chinese text");
  } finally { await ui.setLanguage("zh-CN"); }
  // Source repositories: every curated connector with an open-source server names it (owner/name), Context7 and GitHub included.
  const { LOCAL_PLUGIN_REPOS } = await ui.load("/src/integrationCatalog.ts");
  const repos = await ui.load("/src/connections/repoStars.tsx");
  for (const id of ["context7", "github", "github-readonly", "notion"]) assert.match(getIntegrationPreset(id).repo, /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/, `${id} names its source repository`);
  assert.equal(getIntegrationPreset("context7").repo, "upstash/context7");
  assert.equal(getIntegrationPreset("github").repo, "github/github-mcp-server");
  assert.equal(LOCAL_PLUGIN_REPOS.gog, "openclaw/gogcli", "the curated Gmail path names gogcli's real repository (the one the runner image builds)");
  for (const [count, text] of [[0, "0"], [999, "999"], [1000, "1k"], [1234, "1.2k"], [12345, "12.3k"], [63467, "63.5k"], [999999, "1M"], [1234567, "1.2M"], [-1, ""], [NaN, ""]]) assert.equal(repos.formatStars(count), text, `formatStars(${count})`);
  // Google: the project is read from the client JSON, and every service has its enable link.
  const google = await ui.load("/src/connections/google.ts");
  const clientJSON = JSON.stringify({ web: { client_id: "622599621014-abcdef.apps.googleusercontent.com", client_secret: "s", project_id: "tofi-sample-123456" } });
  assert.deepEqual({ ...google.parseGoogleClient(clientJSON) }, { clientId: "622599621014-abcdef.apps.googleusercontent.com", projectId: "tofi-sample-123456", projectNumber: "622599621014", project: "tofi-sample-123456" });
  assert.equal(google.parseGoogleClient(JSON.stringify({ web: { client_id: "622599621014-x.apps.googleusercontent.com", client_secret: "s" } })).project, "622599621014", "the number from the client id when there is no project id");
  for (const bad of ["", "{}", "not json", JSON.stringify({ installed: { client_id: "1-x", client_secret: "s" } }), JSON.stringify({ web: { client_id: "622599621014-x.apps.googleusercontent.com" } })]) assert.equal(google.parseGoogleClient(bad), null);
  for (const [service, api] of Object.entries(google.GOOGLE_SERVICE_API)) {
    assert.equal(google.googleEnableURL(api, "tofi-sample-123456"), `https://console.cloud.google.com/apis/library/${api}?project=tofi-sample-123456`, service);
    assert.equal(google.googleEnableURL(api), `https://console.cloud.google.com/apis/enableflow;apiid=${api}`, `${service} without a project`);
    assert.equal(google.googleServiceForAPI(api), service);
  }
  assert.equal(google.googleEnableURL("evil.example.com", "tofi-sample-123456"), "");
  assert.equal(google.googleEnableURL("gmail.googleapis.com", "x&y=https://evil.example"), "https://console.cloud.google.com/apis/enableflow;apiid=gmail.googleapis.com", "an unsafe project never reaches a URL");
  assert.equal(google.isConsoleURL("https://evil.example/apis"), false);
  assert.equal(google.isConsoleURL("https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=x1234567"), true);
  console.log("Integration catalog, private token headers and actual browser rendering: PASS (source/auth labels, read-only presets, held-provider setup fence, search, installed state, preserve/replace/delete)");
}
try { await run(); } finally { await ui.close(); }
