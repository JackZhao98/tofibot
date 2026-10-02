import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-renderer-state-"));
after(() => rm(output, { recursive: true, force: true }));
await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
  "src/desktop.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022",
  "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck",
  "--strict", "--declaration", "false", "--pretty", "false",
], { cwd: root });

const A = "123e4567-e89b-12d3-a456-426614174000";
const B = "123e4567-e89b-12d3-a456-426614174001";
const C = "123e4567-e89b-12d3-a456-426614174002";
const snapshot = label => ({ drafts: { conversation: label }, activeConversation: label });
const turn = () => new Promise(resolve => setImmediate(resolve));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

let moduleID = 0;
async function fixture(t, { native = true } = {}) {
  const reads = [], writes = [], timers = new Map(), events = new Map();
  let timerID = 0, nativeFlush;
  const previousWindow = globalThis.window;
  const previousDocument = globalThis.document;
  const bridge = {
    platform: "fixture",
    onFlushState: callback => { nativeFlush = callback; },
    onWindowState: () => {},
    remoteInputFocus: () => {},
    readWorkspaceState: instance => {
      const result = deferred();
      reads.push({ instance, ...result });
      return result.promise;
    },
    writeWorkspaceState: (instance, state) => {
      const result = deferred();
      writes.push({ instance, state: structuredClone(state), ...result });
      return result.promise;
    },
  };
  // Only DOM registration and the preload API surface are stubbed. The real
  // renderer module runs unchanged; no browser, IPC, cookies or credentials.
  globalThis.window = {
    tofiDesktop: native ? bridge : undefined,
    clearTimeout: id => timers.delete(id),
    setTimeout: callback => { timers.set(++timerID, callback); return timerID; },
    addEventListener: (name, callback) => {
      if (!events.has(name)) events.set(name, []);
      events.get(name).push(callback);
    },
  };
  globalThis.document = { documentElement: { dataset: {} }, addEventListener: () => {} };
  t.after(() => {
    for (const write of writes) write.resolve();
    if (previousWindow === undefined) delete globalThis.window; else globalThis.window = previousWindow;
    if (previousDocument === undefined) delete globalThis.document; else globalThis.document = previousDocument;
  });
  const api = await import(`${pathToFileURL(join(output, "desktop.js"))}?fixture=${++moduleID}`);
  return {
    api, reads, writes, timers, nativeFlush,
    pagehide: async () => { for (const callback of events.get("pagehide") || []) await callback(); },
    async begin(instance) {
      const pending = api.hydrateDesktopState(instance);
      await turn();
      return { pending, read: reads.at(-1) };
    },
    async hydrate(instance, value) {
      const { pending, read } = await this.begin(instance);
      read.resolve(value);
      await pending;
    },
  };
}

for (const staleResult of ["success", "rejection"]) {
  test(`reverse hydration ${staleResult} cannot replace newer renderer state`, async t => {
    const f = await fixture(t);
    const old = await f.begin(A);
    const current = await f.begin(B);
    current.read.resolve(snapshot("current"));
    await current.pending;
    if (staleResult === "success") old.read.resolve(snapshot("old"));
    else old.read.reject(new Error("Workspace document is stale"));
    await old.pending;
    assert.deepEqual(f.api.desktopState(), snapshot("current"));
    const flushed = f.api.flushDesktopState();
    assert.equal(f.writes.at(-1).instance, B);
    assert.deepEqual(f.writes.at(-1).state, snapshot("current"));
    f.writes.at(-1).resolve();
    await flushed;
  });
}

test("overlapping same-instance callers both await the one hydration", async t => {
  const f = await fixture(t);
  const first = await f.begin(A);
  let secondSettled = false;
  const second = f.api.hydrateDesktopState(A).then(() => { secondSettled = true; });
  await turn();
  assert.equal(f.reads.length, 1);
  assert.equal(secondSettled, false, "a second caller must not mount with unhydrated state");
  first.read.resolve(snapshot("ready"));
  await Promise.all([first.pending, second]);
  assert.deepEqual(f.api.desktopState(), snapshot("ready"));
  await f.api.hydrateDesktopState(A);
  assert.equal(f.reads.length, 1, "successful hydration remains cached");
});

test("failed hydration is retryable and cannot flush an empty fallback over saved state", async t => {
  const f = await fixture(t);
  const failed = await f.begin(A);
  failed.read.reject(new Error("native read unavailable"));
  await failed.pending;
  f.api.updateDesktopState(snapshot("unhydrated edit"));
  const flushed = f.api.flushDesktopState();
  // Settle even an erroneous write so the assertion can report its destination.
  f.writes.at(-1)?.resolve();
  await flushed;
  assert.equal(f.writes.length, 0);
  const retry = await f.begin(A);
  assert.equal(f.reads.length, 2, "failure must not mark the instance hydrated");
  retry.read.resolve(snapshot("restored"));
  await retry.pending;
  assert.deepEqual(f.api.desktopState(), snapshot("restored"));
});

