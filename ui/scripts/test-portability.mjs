import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-portability-"));
Object.defineProperty(globalThis, "crypto", { value: webcrypto, configurable: true });
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/portabilityCrypto.ts", "src/portabilitySelection.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  const { encryptPortable, decryptPortable, isEncryptedPortable } = await import(pathToFileURL(join(output, "portabilityCrypto.js")));
  const { checkPortableInput, importDefaultCategories, exportDefaultCategories, ENVIRONMENT_CATEGORY } = await import(pathToFileURL(join(output, "portabilitySelection.js")));
  const source = JSON.stringify({ format: "tofi.bundle", version: 1, synthetic: "中文 fixture, pasted synthetic sensitivity marker" });
  const password = "Synthetic-Test-Passphrase-Only";
  const encrypted = await encryptPortable(source, password);
  assert.equal(isEncryptedPortable(encrypted), true);
  assert.equal(encrypted.includes("synthetic sensitivity marker"), false);
  assert.equal(encrypted.includes(password), false);
  assert.equal(await decryptPortable(encrypted, password), source);
  assert.notEqual(await encryptPortable(source, password), encrypted, "fresh salt and nonce required");
  await assert.rejects(decryptPortable(encrypted, "Wrong-Synthetic-Passphrase"), /无法解密/);
  const damaged = JSON.parse(encrypted); damaged.ciphertext = damaged.ciphertext.replace(/^./, damaged.ciphertext[0] === "A" ? "B" : "A");
  await assert.rejects(decryptPortable(JSON.stringify(damaged), password), /无法解密/);
  const version = JSON.parse(encrypted); version.iterations = 1;
  await assert.rejects(decryptPortable(JSON.stringify(version), password), /无法解密/);
  await assert.rejects(encryptPortable(source, "short"), /12/);
  await assert.rejects(encryptPortable("x".repeat(16 * 1024 * 1024 + 1), password), /16 MiB/);
  const legacy = JSON.stringify({ format: "tofi.bot", version: 1, included: ["bot_config"], bot: { name: "Synthetic" } });
  const v2 = JSON.stringify({ format: "tofi.bundle", version: 2, included: ["bot_config", "attachments"] });
  const canary = "Synthetic-ONLY-v3-environment-canary 中文 with exact whitespace ";
  const v3 = JSON.stringify({ format: "tofi.bundle", version: 3, included: ["bot_config", ENVIRONMENT_CATEGORY], vault_environment: [{ id: "synthetic-record", value: canary }] });
  for (const clear of [legacy, v2, v3]) {
    const envelope = await encryptPortable(clear, password);
    assert.equal(envelope.includes(canary), false);
    assert.equal(await decryptPortable(envelope, password), clear);
    assert.doesNotThrow(() => checkPortableInput(clear, true));
  }
  assert.doesNotThrow(() => checkPortableInput(legacy, false));
  assert.doesNotThrow(() => checkPortableInput(v2, false));
  assert.throws(() => checkPortableInput(v3, false), /加密数据包/);
  assert.throws(() => checkPortableInput(v3.replace('"version":3', '"version":4'), true), /版本/);
  assert.equal(exportDefaultCategories.includes(ENVIRONMENT_CATEGORY), false);
  assert.deepEqual(importDefaultCategories(["bot_config", "settings", ENVIRONMENT_CATEGORY]), ["bot_config"]);
  const ui = await readFile(join(root, "src/PortabilitySettings.tsx"), "utf8");
  assert.match(ui, /exported\.bots\?\.forEach/);
  assert.match(ui, /importBody\(preview\.preview_id\)/);
  assert.match(ui, /bundleSource/);
  assert.doesNotMatch(ui, /localStorage|console\.log|JSON\.stringify\(\{[^\n]*(?:exportPassword|importPassword)/);
  console.log("portability encryption: PASS (round trip, fresh nonce, wrong password, corruption, KDF/version, size, browser-only password handling, v1/v2/v3 encrypted roundtrips, sensitive file gating/defaults)");
} finally { await rm(output, { recursive: true, force: true }); }
