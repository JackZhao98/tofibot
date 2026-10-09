import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { fixedTranslation } from "./i18n-harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-desktop-recovery-"));
const originalFetch = globalThis.fetch;
// The panel's copy is asserted in zh-CN, the shipped catalog.
const zh = await fixedTranslation("zh-CN");
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};

try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/BotDesktopPanel.tsx", "src/desktopVideoRecovery.ts", "--types", "vite/client", "--ignoreConfig",
    "--target", "ES2022", "--jsx", "react-jsx", "--module", "ES2022",
    "--moduleResolution", "Bundler", "--outDir", out, "--skipLibCheck",
    "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  const { openDesktopVideo } = await import(pathToFileURL(join(out, "desktopVideoRecovery.js")));
  let calls = [];
  let cancelledBodies = 0;
  const busy = () => new Response(new ReadableStream({ cancel() { cancelledBodies++; } }), { status: 409 });
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return calls.length < 3 ? busy() : new Response("video", { status: 200 });
  };
  assert.equal((await openDesktopVideo("bot-a", new AbortController().signal)).status, 200);
  assert.equal(calls.length, 3);
  assert.equal(cancelledBodies, 2, "discard busy response bodies before retrying");
  assert(calls.every(call => call.url === "/api/bots/bot-a/computer/stream" && call.options.credentials === "same-origin"));
  calls = [];
  globalThis.fetch = async () => { calls.push(true); return busy(); };
  assert.equal((await openDesktopVideo("bot-a", new AbortController().signal)).status, 409);
  assert.equal(calls.length, 4, "permanent contention must stop after three retries");
  for (const status of [410, 503]) {
    calls = [];
    globalThis.fetch = async () => { calls.push(true); return new Response(null, { status }); };
    assert.equal((await openDesktopVideo("bot-a", new AbortController().signal)).status, status);
    assert.equal(calls.length, 1, "stopped/unavailable desktops must not retry");
  }
  calls = [];
  const abort = new AbortController();
  globalThis.fetch = async () => { calls.push(true); return busy(); };
  const pending = openDesktopVideo("bot-a", abort.signal);
  await new Promise(resolve => setTimeout(resolve, 15));
  abort.abort();
  await assert.rejects(pending, { name: "AbortError" });
  assert.equal(calls.length, 1, "hidden/unmounted viewer must cancel the backoff");
  await assert.rejects(openDesktopVideo("bot-b", abort.signal), { name: "AbortError" });
  assert.equal(calls.length, 1, "already cancelled request must not reach fetch");

  // A small hook/JSX harness exercises the actual panel's asynchronous handoff.
  // It deliberately does not claim to emulate MSE decoding or a browser layout.
  const source = (await readFile(join(out, "BotDesktopPanel.js"), "utf8"))
    .replace(/^import .* from "\.\/debugMode";$/m, "const useDebugMode = () => true;")
    .replace(/^import .* from "react\/jsx-runtime";$/m, "const { jsx: _jsx, jsxs: _jsxs } = globalThis.__desktopHarness;")
    .replace(/^import .* from "react";$/m, "const { useEffect, useRef, useState } = globalThis.__desktopHarness; const useLayoutEffect = useEffect;")
    .replace(/^import .* from "\.\/api";$/m, "const { api, ApiError, request } = globalThis.__desktopHarness;")
    .replace(/^import .* from "\.\/DesktopPlaceholder";$/m, "const DesktopPlaceholder = () => null;")
    .replace(/^import .* from "\.\/DesktopVideo";$/m, "const { DesktopVideo } = globalThis.__desktopHarness;")
    .replace(/^import .* from "\.\/icons";$/m, "const { TofiIcon } = globalThis.__desktopHarness;")
    .replace(/^import .* from "\.\/RemoteDesktopControl";$/m, "const { RemoteDesktopControl } = globalThis.__desktopHarness;")
    .replace(/^import .* from "\.\/i18n";$/m, "const { i18n, useTranslation } = globalThis.__desktopHarness;");
  const isolatedSource = source
    .replace(/^import .* from "\.\/BotAvatar";$/m, "const BotAvatar = () => null;")
    .replace(/^import .* from "\.\/DesktopPointerMarker";$/m, "const DesktopPointerMarker = () => null;")
    .replace(/^import .* from "\.\/desktop";$/m, "const isDesktop = false;")
    .replace(/^import .* from "\.\/desktopPresence";$/m, "const useDesktopPresence = () => ({ownership:{owner:null, waiting:[]}, unavailable:false});")
    .replace(/^import "\.\/desktop-presence\.css";$/m, "");
  let errorMappingChecked = false;
  async function panelScenario(switchBot, testInfoOutage = false, startupStopped = false, hideOpening = false, machineStopped = false) {
    const slots = [];
    let cursor = 0;
    let dirty = true;
    let effects = [];
    let tree;
    let deferCapture = hideOpening;
    const captures = [];
    let captureCalls = 0;
    let prepared = !machineStopped;
    let prepareCalls = 0;
    let desktopStarted = !startupStopped;
    let startCalls = 0;
    let infoOutage = false;
    const images = [];
    let deferDecode = false;
    const timers = new Map();
    let timerID = 0;
    const hooks = {
      i18n: zh.i18n, useTranslation: zh.useTranslation,
      jsx: (type, props) => ({ type, props }), jsxs: (type, props) => ({ type, props }),
      DesktopVideo: function DesktopVideo() {}, RemoteDesktopControl: function RemoteDesktopControl() {},
      request: async () => ({owner:null,waiting:[]}),
      TofiIcon: function TofiIcon() {}, ApiError: class ApiError extends Error {},
      useState(initial) {
        const index = cursor++;
        slots[index] ??= { value: initial };
        return [slots[index].value, value => {
          const next = typeof value === "function" ? value(slots[index].value) : value;
          if (next !== slots[index].value) { slots[index].value = next; dirty = true; }
        }];
      },
      useRef(initial) { const index = cursor++; return slots[index] ??= { current: initial }; },
      useEffect(effect, deps) {
        const index = cursor++;
        const previous = slots[index];
        if (!previous || deps.some((value, i) => value !== previous.deps[i])) {
          slots[index] = { deps, cleanup: previous?.cleanup };
          effects.push(() => { slots[index].cleanup?.(); slots[index].cleanup = effect(); });
        }
      },
      api: {
        computerRetry: async () => {prepareCalls++; prepared=true; return {accepted:true};},
        computerInfo: async () => {
          if (infoOutage) throw new Error("synthetic info outage");
          return { state: prepared ? "ready" : "stopped" };
        },
        computerAction: async request => {
          if (request.action === "desktop.start") { startCalls++; desktopStarted = true; return {ok:true,result:{started:true}}; }
          assert.equal(request.action, "desktop.capture", "viewer recovery must never renew or stop a desktop");
          if (!desktopStarted) throw new Error("desktop is not running; call desktop.start first");
          captureCalls++;
          if (!deferCapture) return { ok: true, result: { image_url: "data:image/png;base64,initial" } };
          const capture = deferred();
          captures.push({ ...capture, request });
          return capture.promise;
        },
      },
    };
    globalThis.__desktopHarness = hooks;
    globalThis.document = { visibilityState: "visible" };
    globalThis.window = {
      setTimeout: (callback, delay = 0) => { timers.set(++timerID, { callback, delay }); return timerID; },
      clearTimeout: id => timers.delete(id), addEventListener() {}, removeEventListener() {},
    };
    globalThis.Image = class {
      naturalWidth = 1280; naturalHeight = 800; complete = false;
      set src(value) {
        this.value = value;
        images.push(this);
        if (!deferDecode) queueMicrotask(() => this.onload());
      }
    };
    const { BotDesktopPanel, computerErrorText, frameOperationIsCurrent } = await import(`data:text/javascript;base64,${Buffer.from(isolatedSource + `\n// scenario ${switchBot}:${testInfoOutage}:${startupStopped}:${hideOpening}:${machineStopped}`).toString("base64")}`);
    if (!errorMappingChecked) {
      errorMappingChecked = true;
      assert.equal(computerErrorText(new Error("computer control returned 500 Internal Server Error: desktop resource limit reached (2 active sessions)")), "共享桌面资源暂不可用（2 个活动会话），请稍后重试。");
      assert.equal(computerErrorText(new Error("desktop is stopping; retry shortly")), "共享桌面正在停止，请稍后再试。");
      const busyError = Object.assign(new Error("computer is busy"), { status: 409, code: "computer_busy" });
      assert.equal(computerErrorText(busyError), "电脑正在执行其他操作，请稍后再试。");
      assert.equal(computerErrorText(Object.assign(new Error("not found"), { status: 404, code: "bot_not_found" })), "not found");
      assert.equal(computerErrorText(Object.assign(new Error("run is no longer active"), { status: 409, code: "stale_run" })), "run is no longer active");
      assert.equal(computerErrorText(Object.assign(new Error("computer VM is not configured"), { status: 404, code: "not_configured" })), "共享电脑尚未配置，请先完成服务器安装。");
      assert.equal(computerErrorText(new Error("computer control returned 500: diagnostic details")), "computer control returned 500: diagnostic details");
      const oldOperation = {};
      const currentOperation = {};
      assert.equal(frameOperationIsCurrent(currentOperation, currentOperation), true, "current capture releases its own slot");
      assert.equal(frameOperationIsCurrent(currentOperation, oldOperation), false, "late capture cannot clear a newer slot");
    }
    const props = { botId: "bot-a", botName: "A", members: [{ id: "bot-a", name: "A" }, { id: "bot-b", name: "B" }], onClose() {} };
    const expandedChanges = [];
    props.onExpandedChange = expanded => expandedChanges.push(expanded);
    async function settle() {
      for (let turn = 0; turn < 24; turn++) {
        if (dirty) {
          dirty = false; cursor = 0; effects = [];
          tree = BotDesktopPanel(props);
          for (const effect of effects) effect();
        }
        await Promise.resolve();
      }
    }
    function all(node = tree) {
      if (!node || typeof node !== "object") return [];
      return [node, ...[node.props?.children].flat(Infinity).filter(child => child != null).flatMap(child => all(child))];
    }
    const video = () => all().find(node => node.type === hooks.DesktopVideo);
    const remoteControl = () => all().find(node => node.type === hooks.RemoteDesktopControl);
    await settle();
    await settle();
    if (hideOpening) {
      assert.equal(captures.length,1,"opening has one pending capture");
      all().find(node=>node.type === "button" && node.props["aria-label"] === "隐藏共享电脑").props.onClick();
      for (const slot of slots) slot?.cleanup?.();
      captures[0].resolve({ok:true,result:{image_url:"data:image/png;base64,late-opening"}});
      await settle();
      assert.equal(images.length,0,"hidden startup cannot decode or reattach its late frame");
      assert.equal(startCalls,0,"hidden pending capture cannot later start desktop");
      return;
    }
    assert(captureCalls > 0, `opening connects automatically without a launch click (starts=${startCalls}, nodes=${all().map(n=>typeof n.props?.children === "string" ? n.props.children : "").join("|")})`);
    assert(video(), "viewer should connect with its first screenshot");
    assert.equal(prepareCalls, machineStopped ? 1 : 0, "opening prepares only a stopped machine once");
    assert.equal(startCalls, startupStopped ? 1 : 0, "opening starts a stopped desktop once and never restarts a live one");
    video().props.onState("live");
    await settle();
    assert(remoteControl(), "viewer must be wrapped by the remote control surface");

    // Taking control is owned by RemoteDesktopControl. During its stream
    // reconnect the panel must keep the control surface enabled and retain
    // the hidden remote cursor, otherwise a reconnect releases the lease and
    // leaves the user unable to continue typing/clicking.
    remoteControl().props.onControlChange(true);
    await settle();
    video().props.onState("connecting");
    await settle();
    assert.equal(remoteControl().props.enabled, true, "a human lease survives a video reconnect");
    assert.equal(video().props.cursor, "hidden", "the remote cursor stays hidden while human control is held");
    video().props.onState("live");
    remoteControl().props.onControlChange(false);
    await settle();

    // Exercise the current panel window controls, leaving video mounted.
    all().find(node => node.type === "button" && node.props["aria-label"] === "放大共享电脑").props.onClick();
    await settle();
    assert.equal(expandedChanges.at(-1), true, "expanding reports modal state without replacing the viewer");
    const exitExpand = () => all().find(node => node.type === "button" && node.props["aria-label"] === "缩小共享电脑");
    assert(exitExpand(), "expanded viewer exposes a reversible exit control");
    exitExpand().props.onClick();
    await settle();
    assert.equal(expandedChanges.at(-1), false, "exiting expand reports the normal panel state");
    if (testInfoOutage) {
      deferCapture = true;
      video().props.onState("fallback");
      await settle();
      assert.equal(captures.length, 1, "fallback should have one pending capture");
      infoOutage = true;
      const fireTimer = (delay) => {
        const entry = [...timers.entries()].reverse().find(([, value]) => value.delay === delay);
        assert(entry, `expected timer with delay ${delay}`);
        timers.delete(entry[0]);
        entry[1].callback();
      };
      fireTimer(1_500);
      await settle();
      assert.equal(video().props.enabled, true, "info outage keeps the last viewer mounted");
      const disconnect = all().find(node => node.type === "button" && node.props["aria-label"] === "隐藏共享电脑");
      assert(disconnect && !disconnect.props.disabled, "viewer intent remains cancellable during recovery");
      captures[0].resolve({ ok: true, result: { image_url: "data:image/png;base64,stale" } });
      await settle();
      infoOutage = false;
      deferCapture = false;
      fireTimer(1_500);
      await settle();
      assert(captureCalls >= 2, "recovery must obtain a new read-only capture after the old one settles");
      assert.equal(video().props.enabled, true, "fresh recovery keeps the desktop viewer mounted");
      for (const slot of slots) slot?.cleanup?.();
      return;
    }
    deferCapture = true; deferDecode = true;
    video().props.onState("fallback");
    await settle();
    assert(video(), "hold the last video frame while the replacement is pending");
    assert.equal(remoteControl().props.enabled, false, "a stale transitional frame cannot enable remote input");
    assert.equal(all().filter(node => node.type === "img").length, 0, "never show the original poster as a fallback screenshot");
    assert.equal(captures.length, 1);
    captures[0].resolve({ ok: true, result: { image_url: "data:image/png;base64,fresh" } });
    await settle();
    assert(video(), "download completion alone must not swap before decoding");
    if (switchBot) {
      props.botId="bot-b";props.botName="B";dirty=true;
      await settle();
    }
    images.at(-1).onload();
    await settle();
    if (switchBot) {
      assert.equal(video(), undefined);
      assert.equal(all().filter(node => node.type === "img").length, 1, "shared frame survives a chat Bot switch");
      assert.equal(remoteControl().props.botId,"bot-a","shared control transport must retain its lease identity");
    } else {
      assert.equal(video(), undefined, "unmount video only once a fresh screenshot is ready");
      const image = all().find(node => node.type === "img");
      assert.equal(image.props.src, "data:image/png;base64,fresh");
      assert.equal(remoteControl().props.enabled, true, "fresh fallback frame re-enables the remote control surface");
      assert.equal(image.props.onClick, undefined, "input is handled by RemoteDesktopControl rather than the image");
    }
    for (const slot of slots) slot?.cleanup?.();
  }
  await panelScenario(false);
  await panelScenario(true);
  await panelScenario(false, true);
  await panelScenario(false, false, true);
  await panelScenario(false,false,false,true);
  await panelScenario(false,false,false,false,true);
  console.log("desktop recovery checks: PASS (bounded 409 retries, cancellation, stopped/unavailable, fresh-frame handoff, click gating, shared frame and lease identity across Bot switches)");
} finally {
  globalThis.fetch = originalFetch;
  delete globalThis.__desktopHarness;
  await rm(out, { recursive: true, force: true });
}
