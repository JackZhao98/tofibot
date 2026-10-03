import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-integration-catalog-"));
try {
  execFileSync(join(root, "node_modules/.bin/tsc"), ["src/integrationCatalog.ts", "src/mcpTokenHeaders.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--strict"], { cwd: root });
  const { integrationCatalog, getIntegrationPreset } = await import(pathToFileURL(join(output, "integrationCatalog.js")));
  const { mcpTokenHeaders } = await import(pathToFileURL(join(output, "mcpTokenHeaders.js")));
  const ids = new Set();
  for (const item of integrationCatalog) {
    assert.ok(!ids.has(item.id), `duplicate preset: ${item.id}`);
    ids.add(item.id);
    assert.equal(new URL(item.url).protocol, "https:");
    assert.equal(new URL(item.docsURL).protocol, "https:");
    assert.ok(item.setup.length);
    assert.ok(!("token" in item) && !("headers" in item) && !("env" in item), "presets contain public configuration only");
    assert.equal(getIntegrationPreset(item.id), item);
  }
  const readonly = getIntegrationPreset("github-readonly");
  assert.equal(readonly.url, "https://api.githubcopilot.com/mcp/readonly");
  assert.equal(readonly.upstream, "vendor");
  assert.equal(readonly.auth, "token");
  assert.deepEqual(mcpTokenHeaders("{}", " synthetic-pat ", readonly), { Authorization: "Bearer synthetic-pat" });
  assert.equal(getIntegrationPreset("notion").auth, "oauth", "hosted Notion cannot be advertised as a PAT endpoint");
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
  console.log("Integration catalog and private token headers: PASS (provenance, no-auth/Bearer/custom headers, preserve/replace/delete)");
} finally { await rm(output, { recursive: true, force: true }); }
