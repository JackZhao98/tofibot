import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-reply-ack-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/App.tsx", "--ignoreConfig", "--target", "ES2022", "--jsx", "react-jsx", "--module", "ESNext", "--moduleResolution", "Bundler", "--outDir", out, "--skipLibCheck", "--types", "vite/client", "--declaration", "false", "--pretty", "false"], { cwd: root });
  const helpers = await import(pathToFileURL(join(out, "composerDraft.js")));
  const app = await readFile(join(out, "App.js"), "utf8");
  const sendSource = app.slice(app.indexOf("async function send("), app.indexOf("async function createNewBot("));
  const composer = app.slice(app.indexOf("function Composer("));
  const submitSource = composer.slice(composer.indexOf("async function submit("), composer.indexOf("function handleKeyDown("));
  const storageSource = app.slice(app.indexOf("function readComposerDrafts("), app.indexOf("function Avatar("));
  const compile = (source, name, env) => Function(...Object.keys(env), `${source}; return ${name};`)(...Object.values(env));
  const values = new Map();
  const storageEnv = { isDesktop: false, sessionStorage: { getItem: key => values.get(key), setItem: (key, value) => values.set(key, value) }, updateDesktopState() {} };
  const write = compile(storageSource, "writeComposerDrafts", storageEnv), read = compile(storageSource, "readComposerDrafts", storageEnv);
  let cases = 0;
  for (const mode of ["plain", "reply", "reply-attachment", "reply-attachment-only", "edit", "cancel-reply", "new-reply", "new-attachment", "new-pending", "other-chat-edit"]) {
    const reply = mode === "plain" ? undefined : { id: "synthetic-target", name: "Synthetic Bot", excerpt: "Synthetic original" };
    const attachment = { key: "synthetic-file", id: "uploaded-file", name: "synthetic.txt", size: 3, lastModified: 1, type: "text/plain" };
    const initial = { content: mode === "reply-attachment-only" ? "" : "synthetic response", reply, attachments: mode.startsWith("reply-attachment") ? [attachment] : [] };
    let drafts = { A: initial, B: { content: "other conversation", attachments: [] } };
    let release, payload, persisted;
    const activeIdRef = { current: ["edit", "cancel-reply", "new-reply", "new-attachment", "new-pending"].includes(mode) ? "A" : "B" };
    const env = {
      ...helpers, active: { id: "A" }, activeIdRef, isDesktop: true,
      api: { sendMessage: async (_id, body) => { payload = body; await new Promise(resolve => { release = resolve; }); return { message: { id: "synthetic-stored", created_at: "2026-10-03T00:00:00Z" }, run: { id: "synthetic-run" } }; } },
      setLastUserActivity() {}, setComposerDrafts: update => { drafts = update(drafts); }, emptyComposerDraft: () => ({ content: "", attachments: [] }),
      composerStorageKey: { current: "fixture" }, writeComposerDrafts: (key, next) => { write(key, next); persisted = read(key); },
      arrivalIDs: { current: new Set() }, upsertMessage() {}, updateConversationPreview() {}, setRuns() {}, mergeRunUpdates() {}, jumpLatest() {},
      setError() {}, errorText: String,
    };
    const send = compile(sendSource, "send", env);
    const submitEnv = {
      ...helpers, content: initial.content, attachments: initial.attachments, reply, draft: initial, canCompose: true, sending: false, dictation: {}, conversation: { id: "A" },
      operationRef: { current: 0 }, conversationIdRef: { current: "A" }, isDesktop: true, textareaRef: { current: null }, messageId: () => "synthetic-client",
      onDraftChange: update => { drafts = { ...drafts, A: update(drafts.A) }; write("fixture", drafts); }, onSend: send,
      setSending() {}, setFileError() {}, setUploadingKey() {}, onSent() {},
    };
    const submit = compile(submitSource, "submit", submitEnv);
    const sending = submit({ preventDefault() {} });
    assert.ok(release, "actual Composer must invoke actual Workspace.send");
    const restored = read("fixture").A;
    assert.deepEqual(restored.reply, initial.reply, "storage must preserve each conversation's reply metadata");
    assert.equal(helpers.composerClientMessageId(helpers.composerMessageSignature(restored.content, restored.attachments, restored.reply), restored.pending, () => "duplicate-client"), "synthetic-client", "reload/lost response must reuse the same client ID");
    if (mode === "edit") drafts.A = { ...drafts.A, content: "new draft while sending" };
    if (mode === "cancel-reply") drafts.A = { ...drafts.A, reply: undefined };
    if (mode === "new-reply") drafts.A = { ...drafts.A, reply: { ...reply, id: "another-target" } };
    if (mode === "new-attachment") drafts.A = { ...drafts.A, attachments: [attachment] };
    if (mode === "new-pending") drafts.A = { ...drafts.A, pending: { ...drafts.A.pending, id: "new-client" } };
    if (mode === "other-chat-edit") drafts.B = { ...drafts.B, content: "new draft in B" };
    const changed = drafts.A;
    release(); await sending;
    if (activeIdRef.current === "A") assert.equal(drafts.A, changed, "current Composer callback must not erase newer edits/reply/attachments/pending after acknowledgement");
    else { assert.equal(drafts.A.content, "", "late acknowledgement after chat switch must clear the exact source draft"); assert.equal(persisted.A.reply, undefined); assert.equal(persisted.A.pending, undefined); }
    assert.equal(drafts.B.content, mode === "other-chat-edit" ? "new draft in B" : "other conversation", "acknowledgement must never clear another conversation");
    assert.equal(payload.content, helpers.composerMessageContent(initial.content, reply));
    assert.deepEqual(payload.attachment_ids, initial.attachments.map(a => a.id));
    cases++;
  }
  console.log(`reply acknowledgement checks: PASS (${cases} actual Composer/Workspace/storage scenarios; chat switch, reload, reply attachments and in-flight edits)`);
} finally { await rm(out, { recursive: true, force: true }); }
