import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-remote-control-"));
const originalInterval = globalThis.setInterval;
const originalClearInterval = globalThis.clearInterval;
const heartbeatCallbacks = new Map();
let intervalID = 0;
globalThis.setInterval = callback => { heartbeatCallbacks.set(++intervalID,callback); return intervalID; };
globalThis.clearInterval = id => heartbeatCallbacks.delete(id);
const wait = (ms = 0) => new Promise(resolve => setTimeout(resolve, ms));

class FakeTarget {
  listeners = new Map();
  addEventListener(type, listener) { this.listeners.set(type, listener); }
  removeEventListener(type, listener) { if (this.listeners.get(type) === listener) this.listeners.delete(type); }
  dispatch(type, event = {}) { this.listeners.get(type)?.(event); }
}

class FakeSurface extends FakeTarget {
  captured = new Set();
  media;
  constructor(media) { super(); this.media = media; }
  querySelector() { return this.media; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 640, height: 400 }; }
  setPointerCapture(id) { this.captured.add(id); }
  hasPointerCapture(id) { return this.captured.has(id); }
  releasePointerCapture(id) { this.captured.delete(id); }
}

class FakeImage {
  naturalWidth = 1280;
  naturalHeight = 800;
  getBoundingClientRect() { return { left: 0, top: 0, width: 640, height: 400 }; }
}

