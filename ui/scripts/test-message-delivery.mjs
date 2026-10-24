import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const modules = await openUiModules();
try {
  const { applyInbox } = await modules.load("/src/messageDelivery.ts");
  const list = [{ id: "a", delivery: "queued" }, { id: "b" }];
  const read = applyInbox(list, { message_id: "a", state: "read" });
  assert.equal(read[0].delivery, "read");
  assert.equal(read[1], list[1], "other messages keep their identity");
  assert.equal(applyInbox(read, { message_id: "a", state: "queued" })[0].delivery, "read", "a late queued never undoes read");
  assert.equal(applyInbox(list, { message_id: "missing", state: "read" })[0], list[0], "an unknown message changes nothing");
  assert.equal(applyInbox(list, { message_id: "a", state: "bogus" }), list, "an unknown state changes nothing");
  assert.equal(applyInbox([list[1]], { message_id: "b", state: "queued" })[0].delivery, "queued");
  console.log("message delivery ok");
} finally {
  await modules.close?.();
}
