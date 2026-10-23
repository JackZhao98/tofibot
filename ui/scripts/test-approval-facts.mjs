import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules } from "./ui-modules.mjs";

// An extension approval card shows server-built facts above the Bot's own wording.
// Rendered through the real QuestionCard from the same JSON the server sends.
const ui = await openUiModules({ language: "en" });
try {
  const noop = () => {};
  globalThis.window = { matchMedia: () => ({ matches: false, addEventListener: noop, removeEventListener: noop }), addEventListener: noop, removeEventListener: noop, location: { href: "http://localhost/", origin: "http://localhost" }, navigator: { userAgent: "node" } };
  globalThis.document = { documentElement: { lang: "en", dataset: {} }, addEventListener: noop, removeEventListener: noop };
  const { QuestionCard } = await ui.load("/src/QuestionCard.tsx");
  const { TimezoneProvider } = await ui.load("/src/UserTimezone.tsx");
  const question = facts => ({
    type: "question", question_id: "q1", conversation_id: "c", run_id: "r", bot_id: "b", question_type: "approval", question: "Install a harmless helper?",
    status: "pending", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    approval: { action: "A tiny harmless helper", target: "extension skill_install helper #a1b2c3d4e5f6", impact: "Nothing risky", ...(facts ? { facts } : {}) },
  });
  const render = item => renderToStaticMarkup(createElement(TimezoneProvider, null, createElement(QuestionCard, { item, onChanged: async () => {} })));

  const skill = { kind: "skill_install", name: "invoice-helper", change_id: "#a1b2c3d4e5f6", skill: { description: "Summarize invoices", excerpt: "Fetch invoices and summarize them.", allowed_tools: ["Bash", "Read"], files: ["SKILL.md (120 B)", "fetch.sh (40 B)"], has_scripts: true } };
  const html = render(question(skill));
  assert.match(html, /data-testid="approval-verified"/, "the verified section is rendered");
  for (const text of ["Install a skill", "invoice-helper", "Summarize invoices", "Fetch invoices and summarize them.", "Bash, Read", "SKILL.md (120 B), fetch.sh (40 B)", "Contains script or executable files", "#a1b2c3d4e5f6"]) {
    assert.ok(html.includes(text), `verified section shows ${text}`);
  }
  assert.ok(html.indexOf("approval-verified") < html.indexOf("A tiny harmless helper"), "server facts come before the Bot's wording");
  assert.ok(html.includes("Written by the Bot") && html.includes("A tiny harmless helper") && html.includes("Nothing risky"), "model text stays as secondary text");
  assert.ok(!html.includes("extension skill_install helper #a1b2c3d4e5f6"), "the opaque token is no longer the target shown");

  const publish = { kind: "skill_publish_version", name: "invoice-helper", change_id: "#a1b2c3d4e5f6", skill: { description: "Summarize invoices", files: ["SKILL.md (140 B)", "fetch.py (60 B)"], new_version: 3, added: ["fetch.py"], removed: ["old.sh"], changed: ["SKILL.md"], enabled_bots: 2 } };
  const publishHtml = render(question(publish));
  for (const text of ["Publish a new Skill version", "v3", "Files added", "fetch.py", "Files removed", "old.sh", "Files changed", "Bots that will follow it"]) {
    assert.ok(publishHtml.includes(text), `publish facts show ${text}`);
  }

  const mcp = { kind: "mcp_update", name: "files", source: "https://api.example.com/mcp?key=***", previous_source: "https://old.example.com/mcp", transport: "streamable_http", allow_tools: ["read_file"], header_names: ["Authorization"], change_id: "#0123456789ab" };
  const mcpHtml = render(question(mcp));
  for (const text of ["Change an MCP server", "https://api.example.com/mcp?key=***", "https://old.example.com/mcp", "streamable_http", "Only: read_file", "Authorization"]) {
    assert.ok(mcpHtml.includes(text), `MCP facts show ${text}`);
  }
  const open = render(question({ kind: "mcp_create", name: "x", source: "https://x.example.com/mcp", transport: "streamable_http" }));
  assert.ok(open.includes("Every tool the server offers"), "no allowlist is stated as every tool");

  const plain = render(question(undefined));
  assert.ok(!plain.includes("approval-verified"), "cards without facts are unchanged");
  assert.ok(plain.includes("extension skill_install helper #a1b2c3d4e5f6"), "cards without facts still show the target");

  const hostile = render(question({ kind: "skill_install", name: "<img src=x onerror=alert(1)>", skill: { description: "<script>alert(1)</script>", files: [] } }));
  assert.ok(!hostile.includes("<img") && !hostile.includes("<script>alert"), "facts render as inert text");

  await ui.setLanguage("zh-CN");
  const zh = render(question(skill));
  assert.ok(zh.includes("安装技能") && zh.includes("Tofi 核对的事实"), "the section is translated");
  console.log("PASS extension approval card shows server-built facts above Bot wording");
} finally { await ui.close(); }
