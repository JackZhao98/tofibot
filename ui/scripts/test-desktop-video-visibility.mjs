import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { fixedTranslation } from "./i18n-harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-desktop-visibility-"));
const savedURL = { create: URL.createObjectURL, revoke: URL.revokeObjectURL };
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/DesktopVideo.tsx", "--ignoreConfig", "--target", "ES2022", "--jsx", "react-jsx",
    "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", out,
    "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  const source = (await readFile(join(out, "DesktopVideo.js"), "utf8"))
    .replace(/^import .* from "react\/jsx-runtime";$/m, "const { jsx: _jsx } = globalThis.__visibilityHarness;")
    .replace(/^import .* from "react";$/m, "const { useEffect, useRef, useState } = globalThis.__visibilityHarness;")
    .replace(/^import .* from "\.\/desktopVideoRecovery";$/m, "const { openDesktopVideo } = globalThis.__visibilityHarness;")
    .replace(/^import .* from "\.\/i18n";$/m, "const { useTranslation } = globalThis.__visibilityHarness;");
  const slots = [];
  let cursor = 0, dirty = true, effects = [], tree;
  const timers = new Map();
  let timerID = 0;
  const allocated = new Set();
  const revoked = new Set();
  const canvases = [];
  let failCapture = false;
  let draws = 0;
  let callbacks = [];
  const video = new class extends EventTarget {
    readyState = 0; videoWidth = 1280; videoHeight = 800;
    poster = ""; paused = true;
    pause() { this.paused = true; }
    load() { this.readyState = 0; }
    removeAttribute(name) { this[name] = ""; }
  }();
  const { useTranslation } = await fixedTranslation("zh-CN");
  const hooks = {
    useTranslation,
    jsx: (type, props) => ({ type, props }),
    openDesktopVideo() { throw new Error("Visibility test must not open a network connection"); },
    useState(initial) {
      const index = cursor++;
      slots[index] ??= { value: initial };
      return [slots[index].value, value => {
        if (value !== slots[index].value) { slots[index].value = value; dirty = true; }
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
  };
  globalThis.__visibilityHarness = hooks;
  globalThis.document = new class extends EventTarget {
    hidden = false;
    createElement(tag) {
      assert.equal(tag, "canvas");
      const canvas = {
        width: 0, height: 0,
        getContext: () => ({ drawImage: target => { assert.equal(target, video); draws++; } }),
        toDataURL: () => { if (failCapture) throw new Error("canvas blocked"); return "data:image/jpeg;base64,current-page-B"; },
      };
      canvases.push(canvas);
      return canvas;
    }
  }();
  globalThis.window = {
    setTimeout: callback => { timers.set(++timerID, callback); return timerID; },
    clearTimeout: id => timers.delete(id),
  };
  globalThis.MediaSource = class extends EventTarget { static isTypeSupported() { return true; } };
  URL.createObjectURL = () => { const url = `blob:test-${allocated.size}`; allocated.add(url); return url; };
  URL.revokeObjectURL = url => { assert(!revoked.has(url), "revoke each media URL only once"); revoked.add(url); };
  const { DesktopVideo, desktopPlaybackAdjustment } = await import(`data:text/javascript;base64,${Buffer.from(source).toString("base64")}`);
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 1.12), { time: null, rate: 0.95 }, "thin buffer builds a decoding cushion");
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 1.35), { time: null, rate: 1 }, "healthy cushion plays normally");
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 1.25, 0.95), { time: null, rate: 0.95 }, "hysteresis avoids rate chatter");
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 1.45, 1.12), { time: null, rate: 1.12 }, "catch-up continues until healthy range");
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 2), { time: null, rate: 1.12 }, "ordinary backlog catches up without scrubbing");
  assert.deepEqual(desktopPlaybackAdjustment(1, 0, 3), { time: 2.7, rate: 1 }, "large backlog still has bounded recovery");
  assert.deepEqual(desktopPlaybackAdjustment(1, 3, 3.1), { time: 3, rate: 1 }, "seek stays inside newest playable range");
  const props = { botId: "bot-a", enabled: true, cursor: "visible", poster: "data:image/png;base64,initial-page-A", onState: state => callbacks.push(state) };
  async function settle() {
    for (let turn = 0; turn < 16; turn++) {
      if (dirty) {
        dirty = false; cursor = 0; effects = [];
        tree = DesktopVideo(props);
        tree.props.ref.current = video;
        video.poster = tree.props.poster ?? "";
        for (const effect of effects) effect();
      }
      await Promise.resolve();
    }
  }
  async function visible(hidden) {
    document.hidden = hidden;
    document.dispatchEvent(new Event("visibilitychange"));
    await settle();
  }
  await settle();
  video.readyState = 4;
  video.dispatchEvent(new Event("loadeddata"));
  await settle();
  await visible(true);
  assert.equal(video.poster, "data:image/jpeg;base64,current-page-B");
  assert.equal(draws, 1, "capture only once per hide, not per frame");
  assert(canvases.every(canvas => canvas.width === 0 && canvas.height === 0), "release canvas pixel buffers immediately");
  assert.equal(revoked.size, 1, "hiding must release the stream object URL");
  await visible(false);
  assert.equal(video.poster, "data:image/jpeg;base64,current-page-B", "reconnect must retain B instead of initial A");
  video.readyState = 4;
  props.cursor = "hidden";
  dirty = true;
  await settle();
  assert.equal(video.poster, "data:image/jpeg;base64,current-page-B", "cursor takeover reconnect must retain the last decoded frame");
  props.cursor = "visible";
  dirty = true;
  await settle();
  assert.equal(video.poster, "data:image/jpeg;base64,current-page-B", "cursor release reconnect must retain the last decoded frame");
  video.readyState = 4;
  video.dispatchEvent(new Event("loadeddata"));
  await settle();
  failCapture = true;
  await visible(true);
  assert.equal(video.poster, "", "blocked canvas capture must hide the stale initial poster");
  await visible(false);
  assert.equal(video.poster, "", "failed capture remains blank while reconnecting");
  failCapture = false;
  video.readyState = 4;
  await visible(true);
  props.botId = "bot-b";
  props.poster = "data:image/png;base64,bot-b";
  dirty = true;
  await settle();
  assert.equal(video.poster, props.poster, "Bot B must not inherit Bot A's frozen frame");
  await visible(false);
  video.readyState = 4;
  await visible(true);
  props.enabled = false;
  dirty = true;
  await settle();
  assert.equal(tree.props.poster, props.poster, "disabled viewer must release its captured frame");
  assert.equal(allocated.size, revoked.size, "all detached stream object URLs must be revoked");
  assert.equal(timers.size, 0, "cancel watchdogs when sessions are detached");
  const count = callbacks.length;
  await visible(false);
  assert.equal(callbacks.length, count, "disabled/disposed viewer must have no visibility listener");
  for (const slot of slots) slot?.cleanup?.();
  console.log("desktop visibility checks: PASS (last-frame poster, capture failure, canvas release, Bot/enable isolation, object URL/watchdog cleanup)");
} finally {
  URL.createObjectURL = savedURL.create;
  URL.revokeObjectURL = savedURL.revoke;
  delete globalThis.__visibilityHarness;
  await rm(out, { recursive: true, force: true });
}
