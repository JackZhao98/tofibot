import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules } from "./ui-modules.mjs";

// A Bot's Skills section (decision 0058) renders the whole library as checkboxes: bundles with a member count,
// "set by X at T" per choice, official Skills in their own group; and a share file names Skills, never files.
const ui = await openUiModules({ language: "en" });
try {
  const noop = () => {};
  globalThis.window = { matchMedia: () => ({ matches: false, addEventListener: noop, removeEventListener: noop }), addEventListener: noop, removeEventListener: noop, location: { href: "http://localhost/", origin: "http://localhost" }, navigator: { userAgent: "node" } };
  globalThis.document = { documentElement: { lang: "en", dataset: {} }, addEventListener: noop, removeEventListener: noop };
  const { BotSkillsView } = await ui.load("/src/BotSkills.tsx");
  const bundle = { id: "office-kit", description: "Office helpers", installed_at: "2026-10-19T10:00:00Z", skills: ["alpha-tool", "beta-tool"] };
  const listing = {
    skills: [
      { name: "invoice-helper", description: "Summarize invoices.", enabled: true, set_by: "owner", set_at: "2026-10-21T09:00:00Z" },
      { name: "weekly-report", description: "Draft the weekly report.", enabled: true, set_by: "bot", set_at: "2026-10-22T11:30:00Z" },
      { name: "old-import", description: "Found on disk.", enabled: false },
      { name: "alpha-tool", description: "First tool.", bundle_id: "office-kit", enabled: true, set_by: "migration", set_at: "2026-10-23T08:00:00Z" },
      { name: "beta-tool", description: "Second tool.", bundle_id: "office-kit", enabled: false },
      { name: "spreadsheet", description: "Build workbooks.", builtin: true, enabled: true },
      { name: "slides", description: "Build decks.", builtin: true, enabled: false, set_by: "owner", set_at: "2026-10-22T12:00:00Z" },
    ],
    bundles: [bundle],
  };
  const render = props => renderToStaticMarkup(createElement(BotSkillsView, { botId: "bot_a", listing, loading: false, busy: false, error: "", onToggle: noop, onToggleBundle: noop, ...props }));
  let html = render({});
  const checked = name => new RegExp(`aria-label="${name}"[^>]*checked`).test(html) || new RegExp(`checked=""[^>]*aria-label="${name}"`).test(html);
  for (const text of ["Skills", "invoice-helper", "weekly-report", "old-import", "office-kit", "2 Skills", "Official Skills", "spreadsheet", "slides"]) assert.ok(html.includes(text), `shows ${text}`);
  assert.ok(html.includes("Turned on by you"), "an owner choice names the owner");
  assert.ok(html.includes("Turned on by this Bot"), "a Bot's own choice names the Bot");
  assert.ok(html.includes("Turned off by you"), "an owner switch-off of an official Skill is recorded");
  assert.ok(checked("invoice-helper") && checked("spreadsheet") && !checked("old-import") && !checked("slides"), "checkboxes follow the Bot's own choices");
  assert.ok(html.indexOf("invoice-helper") < html.indexOf("bot-skill-group") && html.indexOf("bot-skill-group") < html.indexOf("spreadsheet"), "official Skills sit in their own group below");
  assert.ok(!html.includes("alpha-tool"), "bundle members stay folded until expanded");
  html = render({ openBundles: ["office-kit"] });
  assert.ok(html.includes("alpha-tool") && html.includes("beta-tool"), "an expanded bundle lists its members");
  assert.ok(html.includes("Carried over from earlier settings"), "a migrated choice says so");

  const empty = render({ listing: { skills: listing.skills.filter(skill => skill.builtin), bundles: [] } });
  assert.ok(empty.includes("The library has no Skills yet"), "an empty library says where to add Skills");
  assert.ok(render({ error: "Could not save. Try again." }).includes('role="alert"'), "a failed save is announced");
  assert.ok(render({ loading: true }).includes("Loading Skills"), "loading is stated");

  await ui.setLanguage("zh-CN");
  const zh = render({});
  assert.ok(zh.includes("由你开启") && zh.includes("官方 Skill"), "the section is translated");

  // The share file carries names only.
  await ui.setLanguage("en");
  const pkg = await ui.load("/src/botPackage.ts");
  const built = pkg.buildBotPackage({ id: "bot_a", name: "Synthetic", instructions: "", model: "default", dm_conversation_id: "dm", created_at: "", archived: false }, ["weekly-report", "invoice-helper"]);
  assert.deepEqual(built.bot.skills, ["invoice-helper", "weekly-report"], "names, sorted");
  const parsed = pkg.parseBotPackage(JSON.stringify({ ...built, bot: { ...built.bot, skills: ["ok-name", "Bad Name", 7, "../x"] } }));
  assert.deepEqual(parsed.bot.skills, ["ok-name"], "only well-formed names survive an import");
  assert.equal(pkg.buildBotPackage({ id: "b", name: "N", instructions: "", model: "m", dm_conversation_id: "d", created_at: "", archived: false }).bot.skills, undefined, "no Skills, no field");
  console.log("PASS Bot Skills: library as checkboxes, bundles fold, set-by lines, official group, empty and error states, translated, share file names only");
} finally { await ui.close(); }
