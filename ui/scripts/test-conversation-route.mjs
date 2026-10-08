import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-conversation-route-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/conversationRoute.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  const { readConversationRoute, resolveConversationRoute, conversationPath } = await import(pathToFileURL(join(output, "conversationRoute.js")));
  const bot = { id: "bot-one", dm_conversation_id: "dm-one" };
  const dm = { id: "dm-one", kind: "dm", bot_id: bot.id };
  const group = { id: "group-one", kind: "group" };
  const bots = [bot], conversations = [dm, group];
  for (const path of ["/", "/index.html"]) assert.deepEqual(readConversationRoute(path), { kind: "home" });
  assert.deepEqual(readConversationRoute("/b/bot-one/"), { kind: "bot", id: bot.id });
  assert.deepEqual(readConversationRoute("/g/group-one"), { kind: "group", id: group.id });
  for (const path of ["/b/", "/g/", "/b/%", "/b/%00", "/b/%2f", "/g/%5c", "/b/%20", "/b/a/b", "/elsewhere", `/b/${"a".repeat(201)}`]) {
    assert.equal(readConversationRoute(path).kind, "unavailable", path);
  }
  for (const conversation of conversations) {
    assert.equal(resolveConversationRoute(readConversationRoute(conversationPath(conversation, bots)), bots, conversations), conversation);
  }
  assert.equal(conversationPath({ id: dm.id, kind: "dm" }, bots), "/b/bot-one", "DM fallback uses Bot identity");
  assert.equal(conversationPath({ ...group, user_visible: false }, bots), null);
  assert.equal(resolveConversationRoute({ kind: "bot", id: "other-account" }, bots, conversations), undefined);
  assert.equal(resolveConversationRoute({ kind: "group", id: "deleted" }, bots, conversations), undefined);
  assert.equal(resolveConversationRoute({ kind: "group", id: dm.id }, bots, conversations), undefined, "DM IDs are not group IDs");
  assert.equal(resolveConversationRoute({ kind: "bot", id: dm.id }, bots, conversations), undefined, "Bot URLs use Bot IDs");
  assert.equal(resolveConversationRoute({ kind: "group", id: group.id }, bots, [{ ...group, user_visible: false }]), undefined);
  assert.equal(resolveConversationRoute({ kind: "bot", id: bot.id }, bots, [{ ...dm, bot_id: "other" }]), undefined);
  assert.equal(resolveConversationRoute({ kind: "bot", id: bot.id }, bots, [{ ...dm, archived: true }]).id, dm.id, "archived links remain readable");
  console.log("PASS: conversation route parsing, identity, authenticated-index isolation and archived access");
} finally {
  await rm(output, { recursive: true, force: true });
}
