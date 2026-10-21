import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();
try {
  const { shouldShowReplyQuote } = await modules.load("/src/replyQuote.ts");
  const m = (id, role, extra = {}) => ({ id, conversation_id: "c", seq: 0, role, content: id, created_at: "2026-10-13T00:00:00Z", ...(role === "bot" ? { sender_bot_id: "bot-a" } : {}), ...extra });
  const show = (list, i, target) => shouldShowReplyQuote(list, i, list.find(x => x.id === target));
  let list = [m("u1", "user"), m("p1", "bot", { kind: "progress" }), m("b1", "bot")];
  assert.equal(show(list, 2, "u1"), false, "adjacent user message: hidden");
  list = [m("u1", "user"), m("b0", "bot", { kind: "segment" }), m("b1", "bot")];
  assert.equal(show(list, 2, "u1"), false, "same Bot's earlier segment in between: hidden");
  list = [m("u1", "user"), m("u2", "user"), m("b1", "bot")];
  assert.equal(show(list, 2, "u1"), true, "older message with a newer user message after it: shown");
  list = [m("u1", "user"), m("b2", "bot", { sender_bot_id: "bot-b" }), m("b1", "bot")];
  assert.equal(show(list, 2, "u1"), true, "group: another Bot's message intervened: shown");
  list = [m("b2", "bot", { sender_bot_id: "bot-b" }), m("b1", "bot")];
  assert.equal(show(list, 1, "b2"), false, "another Bot's message directly above: hidden");
  list = [m("b2", "bot", { sender_bot_id: "bot-b" }), m("u3", "user"), m("b1", "bot")];
  assert.equal(show(list, 2, "b2"), true, "another Bot's older message with the user in between: shown");
  assert.equal(shouldShowReplyQuote(list, 1, undefined), false, "no target: nothing to show");
  assert.equal(shouldShowReplyQuote([m("b1", "bot")], 0, m("gone", "user")), true, "target outside the visible timeline: shown");
  const { readReplyMarker } = await modules.load("/src/replyMarker.ts");
  const id = "123e4567-e89b-12d3-a456-426614174000";
  for (const [name, text] of [["inline", `[message_id=${id}] 正文`], ["own line", `[message_id=${id}]\n正文`], ["own line CRLF", `[message_id=${id}]\r\n\r\n正文`], ["no space", `[message_id=${id}]正文`], ["leading newline", `\n[message_id=${id}]\n正文`], ["leading space", ` [message_id=${id}] 正文`]]) {
    assert.deepEqual(readReplyMarker(text), { ids: [id], body: "正文" }, name);
  }
  assert.deepEqual(readReplyMarker(`[message_id=${id}][message_id=${id}]\n正文`).ids, [id, id], "two markers");
  assert.deepEqual(readReplyMarker("[message_id=123e45"), { ids: [], body: "" }, "marker still streaming: hidden");
  assert.deepEqual(readReplyMarker("\n[message_id"), { ids: [], body: "" }, "indented partial marker: hidden");
  assert.deepEqual(readReplyMarker("plain [message_id=x] text"), { ids: [], body: "plain [message_id=x] text" }, "a mid-text mention is text");
  console.log("reply quote checks: PASS (adjacent, segment, older, group bot, bot target)");
} finally {
  await modules.close();
}
