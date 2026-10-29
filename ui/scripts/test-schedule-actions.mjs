// The schedule card's actions are one row of icon buttons with tooltips and accessible names, and Delete keeps
// its confirmation. The "Edit" control is an icon like the rest, not a bare text pill.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules, uiRoot } from "./ui-modules.mjs";

const modules = await openUiModules({ language: "en" });
try {
  const { ScheduleEditor } = await modules.load("/src/ScheduleEditor.tsx");
  const { ScheduleDeleteButton, ScheduleIconButton } = await modules.load("/src/ScheduleActions.tsx");
  const schedule = { id: "s1", title: "Watch stock", description: "d", conversation_id: "c", bot_id: "b", content: "check", kind: "daily", timezone: "UTC", next_at_utc: "2026-10-29T10:00:00Z", daily_time: "10:00", status: "active", created_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:00Z" };
  const actions = createElement("span", null,
    createElement(ScheduleIconButton, { icon: "pause", label: "Pause", onClick: () => {} }),
    createElement(ScheduleDeleteButton, { label: "Delete", question: "Delete this schedule?", onConfirm: async () => {} }));
  const html = renderToStaticMarkup(createElement(ScheduleEditor, { schedule, disabled: false, onSave: async () => {}, actions }));
  for (const label of ["Edit", "Pause", "Delete"]) {
    assert(html.includes(`aria-label="${label}"`), `${label} has an accessible name`);
    assert(html.includes(`data-hint="${label}"`), `${label} has a tooltip`);
  }
  assert.equal((html.match(/<button/g) || []).length, 3, "edit, pause and delete are the only controls");
  assert.equal((html.match(/work-icon-button/g) || []).length, 3);
  assert(!/<button[^>]*>\s*(Edit|Pause|Delete)\s*<\/button>/.test(html), "no text pill buttons");
  assert(!html.includes("inline-confirmation"), "the confirmation is closed until Delete is pressed");
  assert(html.includes('class="work-actions work-icon-actions"'), "one row");
  const source = await readFile(join(uiRoot, "src/ScheduleActions.tsx"), "utf8");
  assert(source.includes('role="group"') && source.includes('t("confirm.action"'), "Delete asks before it deletes");
  const panel = await readFile(join(uiRoot, "src/WorkPanel.tsx"), "utf8");
  for (const key of ["panel.pause", "panel.resume", "panel.rerun", "panel.delete"]) assert(panel.includes(key));
  console.log("PASS: schedule card actions are one row of labelled icon buttons with a delete confirmation");
} finally { await modules.close(); }
