import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

// The variable-name rule in Settings > Keys & secrets must equal Go `allowedSecretEnv` (internal/app/secrets.go).
const ui = await openUiModules({ language: "en" });
try {
  const m = await ui.load("/src/secretEnvName.ts");
  const ok = ["A", "_", "_X", "MY_API_KEY", "A1", "K9_", "LDX", "PATHS", "HOMEDIR", "A".repeat(128)];
  for (const n of ok) { assert.equal(m.allowedSecretEnv(n), true, n); assert.equal(m.secretEnvProblem(n), null, n); }
  const reserved = ["PATH", "HOME", "BASH_ENV", "ENV", "DISPLAY", "TOFI_WORKSPACE", "PWD", "TMPDIR", "LD_PRELOAD", "LD_", "LD_LIBRARY_PATH"];
  assert.deepEqual([...m.SECRET_ENV_RESERVED_NAMES], ["PATH", "HOME", "BASH_ENV", "ENV", "DISPLAY", "TOFI_WORKSPACE", "PWD", "TMPDIR"], "reserved list is the Go list");
  for (const n of reserved) { assert.equal(m.allowedSecretEnv(n), false, n); assert.deepEqual(m.secretEnvProblem(n), { code: "reserved", name: n }, n); }
  const bad = { "my key": "bad_chars", "A-B": "bad_chars", "a": "bad_chars", "A.B": "bad_chars", "中文": "bad_chars", "1ABC": "leading_digit", ["A".repeat(129)]: "too_long" };
  for (const [n, code] of Object.entries(bad)) { assert.equal(m.allowedSecretEnv(n), false, n); assert.equal(m.secretEnvProblem(n).code, code, n); }
  assert.equal(m.allowedSecretEnv(""), false);
  assert.equal(m.secretEnvProblem(""), null, "empty is 'required', not a name problem");
  assert.equal(m.secretEnvProblem("A!B?A!").chars, "! ?");
  assert.equal(m.normalizeSecretEnvInput("my api-key"), "MY_API_KEY");
  assert.equal(m.normalizeSecretEnvInput("a.b!"), "A.B!", "other characters stay so the error can name them");
  console.log("secret env name ok");
} finally {
  await ui.close();
}
