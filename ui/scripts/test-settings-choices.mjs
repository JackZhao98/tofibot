// Pure rules behind two Settings controls: the language list (browser language first, tagged Default, no "Auto")
// and the approval mode (two standard choices mapped onto the server's AutoReview modes), plus the default Claude model.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-settings-choices-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/languageChoices.ts", "src/approvalMode.ts", "src/defaultModel.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  // Compiled files import siblings without extensions; Node needs them.
  const { writeFile, readFile } = await import("node:fs/promises");
  for (const [file, from, to] of [["languageChoices.js", "./i18n/languages", "./i18n/languages.js"]]) {
    const path = join(output, file);
    await writeFile(path, (await readFile(path, "utf8")).replace(`"${from}"`, `"${to}"`));
  }
  const { languageChoices, selectedLanguageChoice } = await import(pathToFileURL(join(output, "languageChoices.js")));
  const { approvalChoiceFor, reviewModeFor, approvalChoices } = await import(pathToFileURL(join(output, "approvalMode.js")));

  // Language: the browser language leads, named in its own script, tagged Default; no separate Auto entry; no duplicates.
  const fr = languageChoices("fr");
  assert.equal(fr[0].value, "auto");
  assert.equal(fr[0].label, "Français");
  assert.equal(fr[0].tag, "default");
  assert.equal(fr.filter(choice => choice.tag).length, 1, "exactly one Default tag");
  assert.equal(fr.filter(choice => choice.label === "Français").length, 1, "the browser language is not listed twice");
  assert.equal(fr.length, 7);
  assert.ok(!fr.some(choice => /auto/i.test(choice.label)), "no Auto entry");
  assert.equal(languageChoices("en")[0].label, "English");
  // No stored choice, or a stored choice equal to the browser language, selects the Default entry.
  assert.equal(selectedLanguageChoice("auto", "fr"), "auto");
  assert.equal(selectedLanguageChoice("fr", "fr"), "auto");
  assert.equal(selectedLanguageChoice("ja", "fr"), "ja");

  // Approval mode: Auto is the auto reviewer; Ask covers Off and Observe, and keeps an Observe setting made under Advanced.
  assert.deepEqual([...approvalChoices], ["auto", "ask"]);
  assert.equal(approvalChoiceFor("auto"), "auto");
  assert.equal(approvalChoiceFor("off"), "ask");
  assert.equal(approvalChoiceFor("shadow"), "ask");
  assert.equal(reviewModeFor("auto", "off"), "auto");
  assert.equal(reviewModeFor("ask", "auto"), "off");
  assert.equal(reviewModeFor("ask", "shadow"), "shadow");
  assert.equal(reviewModeFor("ask", "off"), "off");

  // Default Claude model: Haiku 5.5 when the list has it (a dated id counts), else the newest Haiku, else Claude's first model.
  const { preferredDefaultModel, DEFAULT_CLAUDE_MODEL } = await import(pathToFileURL(join(output, "defaultModel.js")));
  {
    assert.equal(DEFAULT_CLAUDE_MODEL, "claude-haiku-5-5");
    const m = id => ({ id, provider: id.startsWith("claude") ? "anthropic" : "codex" });
    assert.equal(preferredDefaultModel([m("claude-opus-5"), m("claude-haiku-5-5"), m("claude-haiku-4-5")]).id, "claude-haiku-5-5");
    assert.equal(preferredDefaultModel([m("claude-opus-5"), m("claude-haiku-5-5-20260101")]).id, "claude-haiku-5-5-20260101");
    assert.equal(preferredDefaultModel([m("claude-opus-5"), m("claude-haiku-4-5"), m("claude-haiku-3-5")]).id, "claude-haiku-4-5");
    assert.equal(preferredDefaultModel([m("claude-opus-5"), m("claude-sonnet-5")]).id, "claude-opus-5");
    assert.equal(preferredDefaultModel([m("codex-sol-1")]), undefined, "without Claude nothing is preselected");
  }

  // Organization policy (user groups, design 5.2): what usePolicy() derives from GET /api/config.capabilities. A server without
  // the field means everything is the person's own, and the computer is the host's.
  {
    const { bundleModules } = await import("./lib/bundle.mjs");
    const { policyFrom } = await bundleModules(root, ["src/capabilityPolicy.ts"]);
    const own = { mode: "own", source: "own" };
    const none = policyFrom(undefined);
    assert.equal(none.managed, false);
    assert.equal(none.first, null);
    assert.deepEqual(none.of("computer"), { mode: "host", source: "own" }, "no policy: the computer is the host's");
    assert.equal(none.provided("ai_models"), false);
    const view = overrides => ({ ai_models: own, computer: { mode: "host", source: "own" }, web_search: own, web_fetch: own, ...overrides });
    assert.equal(policyFrom(view({})).managed, false, "all own: nothing is managed");
    const provided = policyFrom(view({ ai_models: { mode: "provided", source: "organization" } }));
    assert.equal(provided.provided("ai_models"), true);
    assert.equal(provided.off("ai_models"), false);
    assert.equal(provided.managed, true);
    assert.equal(provided.first, "ai_models");
    const searchOff = policyFrom(view({ web_search: { mode: "off", source: "none" }, web_fetch: { mode: "builtin", source: "none" } }));
    assert.equal(searchOff.off("web_search"), true, "off reads as nothing to set up");
    assert.equal(searchOff.off("web_fetch"), true, "built-in only reads as nothing to set up");
    assert.equal(searchOff.provided("web_search"), false);
    assert.equal(searchOff.first, "web_search", "the banner links to the first managed page in Settings order");
    const computer = policyFrom(view({ computer: { mode: "provided", source: "organization", via: "e2b" } }));
    assert.equal(computer.of("computer").via, "e2b");
    assert.equal(computer.first, "computer");
    assert.equal(policyFrom(view({ computer: { mode: "none", source: "none" } })).off("computer"), true);
    const stable = policyFrom(view({}));
    assert.equal(typeof stable.of, "function");
  }
  console.log("PASS: language list (Default tag, no Auto), approval mode mapping, default Claude model, organization policy");
} finally {
  await rm(output, { recursive: true, force: true });
}
