// A catalogued server error code is worded in the UI's language where ApiError is built
// (src/api.ts), and payload code fields (error_code, result_code, ...) resolve through the
// same catalogue with codeText. The server's English message stays in ApiError.data.message.
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules } from "./ui-modules.mjs";

const ui = await openUiModules({ language: "zh-CN" });
const realFetch = globalThis.fetch;
try {
  const { request, ApiError } = await ui.load("/src/api.ts");
  const { codeText } = await ui.load("/src/i18n/codeText.ts");
  const reply = (status, error) => { globalThis.fetch = async () => new Response(JSON.stringify({ error }), { status, headers: { "Content-Type": "application/json" } }); };
  const failure = async () => { try { await request("/api/x"); } catch (cause) { return cause; } throw new Error("request did not fail"); };

  reply(409, { code: "draft_conflict", message: "The draft has changed. Refresh to see the latest." });
  let error = await failure();
  assert.ok(error instanceof ApiError);
  assert.equal(error.code, "draft_conflict");
  assert.equal(error.message, "草稿已变化，请刷新后查看。", "zh-CN UI words a catalogued code in Chinese");
  assert.equal(error.data.message, "The draft has changed. Refresh to see the latest.", "the raw English message is kept");

  await ui.setLanguage("en");
  error = await failure();
  assert.equal(error.message, "The draft has changed. Refresh to see the latest.", "en UI shows English");

  await ui.setLanguage("ja");
  error = await failure();
  assert.equal(error.message, "下書きが変更されています。更新して最新の内容を確認してください。");

  await ui.setLanguage("zh-CN");
  reply(409, { code: "not_in_catalogue", message: "Some server sentence." });
  assert.equal((await failure()).message, "Some server sentence.", "an uncatalogued code keeps the server message");
  reply(500, { message: "No code here." });
  assert.equal((await failure()).message, "No code here.");

  // Payload code fields: work execution error/result and the shared-computer OAuth end reason.
  assert.equal(codeText("work_no_result", "work"), "本轮执行已结束，但没有在原对话发布可验收的结果。");
  assert.equal(codeText("vm_oauth_denied", "extensions"), "授权未获批准，可重新连接。");
  assert.equal(codeText("collaborator_incomplete", "tasks"), "协作成员未完成此步骤。");
  assert.equal(codeText("nope_not_there", "work"), undefined);
  assert.equal(codeText(undefined, "work"), undefined);
  // The work board renders execution.error_code / result_code, not the server sentence.
  const { WorkExecution } = await ui.load("/src/WorkExecution.tsx");
  const item = { id: "w1", kind: "task", status: "review", execution: { id: "e1", work_item_id: "w1", status: "done", active: false, error: "The round ended without a reviewable result in the original conversation.", error_code: "work_no_result", result: "The result includes attachments. See the original conversation.", result_code: "work_result_attachments" } };
  const render = () => renderToStaticMarkup(createElement(WorkExecution, { item, pending: false, mutate: async () => {}, onExecuted: () => {} }));
  let html = render();
  assert.ok(html.includes("本轮执行已结束，但没有在原对话发布可验收的结果。"), html);
  assert.ok(html.includes("本轮结果包含附件，请在原对话查看。"), html);
  await ui.setLanguage("en");
  html = render();
  assert.ok(html.includes("The round ended without a reviewable result in the original conversation."), html);
  assert.ok(html.includes("The result includes attachments. See the original conversation."), html);
  assert.equal(codeText("work_result_attachments", "work"), "The result includes attachments. See the original conversation.");

  console.log("API error codes: PASS");
} finally { globalThis.fetch = realFetch; await ui.close(); }
