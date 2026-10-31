// Settings > AI Models, OpenAI-compatible card: the Ollama preset fills the address and shows the Ollama note (no key,
// OLLAMA_HOST, context length), Fetch models lists what the server reports, Save sends the typed IDs, and the saved card
// shows the endpoint with its cost line. The app's /api is answered in the browser with synthetic data.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:compat-provider
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const at = "2026-10-01T00:00:00Z";
const bot = { id: "bot_1", name: "Synthetic Bot", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "conv_1", created_at: at };
const dm = { id: "conv_1", kind: "dm", name: "Synthetic Bot", bot_id: "bot_1", bot_ids: ["bot_1"], updated_at: at, read_seq: 0, user_visible: true };

const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US" });
const page = await context.newPage();
const errors = [];
page.on("pageerror", error => errors.push(error.message));
let status = { id: "compatible", label: "OpenAI-compatible", kind: "endpoint", configured: false, verified_at: "", error: "" };
const sent = { probe: [], config: [] };
await page.route("**/api/**", route => {
  const request = route.request();
  const path = new URL(request.url()).pathname;
  const json = (body, code = 200) => route.fulfill({ status: code, json: body });
  if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
  if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
  if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
  if (path === "/api/bots") return json({ bots: [bot] });
  if (path === "/api/conversations") return json({ conversations: [dm] });
  if (path === "/api/providers") return json({ providers: [{ id: "codex", label: "Codex", kind: "oauth", configured: false, status: "disconnected", verified_at: "", error: "" }, { id: "openai", label: "OpenAI", kind: "api_key", configured: false, verified_at: "", error: "" }, { id: "anthropic", label: "Claude", kind: "api_key", configured: false, verified_at: "", error: "" }, status] });
  if (path === "/api/providers/compatible/models") { sent.probe.push(request.postDataJSON()); return json({ models: [{ id: "qwen3:8b", context_window: 16384 }, { id: "llama3.1:latest" }] }); }
  if (path === "/api/providers/compatible/config" && request.method() === "PUT") {
    const body = request.postDataJSON();
    sent.config.push(body);
    status = { ...status, configured: true, base_url: body.base_url, models: body.models ?? [], verified_at: at };
    return json(status);
  }
  if (path === "/api/models") return json({ models: [], source: "live" });
  if (path === "/api/model-settings") return json({ model: "synthetic-model", reasoning_effort: "" });
  if (path === "/api/codex/status") return json({ connected: false });
  if (path.endsWith("/messages")) return json({ messages: [], drafts: [], has_more: false, event_cursor: 0 });
  if (path.endsWith("/runs")) return json({ runs: [] });
  if (path.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
  if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
  if (path.endsWith("/memories")) return json({ memories: [] });
  if (path.endsWith("/schedules")) return json({ schedules: [] });
  if (path === "/api/questions") return json({ questions: [] });
  if (path === "/api/mail-drafts") return json({ drafts: [] });
  return json({});
});

try {
  await page.goto(`${origin}/settings/models`);
  const card = page.locator("article.provider-card", { has: page.getByRole("heading", { name: "OpenAI-compatible" }) });
  await card.waitFor();
  assert.equal(await card.locator("[data-compat-note='ollama']").count(), 0, "no Ollama note before an Ollama address");
  assert.match(await card.innerText(), /Cost depends on the server/, "the cost line is there before anything is saved");

  await card.getByRole("button", { name: "Ollama" }).click();
  assert.equal(await card.getByLabel("Base URL").inputValue(), "http://host.docker.internal:11434/v1");
  const note = await card.locator("[data-compat-note='ollama']").innerText();
  for (const word of ["OLLAMA_HOST=0.0.0.0", "host.docker.internal", "OLLAMA_CONTEXT_LENGTH"]) assert.ok(note.includes(word), `the Ollama note names ${word}`);

  await card.getByRole("button", { name: "Fetch models" }).click();
  await card.getByText("Models found: 2").waitFor();
  assert.match(await card.locator(".compat-listed").innerText(), /qwen3:8b\s*16,384 tokens/);
  assert.deepEqual(sent.probe[0], { base_url: "http://host.docker.internal:11434/v1" });

  await card.getByLabel("Model IDs").fill("my-model, qwen3:8b");
  await card.getByRole("button", { name: "Save and verify" }).click();
  await card.getByText("Endpoint saved and verified.").waitFor();
  assert.deepEqual(sent.config[0], { base_url: "http://host.docker.internal:11434/v1", models: ["my-model", "qwen3:8b"] }, "no key is sent when none is typed");
  assert.match(await card.innerText(), /host\.docker\.internal:11434\/v1/);
  assert.match(await card.innerText(), /Typed models: my-model, qwen3:8b/);
  assert.ok((await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)) <= 0);
  assert.deepEqual(errors, []);
  console.log("PASS: compatible card: Ollama preset and note, fetch, save without a key, saved summary");
} finally {
  await browser.close();
  await server.close();
}
