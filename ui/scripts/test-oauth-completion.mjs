import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { integrationCatalog, googleAPIEnableURL, googleAudienceURL } from "../src/integrationCatalog.ts";
import { oauthAssetsPlugin } from "../oauthAssetsPlugin.ts";

const expected = {
  "google-gmail": ["gmail", "gmailmcp"],
  "google-drive": ["drive", "drivemcp"],
  "google-docs": ["docs", "docsmcp"],
  "google-sheets": ["sheets", "sheetsmcp"],
  "google-slides": ["slides", "slidesmcp"],
  "google-calendar": ["calendar-json", "calendarmcp"],
};
const google = integrationCatalog.filter(item => item.category === "google");
assert.equal(google.length, 6);
for (const preset of google) {
  assert.deepEqual(preset.googleAPIs, { api: expected[preset.id][0] + ".googleapis.com", mcp: expected[preset.id][1] + ".googleapis.com" });
  for (const api of Object.values(preset.googleAPIs)) {
    const url = new URL(googleAPIEnableURL(api));
    assert.equal(url.origin, "https://console.cloud.google.com");
    assert.equal(url.pathname, "/apis/enableflow;apiid=" + api);
    assert.equal(url.search, "", "enablement links must not infer a project from client_id");
  }
}
assert.equal(googleAudienceURL, "https://console.cloud.google.com/auth/audience");
assert.equal(integrationCatalog.find(item => item.id === "notion").googleAPIs, undefined);

const assets = [];
oauthAssetsPlugin().generateBundle.call({ emitFile: asset => assets.push(asset) });
const css = assets.find(asset => asset.fileName === "oauth/connection.css").source;
const script = assets.find(asset => asset.fileName === "oauth/connection.js").source;
const tokens = readFileSync(new URL("../src/design-tokens.css", import.meta.url), "utf8");
assert.equal(css, tokens + "\n" + readFileSync(new URL("../src/oauth/connection.css", import.meta.url), "utf8"));
assert.doesNotMatch(css, /@import|https?:\/\//, "strict CSP assets have no external imports");
assert.match(readFileSync(new URL("../src/interaction-system.css", import.meta.url), "utf8"), /^@import "\.\/design-tokens.css";/);
assert.match(css, /data-state="pending"[^}]+animation:/);
assert.match(css, /prefers-reduced-motion: reduce/);

function execute({ preference = null, dark = false, blocked = false, pathname = "/api/extensions/mcp/fixture/oauth/callback" } = {}) {
  const document = { documentElement: { dataset: {} } };
  const events = {};
  const media = { matches: dark, addEventListener: (type, listener) => { events.media = listener; } };
  const historyCalls = [];
  const context = {
    document, localStorage: { getItem: () => { if (blocked) throw new Error("blocked"); return preference; } },
    window: { matchMedia: () => media, addEventListener: (type, listener) => { events[type] = listener; } },
    location: { pathname }, history: { replaceState: (...args) => historyCalls.push(args) },
  };
  Object.defineProperty(context.window, "opener", { get() { throw new Error("must not trust or contact opener"); } });
  Object.defineProperty(context.location, "search", { get() { throw new Error("must not read callback query data"); } });
  runInNewContext(script, context);
  return { document, events, media, historyCalls, setPreference: next => { preference = next; } };
}
for (const [preference, dark, expectedTheme] of [[null,false,"light"],[null,true,"dark"],["light",true,"light"],["dark",false,"dark"],["system",true,"dark"],["invalid",false,"light"]]) {
  assert.equal(execute({ preference, dark }).document.documentElement.dataset.theme, expectedTheme);
}
const state = execute({ preference: "system" });
state.media.matches = true; state.events.media();
assert.equal(state.document.documentElement.dataset.theme, "dark");
state.setPreference("light"); state.events.storage({ key: "tofi:appearance" });
assert.equal(state.document.documentElement.dataset.theme, "light");
assert.equal(execute({ dark: true, blocked: true }).document.documentElement.dataset.theme, "dark");
assert.deepEqual(JSON.parse(JSON.stringify(state.historyCalls)), [[null,"","/api/extensions/mcp/fixture/oauth/callback"]]);
assert.equal(execute({ pathname: "/api/extensions/mcp/fixture/oauth/pending" }).historyCalls.length, 0);
console.log("OAuth completion: six Google presets, stable shared-token assets, saved/system appearance, bounded history and no opener/query access PASS");
