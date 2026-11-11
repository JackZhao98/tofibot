import assert from "node:assert/strict";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { bundleModules } from "./lib/bundle.mjs";

// Pure reader for links into the settings: /settings/<tab>#<section>, /settings/connections/<name>, /b/<id>/settings#<section>,
// /g/<id>/settings#<section>. A link only says where to look; ids are validated like the conversation route's.
const root = dirname(dirname(fileURLToPath(import.meta.url)));
{
  const { readSettingsLink, settingsLinkPath, readSettingsHref, readConversationRoute, ...sections } = await bundleModules(root, ["src/settingsRoute.ts", "src/settings/sections.ts", "src/conversationRoute.ts"]);

  assert.deepEqual(readSettingsLink("/settings/general", "#region"), { kind: "settings", tab: "general", section: "region" });
  assert.deepEqual(readSettingsLink("/settings/models"), { kind: "settings", tab: "models" });
  assert.deepEqual(readSettingsLink("/settings/models/", "#defaults"), { kind: "settings", tab: "models", section: "defaults" });
  assert.deepEqual(readSettingsLink("/settings/models", "#nowhere"), { kind: "settings", tab: "models" }, "an unknown section keeps the page");
  assert.deepEqual(readSettingsLink("/settings/general", "#defaults"), { kind: "settings", tab: "general" }, "a section of another tab is not a section here");
  assert.deepEqual(readSettingsLink("/settings/approvals"), { kind: "settings", tab: "permissions" }, "the old id lands on the page that replaced it");
  for (const path of ["/settings", "/settings/", "/settings/nowhere", "/settings/Models", "/settings/models/x", "/settings/constructor", "/settings/__proto__", "/settings/toString"]) {
    assert.equal(readSettingsLink(path), null, path);
  }
  assert.deepEqual(readSettingsLink("/settings/connections/acme_tracker", "#tools", "?tool=create_issue"), { kind: "connection", name: "acme_tracker", section: "tools", tool: "create_issue" });
  assert.equal(readSettingsLink("/settings/connections/acme%20docs"), null, "a name with a space is not an id");
  assert.equal(readSettingsLink("/settings/connections/%00"), null);
  assert.equal(readSettingsLink("/settings/connections/a%2Fb"), null);
  assert.deepEqual(readSettingsLink("/settings/connections/", ""), { kind: "settings", tab: "connections" }, "no name is the Connections tab");
  assert.deepEqual(readSettingsLink("/b/bot-one/settings", "#model"), { kind: "bot-settings", botId: "bot-one", section: "model" });
  assert.deepEqual(readSettingsLink("/b/bot-one/settings"), { kind: "bot-settings", botId: "bot-one", section: "identity" }, "no section opens the first one");
  assert.deepEqual(readSettingsLink("/b/bot-one/settings", "#unknown"), { kind: "bot-settings", botId: "bot-one", section: "identity" });
  assert.deepEqual(readSettingsLink("/g/group-one/settings", "#members"), { kind: "group-settings", groupId: "group-one", section: "members" });
  assert.deepEqual(readSettingsLink("/g/group-one/settings"), { kind: "group-settings", groupId: "group-one", section: "general" });
  for (const path of ["/b//settings", "/b/%/settings", "/b/%00/settings", "/g/%2f/settings", "/b/a/b/settings", `/b/${"a".repeat(201)}/settings`, "/b/x/setting"]) {
    assert.equal(readSettingsLink(path), null, path);
  }

  // Every link the reader accepts is written back unchanged.
  // An older position opens where the setting lives now (settings-simplify section 7). Every row of the table resolves to a real page and section.
  for (const [from, to] of Object.entries(sections.LEGACY_SETTINGS_LINKS)) {
    const [tab, section] = to.split("#");
    assert.ok(tab in sections.SETTINGS_SECTIONS && (!section || sections.SETTINGS_SECTIONS[tab].includes(section)), `${from} -> ${to} exists`);
    const [oldTab, oldSection] = from.split("#");
    assert.deepEqual(readSettingsLink(`/settings/${oldTab}`, oldSection ? `#${oldSection}` : ""), { kind: "settings", tab, ...(section ? { section } : {}) }, `/settings/${from}`);
  }
  assert.deepEqual(readSettingsLink("/settings/allowances", "#nowhere"), { kind: "settings", tab: "permissions" }, "an old page with an unknown section still lands on its replacement");
  assert.deepEqual(readSettingsLink("/settings/followups", "#limits"), { kind: "settings", tab: "advanced", section: "followups" }, "a section of a retired page goes to the page's new home");
  assert.deepEqual(readSettingsLink("/settings/automation"), { kind: "settings", tab: "advanced", section: "behavior" }, "Behavior is a subpage of System");
  assert.deepEqual(readSettingsLink("/settings/tools", "#web"), { kind: "settings", tab: "connections", section: "web" });
  assert.deepEqual(readSettingsLink("/settings/general", "#primary"), { kind: "primary-settings" }, "the Primary Assistant moved to the Bot's own settings");
  assert.equal(settingsLinkPath({ kind: "primary-settings" }), "/settings/general");
  for (const tab of ["allowances", "followups", "tools", "keys", "automation"]) assert.ok(!(tab in sections.SETTINGS_SECTIONS), `${tab} is not a page any more`);
  for (const [tab, list] of Object.entries(sections.SETTINGS_SECTIONS)) {
    if (tab === "devices") continue;
    for (const section of [undefined, ...list]) {
      const link = { kind: "settings", tab, ...(section ? { section } : {}) };
      const path = settingsLinkPath(link);
      const [pathname, hash = ""] = path.split("#");
      assert.deepEqual(readSettingsLink(pathname, hash ? `#${hash}` : ""), link, path);
    }
  }
  for (const section of sections.BOT_SETTINGS_SECTIONS) {
    const link = { kind: "bot-settings", botId: "bot-1", section };
    const [pathname, hash] = settingsLinkPath(link).split("#");
    assert.deepEqual(readSettingsLink(pathname, `#${hash}`), link);
  }
  for (const section of sections.GROUP_SETTINGS_SECTIONS) {
    const link = { kind: "group-settings", groupId: "g1", section };
    const [pathname, hash] = settingsLinkPath(link).split("#");
    assert.deepEqual(readSettingsLink(pathname, `#${hash}`), link);
  }
  const connection = { kind: "connection", name: "acme_tracker", tool: "create_issue", section: "tools" };
  const [cpath, chash] = settingsLinkPath(connection).split("#");
  const [cpathname, csearch] = cpath.split("?");
  assert.deepEqual(readSettingsLink(cpathname, `#${chash}`, `?${csearch}`), connection);

  // A Markdown href: relative paths and this origin only.
  const origin = "https://tofi.example.test";
  assert.deepEqual(readSettingsHref("/settings/models#defaults", origin), { kind: "settings", tab: "models", section: "defaults" });
  assert.deepEqual(readSettingsHref(`${origin}/settings/models#defaults`, origin), { kind: "settings", tab: "models", section: "defaults" });
  for (const href of [undefined, "", "https://elsewhere.example.test/settings/models", "javascript:alert(1)", "mailto:a@b.test", "//elsewhere.example.test/settings/models", "/b/bot-one", "/settings/nowhere", `/settings/models#${"a".repeat(700)}`]) {
    assert.equal(readSettingsHref(href, origin), null, String(href));
  }

  // The conversation route carries the address of a settings link (the lazy reader above resolves it) and keeps the old results for every other address.
  assert.deepEqual(readConversationRoute("/settings/models", "#defaults"), { kind: "home", settings: "/settings/models#defaults" });
  assert.deepEqual(readConversationRoute("/b/bot-one/settings", "#model"), { kind: "bot", id: "bot-one", settings: "/b/bot-one/settings#model" });
  assert.deepEqual(readConversationRoute("/g/group-one/settings", "#members"), { kind: "group", id: "group-one", settings: "/g/group-one/settings#members" });
  assert.deepEqual(readConversationRoute("/"), { kind: "home" });
  assert.deepEqual(readConversationRoute("/b/bot-one"), { kind: "bot", id: "bot-one" });
  assert.equal(readConversationRoute("/b//settings").kind, "unavailable");
  assert.equal(readConversationRoute("/b/%00/settings").kind, "unavailable");
  assert.equal(readConversationRoute("/settingsX").kind, "unavailable");
  assert.equal(readSettingsLink("/settings/nowhere"), null, "an unknown page opens nothing");
  console.log("PASS settings route reader");
}
