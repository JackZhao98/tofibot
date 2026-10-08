import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { openUiModules } from "./ui-modules.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
Object.defineProperty(globalThis, "crypto", { value: webcrypto, configurable: true });
// Error copy is asserted in zh-CN (the shipped Chinese text), then once in English.
const ui = await openUiModules({ language: "zh-CN" });
try {
  const { encryptPortable, decryptPortable, isEncryptedPortable } = await ui.load("/src/portabilityCrypto.ts");
  const { checkPortableInput, importDefaultCategories, exportDefaultCategories, ENVIRONMENT_CATEGORY, portabilityLabel } = await ui.load("/src/portabilitySelection.ts");
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
  assert.equal(portabilityLabel("chats"), "聊天正文");
  assert.equal(portabilityLabel("unknown_server_count"), "unknown_server_count", "unknown categories show as sent");
  await ui.setLanguage("en");
  await assert.rejects(decryptPortable(encrypted, "Wrong-Synthetic-Passphrase"), /Couldn't decrypt/);
  assert.throws(() => checkPortableInput(v3, false), /encrypted bundle/);
  assert.equal(portabilityLabel("chats"), "Chat text");
  const uiSource = await readFile(join(root, "src/PortabilitySettings.tsx"), "utf8");
  assert.match(uiSource, /exported\.bots\?\.forEach/);
  assert.match(uiSource, /importBody\(preview\.preview_id\)/);
  assert.match(uiSource, /bundleSource/);
  assert.doesNotMatch(uiSource, /localStorage|console\.log|JSON\.stringify\(\{[^\n]*(?:exportPassword|importPassword)/);
  console.log("portability encryption: PASS (round trip, fresh nonce, wrong password, corruption, KDF/version, size, browser-only password handling, v1/v2/v3 encrypted roundtrips, sensitive file gating/defaults)");
} finally { await ui.close(); }
