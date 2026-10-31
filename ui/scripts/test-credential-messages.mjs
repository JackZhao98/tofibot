import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

// Settings > Keys & secrets: a failed save or apply must say why, in the person's language.
const ui = await openUiModules({ language: "en" });
try {
  const { credentialMessage } = await ui.load("/src/credentialMessages.ts");
  const en = await import("../src/locales/en/computer.json", { with: { type: "json" } });
  const words = en.default.credentials.error;
  const server = text => new Error(text);
  assert.equal(credentialMessage(server("This credential target already exists"), "x", "env"), words.env_exists, "a duplicate variable is not 'a file with this name'");
  assert.equal(credentialMessage(server("This credential target already exists"), "x", "ssh"), words.exists);
  assert.equal(credentialMessage(server("Wait for this Bot's active task to finish"), "x", "env"), words.busy, "a Bot with an active task is the busy wording");
  const raw = "Secret operation failed; the Bot computer may be unavailable or controlled by another session";
  assert.equal(credentialMessage(server(raw), "x", "env"), raw, "an unmapped server reason is shown as it came");
  assert.equal(credentialMessage("not an error", "fallback", "env"), "fallback");
  assert.equal(credentialMessage(server(""), "fallback", "env"), "fallback");
  console.log("credential messages ok");
} finally {
  await ui.close();
}