test("switch clears old state and fences delayed timer/native writes to their original instance", async t => {
  const f = await fixture(t);
  await f.hydrate(A, snapshot("old"));
  f.api.updateDesktopState(snapshot("last old edit"));
  const oldTimer = [...f.timers.values()][0];
  const current = await f.begin(B);
  assert.deepEqual(f.api.desktopState(), {}, "pending instance must not expose old drafts");
  assert.equal(f.timers.size, 0);
  assert.equal(f.writes.length, 1, "switch flushes a pending final edit before retiring its instance");
  assert.deepEqual({ instance: f.writes[0].instance, state: f.writes[0].state }, { instance: A, state: snapshot("last old edit") });
  f.api.updateDesktopState(snapshot("late old component edit"));
  await f.api.flushDesktopState();
  assert.equal(f.writes.length, 1, "pending hydration cannot write stale component state");
  current.read.resolve(snapshot("current"));
  await current.pending;
  await oldTimer(); // Simulate an already-queued callback after clearTimeout.
  assert.equal(f.writes.length, 1, "old timer must not flush the current instance");
  f.writes[0].reject(new Error("old document context revoked"));
  await turn();
  assert.deepEqual(f.api.desktopState(), snapshot("current"));
  f.api.updateDesktopState({ sidebarCollapsed: true });
  const flushed = f.nativeFlush();
  assert.equal(f.writes.at(-1).instance, B);
  assert.deepEqual(f.writes.at(-1).state, { ...snapshot("current"), sidebarCollapsed: true });
  f.writes.at(-1).resolve();
  await flushed;
});

test("switching away and back fences the earlier read even when instance IDs match", async t => {
  const f = await fixture(t);
  const firstA = await f.begin(A);
  const middleB = await f.begin(B);
  const latestA = await f.begin(A);
  latestA.read.resolve(snapshot("latest A"));
  await latestA.pending;
  firstA.read.resolve(snapshot("stale A"));
  middleB.read.reject(new Error("stale B"));
  await Promise.all([firstA.pending, middleB.pending]);
  assert.deepEqual(f.api.desktopState(), snapshot("latest A"));
});

test("stale completion cannot clear a newer pending hydration or suppress its retry", async t => {
  const f = await fixture(t);
  const old = await f.begin(A);
  const current = await f.begin(B);
  old.read.reject(new Error("stale A"));
  await old.pending;
  const overlap = f.api.hydrateDesktopState(B);
  await turn();
  assert.equal(f.reads.length, 2);
  current.read.reject(new Error("B failed"));
  await Promise.all([current.pending, overlap]);
  const retry = await f.begin(B);
  assert.equal(f.reads.length, 3);
  retry.read.resolve(snapshot("retried B"));
  await retry.pending;
  const switched = await f.begin(C);
  switched.read.reject(new Error("C failed"));
  await switched.pending;
  assert.deepEqual(f.api.desktopState(), {});
});

test("current debounce, native flush and pagehide retain the existing preload API contract", async t => {
  const f = await fixture(t);
  await f.hydrate(A, snapshot("ready"));
  f.api.updateDesktopState({ sidebarCollapsed: true });
  f.api.updateDesktopState({ activeConversation: "edited" });
  assert.equal(f.timers.size, 1);
  const timer = [...f.timers.values()][0];
  const timed = timer();
  assert.equal(f.writes.length, 1);
  assert.equal(f.writes[0].instance, A);
  assert.deepEqual(f.writes[0].state, { ...snapshot("ready"), activeConversation: "edited", sidebarCollapsed: true });
  f.writes[0].resolve();
  await timed;
  const hiding = f.pagehide();
  f.writes.at(-1).resolve();
  await hiding;
  assert.equal(f.writes.length, 2);
  assert.equal(f.writes[1].instance, A);
});

test("web mode remains a no-op", async t => {
  const f = await fixture(t, { native: false });
  assert.equal(f.api.isDesktop, false);
  await f.api.hydrateDesktopState(A);
  f.api.updateDesktopState(snapshot("web"));
  await f.api.flushDesktopState();
  assert.deepEqual(f.api.desktopState(), {});
  assert.equal(f.reads.length + f.writes.length + f.timers.size, 0);
});
