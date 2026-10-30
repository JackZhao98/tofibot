import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { bundleModules } from "./lib/bundle.mjs";

// The Go settings registry (internal/app/settings_registry.go) is the source of the links a Bot hands to a person. This test reads its
// export (src/generated/settingsRegistry.json) and fails when a link does not resolve, when a tab or section exists on one side only,
// or when a section id is missing from the component that owns it.
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const registry = JSON.parse(await readFile(join(root, "src/generated/settingsRegistry.json"), "utf8"));
const source = async path => readFile(join(root, "src", path), "utf8");
{
  const { readSettingsLink, SIDE_PARAMS, ...sections } = await bundleModules(root, ["src/settingsRoute.ts", "src/settings/sections.ts", "src/conversationRoute.ts"]);

  // 1. Tabs and sections are the same set on both sides.
  const tsTabs = Object.fromEntries(Object.entries(sections.SETTINGS_SECTIONS).map(([tab, list]) => [tab, [...list]]));
  const goTabs = Object.fromEntries(registry.tabs.map(tab => [tab.id, tab.sections ?? []]));
  assert.deepEqual(goTabs, tsTabs, "settings/sections.ts and the registry list the same tabs and sections");
  assert.deepEqual(registry.bot_sections, [...sections.BOT_SETTINGS_SECTIONS]);
  assert.deepEqual(registry.group_sections, [...sections.GROUP_SETTINGS_SECTIONS]);
  assert.deepEqual(registry.connection_sections, [...sections.CONNECTION_SECTIONS]);
  assert.deepEqual(registry.sidebar_views, [...SIDE_PARAMS]);

  // 2. A new SettingsTab with no registry entry fails; so does a registry tab the shell does not have.
  const shell = await source("SettingsShell.tsx");
  const union = /export type SettingsTab=([^;]+);/.exec(shell)?.[1];
  assert.ok(union, "SettingsTab union found in SettingsShell.tsx");
  const shellTabs = [...union.matchAll(/"([a-z]+)"/g)].map(match => match[1]).sort();
  assert.deepEqual(shellTabs, Object.keys(tsTabs).sort(), "every SettingsTab has a registry entry and every registry tab is a SettingsTab");

  // 3. Every link in the registry resolves to a page and a section the UI has.
  const fill = path => path.replace("{bot}", "bot-one").replace("{group}", "group-one").replace("{name}", "acme_tracker");
  let checked = 0;
  for (const setting of registry.settings) {
    const link = fill(setting.link);
    const [pathAndQuery, hash = ""] = link.split("#");
    const [pathname, search = ""] = pathAndQuery.split("?");
    if (pathname === "/") continue;
    if (/^\/b\/[^/]+$/.test(pathname)) {
      const side = new URLSearchParams(search).get("side");
      assert.ok(SIDE_PARAMS.includes(side), `${setting.key}: ${link} names a sidebar view`);
      checked++;
      continue;
    }
    const parsed = readSettingsLink(pathname, hash ? `#${hash}` : "", search ? `?${search}` : "");
    assert.ok(parsed, `${setting.key}: ${link} resolves`);
    if (hash) assert.equal(parsed.section, hash, `${setting.key}: ${link} keeps its section`);
    checked++;
  }
  assert.ok(checked > 40, "most registry links are checked");

  // 4. Every section id is in the component that owns it.
  const pages = (await Promise.all(["settings/SettingsPages.tsx", "StandingAllowances.tsx", "FollowupsSettings.tsx", "DictationSettings.tsx"].map(source))).join("\n");
  const ids = new Set([...pages.matchAll(/(?:<SectionAnchor|<SettingsSection)[^>]*?\bid="([a-z]+)"/g)].map(match => match[1]));
  const automation = await source("AutomationSettings.tsx");
  const groupOrder = /const groupOrder: AutomationGroup\[\] = \[([^\]]+)\]/.exec(automation)?.[1].match(/"([a-z]+)"/g)?.map(item => item.slice(1, -1));
  assert.deepEqual(groupOrder, tsTabs.automation, "the automation sections are the Behavior page's groups, in order");
  assert.match(automation, /<SettingsSection key=\{group\} id=\{group\}/, "each Behavior group carries its id");
  for (const [tab, list] of Object.entries(tsTabs)) {
    if (tab === "automation") continue;
    for (const section of list) assert.ok(ids.has(section), `${tab}#${section} has an id in its component`);
  }
  const bot = await source("BotSettingsPanel.tsx");
  const botIds = [...(/const SECTIONS:[^=]+= \[([^;]+)\];/.exec(bot)?.[1] ?? "").matchAll(/id: "([a-z]+)"/g)].map(match => match[1]);
  assert.deepEqual(botIds, [...sections.BOT_SETTINGS_SECTIONS], "Bot settings sections match");
  const group = await source("GroupSettingsPanel.tsx");
  for (const section of sections.GROUP_SETTINGS_SECTIONS) assert.ok(group.includes(`data-section="${section}"`), `group settings section ${section}`);
  console.log(`PASS settings registry: ${registry.settings.length} keys, ${checked} links resolve, ${Object.values(tsTabs).flat().length} sections present`);
}
