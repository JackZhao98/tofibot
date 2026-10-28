// Reply, attachment and pending metadata through storage, and the acknowledgement of a sent draft (composerDraft.ts).
// The pure functions the Composer and the Workspace call are imported; nothing is evaluated from sliced source.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-reply-ack-"));
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/composerDraft.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", out, "--skipLibCheck", "--declaration", "false", "--pretty", "false"], { cwd: root });
  const h = await import(pathToFileURL(join(out, "composerDraft.js")));
  const empty = () => ({ content: "", attachments: [] });
  const roundTrip = drafts => h.parseStoredDrafts(JSON.stringify(h.storableDrafts(drafts).kept));
  let cases = 0;
  for (const mode of ["plain", "reply", "reply-attachment", "reply-attachment-only", "edit", "cancel-reply", "new-reply", "new-attachment", "new-pending", "other-chat-edit"]) {
    const reply = mode === "plain" ? undefined : { id: "synthetic-target", name: "Synthetic Bot", excerpt: "Synthetic original" };
    const attachment = { key: "synthetic-file", id: "uploaded-file", name: "synthetic.txt", size: 3, lastModified: 1, type: "text/plain" };
    const initial = { content: mode === "reply-attachment-only" ? "" : "synthetic response", reply, attachments: mode.startsWith("reply-attachment") ? [attachment] : [] };
    const signature = h.composerMessageSignature(initial.content, initial.attachments, reply);
    // The Composer records the pending id with the draft before sending.
    let drafts = { A: { ...initial, pending: { signature, id: "synthetic-client" } }, B: { content: "other conversation", attachments: [] } };
    const restored = roundTrip(drafts).A;
    assert.deepEqual(restored.reply, initial.reply, "storage must preserve each conversation's reply metadata");
    assert.equal(h.composerClientMessageId(h.composerMessageSignature(restored.content, restored.attachments, restored.reply), restored.pending, () => "duplicate-client"), "synthetic-client", "reload/lost response must reuse the same client ID");
    assert.equal(h.composerMessageContent(restored.content, restored.reply), h.composerMessageContent(initial.content, reply), "the same wire content after a reload");
    if (mode === "edit") drafts.A = { ...drafts.A, content: "new draft while sending" };
    if (mode === "cancel-reply") drafts.A = { ...drafts.A, reply: undefined };
    if (mode === "new-reply") drafts.A = { ...drafts.A, reply: { ...reply, id: "another-target" } };
    if (mode === "new-attachment") drafts.A = { ...drafts.A, attachments: [attachment] };
    if (mode === "new-pending") drafts.A = { ...drafts.A, pending: { ...drafts.A.pending, id: "new-client" } };
    if (mode === "other-chat-edit") drafts.B = { ...drafts.B, content: "new draft in B" };
    const changed = drafts.A;
    const next = h.clearSentDraft(drafts, "A", signature, "synthetic-client", empty());
    const newer = ["edit", "cancel-reply", "new-reply", "new-attachment", "new-pending"].includes(mode);
    if (newer) assert.equal(next, drafts, "an acknowledgement must not erase a newer edit/reply/attachment/pending");
    else {
      assert.deepEqual(next.A, empty(), "the acknowledgement clears the exact source draft");
      assert.equal(roundTrip(next).A, undefined, "and nothing of it stays in storage");
    }
    assert.equal(next.B.content, mode === "other-chat-edit" ? "new draft in B" : "other conversation", "acknowledgement must never clear another conversation");
    assert.equal(h.clearSentDraft(drafts, "A", signature, "someone-else", empty()), drafts, "another message's acknowledgement clears nothing");
    void changed;
    cases++;
  }
  assert.deepEqual(h.parseStoredDrafts("not json"), {}, "damaged storage reads as empty");
  assert.deepEqual(h.parseStoredDrafts(JSON.stringify({ A: { content: 3, attachments: [] } })), {}, "a malformed draft is dropped");
  const files = h.storableDrafts({ A: { content: "x", attachments: [{ key: "k", name: "n", size: 1, lastModified: 1, file: { bytes: 1 } }] } });
  assert.equal(files.stored.A.attachments[0].file, undefined, "file bytes are never stored");
  console.log(`reply acknowledgement checks: PASS (${cases} scenarios through the real storage and acknowledgement functions)`);
} finally { await rm(out, { recursive: true, force: true }); }
