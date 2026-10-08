import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { openUiModules } from "./ui-modules.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-multiplex-events-"));
const sources = [];
class FakeEventSource extends EventTarget {
  constructor(url) { super(); this.url = url; this.closed = false; sources.push(this); }
  close() { this.closed = true; }
  emit(type, data, id = "") { this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data), lastEventId: String(id) })); }
  open() { this.onopen?.(); }
  fail() { this.onerror?.(); }
}
globalThis.EventSource = FakeEventSource;
let ui;
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/App.tsx", "--ignoreConfig", "--target", "ES2022", "--jsx", "react-jsx",
    "--module", "ESNext", "--moduleResolution", "Bundler", "--outDir", out,
    "--skipLibCheck", "--types", "vite/client", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  // api.ts imports the i18n runtime, which only resolves through Vite.
  ui = await openUiModules();
  const { openConversationEvents, openWorkspaceEvents } = await ui.load("/src/api.ts");
  const events = [], workspaces = [], resets = [];
  const legacy = openConversationEvents("old", 42, (...args) => events.push(args), () => {});
  assert.equal(legacy.url, "/api/conversations/old/events?after=42", "legacy call must not request workspace multiplexing");
  legacy.close();
  const combined = openConversationEvents("chat", 42, (...args) => events.push(args), () => {}, {
    after: 3, onEvent: (...args) => workspaces.push(args), onReset: () => resets.push(true),
  });
  assert.equal(combined.url, "/api/conversations/chat/events?after=42&workspace_after=3");
  combined.emit("workspace", { scope: "config", revision: 4 }, 42);
  combined.emit("delta", { conversation_id: "chat", text: "hi" }, 43);
  assert.equal(workspaces[0][1], 4, "workspace cursor comes from revision, not inherited Last-Event-ID");
  assert.equal(events[0][2], 43, "chat cursor remains independent");
  combined.emit("workspace", { scope: "config", revision: "5" }, 43);
  combined.emit("workspace", { scope: "secrets", revision: 5 }, 43);
  combined.emit("workspace", null, 43);
  assert.equal(workspaces.length, 1, "reject malformed invalidations");
  combined.emit("workspace_reset", { revision: 0 }, 43);
  assert.equal(resets.length, 1);
  combined.close();
  const standalone = openWorkspaceEvents(4, (...args) => workspaces.push(args), () => {}, () => resets.push(true));
  standalone.emit("workspace", { scope: "bots", revision: 5 }, 4);
  assert.equal(workspaces.length, 1, "dedicated workspace SSE still validates its own event ID");
  standalone.emit("workspace", { scope: "bots", revision: 5 }, 5);
  standalone.emit("workspace_reset", { revision: 0 });
  assert.equal(workspaces.at(-1)[1], 5);
  assert.equal(resets.length, 2);
  standalone.close();

  // Exercise the actual compiled App connection callbacks, isolated from visual
  // components. This keeps delayed events/timers deterministic without claiming
  // to emulate the browser's HTTP connection pool.
  const app = await readFile(join(out, "App.js"), "utf8");
  function between(start, end) {
    const first = app.indexOf(start), last = app.indexOf(end, first);
    assert(first >= 0 && last > first, `locate App callback block: ${start}`);
    return app.slice(first, last);
  }
  const workspaceCode = between("const handleWorkspaceEvent = useCallback(", "useEffect(() => { void refreshIndex();");
  const conversationCode = between("const connectEvents = useCallback(", "useEffect(() => {");
  const configCallbacks = between("const applyConfig = useCallback(", "useEffect(() => { void refreshIndex();");
  const chatCallbacks = between("const updateConversationPreview = useCallback(", "useEffect(() => {");
  const memoSlots = [];
  let memoIndex = 0;
  const memo = (callback, deps) => {
    const index = memoIndex++;
    const previous = memoSlots[index];
    if (!previous || deps.some((value, i) => !Object.is(value, previous.deps[i]))) memoSlots[index] = { callback, deps };
    return memoSlots[index].callback;
  };
  const identityEnv = { useCallback: memo, configRef: { current: null }, setConfig() {}, setCodexStatusRefresh() {}, questions: { refresh() {} } };
  const renderCallbacks = new Function(...Object.keys(identityEnv), `${configCallbacks}\n${chatCallbacks}\nreturn {applyConfig,connectEvents,connectWorkspaceEvents,refreshWorkspaceOnConnect,handleWorkspaceEvent};`);
  const initialCallbacks = renderCallbacks(...Object.values(identityEnv));
  for (let render = 0; render < 20; render++) {
    initialCallbacks.applyConfig({ default_model: `model-${render}`, model_configured: render % 2 === 0, provider: "test" });
    memoIndex = 0;
    const next = renderCallbacks(...Object.values(identityEnv));
    for (const key of Object.keys(initialCallbacks)) assert.equal(next[key], initialCallbacks[key], `config refresh must not change ${key} identity/restart the snapshot effect`);
  }
  const refs = Object.fromEntries([
    ["activeIdRef", null], ["eventCursor", 42], ["eventSource", null], ["reconnectTimer", undefined],
    ["eventConnectTimer", undefined], ["reconnectAttempt", 0], ["workspaceEventCursor", 3],
    ["workspaceEventSource", null], ["workspaceReconnectTimer", undefined], ["workspaceConnectTimer", undefined],
    ["workspaceReconnectAttempt", 0], ["indexInitialized", true],
  ].map(([key, value]) => [key, { current: value }]));
  const timers = new Map();
  let timerID = 0, extensionRefresh = 0;
  const refreshes = [], chats = [], connectionStates = [];
  const env = {
    ...refs, openConversationEvents, openWorkspaceEvents, useCallback: callback => callback,
    refreshIndex: () => refreshes.push("index"), refreshConversations: () => refreshes.push("conversations"),
    refreshConfig: () => refreshes.push("config"), setExtensionRefresh: update => { extensionRefresh = update(extensionRefresh); }, setScheduleRefresh() {},
    setStreamConnected: value => connectionStates.push(value),
    handleEvent: (...args) => { chats.push(args); refs.eventCursor.current = args[3]; },
    questions: { refresh() {} },
    window: {
      setTimeout: (callback, delay) => { timers.set(++timerID, { callback, delay }); return timerID; },
      clearTimeout: id => timers.delete(id),
    },
  };
  const create = new Function(...Object.keys(env), `${workspaceCode}\n${conversationCode}\nreturn {connectWorkspaceEvents,connectEvents};`);
  const connect = create(...Object.values(env));
  connect.connectWorkspaceEvents();
  const emptySource = refs.workspaceEventSource.current;
  assert(emptySource && !emptySource.closed);
  emptySource.open();
  assert.equal(timers.size, 0, "onopen clears its connection timeout");
  emptySource.emit("workspace", { scope: "bots", revision: 4 }, 4);
  assert.equal(refs.workspaceEventCursor.current, 4);
  // Mirror the empty-workspace effect's cleanup as a conversation is selected.
  refs.activeIdRef.current = "chat-a";
  emptySource.close(); refs.workspaceEventSource.current = null;
  connect.connectEvents("chat-a");
  const first = refs.eventSource.current;
  assert(first.url.endsWith("after=42&workspace_after=4"));
  connect.connectWorkspaceEvents();
  assert.equal(refs.workspaceEventSource.current, null, "active conversations must not open a second workspace SSE");
  emptySource.emit("workspace", { scope: "config", revision: 5 }, 5);
  assert.equal(refs.workspaceEventCursor.current, 4, "late closed-source revision must remain replayable");
  first.open();
  assert.equal(extensionRefresh, 0, "ordinary opens do not reset extensions");
  assert.equal(timers.size, 0);
  first.emit("workspace", { scope: "config", revision: 5 }, 42);
  first.emit("delta", { conversation_id: "chat-a", text: "a" }, 43);
  assert.equal(refs.workspaceEventCursor.current, 5);
  assert.equal(refs.eventCursor.current, 43);
  assert.equal(extensionRefresh, 1);
  first.fail();
  const retry = timers.get(refs.reconnectTimer.current);
  assert.equal(retry.delay, 1000);
  retry.callback();
  const second = refs.eventSource.current;
  assert(second !== first && first.closed);
  assert(second.url.endsWith("after=43&workspace_after=5"), "reconnect transmits both latest cursors");
  const secondTimer = refs.eventConnectTimer.current;
  retry.callback();
  assert.equal(refs.eventSource.current, second, "late reconnect timer must not replace a newer source");
  first.fail(); first.open();
  first.emit("workspace", { scope: "config", revision: 1 }, 43);
  assert.equal(refs.eventSource.current, second, "late old error must not clear a new same-conversation source");
  assert.equal(refs.workspaceEventCursor.current, 5);
  assert(timers.has(secondTimer), "old onopen must not clear the new source's watchdog");
  second.open();
  refs.activeIdRef.current = "chat-b";
  second.emit("workspace", { scope: "bots", revision: 6 }, 43);
  assert.equal(refs.workspaceEventCursor.current, 5, "switch gap must not consume an unhandled revision");
  refs.eventCursor.current = 100;
  connect.connectEvents("chat-b");
  const third = refs.eventSource.current;
  assert(second.closed);
  assert(third.url.endsWith("after=100&workspace_after=5"));
  third.open();
  third.emit("workspace", { scope: "bots", revision: 6 }, 100);
  assert.equal(refs.workspaceEventCursor.current, 6);
  third.emit("workspace_reset", { revision: 0 }, 100);
  assert.equal(refs.workspaceEventCursor.current, 0);
  third.emit("workspace", { scope: "config", revision: 1 }, 100);
  assert.equal(refs.workspaceEventCursor.current, 1, "first mutation after an empty DB restore must not be deduplicated");
  assert.equal(refs.eventCursor.current, 100);
  third.fail();
  timers.get(refs.reconnectTimer.current).callback();
  const stalled = refs.eventSource.current;
  const watchdog = timers.get(refs.eventConnectTimer.current);
  assert.equal(watchdog.delay, 10000);
  watchdog.callback();
  assert(stalled.closed && refs.eventSource.current === null, "connect timeout must close the waiting connection before retry");
  assert.equal(sources.filter(source => !source.closed).length, 0);
  assert.equal(chats.length, 1, "workspace notifications never enter the conversation reducer");
  assert(refreshes.includes("config") && refreshes.includes("conversations"));

  const retryMarker = app.indexOf("let snapshotRetryTimer;");
  const effectStart = app.lastIndexOf("useEffect(() => {", retryMarker);
  const effectEndMarker = "}, [activeId, connectEvents]);";
  const effectEnd = app.indexOf(effectEndMarker, retryMarker);
  assert(retryMarker > 0 && effectStart >= 0 && effectEnd > retryMarker);
  const snapshotCode = app.slice(effectStart, effectEnd + effectEndMarker.length);
  const pendingRead = () => {
    let resolve;
    const promise = new Promise(done => { resolve = done; });
    return { promise, resolve };
  };
  async function settle() { for (let i = 0; i < 20; i++) await Promise.resolve(); }
  function snapshotHarness(mode) {
    const state = {}, pendingTimers = new Map(), connections = [];
    const slow = pendingRead();
    let readCount = 0, nextTimer = 0, cleanup;
    const page = { messages: [{ id: "message", seq: 12 }], drafts: [], has_more: false, event_cursor: 77 };
    const snapshotRefs = Object.fromEntries(["eventCursor", "snapshotEventCursor", "snapshotMaxSeq", "snapshotCursorKnown", "eventSource", "reconnectTimer", "eventConnectTimer"].map(key => [key, { current: undefined }]));
    const setters = Object.fromEntries(["LoadingMessages", "LoadedId", "LoadingOlder", "HistoryError", "Messages", "Drafts", "Runs", "ToolActivities", "ToolDetailActivities", "ToolSummaries", "ToolDetailState", "ToolSummaryState", "Memories", "HasMore", "Error", "SnapshotError"].map(key => [`set${key}`, value => {
      state[key] = typeof value === "function" ? value(state[key]) : value;
    }]));
    const input = {
      ...snapshotRefs, ...setters, activeId: "chat-a", errorText: error => error.message, arrivalIDs: { current: new Set() },
      toolActivityKey: item => item.id,
      useEffect: effect => { cleanup = effect(); },
      connectEvents: id => { connections.push({ id, cursor: snapshotRefs.eventCursor.current, loadedId: state.LoadedId }); },
      api: {
        messages: () => {
          readCount++;
          if (mode === "pending") return slow.promise;
          if (mode === "fail" || readCount === 1) return Promise.reject(new Error("snapshot unavailable"));
          return Promise.resolve(page);
        },
        runs: () => mode === "recover" && readCount === 1 ? slow.promise : Promise.resolve({ runs: [] }),
        memories: async () => ({ memories: [] }), toolActivities: async () => ({ activities: [] }),
      },
      window: {
        setTimeout: (callback, delay) => { pendingTimers.set(++nextTimer, { callback, delay }); return nextTimer; },
        clearTimeout: id => pendingTimers.delete(id),
      },
    };
    new Function(...Object.keys(input), snapshotCode)(...Object.values(input));
    const fire = () => {
      assert.equal(pendingTimers.size, 1);
      const [id, timer] = [...pendingTimers][0];
      pendingTimers.delete(id);
      timer.callback();
      return timer.delay;
    };
    return { state, pendingTimers, connections, slow, snapshotRefs, page, fire, cleanup: () => cleanup(), reads: () => readCount };
  }
  const recovered = snapshotHarness("recover");
  await settle();
  assert.equal(recovered.state.SnapshotError, "snapshot unavailable");
  assert.equal(recovered.pendingTimers.size, 0, "wait for sibling reads before scheduling the next attempt");
  assert.equal(recovered.connections.length, 0, "never consume chat events before snapshot success");
  recovered.slow.resolve({ runs: [] });
  await settle();
  assert.equal(recovered.fire(), 1000);
  await settle();
  assert.deepEqual(recovered.connections, [{ id: "chat-a", cursor: 77, loadedId: "chat-a" }]);
  assert.equal(recovered.state.SnapshotError, "");
  assert.equal(recovered.state.LoadingMessages, false);
  recovered.cleanup();
  const cancelled = snapshotHarness("fail");
  await settle();
  const lateTimer = [...cancelled.pendingTimers.values()][0];
  cancelled.cleanup();
  assert.equal(cancelled.pendingTimers.size, 0, "switch/unmount clears the scheduled snapshot retry");
  lateTimer.callback();
  await settle();
  assert.equal(cancelled.reads(), 1, "even an already queued old timer cannot retry after cleanup");
  assert.equal(cancelled.connections.length, 0);
  const lateResult = snapshotHarness("pending");
  lateResult.cleanup();
  lateResult.snapshotRefs.eventCursor.current = 999;
  lateResult.slow.resolve(lateResult.page);
  await settle();
  assert.equal(lateResult.snapshotRefs.eventCursor.current, 999, "late old snapshot must not overwrite the next conversation's cursor");
  assert.equal(lateResult.connections.length, 0);
  const exhausted = snapshotHarness("fail");
  await settle();
  for (const delay of [1000, 2000, 4000]) {
    assert.equal(exhausted.fire(), delay);
    await settle();
  }
  assert.equal(exhausted.reads(), 4, "snapshot retries have a finite three-retry budget");
  assert.equal(exhausted.pendingTimers.size, 0);
  assert.equal(exhausted.connections.length, 0);
  exhausted.state.Error = ""; // An unrelated config refresh clears general errors.
  assert.equal(exhausted.state.SnapshotError, "snapshot unavailable", "persistent snapshot errors remain visible independently");
  exhausted.cleanup();
  console.log("multiplex SSE checks: PASS (independent cursors, source isolation, replay, timeout/reset, serial snapshot recovery, retry cancellation and finite budget)");
} finally {
  delete globalThis.EventSource;
  await ui?.close();
  await rm(out, { recursive: true, force: true });
}