try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/RemoteDesktopControl.tsx", "src/desktop.ts", "--ignoreConfig", "--target", "ES2022",
    "--jsx", "react-jsx", "--module", "ES2022", "--moduleResolution", "Bundler",
    "--outDir", out, "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });

  const requests = [];
  let acquireResult = Promise.resolve({ ok: true, result: { control_id: "control-1" } });
  let renewResult = { ok:true,result:{} };
  let clipboardResult = { text: "copied from Linux" };
  const harness = {
    jsx: (type, props) => ({ type, props }),
    jsxs: (type, props) => ({ type, props }),
    useState: (initial) => {
      const index = harness.cursor++;
      harness.slots[index] ??= { value: initial };
      return [harness.slots[index].value, value => {
        const next = typeof value === "function" ? value(harness.slots[index].value) : value;
        if (next !== harness.slots[index].value) { harness.slots[index].value = next; harness.dirty = true; }
      }];
    },
    useRef: (initial) => {
      const index = harness.cursor++;
      return harness.slots[index] ??= { current: initial };
    },
    useEffect: (effect, deps) => {
      const index = harness.cursor++;
      const previous = harness.slots[index];
      const changed = !previous || deps.some((value, offset) => value !== previous.deps[offset]);
      if (changed) {
        harness.slots[index] = { deps, cleanup: undefined };
        harness.effects.push(() => {
          previous?.cleanup?.();
          harness.slots[index].cleanup = effect();
        });
      }
    },
    slots: [], cursor: 0, effects: [], dirty: true,
    request: async (path, init) => {
      if (harness.ownershipRequest && !path.includes("/actions")) return harness.ownershipRequest(path, init);
      const body = JSON.parse(String(init.body));
      requests.push({ action: body.action, args: body.args });
      if (body.action === "desktop.control.acquire") return acquireResult;
      if (body.action === "desktop.control.renew") return renewResult;
      if (body.action === "desktop.control.clipboard.read") return { ok: true, result: clipboardResult };
      return { ok: true, result: { accepted: true } };
    },
  };
  globalThis.__remoteHarness = harness;
  globalThis.HTMLVideoElement = class {};
  globalThis.HTMLImageElement = FakeImage;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async () => {} } } });
  globalThis.document = new FakeTarget();
  globalThis.document.hidden = false;
  globalThis.window = new FakeTarget();
  globalThis.window.setTimeout = setTimeout;
  globalThis.window.clearTimeout = clearTimeout;

  let source = await readFile(join(out, "RemoteDesktopControl.js"), "utf8");
  source = source
    .replace(/^import .* from "\.\/GazeAvatar";$/m, "const GazeAvatar = () => null;")
    .replace(/^import .* from "\.\/desktop";$/m, "const isDesktop = false;")
    .replace(/^import .* from "react\/jsx-runtime";$/m, "const { jsx, jsxs } = globalThis.__remoteHarness; const _jsx = jsx; const _jsxs = jsxs;")
    .replace(/^import .* from "react";$/m, "const { useEffect, useRef, useState } = globalThis.__remoteHarness;")
    .replace(/^import .* from "\.\/api";$/m, "const { request } = globalThis.__remoteHarness; class ApiError extends Error {}");
  const harnessModule = join(out, "RemoteDesktopControl.harness.mjs");
  await writeFile(harnessModule, source);
  const { RemoteDesktopControl } = await import(pathToFileURL(harnessModule).href);

  const render = (props) => {
    harness.cursor = 0;
    harness.effects = [];
    harness.dirty = false;
    const tree = RemoteDesktopControl(props);
    for (const effect of harness.effects) effect();
    return tree;
  };
  const all = node => !node || typeof node !== "object" ? [] : [node, ...[node.props?.children].flat(Infinity).filter(Boolean).flatMap(all)];
  const mount = (expanded = true) => {
    // This tiny runner models one mounted component at a time. Tear down the
    // prior instance before reusing its hook slots so [botId] effects run just
    // as they do after a real unmount/remount.
    if (harness.mounted) {
      for (const slot of harness.slots) slot?.cleanup?.();
      harness.slots = [];
    }
    harness.mounted = true;
    const media = new FakeImage();
    const surface = new FakeSurface(media);
    const input = { value: "", style: {}, focusCount: 0, focus() { this.focusCount++; }, blur() {} };
    let tree;
    const props = { botId: "bot-a", enabled: true, expanded, onExpand: () => { props.expanded = true; }, onControlChange: owned => { props.owned = owned; }, controlRef: { current: null }, children: null };
    const rerender = () => {
      tree = render(props);
      const nodes = all(tree);
      const surfaceNode = nodes.find(node => node.type === "div" && node.props?.className === "remote-desktop-surface");
      const inputNode = nodes.find(node => node.type === "textarea" && node.props?.className === "remote-keyboard-input");
      surfaceNode.props.ref.current = surface;
      inputNode.props.ref.current = input;
      return { surfaceNode, inputNode };
    };
    rerender();
    return { props, rerender, getTree: () => tree, surface, input };
  };
  const event = (currentTarget, extra = {}) => ({ currentTarget, preventDefault() { this.prevented = true; }, stopPropagation() {}, ...extra });
  const flushRenders = async () => {
    for (let i = 0; i < 8 && harness.dirty; i++) { harness.dirty = false; current.rerender(); await Promise.resolve(); }
  };

  let current = mount(false);
  const preview = current.rerender();
  const previewContext = event(current.surface);
  preview.surfaceNode.props.onContextMenu(previewContext);
  assert(previewContext.prevented, "preview suppresses only the host context menu before ownership");
  preview.surfaceNode.props.onPointerDown(event(current.surface, { pointerId: 1, clientX: 320, clientY: 200, button: 0 }));
  assert.equal(requests.length, 0, "preview click only enlarges, without acquiring or sending remote input");
  current.props.expanded = true;
  current.rerender().surfaceNode.props.onPointerDown(event(current.surface, { pointerId: 1, pointerType: "mouse", clientX: 320, clientY: 200, button: 0 }));
  assert(!requests.some(request => request.action === "desktop.control.input"), "preview first click must not send a mouse down");
  await wait();
  await flushRenders();
  assert.equal(current.props.owned, true, "acquire callback reports ownership");

  const surfaceNode = current.rerender().surfaceNode;
  const ownedContext = event(current.surface);
  surfaceNode.props.onContextMenu(ownedContext);
  assert(ownedContext.prevented, "owned screen suppresses the host context menu");
  const beforeRightClick = requests.length;
  surfaceNode.props.onPointerDown(event(current.surface, { pointerId: 90, clientX: 100, clientY: 100, button: 2 }));
  surfaceNode.props.onPointerUp(event(current.surface, { pointerId: 90, clientX: 100, clientY: 100, button: 2 }));
  await wait(30);
  assert.deepEqual(requests.slice(beforeRightClick).flatMap(request => request.action === "desktop.control.input" ? request.args.events : []).map(({type, button}) => ({type, button})), [{type:"down",button:3},{type:"up",button:3}], "guest right click retains down/up button 3");
  const beforeLeftClick = requests.length;
  surfaceNode.props.onPointerDown(event(current.surface, { pointerId: 2, clientX: 100, clientY: 100, button: 0 }));
  surfaceNode.props.onPointerMove(event(current.surface, { pointerId: 2, clientX: 110, clientY: 110 }));
  surfaceNode.props.onPointerMove(event(current.surface, { pointerId: 2, clientX: 120, clientY: 120 }));
  surfaceNode.props.onPointerUp(event(current.surface, { pointerId: 2, clientX: 120, clientY: 120, button: 0 }));
  await wait(30);
  const inputBatches = requests.slice(beforeLeftClick).filter(request => request.action === "desktop.control.input");
  assert.equal(inputBatches.length, 1, "down/move/up should stay in one ordered batch");
  assert.deepEqual(inputBatches[0].args.events.map(event => event.type), ["down", "move", "up"], "move coalescing must not cross down/up edges");
  assert.deepEqual(inputBatches[0].args.events[1], { type: "move", x: 240, y: 240 }, "adjacent moves keep the latest coordinate");

  const beforeIME = requests.length;
  const inputNode = current.rerender().inputNode;
  inputNode.props.onCompositionStart();
  current.input.value = "拼音";
  inputNode.props.onInput(event(current.input, { nativeEvent: { isComposing: true } }));
  inputNode.props.onCompositionEnd(event(current.input, { data: "拼音" }));
  current.input.value = "拼音";
  inputNode.props.onInput(event(current.input, { nativeEvent: { isComposing: false } }));
  await wait(30);
  const imeEvents = requests.slice(beforeIME).flatMap(request => request.action === "desktop.control.input" ? request.args.events : []).filter(event => event.type === "text");
  assert.deepEqual(imeEvents.map(event => event.text), ["拼音"], "IME commit must be delivered exactly once");

  const beforeTextMatrix = requests.length;
  const insert = (text, inputType = "insertText") => {
    current.input.value = text;
    inputNode.props.onInput(event(current.input, { nativeEvent: { inputType, isComposing: false } }));
  };
  // Delayed trailing IME commit must not rely on a zero-delay timer.
  inputNode.props.onCompositionStart();
  current.input.value = "，。！？";
  inputNode.props.onCompositionEnd(event(current.input, { data: current.input.value }));
  await wait(5);
  insert("，。！？", "insertFromComposition");
  insert(",.!? abc");
  // The same committed text can be deliberately typed again.
  inputNode.props.onCompositionStart();
  current.input.value = "文";
  inputNode.props.onInput(event(current.input, { nativeEvent: { isComposing: true, inputType: "insertCompositionText" } }));
  inputNode.props.onCompositionEnd(event(current.input, { data: "文" }));
  inputNode.props.onKeyDown(event(current.input, { key: "w", code: "KeyW", nativeEvent: {} }));
  insert("文", "insertText");
  const space = event(current.input, { key: " ", code: "Space", nativeEvent: {} });
  inputNode.props.onKeyDown(space);
  assert(!space.prevented, "ordinary space uses local committed text");
  insert(" ");
  inputNode.props.onPaste(event(current.input, { clipboardData: { getData: () => "line one\nline two" } }));
  for (const [key, code] of [["Enter", "Enter"], ["Backspace", "Backspace"]]) {
    inputNode.props.onKeyDown(event(current.input, { key, code, nativeEvent: {} }));
    inputNode.props.onKeyUp(event(current.input, { key, code }));
  }
  inputNode.props.onKeyDown(event(current.input, { key: "Enter", code: "Enter", nativeEvent: { keyCode: 229 } }));
  inputNode.props.onKeyDown(event(current.input, { key: "Shift", code: "ShiftLeft", nativeEvent: {} }));
  inputNode.props.onBlur();
  await wait(30);
  const matrix = requests.slice(beforeTextMatrix).flatMap(r => r.action === "desktop.control.input" ? r.args.events : []);
  assert.equal(matrix.filter(e => e.type === "text").map(e => e.text).join(""), "，。！？,.!? abc文文 line one\nline two");
  assert.deepEqual(matrix.filter(e => e.type === "keydown").map(e => e.key), ["Return", "BackSpace", "Shift_L"]);
  assert.equal(matrix.at(-1).type, "reset", "blur releases all guest modifiers");

  const beforeCompositionOrders = requests.length;
  for (const trailingType of ["insertFromComposition", "insertCompositionText", "insertText"]) {
    inputNode.props.onCompositionStart();
    current.input.value = "同";
    // Safari-like final input can arrive before compositionend.
    inputNode.props.onInput(event(current.input, { nativeEvent: { inputType: trailingType, isComposing: false } }));
    inputNode.props.onCompositionEnd(event(current.input, { data: "同" }));
    await wait(2);
    insert("同", trailingType);
    inputNode.props.onKeyDown(event(current.input, { key: "t", code: "KeyT", nativeEvent: {} }));
    insert("同", "insertText");
  }
  await wait(30);
  assert.equal(requests.slice(beforeCompositionOrders).flatMap(r => r.action === "desktop.control.input" ? r.args.events : []).filter(e => e.type === "text").map(e => e.text).join(""), "同同同同同同", "each composition has one commit, followed by an independent identical local edit");

  const beforeChangedKeyUp = requests.length;
  inputNode.props.onKeyDown(event(current.input,{key:"A",code:"KeyA",ctrlKey:true,nativeEvent:{}}));
  inputNode.props.onKeyUp(event(current.input,{key:"a",code:"KeyA"}));
  await wait(30);
  const changedKeyEvents = requests.slice(beforeChangedKeyUp).flatMap(r=>r.action === "desktop.control.input" ? r.args.events : []);
  assert.deepEqual(changedKeyEvents.map(e=>e.key),["A","A"],"keyup releases the originally sent key even after local modifier changes");

  const beforeClipboard = requests.length;
  inputNode.props.onKeyDown(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  await wait(50);
  const clipboardRequests = requests.slice(beforeClipboard);
  const clipboardEvents = clipboardRequests.flatMap(request => request.action === "desktop.control.input" ? request.args.events : []);
  assert.deepEqual(clipboardEvents.map(event => [event.type, event.key]), [["keydown", "Control_L"], ["keydown", "c"], ["keyup", "c"], ["keyup", "Control_L"]], "Ctrl+C must send the remote shortcut in order");
  assert(!clipboardEvents.some(event => event.type === "clipboard-barrier"), "clipboard barrier is local queue state, never a guest input event");
  assert(clipboardRequests.findIndex(request => request.action === "desktop.control.clipboard.read") > clipboardRequests.findIndex(request => request.action === "desktop.control.input"), "clipboard read must follow the flushed shortcut");
  assert(clipboardRequests.some(request => request.action === "desktop.control.clipboard.read"), "copy must read the remote clipboard after input flush");

  // A clipboard barrier that reports no change must be consumed locally. It
  // must neither create a fallback dialog nor leak a synthetic input event.
  clipboardResult = { changed: false };
  const beforeUnchangedCopy = requests.length;
  inputNode.props.onKeyDown(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  await wait(50);
  await flushRenders();
  const unchangedCopyRequests = requests.slice(beforeUnchangedCopy);
  assert.equal(unchangedCopyRequests.filter(request => request.action === "desktop.control.clipboard.read").length, 1, "changed:false still consumes the clipboard barrier");
  assert(!all(current.getTree()).some(node => node.props?.className === "remote-copy-fallback"), "changed:false must not show a clipboard fallback");
  clipboardResult = { text: "copied from Linux" };

  const closeCopyFallback = async () => {
    await flushRenders();
    const fallback = all(current.getTree()).find(node => node.props?.className === "remote-copy-fallback");
    assert(fallback, "clipboard fallback must be rendered when browser clipboard is unavailable");
    const fallbackText = all(fallback).find(node => node.type === "textarea" && node.props?.["aria-label"] === "从远程桌面复制的文字");
    assert.equal(fallbackText?.props?.value, "copied from Linux", "clipboard fallback must preserve the remote text");
    assert.equal(current.props.owned, true, "clipboard fallback must keep desktop control ownership");
    all(fallback).find(node => node.type === "button")?.props?.onClick();
    await flushRenders();
  };

  // The formal HTTP entry can lack navigator.clipboard entirely.
  const browserClipboard = navigator.clipboard;
  navigator.clipboard = undefined;
  inputNode.props.onKeyDown(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  await wait(50);
  await closeCopyFallback();

  // Permission policy can expose the API while rejecting the write.
  navigator.clipboard = { writeText: async () => { throw new Error("clipboard denied"); } };
  inputNode.props.onKeyDown(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  await wait(50);
  await closeCopyFallback();
  navigator.clipboard = browserClipboard;

  // Releasing and reacquiring must switch all subsequent input to the new
  // control id; an old queued session must never be reused.
  const oldControlRequests = requests.length;
  await current.props.controlRef.current.release();
  current.rerender();
  acquireResult = Promise.resolve({ ok: true, result: { control_id: "control-2" } });
  current.rerender().surfaceNode.props.onPointerDown(event(current.surface, { pointerId: 5, clientX: 320, clientY: 200, button: 0 }));
  await wait(20);
  await flushRenders();
  const reacquiredSurface = current.rerender().surfaceNode;
  reacquiredSurface.props.onPointerDown(event(current.surface, { pointerId: 6, clientX: 100, clientY: 100, button: 0 }));
  await wait(30);
  const reacquiredInputs = requests.slice(oldControlRequests).filter(request => request.action === "desktop.control.input");
  assert(reacquiredInputs.length > 0, "reacquired control must accept input");
  assert(reacquiredInputs.every(request => request.args.control_id === "control-2"), "reacquired input must not use the released session");

  // Holding Control across copy and the next shortcut must preserve the
  // modifier. The copy path must not synthesize an early Control-up, and the
  // following key must remain behind the clipboard barrier.
  const beforeHeldModifier = requests.length;
  const heldInput = current.rerender().inputNode;
  heldInput.props.onKeyDown(event(current.input, { key: "Control", code: "ControlLeft", ctrlKey: false, metaKey: false, nativeEvent: { isComposing: false } }));
  heldInput.props.onKeyDown(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  heldInput.props.onKeyDown(event(current.input, { key: "a", code: "KeyA", ctrlKey: true, metaKey: false, nativeEvent: { isComposing: false } }));
  heldInput.props.onKeyUp(event(current.input, { key: "c", code: "KeyC", ctrlKey: true, metaKey: false }));
  heldInput.props.onKeyUp(event(current.input, { key: "a", code: "KeyA", ctrlKey: true, metaKey: false }));
  heldInput.props.onKeyUp(event(current.input, { key: "Control", code: "ControlLeft", ctrlKey: false, metaKey: false }));
  await wait(70);
  const heldRequests = requests.slice(beforeHeldModifier);
  const heldInputEvents = heldRequests.flatMap(request => request.action === "desktop.control.input" ? request.args.events : []);
  assert.deepEqual(heldInputEvents.map(event => [event.type, event.key]), [
    ["keydown", "Control_L"], ["keydown", "c"], ["keyup", "c"],
    ["keydown", "a"], ["keyup", "a"], ["keyup", "Control_L"],
  ], "copy must preserve a held modifier through the next shortcut");
  const heldClipboardIndex = heldRequests.findIndex(request => request.action === "desktop.control.clipboard.read");
  const heldInputBatches = heldRequests.filter(request => request.action === "desktop.control.input");
  assert.equal(heldInputBatches.length, 2, "held modifier copy should split input at the clipboard barrier");
  assert(heldClipboardIndex > 0 && heldClipboardIndex < heldRequests.length - 1, "held modifier clipboard read must separate copy from the next key");

  // A late acquire response after release must be cleaned up without granting ownership.
  const late = mount(true);
  let resolveAcquire;
  acquireResult = new Promise(resolve => { resolveAcquire = resolve; });
  const lateSurface = late.rerender().surfaceNode;
  lateSurface.props.onPointerDown(event(late.surface, { pointerId: 3, clientX: 320, clientY: 200, button: 0 }));
  await Promise.resolve();
  await late.props.controlRef.current.release();
  resolveAcquire({ ok: true, result: { control_id: "late-control" } });
  await wait(20);
  assert.equal(late.props.owned, false, "released pending acquire must not become owned");
  assert(requests.some(request => request.action === "desktop.control.release" && request.args.control_id === "late-control"), "late acquire must release the newly issued lease");

  // Touch preview and viewing stage never acquire implicitly or summon the keyboard.
  acquireResult = Promise.resolve({ ok: true, result: { control_id: "touch-control" } });
  const touch = mount(false);
  await wait();
  const touchStart = requests.length;
  touch.rerender().surfaceNode.props.onPointerDown(event(touch.surface, { pointerId: 9, pointerType: "touch", clientX: 100, clientY: 100, button: 0 }));
  touch.rerender().surfaceNode.props.onPointerDown(event(touch.surface, { pointerId: 10, pointerType: "touch", clientX: 100, clientY: 100, button: 0 }));
  assert.equal(requests.length, touchStart, "touch preview and enlarged viewing surface send no control requests");
  touch.props.expanded = true;
  touch.rerender();
  touch.rerender().surfaceNode.props.onPointerDown(event(touch.surface, {pointerId: 11, pointerType: "touch", clientX:100, clientY:100, button:0}));
  await wait();
  touch.rerender();
  assert.equal(touch.input.focusCount, 0, "explicit touch takeover does not automatically open the keyboard");
  const keyboard = all(touch.getTree()).find(node => node.props?.["aria-label"] === "打开远程键盘");
  keyboard.props.onClick();
  assert.equal(touch.input.focusCount, 1, "keyboard is focused only through its explicit button");
  const beforeUp = requests.length;
  touch.rerender().surfaceNode.props.onPointerUp(event(touch.surface, { pointerId: 10, pointerType: "touch", clientX: 100, clientY: 100, button: 0 }));
  await wait(25);
  assert.equal(requests.length, beforeUp, "the takeover tap cannot leak an unmatched mouse-up");

  // Human requests bind the displayed owner and never cancel its run.
  const takeover = mount(true);
  takeover.props.blocked = true;
  takeover.props.takeoverRun = {id: "current-bot-run", name: "Alpha"};
  const beforeTakeover = requests.length;
  takeover.rerender().surfaceNode.props.onPointerDown(event(takeover.surface, {pointerId:12, pointerType:"mouse",clientX:100,clientY:100,button:0}));
  await wait(20);
  assert.deepEqual(requests.slice(beforeTakeover).map(r=>[r.action,r.args]), [["desktop.control.acquire",{expected_owner:"current-bot-run"}]], "first takeover click requests only a safe lease transfer");
  const foreignTab = mount(true);
  foreignTab.props.blocked = true;
  const beforeForeign = requests.length;
  foreignTab.rerender().surfaceNode.props.onPointerDown(event(foreignTab.surface, {pointerId:13, pointerType:"mouse",clientX:100,clientY:100,button:0}));
  await wait(20);
  assert.equal(requests.length,beforeForeign,"another human tab cannot be displaced");

  // Synthetic local clock covers idle handoff barriers and rejects hover as activity.
  const realNow = Date.now;
  let syntheticNow = realNow();
  Date.now = () => syntheticNow;
  try {
    acquireResult = Promise.resolve({ok:true,result:{control_id:"activity-control"}});
    current = mount(true);
    current.rerender().surfaceNode.props.onPointerDown(event(current.surface,{pointerId:20,pointerType:"mouse",clientX:100,clientY:100,button:0}));
    await wait(); await flushRenders();
    syntheticNow += 20000;
    const heartbeat = () => [...heartbeatCallbacks.values()].at(-1)();
    const lastRenew = () => requests.filter(r=>r.action === "desktop.control.renew").at(-1);
    let activityInput = current.rerender().inputNode;
    activityInput.props.onCompositionStart(); await wait(30); heartbeat(); await wait();
    assert.equal(lastRenew().args.interacting,true,"composition blocks idle handoff");
    activityInput.props.onCompositionEnd(event(current.input,{data:""})); syntheticNow += 20000;
    activityInput.props.onKeyDown(event(current.input,{key:"Shift",code:"ShiftLeft",nativeEvent:{}})); await wait(30);syntheticNow += 20000;heartbeat();await wait();
    assert.equal(lastRenew().args.interacting,true,"held modifier blocks idle handoff");
    activityInput.props.onKeyUp(event(current.input,{key:"Shift",code:"ShiftLeft"}));await wait(30);syntheticNow += 20000;
    const activitySurface = current.rerender().surfaceNode;
    activitySurface.props.onPointerDown(event(current.surface,{pointerId:21,pointerType:"mouse",clientX:100,clientY:100,button:0}));await wait(30);syntheticNow += 20000;heartbeat();await wait();
    assert.equal(lastRenew().args.interacting,true,"drag blocks idle handoff");
    activitySurface.props.onPointerUp(event(current.surface,{pointerId:21,pointerType:"mouse",clientX:100,clientY:100,button:0}));await wait(30);syntheticNow += 20000;
    activitySurface.props.onPointerMove(event(current.surface,{pointerId:22,pointerType:"mouse",clientX:110,clientY:100}));await wait(30);heartbeat();await wait();
    assert.equal(lastRenew().args.interacting,false,"hover does not renew human activity");
    renewResult = {ok:true,result:{released:true,reason:"bot_waiting"}};heartbeat();await wait();await flushRenders();
    assert.equal(current.props.owned,false,"server idle handoff drops local lease");
    activityInput.props.onInput(event(current.input,{nativeEvent:{inputType:"insertText"}}));
    renewResult = {ok:true,result:{}};
  } finally {Date.now = realNow;}

  // Clean up the live lease as an unmount would.
  acquireResult = Promise.resolve({ ok: true, result: { control_id: "cleanup-control" } });
  const cleanup = mount(true);
  cleanup.rerender().surfaceNode.props.onPointerDown(event(cleanup.surface, { pointerId: 4, clientX: 320, clientY: 200, button: 0 }));
  await wait(10);
  for (const slot of harness.slots) slot?.cleanup?.();
  await wait(10);
  assert(requests.some(request => request.action === "desktop.control.release" && request.args.control_id === "cleanup-control"), "unmount must release the active lease");
  console.log("remote desktop control checks: PASS (preview click, pointer edge ordering, IME once, clipboard barrier, held modifier, safe Bot handoff without cancellation, cross-tab protection, lease cleanup)");
} finally {
  globalThis.setInterval = originalInterval;
  globalThis.clearInterval = originalClearInterval;
  await rm(out, { recursive: true, force: true });
}
