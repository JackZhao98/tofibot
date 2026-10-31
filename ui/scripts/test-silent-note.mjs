import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

// A co-addressed Bot that said its pick as a note and then ended silent leaves no answer. The finished run
// still draws one quiet, collapsed row with its note; a run that did answer draws nothing extra.
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const server = await createServer({ configFile: false, root: ui, server: { middlewareMode: true, hmr: false, ws: false }, logLevel: "error" });
const originalDocument = globalThis.document;
try {
  const i18n = await server.ssrLoadModule("/src/i18n/index.ts");
  await i18n.i18nReady; await i18n.loadLanguage("en");
  const p = await server.ssrLoadModule("/src/taskIssuePresentation.ts");
  const { TaskRunBlock, taskBlockVisible } = await server.ssrLoadModule("/src/TaskRunBlock.tsx");
  await (await server.ssrLoadModule("/src/lazyPanels.tsx")).whenPanelLoaded("taskActivity");
  const at = "2026-10-30T10:00:00Z";
  const run = { id: "r1", conversation_id: "g", bot_id: "mochi", kind: "group_mention", trigger_message_id: "u1", status: "done", created_at: at, updated_at: at };
  const user = { id: "u1", conversation_id: "g", seq: 1, role: "user", content: "@Mochi pick one @Biscuit write a checklist", created_at: at };
  const assistant = (id, seq, kind, content, extra = {}) => ({ id, conversation_id: "g", seq, role: "assistant", sender_bot_id: "mochi", run_id: "r1", content, created_at: at, ...(kind ? { kind } : {}), ...extra });
  const note = assistant("n1", 2, "progress", "My pick: **Glance**.", { client_message_id: "say-note:abc" });
  const props = (messages, extra = {}) => ({ owner: p.buildTaskOwners([run], messages, [], [])[0], tools: [], questions: [], drafts: [], messages, summaries: [], details: {}, connected: true, locale: "en", hideActivity: true, botName: "Mochi", showName: true, onOpenTools: () => {}, onRefresh: async () => {}, renderQuestion: () => null, renderDraft: () => null, ...extra });
  const render = messages => { globalThis.document = { documentElement: { lang: "en" } }; return renderToStaticMarkup(createElement(TaskRunBlock, props(messages))); };

  assert.equal(taskBlockVisible(props([user, note])), true, "a silent finished run with a note draws its row");
  const html = render([user, note]);
  assert.match(html, /<details class="task-silent-note">/, "collapsed by default (no open attribute)");
  assert.match(html, /<summary>Left a note<\/summary>/);
  assert.match(html, /<strong>Glance<\/strong>/, "the note is rendered as Markdown inside the disclosure");

  for (const kind of ["", "segment"]) {
    const answer = assistant("a1", 3, kind, "Here is the pick.");
    assert.equal(taskBlockVisible(props([user, note, answer])), false, `a visible answer (${JSON.stringify(kind)}) keeps today's behaviour: no row`);
    assert.ok(!render([user, note, answer]).includes("task-silent-note"), "no extra row beside an answer");
  }
  assert.equal(taskBlockVisible(props([user, assistant("i1", 3, "internal", "thinking")])), false, "private working text is never a note row");
  const running = { ...run, status: "running" };
  const live = props([user, note]); live.owner = p.buildTaskOwners([running], [user, note], [], [])[0];
  assert.ok(!renderToStaticMarkup(createElement(TaskRunBlock, live)).includes("task-silent-note"), "a still-running run keeps today's behaviour");
  console.log("PASS: silent run with a note draws one collapsed row; answered runs add nothing");
} finally { globalThis.document = originalDocument; await server.close(); }
