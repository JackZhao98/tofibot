import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules } from "./ui-modules.mjs";

// "Always allow" on an approval card: shown only when the server marks the call eligible (approval.standing),
// never on a plain card, an answered card or a high-risk card (the server omits the offer there);
// a card a standing allowance decided is labelled as such. Rendered through the real QuestionCard.
const ui = await openUiModules({ language: "en" });
try {
  const noop = () => {};
  globalThis.window = { matchMedia: () => ({ matches: false, addEventListener: noop, removeEventListener: noop }), addEventListener: noop, removeEventListener: noop, location: { href: "http://localhost/", origin: "http://localhost" }, navigator: { userAgent: "node" } };
  globalThis.document = { documentElement: { lang: "en", dataset: {} }, addEventListener: noop, removeEventListener: noop };
  const { QuestionCard } = await ui.load("/src/QuestionCard.tsx");
  const { TimezoneProvider } = await ui.load("/src/UserTimezone.tsx");
  const { autoReviewPresentation, STANDING_ALLOWANCE_ACTOR } = await ui.load("/src/questionTimeline.ts");
  const card = extra => ({
    type: "question", question_id: "q1", conversation_id: "c", run_id: "r", bot_id: "b", question_type: "approval", question: "Allow this external tool call?",
    status: "pending", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    approval: { action: "Call notion-update-page", target: "MCP server notion", impact: "May change data", ...extra },
  });
  const render = item => renderToStaticMarkup(createElement(TimezoneProvider, null, createElement(QuestionCard, { item, bot: { id: "b", name: "tofi" }, onChanged: async () => {} })));

  const offered = render(card({ standing: { server: "notion", tool: "notion-update-page" } }));
  assert.match(offered, /class="approval-always"[^>]*>Always allow</, "an eligible call offers Always allow in the action row");
  assert.ok(offered.indexOf("approval-decline") < offered.indexOf("approval-always") && offered.indexOf("approval-accept") < offered.indexOf("approval-always"), "it sits next to Approve and Deny");
  assert.ok(offered.includes("tofi") && offered.includes("notion-update-page") && offered.includes("also when it works on its own") && offered.includes("on a schedule"), "the hint names the Bot and tool and says it also applies when the Bot works on its own, for example on a schedule");

  assert.match(offered, /class="text-button approval-manage"[^>]*aria-label="Manage notion-update-page on notion in Settings"/, "the hint ends in a Manage link that names the tool and the connection");
  assert.ok(offered.indexOf("approval-always-hint") < offered.indexOf("approval-manage"), "the link sits in the hint, after the sentence it belongs to");

  const plain = render(card({}));
  assert.ok(!plain.includes("approval-manage"), "no allowance on offer, nothing to manage");
  assert.ok(!plain.includes("approval-always"), "a card with no offer (high-risk, hidden chat, non-MCP) has no Always allow");
  const answered = render({ ...card({ standing: { server: "notion", tool: "notion-update-page" } }), status: "answered", answer: true, answered_by: "human" });
  assert.ok(!answered.includes("approval-always"), "an answered card has no Always allow");

  const decided = { ...card({ standing: undefined }), status: "answered", answer: true, answered_by: STANDING_ALLOWANCE_ACTOR };
  decided.approval.review = { source: "standing_allowance", status: "human_decided", reason: "x", model: "", decision_source: "standing_allowance", grant_id: "g1" };
  assert.equal(autoReviewPresentation(decided).badge, "Always allowed", "a standing decision is labelled");
  assert.match(render(decided), /human_decided/, "the resolved card keeps the decision record");

  await ui.setLanguage("zh-CN");
  assert.ok(render(card({ standing: { server: "notion", tool: "notion-update-page" } })).includes("始终允许"), "translated");
  assert.ok(render(card({ standing: { server: "notion", tool: "notion-update-page" } })).includes("自行工作时"), "the unattended wording is translated too");
  console.log("PASS Always allow appears only on eligible approval cards and standing decisions are labelled");
} finally { await ui.close(); }
