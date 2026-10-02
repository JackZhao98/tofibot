import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-oauth-routing-"));
try {
  execFileSync(join(root, "node_modules/.bin/tsc"), ["src/mcpOAuthRoute.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck"], { cwd: root });
  const { mcpOAuthRoute } = await import(pathToFileURL(join(output, "mcpOAuthRoute.js")));
  const options = { vm_available: true, web_callback_origin: "https://tofi.example", desktop_redirect_uri: "http://127.0.0.1:43821/oauth/callback" };
  assert.equal(mcpOAuthRoute("https:", false, false, options).mode, "web", "HTTPS never prefers an available VM");
  assert.equal(mcpOAuthRoute("http:", true, true, options).mode, "desktop", "native loopback is not an insecure Web session");
  assert.equal(mcpOAuthRoute("https:", true, true, options).mode, "desktop");
  assert.equal(mcpOAuthRoute("http:", true, false, options).mode, "blocked", "old client must update, not silently use VM");
  assert.equal(mcpOAuthRoute("http:", false, false, options).mode, "vm");
  assert.equal(mcpOAuthRoute("http:", false, false, { ...options, vm_available: false }).mode, "blocked");
  assert.equal(mcpOAuthRoute("https:", false, false, { ...options, web_callback_origin: "" }).mode, "blocked");
  assert.equal(mcpOAuthRoute("https:", false, false, null).mode, "blocked");
  console.log("MCP OAuth entry routing: PASS");
} finally { await rm(output, { recursive: true, force: true }); }
