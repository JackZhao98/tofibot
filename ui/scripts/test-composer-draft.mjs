import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const uiRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const outputDir = await mkdtemp(join(tmpdir(), "tofi-composer-draft-"));

try {
  await run(join(uiRoot, "node_modules/.bin/tsc"), [
    "src/composerDraft.ts", "--ignoreConfig", "--target", "ES2022",
    "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", outputDir,
    "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: uiRoot });
  const { composerClientMessageId, composerMessageSignature, composerMessageContent, composerDraftMatchesMessage } = await import(pathToFileURL(join(outputDir, "composerDraft.js")));
  const assert = (condition, reason) => { if (!condition) throw new Error(reason); };
  const attachments = [{ key: "selected-file" }];
  const beforeUpload = composerMessageSignature(" retry this ", attachments);
  const afterUpload = composerMessageSignature("retry this", [{ key: "selected-file", id: "server-attachment" }]);
  assert(beforeUpload === afterUpload, "upload IDs must not change the message signature");
  let generated = 0;
  const first = composerClientMessageId(beforeUpload, undefined, () => `client-${++generated}`);
  const retry = composerClientMessageId(afterUpload, { signature: afterUpload, id: first }, () => `client-${++generated}`);
  assert(first === "client-1" && retry === first && generated === 1, "failed send retry must reuse the client message ID");
  const ordered = composerMessageSignature("retry this", [{ key: "first" }, { key: "second" }]);
  const reversed = composerMessageSignature("retry this", [{ key: "second" }, { key: "first" }]);
  assert(ordered !== reversed, "attachment order is part of the stable signature");
  const reply = { id: "synthetic-target", name: "Synthetic Bot", excerpt: "Synthetic original" };
  const replySignature = composerMessageSignature(" retry this ", attachments, reply);
  assert(replySignature === composerMessageSignature(composerMessageContent("retry this", reply), attachments), "payload and source draft must share one canonical signature");
  const draft = { content: "retry this", attachments, reply, pending: { signature: replySignature, id: first } };
  const restored = JSON.parse(JSON.stringify(draft));
  assert(composerClientMessageId(composerMessageSignature(restored.content, restored.attachments, restored.reply), restored.pending, () => "duplicate") === first, "reload preserves reply and retry ID");
  assert(composerDraftMatchesMessage(restored, replySignature, first), "delayed reply acknowledgement matches the source draft after switch/reload");
  assert(composerDraftMatchesMessage({ ...draft, attachments: [{ key: "selected-file", id: "uploaded" }] }, replySignature, first), "reply attachment upload must preserve acknowledgement identity");
  for (const changed of [
    { content: "new draft" }, { reply: undefined }, { reply: { ...reply, id: "new-target" } },
    { attachments: [] }, { pending: { signature: replySignature, id: "new-send" } },
  ]) assert(!composerDraftMatchesMessage({ ...draft, ...changed }, replySignature, first), "late acknowledgement must preserve newer content/reply/attachments/send");
  console.log("composer draft checks: PASS (upload/reply-stable signature, persisted reply retry, delayed acknowledgement, newer draft preservation)");
} finally {
  await rm(outputDir, { recursive: true, force: true });
}
